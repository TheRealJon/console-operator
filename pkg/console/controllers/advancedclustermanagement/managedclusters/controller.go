package managedcluster

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	// k8s
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/dynamic"
	coreclientv1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/klog/v2"

	// openshift
	operatorv1 "github.com/openshift/api/operator/v1"
	configinformer "github.com/openshift/client-go/config/informers/externalversions"
	oauthclientv1 "github.com/openshift/client-go/oauth/clientset/versioned/typed/oauth/v1"
	operatorclientv1 "github.com/openshift/client-go/operator/clientset/versioned/typed/operator/v1"
	v1 "github.com/openshift/client-go/operator/informers/externalversions/operator/v1"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/resource/resourceapply"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
	clusterclientv1 "open-cluster-management.io/api/client/cluster/clientset/versioned/typed/cluster/v1"
	workclientv1 "open-cluster-management.io/api/client/work/clientset/versioned/typed/work/v1"
	clusterv1 "open-cluster-management.io/api/cluster/v1"
	workv1 "open-cluster-management.io/api/work/v1"

	//subresources
	configmapsub "github.com/openshift/console-operator/pkg/console/subresource/configmap"
	managedclusterviewsub "github.com/openshift/console-operator/pkg/console/subresource/managedclusterview"
	manifestworksub "github.com/openshift/console-operator/pkg/console/subresource/manifestwork"

	// console-operator
	"github.com/openshift/console-operator/pkg/api"
	"github.com/openshift/console-operator/pkg/console/controllers/util"
	"github.com/openshift/console-operator/pkg/console/status"
	"github.com/openshift/console-operator/pkg/console/subresource/consoleserver"
)

type ManagedClusterController struct {
	operatorClient       v1helpers.OperatorClient
	operatorConfigClient operatorclientv1.ConsoleInterface
	configMapClient      coreclientv1.ConfigMapsGetter
	managedClusterClient clusterclientv1.ManagedClustersGetter
	dynamicClient        dynamic.Interface
	secretsClient        coreclientv1.SecretsGetter
	oauthClient          oauthclientv1.OAuthClientsGetter
	workClient           workclientv1.ManifestWorksGetter
}

func NewManagedClusterController(
	// top level config
	configInformer configinformer.SharedInformerFactory,

	// clients
	operatorClient v1helpers.OperatorClient,
	operatorConfigClient operatorclientv1.ConsoleInterface,
	configMapClient coreclientv1.ConfigMapsGetter,
	managedClusterClient clusterclientv1.ClusterV1Interface,
	dynamicClient dynamic.Interface,
	secretsClient coreclientv1.SecretsGetter,
	oauthClient oauthclientv1.OAuthClientsGetter,
	workClient workclientv1.ManifestWorksGetter,

	// informers
	operatorConfigInformer v1.ConsoleInformer,

	// events
	recorder events.Recorder,
) factory.Controller {
	ctrl := &ManagedClusterController{
		operatorClient:       operatorClient,
		operatorConfigClient: operatorConfigClient,
		configMapClient:      configMapClient,
		managedClusterClient: managedClusterClient,
		dynamicClient:        dynamicClient,
		secretsClient:        secretsClient,
		oauthClient:          oauthClient,
		workClient:           workClient,
	}

	return factory.New().
		WithFilteredEventsInformers( // configs
			util.IncludeNamesFilter(api.ConfigResourceName),
			configInformer.Config().V1().Consoles().Informer(),
			operatorConfigInformer.Informer(),
		).ResyncEvery(1*time.Minute).WithSync(ctrl.Sync).
		ToController("ManagedClusterController", recorder.WithComponentSuffix("managed-cluster-controller"))
}

