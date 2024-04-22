
package core

import (
	ctx "context"
	"log"
	"time"
	"flag"
	"fmt"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	// "github.com/golang/glog"
	// "k8s.io/kubernetes/plugin/pkg/scheduler/schedulercache"
	// "repair/simulator"
	// "repair/cloudprovider"
	// "repair/processors/status"
	"github.com/phongvu0403/secret-manager/core/utils"
	"strings"

	apiv1 "k8s.io/api/core/v1"
	kube_client "k8s.io/client-go/kubernetes"
	klog "k8s.io/klog/v2"

)

var (
	skipNodesWithSystemPods = flag.Bool("skip-nodes-with-system-pods", false,
		"If true Cluster autorepair will never delete nodes with pods from kube-system (except for DaemonSet "+
			"or mirror pods)")
	skipNodesWithLocalStorage = flag.Bool("skip-nodes-with-local-storage", false,
		"If true Cluster autorepair will never delete nodes with pods with local storage, e.g. EmptyDir or HostPath")

	minReplicaCount = flag.Int("min-replica-count", 0,
		"Minimum number or replicas that a replica set or replication controller should have to allow their pods deletion in auto repair")
)

// Get common nodes between two nodelists
func CommonNodes(nodelist1 []*apiv1.Node, nodelist2 []*apiv1.Node) []*apiv1.Node {
	var commonElems [] *apiv1.Node
	for _, elem := range nodelist1 {
		if ContainsNode(elem, nodelist2) {
			commonElems = append(commonElems, elem)
		}
	}
	return commonElems
}

// Check if nodelist contains passed node
func ContainsNode(node *apiv1.Node, nodelist []*apiv1.Node) bool {
	for _, elem := range nodelist {
		if node.Name == elem.Name {
			return true
		}
	}
	return false	
}

// Calculate possible expansion size for a ASG based on allowable maximum size
func (a *StaticAutorepair) CalcExpandSize(kubeclient kube_client.Interface, toBeRepairedNodes []* apiv1.Node) int {
	currReadyNodes, _ := a.AllNodeLister().List()
	desiredSize, _ := WorkerNodeCount(currReadyNodes)
	maxSize := utils.GetMaxSizeNodeGroup(kubeclient)

	desiredIncreaseSize, _ := WorkerNodeCount(toBeRepairedNodes)
    allowableIncreaseSize := maxSize - desiredSize
	effectiveIncreaseSize := 0

    if allowableIncreaseSize >= desiredIncreaseSize {
    	effectiveIncreaseSize = desiredIncreaseSize
    } else {
    	effectiveIncreaseSize = allowableIncreaseSize
    }

    return effectiveIncreaseSize
}

// // Method used to extract not ready nodes for a particular cluster, notReadyNodes passed (maybe stale node status)
// func (a *StaticAutorepair) AsgNotReadyNodes(desiredAsg cloudprovider.NodeGroup, notReadyNodes []* apiv1.Node) ([]*apiv1.Node, error) {
// 	belongsToAsg := make([]*apiv1.Node, 0, len(notReadyNodes))
	
// 	for  _, node := range notReadyNodes {	
// 		asg, err := a.AutorepairingContext.CloudProvider.NodeGroupForNode(node)
// 		if err != nil {
// 			glog.Errorf("Failed to get node group: %v", err)
// 			return []*apiv1.Node{}, err
// 		}

// 		if asg == desiredAsg {
// 			belongsToAsg = append(belongsToAsg, node)
// 		}
// 	}

// 	return belongsToAsg, nil
// }

// // Ready ASG nodes based on real-time, used to poll on newly created nodes (Latest node status)
// func (a *StaticAutorepair) AsgReadyNodes(desiredAsg cloudprovider.NodeGroup) ([]*apiv1.Node, error) {
// 	ReadyNodeLister := a.ReadyNodeLister()
// 	readyNodes, _ := ReadyNodeLister.List()
	
// 	belongsToAsg := make([]*apiv1.Node, 0, len(readyNodes))
// 	for  _, node := range readyNodes {
		
// 		asg, err := a.AutorepairingContext.CloudProvider.NodeGroupForNode(node)
// 		if err != nil {
// 			glog.Errorf("Failed to get node group: %v", err)
// 			return []*apiv1.Node{}, err
// 		}

