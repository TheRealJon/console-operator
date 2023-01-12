package managedclusteroauthclientcontroller

import (
	"context"
	"fmt"
	"time"

	// k8s
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	coreclientv1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/klog/v2"

	// openshift
	oauthv1 "github.com/openshift/api/oauth/v1"
	operatorv1 "github.com/openshift/api/operator/v1"
	configclientv1 "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"
	configinformer "github.com/openshift/client-go/config/informers/externalversions"
	oauthv1client "github.com/openshift/client-go/oauth/clientset/versioned/typed/oauth/v1"
	operatorclientv1 "github.com/openshift/client-go/operator/clientset/versioned/typed/operator/v1"
	operatorinformersv1 "github.com/openshift/client-go/operator/informers/externalversions/operator/v1"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
	clusterclientv1 "open-cluster-management.io/api/client/cluster/clientset/versioned/typed/cluster/v1"
	workclientv1 "open-cluster-management.io/api/client/work/clientset/versioned/typed/work/v1"
	clusterv1 "open-cluster-management.io/api/cluster/v1"

	//subresources
	manifestworksub "github.com/openshift/console-operator/pkg/console/subresource/manifestwork"
	oauthsub "github.com/openshift/console-operator/pkg/console/subresource/oauthclient"

	// console-operator
	"github.com/openshift/console-operator/pkg/api"
	"github.com/openshift/console-operator/pkg/console/controllers/util"
	"github.com/openshift/console-operator/pkg/console/status"
)

const ConditionPrefix string = "ManagedClusterOauthClientSync"

type Controller struct {
	operatorClient       v1helpers.OperatorClient
	operatorConfigClient operatorclientv1.ConsoleInterface
	secretClient         coreclientv1.SecretsGetter
	oauthClient          oauthv1client.OAuthClientsGetter
	managedClusterClient clusterclientv1.ManagedClustersGetter
	workClient           workclientv1.ManifestWorksGetter
}

func New(
	// top level config
	configClient configclientv1.ConfigV1Interface,
	configInformer configinformer.SharedInformerFactory,

	// clients
	operatorClient v1helpers.OperatorClient,
	operatorConfigClient operatorclientv1.ConsoleInterface,
	secretClient coreclientv1.SecretsGetter,
	managedClusterClient clusterclientv1.ManagedClustersGetter,
	oauthClient oauthv1client.OAuthClientsGetter,
	workClient workclientv1.ManifestWorksGetter,

	// informers
	operatorConfigInformer operatorinformersv1.ConsoleInformer,

	// events
	recorder events.Recorder,
) factory.Controller {
	ctrl := &Controller{
		operatorClient:       operatorClient,
		operatorConfigClient: operatorConfigClient,
		secretClient:         secretClient,
		managedClusterClient: managedClusterClient,
		oauthClient:          oauthClient,
		workClient:           workClient,
	}

	configV1Informers := configInformer.Config().V1()

	return factory.New().
		WithFilteredEventsInformers( // configs
			util.IncludeNamesFilter(api.ConfigResourceName),
			configV1Informers.Consoles().Informer(),
			operatorConfigInformer.Informer(),
		).
		ResyncEvery(1*time.Minute).
		WithSync(ctrl.Sync).
		ToController("ManagedClusterOAuthClientController", recorder.WithComponentSuffix("managed-cluster-oauth-client-controller"))
}