func (c *ManagedClusterController) Sync(ctx context.Context, controllerContext factory.SyncContext) error {
	operatorConfig, err := c.operatorConfigClient.Get(ctx, api.ConfigResourceName, metav1.GetOptions{})
	if err != nil {
		return err
	}

	switch operatorConfig.Spec.ManagementState {
	case operatorv1.Managed:
		klog.V(4).Info("console-operator is in a managed state: syncing managed clusters")
	case operatorv1.Unmanaged:
		klog.V(4).Info("console-operator is in an unmanaged state: skipping managed cluster sync")
		return nil
	case operatorv1.Removed:
		klog.V(4).Infof("console-operator is in a removed state: deleting managed clusters")
		return c.removeManagedClusters(ctx)
	default:
		return fmt.Errorf("unknown state: %v", operatorConfig.Spec.ManagementState)
	}

	statusHandler := status.NewStatusHandler(c.operatorClient)

	// Get a list of validated ManagedCluster resources
	managedClusters, errReason, err := c.SyncManagedClusterList(ctx)
	statusHandler.AddConditions(status.HandleProgressingOrDegraded("ManagedClusterSync", errReason, err))
	if err != nil || len(managedClusters) == 0 {
		return c.removeManagedClusters(ctx)
	}

	// Create managed cluster views for ingress cert
	oAuthServerCertMCVs, errReason, err := c.SyncOAuthServerCertManagedClusterViews(ctx, operatorConfig, managedClusters)
	statusHandler.AddConditions(status.HandleProgressingOrDegraded("ManagedClusterSync", errReason, err))

	// Create config maps for each managed cluster ingress cert bundle
	errReason, err = c.SyncOAuthServerCertConfigMaps(oAuthServerCertMCVs, ctx, operatorConfig, controllerContext.Recorder())
	statusHandler.AddConditions(status.HandleProgressingOrDegraded("ManagedClusterSync", errReason, err))

	// Create managed cluster views for OLM config
	errReason, err = c.SyncOLMConfigManagedClusterViews(ctx, operatorConfig, managedClusters)
	statusHandler.AddConditions(status.HandleProgressingOrDegraded("ManagedClusterSync", errReason, err))

	// Create config maps for each managed cluster API server ca bundle
	errReason, err = c.SyncAPIServerCertConfigMaps(managedClusters, ctx, operatorConfig, controllerContext.Recorder())
	statusHandler.AddConditions(status.HandleProgressingOrDegraded("ManagedClusterSync", errReason, err))
	if err != nil {
		return statusHandler.FlushAndReturn(err)
	}

	// Create  manged cluster config map
	errReason, err = c.SyncManagedClusterConfigMap(managedClusters, ctx, operatorConfig, controllerContext.Recorder())
	statusHandler.AddConditions(status.HandleProgressingOrDegraded("ManagedClusterSync", errReason, err))
	return statusHandler.FlushAndReturn(err)
}

func (c *ManagedClusterController) SyncManagedClusterList(ctx context.Context) ([]clusterv1.ManagedCluster, string, error) {
	managedClusters, err := c.managedClusterClient.ManagedClusters().List(ctx, metav1.ListOptions{LabelSelector: fmt.Sprintf("local-cluster!=true")})

	// Not degraded, API is not found which means ACM isn't installed
	if apierrors.IsNotFound(err) {
		return nil, "", nil
	}

	if err != nil {
		return nil, "ErrorListingManagedClusters", err
	}

	valid := []clusterv1.ManagedCluster{}
	for _, managedCluster := range managedClusters.Items {
		clusterName := managedCluster.GetName()

		// Ensure client configs exists
		clientConfigs := managedCluster.Spec.ManagedClusterClientConfigs
		if len(clientConfigs) == 0 {
			klog.V(4).Infoln(fmt.Sprintf("Skipping managed cluster %v, no client config found", clusterName))
			continue
		}

		// Ensure client config CA bundle exists
		if clientConfigs[0].CABundle == nil {
			klog.V(4).Infoln(fmt.Sprintf("Skipping managed cluster %v, client config CA bundle not found", clusterName))
			continue
		}

		// Ensure client config URL exists
		if clientConfigs[0].URL == "" {
			klog.V(4).Infof("Skipping managed cluster %v, client config URL not found", clusterName)
			continue
		}

		// Check the claims if version and product are supported
		validProduct, validVersion := util.IsSupportedMangedCluster(managedCluster)
		// Omit clusters that have unsupported product name defined in api UnsupportedClusterProducts
		if !validProduct {
			klog.V(4).Infof("Skipping managed cluster %q, product is unsupported", clusterName)
			continue
		}

		// Omit any clusters with version less than ocp 4.0.0
		if !validVersion {
			klog.V(4).Infof("Skipping managed cluster %q, versions prior to openshift 4.0.0 are not supported", clusterName)
			continue
		}
		valid = append(valid, managedCluster)
	}

	return valid, "", nil
}