// 		if asg == desiredAsg {
// 			belongsToAsg = append(belongsToAsg, node)
// 		}
// 	}

// 	return belongsToAsg, nil
// }

// Increases size of ASG, Blocking call untill all nodes are ready
func (a *StaticAutorepair) IncreaseSize(kubeclient kube_client.Interface, toBeRepairedNodes []* apiv1.Node, incSize int,
	domainAPI, vpcID, accessToken, idCluster, clusterIDPortal string) {
	sleepTime := 60 * time.Second
	timeOut := 30
	iter := 0
	currReadyNodes, _ := a.AllNodeLister().List()
	size, _ := WorkerNodeCount(currReadyNodes)
	prevSize := size
	currSize := size
	// domainAPI := utils.GetDomainApiConformEnv(env, stg)
	if utils.CheckStatusCluster(domainAPI, vpcID, accessToken, clusterIDPortal) {
		for i := incSize; i > 0; i-- {
			klog.V(4).Infof("Scaling up %v node", i)
			response, _ := utils.PerformScaleUp(domainAPI, vpcID, accessToken, i, idCluster, clusterIDPortal)
			if strings.Contains(response, "quota reached") {
				klog.V(4).Infof("Quota of VPC reached")
				continue
			} else {
				break
			}
		}
		// Waiting for nodes to become ready
		for (currSize != prevSize + incSize) && (iter < timeOut) {
			time.Sleep(10 * time.Second)
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
				for i := incSize; i > 0; i-- {
					klog.V(4).Infof("Scaling up %v node", i)
					response, _ := utils.PerformScaleUp(domainAPI, vpcID, accessToken, i, idCluster, clusterIDPortal)
					if strings.Contains(response, "quota reached") {
						klog.V(4).Infof("Quota of VPC reached")
						continue
					} else {
						break
					}
				}
				// utils.PerformScaleUp(domainAPI, vpcID, accessToken, incSize, idCluster, clusterIDPortal)
				time.Sleep(3 * sleepTime)
			}
			time.Sleep(sleepTime)
    	
			currReadyNodes, _ := a.AllNodeLister().List()
			currSize, _ = WorkerNodeCount(currReadyNodes)
			iter += 1
		}
		time.Sleep(sleepTime * 3)
	} else {
		klog.V(4).Infof("status of cluster is: %v", utils.CheckStatusCluster(domainAPI, vpcID, accessToken, clusterIDPortal))
		klog.V(4).Infof("Another action is being performed")
		klog.V(4).Infof("Waiting for scaling ...")
		return
	}

// 	Waiting for nodes to become ready
    // for (currSize != prevSize + incSize) && (iter < timeOut)  {
    // 	//glog.Infof("Waiting for %d node(s) to become ready", prevSize + incSize - currSize)
    // 	time.Sleep(sleepTime)
 // 	currReadyNodes, _ := a.AllNodeLister().List()
    // 	currSize, _ = WorkerNodeCount(currReadyNodes)
    // 	iter += 1
    // }
	if (currSize != prevSize){
		//glog.Info("Newly created nodes are now ready !\n")
		fmt.Printf("Newly created nodes are now ready !\n")
	} else {
		fmt.Printf("IncreaseSize Fail !\n")
	}
    
    return 
}