func (c *Controller) Sync(ctx context.Context, controllerContext factory.SyncContext) error {
	operatorConfig, err := c.operatorConfigClient.Get(ctx, api.ConfigResourceName, metav1.GetOptions{})
	if err != nil {
		return err
	}

	switch operatorConfig.Spec.ManagementState {
	case operatorv1.Managed:
		klog.V(4).Info("console-operator is in a managed state: syncing managed cluster oauth clients")
	case operatorv1.Unmanaged:
		klog.V(4).Info("console-operator is in an unmanaged state: skipping managed cluster oauth client sync")
		return nil
	case operatorv1.Removed:
		klog.V(4).Infof("console-operator is in a removed state: deleting managed cluster oauth clients")
		return c.remove(ctx)
	default:
		return fmt.Errorf("unknown state: %v", operatorConfig.Spec.ManagementState)
	}

	statusHandler := status.NewStatusHandler(c.operatorClient)

	// Get the local OAuthClient. If this fails, do not proceed. We can't create OAuth clients on
	// managed clusters without the local client.
	localOAuthClient, reason, err := c.GetLocalOAuthClient(ctx)
	statusHandler.AddConditions(status.HandleProgressingOrDegraded(ConditionPrefix, reason, err))
	if err != nil {
		return c.remove(ctx)
	}

	managedClusterList, reason, err := c.SyncManagedClusters(ctx)
	statusHandler.AddConditions(status.HandleProgressingOrDegraded(ConditionPrefix, reason, err))
	if err != nil || len(managedClusterList.Items) == 0 {
		return c.remove(ctx)
	}

	// Create ManifestWorks which will create oauth clients on each managed cluster
	errs := []error{}
	for _, managedCluster := range managedClusterList.Items {
		validCluster, validClusterVersion := util.IsSupportedMangedCluster(managedCluster)
		if !validCluster || !validClusterVersion {
			klog.V(4).Infof("Skipping oauth client ManifestWork on managed cluster %s, cluster is not supported./n", managedCluster.Name)
			continue
		}

		reason, err = c.SyncOauthClientManifestWork(
			ctx,
			operatorConfig,
			managedCluster,
			localOAuthClient.Secret,
			localOAuthClient.RedirectURIs,
		)
		statusHandler.AddConditions(status.HandleProgressingOrDegraded(ConditionPrefix, reason, err))
		if err != nil {
			klog.V(4).Infof("Error syncing OAuthClient ManifestWork for managed cluster %s: %v./n", managedCluster.Name, err)
			errs = append(errs, err)
		}
	}

	err = utilerrors.NewAggregate(errs)
	return statusHandler.FlushAndReturn(err)
}

func (c *Controller) GetLocalOAuthClient(ctx context.Context) (*oauthv1.OAuthClient, string, error) {
	oAuthClient, err := c.oauthClient.OAuthClients().Get(ctx, oauthsub.Stub().Name, metav1.GetOptions{})
	if err != nil {
		return nil, "GetError", fmt.Errorf("Failed to get local OAuthClient: %v", err)
	}
	return oAuthClient, "", nil
}

func (c *Controller) SyncManagedClusters(ctx context.Context) (*clusterv1.ManagedClusterList, string, error) {
	managedClusterList, err := c.managedClusterClient.ManagedClusters().List(ctx, metav1.ListOptions{})
	if err != nil {
		return managedClusterList, "ListError", err
	}
	return managedClusterList, "", nil
}

func (c *Controller) SyncOauthClientManifestWork(
	ctx context.Context,
	operatorConfig *operatorv1.Console,
	managedCluster clusterv1.ManagedCluster,
	clientSecret string,
	redirectUris []string,
) (string, error) {
	managedClusterName := managedCluster.Name
	required := manifestworksub.DefaultManagedClusterOAuthClientManifestWork(operatorConfig, managedClusterName, clientSecret, redirectUris)
	_, err := manifestworksub.ApplyManifestWork(ctx, c.workClient.ManifestWorks(managedClusterName), required)
	if err != nil {
		return "ApplyError", fmt.Errorf("failed to apply OauthClient ManifestWork for managed cluster %s: %v", managedClusterName, err)
	}
	return "", nil
}

func (c *Controller) remove(ctx context.Context) error {
	manifestWorks, err := c.workClient.ManifestWorks("").List(ctx, metav1.ListOptions{LabelSelector: api.ManagedClusterLabel})
	if err != nil {
		return err
	}

	errors := []error{}
	for _, manifestWork := range manifestWorks.Items {
		err := c.workClient.ManifestWorks(manifestWork.Namespace).Delete(ctx, manifestWork.Name, metav1.DeleteOptions{})
		if err != nil && !apierrors.IsNotFound(err) {
			errors = append(errors, err)
		}
	}
	return utilerrors.NewAggregate(errors)
}