func (c *ManagedClusterController) SyncOAuthServerCertManagedClusterViews(ctx context.Context, operatorConfig *operatorv1.Console, managedClusters []clusterv1.ManagedCluster) ([]*unstructured.Unstructured, string, error) {
	errs := []string{}
	mcvs := []*unstructured.Unstructured{}
	for _, managedCluster := range managedClusters {
		mcv, err := c.dynamicClient.Resource(api.ManagedClusterViewGroupVersionResource).Namespace(managedCluster.Name).Get(ctx, api.OAuthServerCertManagedClusterViewName, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			required, err := managedclusterviewsub.DefaultOAuthServerCertView(operatorConfig, managedCluster.Name)
			if err != nil {
				errs = append(errs, fmt.Sprintf("Error initializing oauth server cert ManagedClusterView for cluster %s: %v", managedCluster.Name, err))
				continue
			}
			mcv, err = c.dynamicClient.Resource(api.ManagedClusterViewGroupVersionResource).Namespace(managedCluster.Name).Create(ctx, required, metav1.CreateOptions{})
		}

		if err != nil || mcv == nil {
			errs = append(errs, fmt.Sprintf("Error syncing oauth server cert ManagedClusterView for cluster %s: %v", managedCluster.Name, err))
		} else {
			mcvs = append(mcvs, mcv)
		}
	}

	if len(errs) > 0 {
		return nil, "OAuthServerCertManagedClusterViewSyncError", errors.New(strings.Join(errs, "\n"))
	}

	return mcvs, "", nil
}

func (c *ManagedClusterController) SyncOLMConfigManagedClusterViews(ctx context.Context, operatorConfig *operatorv1.Console, managedClusters []clusterv1.ManagedCluster) (string, error) {
	errs := []string{}
	for _, managedCluster := range managedClusters {
		mcv, err := c.dynamicClient.Resource(api.ManagedClusterViewGroupVersionResource).Namespace(managedCluster.Name).Get(ctx, api.OLMConfigManagedClusterViewName, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			required, err := managedclusterviewsub.DefaultOLMConfigView(operatorConfig, managedCluster.Name)
			if err != nil {
				errs = append(errs, fmt.Sprintf("Error initializing OLM config ManagedClusterView for cluster %s: %v", managedCluster.Name, err))
				continue
			}
			mcv, err = c.dynamicClient.Resource(api.ManagedClusterViewGroupVersionResource).Namespace(managedCluster.Name).Create(ctx, required, metav1.CreateOptions{})
		}

		if err != nil || mcv == nil {
			errs = append(errs, fmt.Sprintf("Error syncing OLM config ManagedClusterView for cluster %s: %v", managedCluster.Name, err))
		}
	}

	if len(errs) > 0 {
		return "OLMConfigManagedClusterViewSyncError", errors.New(strings.Join(errs, "\n"))
	}

	return "", nil
}

func (c *ManagedClusterController) SyncOAuthServerCertConfigMaps(oAuthServerCertMCVs []*unstructured.Unstructured, ctx context.Context, operatorConfig *operatorv1.Console, recorder events.Recorder) (string, error) {
	errs := []string{}
	for _, oAuthServerCertMCV := range oAuthServerCertMCVs {
		clusterName := oAuthServerCertMCV.GetNamespace()
		certBundle, _ := managedclusterviewsub.GetCertBundle(oAuthServerCertMCV)
		if certBundle == "" {
			klog.V(4).Infoln(fmt.Sprintf("Skipping OAuth server certificate ConfigMap sync for managed cluster %v, cert bundle is empty", clusterName))
			continue
		}

		required := configmapsub.DefaultManagedClusterOAuthServerCertConfigMap(clusterName, certBundle, operatorConfig)
		_, _, configMapApplyError := resourceapply.ApplyConfigMap(ctx, c.configMapClient, recorder, required)
		if configMapApplyError != nil {
			klog.V(4).Infoln(fmt.Sprintf("Skipping OAuth server certificate ConfigMap sync for managed cluster %v, Error applying ConfigMap", clusterName))
			errs = append(errs, configMapApplyError.Error())
			continue
		}
	}

	if len(errs) > 0 {
		return "ManagedClusterIngressCertConfigMapSyncError", errors.New(strings.Join(errs, "\n"))
	}

	return "", nil
}

