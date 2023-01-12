package manifestwork

import (
	"errors"

	operatorv1 "github.com/openshift/api/operator/v1"
	"github.com/openshift/console-operator/pkg/api"
	oauthclientsub "github.com/openshift/console-operator/pkg/console/subresource/oauthclient"
	"github.com/openshift/console-operator/pkg/console/subresource/util"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"
	workv1 "open-cluster-management.io/api/work/v1"
)

func DefaultManagedClusterOAuthClientManifestWork(operatorConfig *operatorv1.Console, namespace string, secret string, redirects []string) *workv1.ManifestWork {
	manifestWork := ManagedClusterOAuthClientManifestWorkStub(namespace)
	oauthClient := oauthclientsub.DefaultManagedClusterOauthClient(secret, redirects)
	WithManifest(manifestWork, oauthClient)
	util.AddOwnerRef(manifestWork, util.OwnerRefFrom(operatorConfig))
	return manifestWork
}

func ManagedClusterOAuthClientManifestWorkStub(namespace string) *workv1.ManifestWork {
	return &workv1.ManifestWork{
		ObjectMeta: metav1.ObjectMeta{
			Name:      api.ManagedClusterOauthClientManifestWork,
			Namespace: namespace,
			Labels:    util.LabelsForManagedClusterResources(""),
		},
		Spec: workv1.ManifestWorkSpec{
			Executor: &workv1.ManifestWorkExecutor{
				Subject: workv1.ManifestWorkExecutorSubject{
					Type: workv1.ExecutorSubjectTypeServiceAccount,
					ServiceAccount: &workv1.ManifestWorkSubjectServiceAccount{
						Namespace: api.OpenShiftConsoleOperatorNamespace,
						Name:      api.OpenShiftConsoleOperatorExecutor,
					},
				},
			},
			Workload: workv1.ManifestsTemplate{
				Manifests: []workv1.Manifest{},
			},
		},
	}
}

func WithManifest(manifestWork *workv1.ManifestWork, object runtime.Object) {
	if &manifestWork.Spec == nil {
		manifestWork.Spec = workv1.ManifestWorkSpec{}
	}

	if &manifestWork.Spec.Workload == nil {
		manifestWork.Spec.Workload = workv1.ManifestsTemplate{}
	}

	if &manifestWork.Spec.Workload.Manifests == nil {
		manifestWork.Spec.Workload.Manifests = []workv1.Manifest{}
	}

	manifest := workv1.Manifest{RawExtension: runtime.RawExtension{Object: object}}
	manifestWork.Spec.Workload.Manifests = append(manifestWork.Spec.Workload.Manifests, manifest)
}

func GetOAuthClientSecret(manifestWork *workv1.ManifestWork) (string, error) {
	if &manifestWork.Spec.Workload == nil {
		return "", errors.New("Unable to parse OAuthClient from ManifestWork. No workload.")
	}

	if &manifestWork.Spec.Workload.Manifests == nil || len(manifestWork.Spec.Workload.Manifests) == 0 {
		return "", errors.New("Unable to parse OAuthClient from ManifestWork. No manifests.")
	}

	if len(manifestWork.Spec.Workload.Manifests[0].Raw) == 0 {
		return "", errors.New("Unable to parse OAuthClient from ManifestWork. Manifest is empty.")
	}

	oauthClient, err := oauthclientsub.ReadOAuthClientV1([]byte(manifestWork.Spec.Workload.Manifests[0].Raw))
	if err != nil {
		klog.V(4).Infof("Unable to parse OAuthClient from ManifestWork: %v", err)
		return "", err
	}
	secretString := oauthclientsub.GetSecretString(oauthClient)
	return secretString, nil
}
