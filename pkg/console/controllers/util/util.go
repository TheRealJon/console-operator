package util

import (
	//k8s
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/klog/v2"

	//github
	"github.com/blang/semver"
	"github.com/openshift/console-operator/pkg/api"
	"github.com/openshift/library-go/pkg/controller/factory"
	clusterv1 "open-cluster-management.io/api/cluster/v1"
)

// Return func which returns true if obj name is in names
func IncludeNamesFilter(names ...string) factory.EventFilterFunc {
	nameSet := sets.NewString(names...)
	return func(obj interface{}) bool {
		metaObj := obj.(metav1.Object)
		return nameSet.Has(metaObj.GetName())
	}
}

// Inverse of IncludeNamesFilter
func ExcludeNamesFilter(names ...string) factory.EventFilterFunc {
	return func(obj interface{}) bool {
		return !IncludeNamesFilter(names...)(obj)
	}
}

// Return a func which returns true if obj matches on every label in labels
// (i.e for each key in labels map, obj.metadata.labels[key] is equal to labels[key])
func LabelFilter(labels map[string]string) factory.EventFilterFunc {
	return func(obj interface{}) bool {
		metaObj := obj.(metav1.Object)
		objLabels := metaObj.GetLabels()
		for k, v := range labels {
			if objLabels[k] != v {
				return false
			}
		}
		return true
	}
}

// contains checks if a string is present in a slice
func SliceContains(s []string, value string) bool {
	for _, v := range s {
		if v == value {
			return true
		}
	}
	return false
}

func IsSupportedManagedClusterVersion(productVersion string) bool {
	version, err := semver.Parse(productVersion)
	if err != nil {
		klog.V(4).Infof("unable to parse %q version", productVersion)
		return false
	}
	return version.Compare(semver.MustParse("4.0.0")) == 1
}

func IsSupportedMangedCluster(managedCluster clusterv1.ManagedCluster) (bool, bool) {
	isValidProduct := false
	isValidVersion := false
	for _, claim := range managedCluster.Status.ClusterClaims {
		if claim.Name == api.ManagedClusterClaimProductAnnotation && SliceContains(api.SupportedClusterProducts, claim.Value) {
			isValidProduct = true
			continue
		}
		if claim.Name == api.ManagedClusterClaimVersionAnnotation && IsSupportedManagedClusterVersion(claim.Value) {
			isValidVersion = true
			continue
		}
	}
	return isValidProduct, isValidVersion
}

func IsConditionMet(conditions []metav1.Condition, conditionType string, conditionStatus metav1.ConditionStatus) bool {
	for _, condition := range conditions {
		if condition.Status == conditionStatus && condition.Type == conditionType {
			return true
		}
	}
	return false
}
