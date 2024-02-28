package features

import (
	"k8s.io/apimachinery/pkg/util/runtime"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/component-base/featuregate"

	configv1 "github.com/openshift/api/config/v1"
)

var OpenshiftFeatureGates = []configv1.FeatureGateName{
	configv1.FeatureGateRouteExternalCertificate,
}

func init() {
	registerOpenshiftFeatures()
}

func registerOpenshiftFeatures() {
	osKubeFeatureGates := map[featuregate.Feature]featuregate.FeatureSpec{}

	for _, featureGateName := range OpenshiftFeatureGates {
		osFeature := featuregate.Feature(featureGateName)

		osKubeFeatureGates[osFeature] = featuregate.FeatureSpec{
			Default: false, PreRelease: featuregate.Alpha,
		}
	}

	runtime.Must(utilfeature.DefaultMutableFeatureGate.Add(osKubeFeatureGates))
}