// Using ManagedCluster.spec.ManagedClusterClientConfigs, sync ConfigMaps containing the API server CA bundle for each managed cluster
// If a managed cluster doesn't have complete client config yet, the information is logged, but no error is returned
// If applying any ConfigMap fails, an error and reason are returned
func (c *ManagedClusterController) SyncAPIServerCertConfigMaps(managedClusters []clusterv1.ManagedCluster, ctx context.Context, operatorConfig *operatorv1.Console, recorder events.Recorder) (string, error) {
	errs := []string{}
	for _, managedCluster := range managedClusters {
		// Apply the config map. If this fails for any managed cluster, operator is degraded
		clusterName := managedCluster.GetName()
		caBundle := managedCluster.Spec.ManagedClusterClientConfigs[0].CABundle
		required := configmapsub.DefaultAPIServerCAConfigMap(managedCluster.GetName(), caBundle, operatorConfig)
		_, _, configMapApplyError := resourceapply.ApplyConfigMap(ctx, c.configMapClient, recorder, required)
		if configMapApplyError != nil {
			klog.V(4).Infoln(fmt.Sprintf("Skipping API server CA ConfigMap sync for managed cluster %v, Error applying ConfigMap", clusterName))
			errs = append(errs, configMapApplyError.Error())
			continue
		}
	}

	// Return any apply errors that occurred
	if len(errs) > 0 {
		return "APIServerCAConfigMapSyncError", errors.New(strings.Join(errs, "\n"))
	}

	// Success
	return "", nil
}

// Using ManagedClusters.Spec.ManagedClusterClientConfigs and previously synced CA bundles, sync a ConfigMap containing serverconfig.ManagedClusterConfig YAML for each managed cluster
// If a managed cluster doesn't have an API server CA bundle ConfigMap yet or the client config is incomplete, this is logged, but no error is returned
// If applying the ConfigMap fails, an error and reason are returned
func (c *ManagedClusterController) SyncManagedClusterConfigMap(managedClusters []clusterv1.ManagedCluster, ctx context.Context, operatorConfig *operatorv1.Console, recorder events.Recorder) (string, error) {
	managedClusterConfigs := []consoleserver.ManagedClusterConfig{}
	for _, managedCluster := range managedClusters {
		clusterName := managedCluster.GetName()
		klog.V(4).Infoln(fmt.Sprintf("Building config for managed cluster: %v", clusterName))

		// Check that managed cluster API server CA ConfigMap has already been synced, skip if not found
		_, err := c.configMapClient.ConfigMaps(api.OpenShiftConsoleNamespace).Get(ctx, configmapsub.APIServerCAConfigMapName(clusterName), metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			klog.V(4).Infof("API server CA file not found for managed cluster %v", clusterName)
			continue
		}

		// Skip if unable to get managed cluster API server config map for any other reason
		if err != nil {
			klog.V(4).Infof("Error getting API server CA file for managed cluster %v", clusterName)
			continue
		}

		// Check that managed cluster OAuth server CA ConfigMap has already been synced, skip if not found
		_, err = c.configMapClient.ConfigMaps(api.OpenShiftConsoleNamespace).Get(ctx, configmapsub.ManagedClusterOAuthServerCertConfigMapName(clusterName), metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			klog.V(4).Infof("OAuth server CA file not found for managed cluster %v", clusterName)
			continue
		}

		// Skip if unable to get managed cluster OAuth server config map for any other reason
		if err != nil {
			klog.V(4).Infof("Error getting OAuth server CA file for managed cluster %v", clusterName)
			continue
		}

		// Check that managed cluster OAuth client ManifestWork has already been synced, skip if not found
		oAuthClientWork, err := c.workClient.ManifestWorks(clusterName).Get(ctx, api.ManagedClusterOauthClientManifestWork, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			klog.V(4).Infof("OAuth client ManifestWork not found for managed cluster %v", clusterName)
			continue
		}

		// Skip if unable to get managed cluster OAuth client ManifestWork for any other reason
		if err != nil {
			klog.V(4).Infof("Error getting OAuth client ManifestWork for managed cluster %v", clusterName)
			continue
		}

		if !util.IsConditionMet(oAuthClientWork.Status.Conditions, workv1.WorkAvailable, metav1.ConditionTrue) {
			klog.V(4).Infof("OAuthClient is not yet available on managed cluster %s", clusterName)
			continue
		}

		oAuthClientSecretString, err := manifestworksub.GetOAuthClientSecret(oAuthClientWork)
		if err != nil {
			klog.V(4).Infof("Error getting OAuthClient secret string for managed cluster %v", clusterName)
			continue
		}

		// Check that managed cluster olm config MCV has already been synced, skip if not found
		olmConfigMCV, err := c.dynamicClient.Resource(api.ManagedClusterViewGroupVersionResource).Namespace(clusterName).Get(ctx, api.OLMConfigManagedClusterViewName, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			klog.V(4).Infof("OLM config ManagedClusterView not found for managed cluster %v", clusterName)
			continue
		}

		// Skip if unable to get olm config MCV for any other reason
		if err != nil {
			klog.V(4).Infof("Error getting OLM config ManagedClusterView for managed cluster %s: %v", clusterName, err)
			continue
		}

		copiedCSVsDisabled, err := managedclusterviewsub.GetOLMConfigCopiedCSVDisabled(olmConfigMCV)
		if err != nil {
			klog.V(4).Infof("Error getting copiedCSVDisabled field for managed cluster %s: %v", clusterName, err)
			continue
		}

		managedClusterConfigs = append(managedClusterConfigs, consoleserver.ManagedClusterConfig{
			Name: clusterName,
			APIServer: consoleserver.ManagedClusterAPIServerConfig{
				URL:    managedCluster.Spec.ManagedClusterClientConfigs[0].URL,
				CAFile: configmapsub.APIServerCAFileMountPath(clusterName),
			},
			Oauth: consoleserver.ManagedClusterOAuthConfig{
				CAFile:       configmapsub.ManagedClusterOAuthServerCAFileMountPath(clusterName),
				ClientID:     api.ManagedClusterOAuthClientName,
				ClientSecret: oAuthClientSecretString,
			},
			CopiedCSVsDisabled: copiedCSVsDisabled,
		})
	}

	if len(managedClusterConfigs) > 0 {
		required, err := configmapsub.DefaultManagedClustersConfigMap(operatorConfig, managedClusterConfigs)
		if err != nil {
			return "FailedMarshallingYAML", err
		}

		if _, _, applyErr := resourceapply.ApplyConfigMap(ctx, c.configMapClient, recorder, required); applyErr != nil {
			return "FailedApply", applyErr
		}
	}

	return "", nil
}

