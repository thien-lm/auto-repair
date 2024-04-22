package core

import (
	"time"
	
	"github.com/phongvu0403/secret-manager/utils/errors"
	"github.com/phongvu0403/secret-manager/context"
	kube_util "github.com/phongvu0403/secret-manager/utils/kubernetes"
	kube_client "k8s.io/client-go/kubernetes"
	kube_record "k8s.io/client-go/tools/record"
)

// AutorepairOptions is the whole set of options for configuring an autorepair
type AutorepairOptions struct {
	context.AutorepairingOptions
}

// Autorepair is the main component of CA which scales up/down node groups according to its configuration
// The configuration can be injected at the creation of an autorepair
type Autorepair interface {
	// RunOnce represents an iteration in the control-loop of CA
	RunOnce(repairtime time.Duration, kubeclient kube_client.Interface, vpcID, accessToken, idCluster, clusterIDPortal, callbackURL string) (errors.AutorepairError) 
}

// NewAutorepair creates an autorepair of an appropriate type according to the parameters
func NewAutorepair(opts AutorepairOptions, kubeClient kube_client.Interface,
	kubeEventRecorder kube_record.EventRecorder, listerRegistry kube_util.ListerRegistry) (Autorepair, errors.AutorepairError) {

	autorepairBuilder := NewAutorepairBuilder(opts.AutorepairingOptions, kubeClient, kubeEventRecorder, listerRegistry )
	return NewDynamicAutorepair(autorepairBuilder)
}