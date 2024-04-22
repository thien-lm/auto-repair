package core

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/phongvu0403/secret-manager/core/utils"
	"github.com/phongvu0403/secret-manager/utils/errors"
	// "repair/context"
	"github.com/phongvu0403/secret-manager/processors/status"

	// appsv1 "k8s.io/api/apps/v1"
	apiv1 "k8s.io/api/core/v1"
	kube_client "k8s.io/client-go/kubernetes"
	klog "k8s.io/klog/v2"
)

type scaleUpResourcesLimits map[string]int64
type scaleUpResourcesDelta map[string]int64

// used as a value in scaleUpResourcesLimits if actual limit could not be obtained due to errors talking to cloud provider
const scaleUpLimitUnknown = math.MaxInt64


func computeBelowMax(total int64, max int64) int64 {
	if total < max {
		return max - total
	}
	return 0
}

type scaleUpLimitsCheckResult struct {
	exceeded          bool
	exceededResources []string
}

func scaleUpLimitsNotExceeded() scaleUpLimitsCheckResult {
	return scaleUpLimitsCheckResult{false, []string{}}
}



type skippedReasons struct {
	message []string
}

func (sr *skippedReasons) Reasons() []string {
	return sr.message
}

var (
	backoffReason         = &skippedReasons{[]string{"in backoff after failed scale-up"}}
	maxLimitReachedReason = &skippedReasons{[]string{"max node group size reached"}}
	notReadyReason        = &skippedReasons{[]string{"not ready for scale-up"}}
)

func maxResourceLimitReached(resources []string) *skippedReasons {
	return &skippedReasons{[]string{fmt.Sprintf("max cluster %s limit reached", strings.Join(resources, ", "))}}
}

// ScaleUp tries to scale the cluster up. Return true if it found a way to increase the size,
// false if it didn't and error if an error occurred. Assumes that all nodes in the cluster are
// ready and in sync with instance groups.

func ScaleUp(nodes []*apiv1.Node, kubeclient kube_client.Interface, accessToken, vpcID, idCluster, clusterIDPortal,
	callbackURL string, numberNodeScaleUp int) (*status.ScaleUpStatus, errors.AutorepairError) {
	// From now on we only care about unschedulable pods that were marked after the newest
	// node became available for the scheduler.
	// if len(unschedulablePods) == 0 {
	// 	klog.V(1).Info("No unschedulable pods")
	// 	return &status.ScaleUpStatus{Result: status.ScaleUpNotNeeded}, nil
	// }

	// skippedNodeGroups := map[string]status.Reasons{}
	
	var numberWorkerNode int = 0
	for _, node := range nodes {
		if strings.Contains(node.Name, "worker") {
			numberWorkerNode += 1
		}
	}
	//fmt.Println()
	//fmt.Println("Number of worker node: ", numberWorkerNode)
	// numberNodeScaleUp := CalculateNewNodeScaledUp(kubeclient, nodes)
	// if numberNodeScaleUp == 0 {
	// 	return &status.ScaleUpStatus{
	// 		Result:                  status.ScaleUpNotNeeded,
	// 	}, nil
	// }
	// if (numberWorkerNode + numberNodeScaleUp) > utils.GetMaxSizeNodeGroup(kubeclient) {
	// 	klog.V(4).Infof("Skipping node group - max size reached")
	// 	klog.V(4).Infof("Number of nodes need to be scaled up is: %v", numberNodeScaleUp)
	// 	//fmt.Println("Number of nodes need to be scaled up is: ", numberNodeScaleUp)
	// 	//fmt.Println("Max node group size reached")
	// 	klog.V(4).Infof("Max node group size reached")
	// 	klog.V(4).Infof("You need to increase max group size")
	// 	//fmt.Println("You need to increase max group size")
	// 	numberNodeScaleUp = utils.GetMaxSizeNodeGroup(kubeclient) - numberWorkerNode
	// 	//fmt.Println("scaling up ", numberNodeScaleUp, " node")
	// 	//fmt.Println("waiting for job running in AWX successfully")
	// 	if numberNodeScaleUp == 0 {
	// 		return &status.ScaleUpStatus{
	// 			Result:                  status.ScaleUpNotNeeded,
	// 			// PodsRemainUnschedulable: getRemainingPods(podEquivalenceGroups, skippedNodeGroups),
	// 			//ConsideredNodeGroups:    nodeGroups,
	// 		}, nil
	// 	}
	// }
	//numberNodeScaleUp = numberWorkerNode
	klog.V(4).Infof("Scaling up %v node", numberNodeScaleUp)
	//fmt.Println("scaling up ", numberNodeScaleUp, " node")
	//fmt.Println("waiting for job running in AWX successfully")
	domainAPI := utils.GetDomainApiConformEnv(callbackURL)
	if utils.CheckStatusCluster(domainAPI, vpcID, accessToken, clusterIDPortal) {
		utils.PerformScaleUp(domainAPI, vpcID, accessToken, numberNodeScaleUp, idCluster, clusterIDPortal)
		for {
			time.Sleep(30 * time.Second)
			isSucceededStatus := utils.CheckStatusCluster(domainAPI, vpcID, accessToken, clusterIDPortal)
			//fmt.Println("status of cluster is SCALING")
			klog.V(1).Infof("Status of cluster is SCALING")
			if isSucceededStatus {
				//fmt.Println("status of cluster is SUCCEEDED")
				klog.V(1).Infof("Status of cluster is SUCCEEDED")
				break
			}
			isErrorStatus := utils.CheckErrorStatusCluster(domainAPI, vpcID, accessToken, clusterIDPortal)
			if isErrorStatus {
				utils.PerformScaleUp(domainAPI, vpcID, accessToken, numberNodeScaleUp, idCluster, clusterIDPortal)
				for {
					time.Sleep(30 * time.Second)
					if utils.CheckStatusCluster(domainAPI, vpcID, accessToken, clusterIDPortal) {
						break
					}
				}
				break
			}
		}
	} else {
		klog.V(1).Infof("status of cluster is: %v", utils.CheckStatusCluster(domainAPI, vpcID, accessToken, clusterIDPortal))
		klog.V(1).Infof("Another action is being performed")
		klog.V(1).Infof("Waiting for scaling ...")
		return &status.ScaleUpStatus{
			Result:                  status.ScaleUpNotNeeded,
			// PodsRemainUnschedulable: getRemainingPods(podEquivalenceGroups, skippedNodeGroups),
			//ConsideredNodeGroups:    nodeGroups,
		}, nil
	}

	
	klog.V(1).Infof("End of scale up process")
	return &status.ScaleUpStatus{
		Result:                  status.ScaleUpSuccessful,
		// PodsRemainUnschedulable: getRemainingPods(podEquivalenceGroups, skippedNodeGroups),
		//ConsideredNodeGroups:    nodeGroups,
	}, nil
}




func scaleUpError(s *status.ScaleUpStatus, err errors.AutorepairError) (*status.ScaleUpStatus, errors.AutorepairError) {
	s.ScaleUpError = &err
	s.Result = status.ScaleUpError
	return s, err
}