func (c *ManagedClusterController) removeManagedClusters(ctx context.Context) error {
	klog.V(4).Info("Removing managed cluster resources.")
	errs := []string{}
	err := c.removeManagedClusterConfigMaps(ctx)
	if err != nil {
		errs = append(errs, err.Error())
	}

	err = c.removeManagedClusterViews(ctx)
	if err != nil {
		errs = append(errs, err.Error())
	}

	if len(errs) > 0 {
		klog.Errorf("Errors were encountered while removing managed cluster resources: %v", errs)
		return errors.New(strings.Join(errs, "\n"))
	}

	return nil
}

func (c *ManagedClusterController) removeManagedClusterViews(ctx context.Context) error {
	errs := []string{}
	mcvs, err := c.dynamicClient.Resource(api.ManagedClusterViewGroupVersionResource).List(ctx, metav1.ListOptions{LabelSelector: api.ManagedClusterLabel})

	if apierrors.IsNotFound(err) {
		return nil
	}

	if err != nil {
		return err
	}

	if len(mcvs.Items) == 0 {
		return nil
	}

	for _, mcv := range mcvs.Items {
		deletionErr := c.dynamicClient.Resource(api.ManagedClusterViewGroupVersionResource).Namespace(mcv.GetNamespace()).Delete(ctx, mcv.GetName(), metav1.DeleteOptions{})
		if deletionErr != nil && !apierrors.IsNotFound(deletionErr) {
			errs = append(errs, deletionErr.Error())
		}
	}

	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "\n"))
	}
	return nil
}

func (c *ManagedClusterController) removeManagedClusterConfigMaps(ctx context.Context) error {
	errs := []string{}
	configMaps, err := c.configMapClient.ConfigMaps(api.OpenShiftConsoleNamespace).List(ctx, metav1.ListOptions{LabelSelector: api.ManagedClusterLabel})

	if err != nil {
		return err
	}

	if len(configMaps.Items) == 0 {
		return nil
	}

	for _, configMap := range configMaps.Items {
		deletionErr := c.configMapClient.ConfigMaps(api.OpenShiftConsoleNamespace).Delete(ctx, configMap.GetName(), metav1.DeleteOptions{})
		if deletionErr != nil && !apierrors.IsNotFound(deletionErr) {
			errs = append(errs, deletionErr.Error())
		}
	}

	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "\n"))
	}
	return nil
}
