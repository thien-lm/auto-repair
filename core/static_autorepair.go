package core

import (
	"fmt"
	"time"

	"github.com/golang/glog"

	"github.com/phongvu0403/secret-manager/clusterstate/utils"
	"github.com/phongvu0403/secret-manager/utils/errors"
	"github.com/phongvu0403/secret-manager/context"

	kube_util "github.com/phongvu0403/secret-manager/utils/kubernetes"

	kube_client "k8s.io/client-go/kubernetes"
	kube_record "k8s.io/client-go/tools/record"
)

// StaticAutorepair is an autorepair which has all the core functionality of a CA but without the reconfiguration feature
type StaticAutorepair struct {
	*context.AutorepairingContext
	kube_util.ListerRegistry
	lastScaleUpTime         time.Time
	lastScaleDownDeleteTime time.Time
	lastScaleDownFailTime   time.Time
}

// NewStaticAutorepair creates an instance of Autorepair filled with provided parameters
func NewStaticAutorepair(opts context.AutorepairingOptions,
	kubeClient kube_client.Interface, kubeEventRecorder kube_record.EventRecorder, listerRegistry kube_util.ListerRegistry) (*StaticAutorepair, errors.AutorepairError) {
	logRecorder, err := utils.NewStatusMapRecorder(kubeClient, opts.ConfigNamespace, kubeEventRecorder, opts.WriteStatusConfigMap, opts.StatusConfigMapName)
	if err != nil {
		glog.Error("Failed to initialize status configmap, unable to write status events")
		logRecorder, _ = utils.NewStatusMapRecorder(kubeClient, opts.ConfigNamespace, kubeEventRecorder, false, opts.StatusConfigMapName)
	}
	autorepairingContext, errctx := context.NewAutorepairContext(opts, kubeClient, kubeEventRecorder, logRecorder, listerRegistry)
	if errctx != nil {
		return nil, errctx
	}

	return &StaticAutorepair{
		AutorepairingContext:    autorepairingContext,
		ListerRegistry:          listerRegistry,
		lastScaleUpTime:         time.Now(),
		lastScaleDownDeleteTime: time.Now(),
		lastScaleDownFailTime:   time.Now(),
	}, nil
}

// RunOnce iterates over node groups and repairs them if necessary
func (a *StaticAutorepair) RunOnce(repairtime time.Duration, kubeclient kube_client.Interface, vpcID,
	accessToken, idCluster, clusterIDPortal, callbackURL string) (errors.AutorepairError) {
	sleepTime := repairtime
	restartDelay := 1 * time.Minute

	notReadyNodeLister := a.NotReadyNodeLister()
	notReadyNodes, _ := notReadyNodeLister.List()
	if len(notReadyNodes) == 0 {
		//glog.Infof("All the nodes are healthy")
		return nil
	}
	for  _, node := range notReadyNodes {	
		fmt.Printf("%s",node.GetName())
	}
	fmt.Printf("Number of not ready nodes detected: %v\n", len(notReadyNodes))
 	fmt.Printf("Waiting for %v to confirm unhealthy nodes\n", repairtime)

	time.Sleep(sleepTime)

	stillNotReadyNodeLister := a.NotReadyNodeLister()
	stillNotReadyNodes, _ := stillNotReadyNodeLister.List()

	fmt.Printf("Confirmed unhealthy nodes: %v", len(stillNotReadyNodes))

	toBeRepairedNodes := CommonNodes(notReadyNodes, stillNotReadyNodes)
	if len(toBeRepairedNodes) != 0 {
		fmt.Printf("Auto-Node-Repair: Initializing auto repair. %d nodes unhealthy !", len(notReadyNodes))
		a.RepairNodes(toBeRepairedNodes, kubeclient, vpcID, accessToken, idCluster, clusterIDPortal, callbackURL)
		fmt.Printf("Auto-Node-Repair: Unhealthy nodes have been replaced")
		time.Sleep(restartDelay)
	} /*else{
		glog.Info("Not ready nodes were recovered")
	}*/
	
	return nil
}