// // Delete nodes from ASG, Blocking call
func (a *StaticAutorepair) DeleteNodes(kubeclient kube_client.Interface, nodeList []*apiv1.Node, domainAPI, vpcID, accessToken, idCluster, clusterIDPortal string) {
	sleepTime := 30 * time.Second
	timeOut := 6
	iter := 0

	// decSize := len(nodeList)
	decSize, workerNameToRemoveList := WorkerNodeCount(nodeList)

	currReadyNodes, _ := a.AllNodeLister().List()
	size, _ := WorkerNodeCount(currReadyNodes)
	prevSize := size
	currSize := size

// 	pdbs, err := a.PodDisruptionBudgetLister().List()
// 	allScheduledPods, err := a.ScheduledPodLister().List()

// 	if err != nil {
// 		glog.Errorf("Failed to list scheduled pods: %v", err)	
// 	}
	if utils.CheckStatusCluster(domainAPI, vpcID, accessToken, clusterIDPortal) {
		utils.PerformScaleDown(domainAPI, vpcID, accessToken, idCluster, clusterIDPortal, workerNameToRemoveList)
		// Waiting for node to be deleted
		for (currSize != prevSize - decSize) && (iter < timeOut)  {		
			fmt.Println("Waiting for ", currSize + decSize - prevSize,"node(s) to be deleted")
			currReadyNodes, _ := a.AllNodeLister().List()
			currSize, _ = WorkerNodeCount(currReadyNodes)
			time.Sleep(4 * sleepTime)

			iter += 1
		}
		isErrorStatus := utils.CheckErrorStatusCluster(domainAPI, vpcID, accessToken, clusterIDPortal)
		if isErrorStatus {
			utils.PerformScaleDown(domainAPI, vpcID, accessToken, idCluster, clusterIDPortal, workerNameToRemoveList)
			// Waiting for node to be deleted
			iter = 0
			for (currSize != prevSize - decSize) && (iter < timeOut)  {		
				fmt.Println("Waiting for ", currSize + decSize - prevSize,"node(s) to be deleted")
				currReadyNodes, _ := a.AllNodeLister().List()
				currSize, _ = WorkerNodeCount(currReadyNodes)
				time.Sleep(4 * sleepTime)
				iter += 1
			}
			time.Sleep(3 * sleepTime)
		}
		time.Sleep(sleepTime)
		time.Sleep(sleepTime * 6)
	} else {
		klog.V(4).Infof("status of cluster is: %v", utils.CheckStatusCluster(domainAPI, vpcID, accessToken, clusterIDPortal))
		klog.V(4).Infof("Another action is being performed")
		klog.V(4).Infof("Waiting for scaling ...")
		return
	}

	if (currSize != prevSize){
		//glog.Info("Newly created nodes are now ready !\n")
		fmt.Printf("Remove NodeReady Node Successful !\n")
	} else {
		fmt.Printf("DeleteNodes Fail !\n")
	}


// 		var podsToRemove []*apiv1.Pod
	// 	err := deleteNode(a.AutorepairingContext, node, podsToRemove)
	// 	if err != nil {
	// 		glog.Errorf("Failed to delete %s: %v", node.Name, err)
	// 		return
	// 	}
	// }


// 	// Waiting for node to be deleted
		// for (currSize != prevSize - decSize) && (iter < timeOut)  {		
		// 	fmt.Println("Waiting for ", currSize + decSize - prevSize,"node(s) to be deleted")
		// 	time.Sleep(sleepTime)
		// 	currReadyNodes, _ := a.AllNodeLister().List()
		// 	currSize, _ = WorkerNodeCount(currReadyNodes)
		// }
// 	//glog.Info("Nodes are successfully deleted !\n")
}

// Repair nodes in a particular ASG
func (a *StaticAutorepair) AsgRepairNodes(kubeclient kube_client.Interface, toBeRepairedNodes []*apiv1.Node, domainAPI, vpcID, accessToken, idCluster, clusterIDPortal string) {
	allNode, _ := a.AllNodeLister().List()
	desiredSize, _ := WorkerNodeCount(allNode) //+  WorkerNodeCount(toBeRepairedNodes)
	maxSize := utils.GetMaxSizeNodeGroup(kubeclient)
	// numberNodeScaleUp := len(toBeRepairedNodes)
	toBeRepairedNodesSize, _ := WorkerNodeCount(toBeRepairedNodes)
	if toBeRepairedNodesSize == 0 {
		return
	} else {
		incSize := 1
		if desiredSize >= maxSize {
			// Delete first as ASG is already at max size 
			a.DeleteNodes(kubeclient, toBeRepairedNodes[0:incSize], domainAPI, vpcID, accessToken, idCluster, clusterIDPortal)	
			a.IncreaseSize(kubeclient, toBeRepairedNodes[0:incSize], incSize, domainAPI, vpcID, accessToken, idCluster, clusterIDPortal)
			// return
		} else{
			incSize = a.CalcExpandSize(kubeclient, toBeRepairedNodes)
			a.IncreaseSize(kubeclient, toBeRepairedNodes[0:incSize], incSize, domainAPI, vpcID, accessToken, idCluster, clusterIDPortal)
			a.DeleteNodes(kubeclient, toBeRepairedNodes[0:incSize],domainAPI, vpcID, accessToken, idCluster, clusterIDPortal)
			// return
		}
		// Recurrisvely call ASG until its healthy
		a.AsgRepairNodes(kubeclient, toBeRepairedNodes[incSize:], domainAPI, vpcID, accessToken, idCluster, clusterIDPortal)
		return
	}
}

