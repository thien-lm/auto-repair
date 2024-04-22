package core

import (
	"github.com/phongvu0403/secret-manager/utils/errors"
	"github.com/phongvu0403/secret-manager/context"
	kube_util "github.com/phongvu0403/secret-manager/utils/kubernetes"
	kube_client "k8s.io/client-go/kubernetes"
	kube_record "k8s.io/client-go/tools/record"
)

// AutorepairBuilder builds an instance of Autorepair which is the core of CA
type AutorepairBuilder interface {
	//SetDynamicConfig(config dynamic.Config) AutorepairBuilder
	Build() (Autorepair, errors.AutorepairError)
}

// AutorepairBuilderImpl builds new autorepairs from its state including initial `AutoscalingOptions` given at startup and
// `dynamic.Config` read on demand from the configmap
type AutorepairBuilderImpl struct {
	autorepairingOptions context.AutorepairingOptions
	// dynamicConfig      *dynamic.Config TODO: Check if we really need dynamic repair based on configMap
	kubeClient         kube_client.Interface
	kubeEventRecorder  kube_record.EventRecorder
	listerRegistry     kube_util.ListerRegistry
}

// NewAutorepairBuilder builds an AutorepairBuilder from required parameters
func NewAutorepairBuilder(autorepairingOptions context.AutorepairingOptions,
	kubeClient kube_client.Interface, kubeEventRecorder kube_record.EventRecorder, listerRegistry kube_util.ListerRegistry) *AutorepairBuilderImpl {
	return &AutorepairBuilderImpl{
		autorepairingOptions: autorepairingOptions,
		kubeClient:         kubeClient,
		kubeEventRecorder:  kubeEventRecorder,
		listerRegistry:     listerRegistry,
	}
}

// Build an autorepair according to the builder's state
func (b *AutorepairBuilderImpl) Build() (Autorepair, errors.AutorepairError) {
	options := b.autorepairingOptions

	return NewStaticAutorepair(options, b.kubeClient, b.kubeEventRecorder, b.listerRegistry)
}
