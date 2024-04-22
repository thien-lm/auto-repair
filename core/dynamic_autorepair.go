package core

import (
	"time"
	"github.com/phongvu0403/secret-manager/utils/errors"
	kube_client "k8s.io/client-go/kubernetes"
)

// DynamicAutorepair is a variant of autorepair which supports dynamic reconfiguration at runtime
type DynamicAutorepair struct {
	autorepair        Autorepair
	autorepairBuilder AutorepairBuilder
}

// NewDynamicAutorepair builds a DynamicAutorepair from required parameters
func NewDynamicAutorepair(autorepairBuilder AutorepairBuilder) (*DynamicAutorepair, errors.AutorepairError) {
	autorepair, err := autorepairBuilder.Build()
	if err != nil {
		return nil, err
	}
	return &DynamicAutorepair{
		autorepair:        autorepair,
		autorepairBuilder: autorepairBuilder,
	}, nil
}

// Dynamic method used to invoke the static RunOnce method to repair nodes
func (a *DynamicAutorepair) RunOnce(repairtime time.Duration, kubeclient kube_client.Interface, vpcID, accessToken, idCluster, clusterIDPortal, callbackURL string) (errors.AutorepairError) {
	return a.autorepair.RunOnce(repairtime, kubeclient, vpcID, accessToken, idCluster, clusterIDPortal, callbackURL)
}