// count the number of worker nodes
func WorkerNodeCount(nodes []*apiv1.Node) (int, []string) {
	numberWorkerNode := 0
	var workerNodeNameList []string

	for _, node := range nodes {
		if strings.Contains(node.Name, "worker") {
			numberWorkerNode += 1
			workerNodeNameList = append(workerNodeNameList, node.Name)
		}
	}
	return numberWorkerNode, workerNodeNameList
}


// Repair nodes for a particular kubernetes cluster
func (a *StaticAutorepair) RepairNodes(toBeRepairedNodes []*apiv1.Node, kubeclient kube_client.Interface, vpcID, accessToken, idCluster, clusterIDPortal,
	callbackURL string) {
	currentTime := time.Now()
	// scaleUpStatus, typedErr := ScaleUp(toBeRepairedNodes, kubeclient, accessToken, vpcID, idCluster, clusterIDPortal, callbackURL, 0)


	// if typedErr != nil {
	// 	klog.Errorf("Failed to scale up: %v", typedErr)
	// 	return
	// }
	
	// if scaleUpStatus.Result == status.ScaleUpSuccessful {
	// 	a.lastScaleUpTime = currentTime
	// 	return
	// }
	// klog.V(4).Infof("Starting scale down")
	// fmt.Println("Starting scale down")
	// scaleDownStart := time.Now()
	// scaleDownStatus, typedErr := ScaleDown(scaleDownStart, kubeclient, accessToken, vpcID, idCluster,
	// 	clusterIDPortal, callbackURL)
	// if scaleDownStatus.Result == status.ScaleDownNodeDeleteStarted {
	// 	a.lastScaleDownDeleteTime = currentTime
	// 	//a.clusterStateRegistry.Recalculate()
	// }
	// if typedErr != nil {
	// 	klog.Errorf("Failed to scale down: %v", typedErr)
	// 	a.lastScaleDownFailTime = currentTime
	// }
	domainAPI := utils.GetDomainApiConformEnv(callbackURL)
	a.AsgRepairNodes(kubeclient, toBeRepairedNodes, domainAPI, vpcID, accessToken, idCluster, clusterIDPortal)
	a.lastScaleDownFailTime = currentTime
	return
}

func checkWorkerNodeCanBeRemove(kubeclient kube_client.Interface, workerNodeName string) bool {
	var canBeRemove bool = true
	pods, err := kubeclient.CoreV1().Pods("").List(ctx.Background(), metav1.ListOptions{})
	if err != nil {
		log.Fatal(err)
	}
	for _, pod := range pods.Items {
		if pod.Spec.NodeName == workerNodeName && pod.OwnerReferences[0].Kind != "DaemonSet" {
			replicaset, _ := kubeclient.AppsV1().ReplicaSets(pod.Namespace).Get(ctx.Background(),
				pod.OwnerReferences[0].Name, metav1.GetOptions{})
			//if err != nil {
			//	log.Fatal(err)
			//}
			if replicaset.Status.Replicas == 1 {
				klog.V(1).Infof("If you want to scale down, you should evict pod %s in namespace %s "+
					"because your replicaset %s has only one replica", pod.Name, pod.Namespace,
					replicaset.Name)
				canBeRemove = false
			}
			for _, volume := range pod.Spec.Volumes {
				if volume.EmptyDir != nil {
					klog.V(1).Infof("If you want to scale down, you should evict pod %s"+
						" in namespace %s because pod has local storage", pod.Name, pod.Namespace)
					canBeRemove = false
				}
			}
		}
	}
	return canBeRemove
}
