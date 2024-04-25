/*
Copyright 2016 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package core

import (
	ctx "context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"strings"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/types"

	"github.com/phongvu0403/secret-manager/core/utils"

	// "repair/clusterstate"
	// "repair/context"
	// "k8s.io/autoscaler/cluster-autoscaler/metrics"
	// "repair/processors"
	// "k8s.io/autoscaler/cluster-autoscaler/simulator"
	// "k8s.io/autoscaler/cluster-autoscaler/utils/daemonset"
	// "k8s.io/autoscaler/cluster-autoscaler/utils/deletetaint"
	"github.com/phongvu0403/secret-manager/utils/errors"
	"github.com/phongvu0403/secret-manager/utils/kubernetes"
	// schedulerframework "k8s.io/kubernetes/pkg/scheduler/framework"

	apiv1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1beta1"
	kube_errors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	// "k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	"github.com/phongvu0403/secret-manager/processors/status"
	kube_client "k8s.io/client-go/kubernetes"
	kube_record "k8s.io/client-go/tools/record"
	klog "k8s.io/klog/v2"
)

const (
	// ScaleDownDisabledKey is the name of annotation marking node as not eligible for scale down.
	ScaleDownDisabledKey = "cluster-autoscaler.kubernetes.io/scale-down-disabled"
	// DelayDeletionAnnotationPrefix is the prefix of annotation marking node as it needs to wait
	// for other K8s components before deleting node.
	DelayDeletionAnnotationPrefix = "delay-deletion.cluster-autoscaler.kubernetes.io/"
)

const (
	// MaxKubernetesEmptyNodeDeletionTime is the maximum time needed by Kubernetes to delete an empty node.
	MaxKubernetesEmptyNodeDeletionTime = 3 * time.Minute
	// MaxCloudProviderNodeDeletionTime is the maximum time needed by cloud provider to delete a node.
	MaxCloudProviderNodeDeletionTime = 5 * time.Minute
	// MaxPodEvictionTime is the maximum time CA tries to evict a pod before giving up.
	MaxPodEvictionTime = 2 * time.Minute
	// EvictionRetryTime is the time after CA retries failed pod eviction.
	EvictionRetryTime = 10 * time.Second
	// PodEvictionHeadroom is the extra time we wait to catch situations when the pod is ignoring SIGTERM and
	// is killed with SIGKILL after MaxGracefulTerminationTime
	PodEvictionHeadroom = 30 * time.Second
	// DaemonSetEvictionEmptyNodeTimeout is the time to evict all DaemonSet pods on empty node
	DaemonSetEvictionEmptyNodeTimeout = 10 * time.Second
	// DeamonSetTimeBetweenEvictionRetries is a time between retries to create eviction that uses for DaemonSet eviction for empty nodes
	DeamonSetTimeBetweenEvictionRetries = 3 * time.Second
)

// NodeDeletionTracker keeps track of node deletions.
type NodeDeletionTracker struct {
	sync.Mutex
	nonEmptyNodeDeleteInProgress bool
	// A map of node delete results by node name. It's being constantly emptied into ScaleDownStatus
	// objects in order to notify the ScaleDownStatusProcessor that the node drain has ended or that
	// an error occurred during the deletion process.
	nodeDeleteResults map[string]status.NodeDeleteResult
	// A map which keeps track of deletions in progress for nodepools.
	// Key is a node group id and value is a number of node deletions in progress.
	deletionsInProgress map[string]int
}

// Get current time. Proxy for unit tests.
var now func() time.Time = time.Now

// NewNodeDeletionTracker creates new NodeDeletionTracker.
func NewNodeDeletionTracker() *NodeDeletionTracker {
	return &NodeDeletionTracker{
		nodeDeleteResults:   make(map[string]status.NodeDeleteResult),
		deletionsInProgress: make(map[string]int),
	}
}

// IsNonEmptyNodeDeleteInProgress returns true if a non empty node is being deleted.
func (n *NodeDeletionTracker) IsNonEmptyNodeDeleteInProgress() bool {
	n.Lock()
	defer n.Unlock()
	return n.nonEmptyNodeDeleteInProgress
}

// SetNonEmptyNodeDeleteInProgress sets non empty node deletion in progress status.
func (n *NodeDeletionTracker) SetNonEmptyNodeDeleteInProgress(status bool) {
	n.Lock()
	defer n.Unlock()
	n.nonEmptyNodeDeleteInProgress = status
}

// StartDeletion increments node deletion in progress counter for the given nodegroup.
func (n *NodeDeletionTracker) StartDeletion(nodeGroupId string) {
	n.Lock()
	defer n.Unlock()
	n.deletionsInProgress[nodeGroupId]++
}

// EndDeletion decrements node deletion in progress counter for the given nodegroup.
func (n *NodeDeletionTracker) EndDeletion(nodeGroupId string) {
	n.Lock()
	defer n.Unlock()

	value, found := n.deletionsInProgress[nodeGroupId]
	if !found {
		klog.Errorf("This should never happen, counter for %s in DelayedNodeDeletionStatus wasn't found", nodeGroupId)
		return
	}
	if value <= 0 {
		klog.Errorf("This should never happen, counter for %s in DelayedNodeDeletionStatus isn't greater than 0, counter value is %d", nodeGroupId, value)
	}
	n.deletionsInProgress[nodeGroupId]--
	if n.deletionsInProgress[nodeGroupId] <= 0 {
		delete(n.deletionsInProgress, nodeGroupId)
	}
}

// GetDeletionsInProgress returns the number of deletions in progress for the given node group.
func (n *NodeDeletionTracker) GetDeletionsInProgress(nodeGroupId string) int {
	n.Lock()
	defer n.Unlock()
	return n.deletionsInProgress[nodeGroupId]
}

// AddNodeDeleteResult adds a node delete result to the result map.
func (n *NodeDeletionTracker) AddNodeDeleteResult(nodeName string, result status.NodeDeleteResult) {
	n.Lock()
	defer n.Unlock()
	n.nodeDeleteResults[nodeName] = result
}

// GetAndClearNodeDeleteResults returns the whole result map and replaces it with a new empty one.
func (n *NodeDeletionTracker) GetAndClearNodeDeleteResults() map[string]status.NodeDeleteResult {
	n.Lock()
	defer n.Unlock()
	results := n.nodeDeleteResults
	n.nodeDeleteResults = make(map[string]status.NodeDeleteResult)
	return results
}

type scaleDownResourcesLimits map[string]int64
type scaleDownResourcesDelta map[string]int64

// used as a value in scaleDownResourcesLimits if actual limit could not be obtained due to errors talking to cloud provider
const scaleDownLimitUnknown = math.MinInt64


// TryToScaleDown tries to scale down the cluster. It returns a result inside a ScaleDownStatus indicating if any node was
// removed and error if such occurred.
func ScaleDown(currentTime time.Time, kubeclient kube_client.Interface,
	accessToken, vpcID, idCluster, clusterIDPortal, callbackURL, workerNameToRemove  string) (*status.ScaleDownStatus, errors.AutorepairError) {
	// scaleDownStatus := &status.ScaleDownStatus{NodeDeleteResults: sd.nodeDeletionTracker.GetAndClearNodeDeleteResults()}
	scaleDownStatus := &status.ScaleDownStatus{}
	// nodeDeletionDuration := time.Duration(0)
	// findNodesToRemoveDuration := time.Duration(0)

	// candidateNames := make([]string, 0)



	// if len(candidateNames) == 0 {
	// 	klog.V(1).Infof("No candidates for scale down")
	// 	scaleDownStatus.Result = status.ScaleDownNoUnneeded
	// 	return scaleDownStatus, nil
	// }


	//var workerNameToRemove string
	//klog.V(1).Infof("Find nodes to remove")
	// for _, nodeName := range nodesWithoutMasterNames {
	// 	if strings.HasSuffix(nodeName, "worker"+strconv.Itoa(len(nodesWithoutMasterNames))) {
	// 		workerNameToRemove = nodeName
	// 	}
	// }
	
	if !CheckWorkerNodeCanBeScaleDown(kubeclient, workerNameToRemove) {
		klog.V(1).Infof("Cannot perform scale down action")
		scaleDownStatus.Result = status.ScaleDownNoUnneeded
		return scaleDownStatus, nil
	}
	klog.V(1).Infof("Scaling down node %s", workerNameToRemove)

	domainAPI := utils.GetDomainApiConformEnv(callbackURL)
	workerNodeNameList := make([]string, 0)
	workerNodeNameList = append(workerNodeNameList, workerNameToRemove) //draft

	if utils.CheckStatusCluster(domainAPI, vpcID, accessToken, clusterIDPortal) {
		//cordonWorkerNodeAndDeletePod(kubeclient, workerNameToRemove)

		utils.PerformScaleDown(domainAPI, vpcID, accessToken, idCluster, clusterIDPortal, workerNodeNameList) // draft
		for {
			var count int = 0
			time.Sleep(30 * time.Second)
			isSucceededStatus := utils.CheckStatusCluster(domainAPI, vpcID, accessToken, clusterIDPortal)
			//fmt.Println("status of cluster is SCALING")
			klog.V(1).Infof("Status of cluster is SCALING")
			count = count + 1
			if isSucceededStatus {
				//fmt.Println("status of cluster is SUCCEEDED")
				klog.V(1).Infof("Status of cluster is SUCCEEDED")
				break
			}
			if count > 100 {
				break //break if timeout (50 minutes)
			}
			isErrorStatus := utils.CheckErrorStatusCluster(domainAPI, vpcID, accessToken, clusterIDPortal)
			if isErrorStatus {
				utils.PerformScaleDown(domainAPI, vpcID, accessToken, idCluster, clusterIDPortal, workerNodeNameList)
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
			fmt.Printf("debugging %s", workerNameToRemove)

		klog.V(1).Infof("Another action is being performed")
		klog.V(1).Infof("Waiting for scaling ...")
		scaleDownStatus.Result = status.ScaleDownNoUnneeded
		return scaleDownStatus, nil
	}

	fmt.Printf("done scale down node %s", workerNameToRemove)

	//scaleDownStatus.ScaledDownNodes = sd.mapNodesToStatusScaleDownNodes([]*apiv1.Node{toRemove.Node}, candidateNodeGroups,
	// map[string][]*apiv1.Pod{toRemove.Node.Name: toRemove.PodsToReschedule})
	scaleDownStatus.Result = status.ScaleDownNodeDeleteStarted
	//fmt.Println("end of scale down process")
	klog.V(1).Infof("End of scale down process")
	return scaleDownStatus, nil
}



func evictPod(podToEvict *apiv1.Pod, isDaemonSetPod bool, client kube_client.Interface, recorder kube_record.EventRecorder,
	maxGracefulTerminationSec int, retryUntil time.Time, waitBetweenRetries time.Duration) status.PodEvictionResult {
	recorder.Eventf(podToEvict, apiv1.EventTypeNormal, "ScaleDown", "deleting pod for node scale down")

	maxTermination := int64(apiv1.DefaultTerminationGracePeriodSeconds)
	if podToEvict.Spec.TerminationGracePeriodSeconds != nil {
		if *podToEvict.Spec.TerminationGracePeriodSeconds < int64(maxGracefulTerminationSec) {
			maxTermination = *podToEvict.Spec.TerminationGracePeriodSeconds
		} else {
			maxTermination = int64(maxGracefulTerminationSec)
		}
	}

	var lastError error
	for first := true; first || time.Now().Before(retryUntil); time.Sleep(waitBetweenRetries) {
		first = false
		eviction := &policyv1.Eviction{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: podToEvict.Namespace,
				Name:      podToEvict.Name,
			},
			DeleteOptions: &metav1.DeleteOptions{
				GracePeriodSeconds: &maxTermination,
			},
		}
		lastError = client.CoreV1().Pods(podToEvict.Namespace).Evict(ctx.TODO(), eviction)
		if lastError == nil || kube_errors.IsNotFound(lastError) {
			return status.PodEvictionResult{Pod: podToEvict, TimedOut: false, Err: nil}
		}
	}
	if !isDaemonSetPod {
		klog.Errorf("Failed to evict pod %s, error: %v", podToEvict.Name, lastError)
		recorder.Eventf(podToEvict, apiv1.EventTypeWarning, "ScaleDownFailed", "failed to delete pod for ScaleDown")
	}
	return status.PodEvictionResult{Pod: podToEvict, TimedOut: true, Err: fmt.Errorf("failed to evict pod %s/%s within allowed timeout (last error: %v)", podToEvict.Namespace, podToEvict.Name, lastError)}
}

func waitForDelayDeletion(node *apiv1.Node, nodeLister kubernetes.NodeLister, timeout time.Duration) errors.AutorepairError {
	if timeout != 0 && hasDelayDeletionAnnotation(node) {
		klog.V(1).Infof("Wait for removing %s annotations on node %v", DelayDeletionAnnotationPrefix, node.Name)
		err := wait.Poll(5*time.Second, timeout, func() (bool, error) {
			klog.V(5).Infof("Waiting for removing %s annotations on node %v", DelayDeletionAnnotationPrefix, node.Name)
			freshNode, err := nodeLister.Get(node.Name)
			if err != nil || freshNode == nil {
				return false, fmt.Errorf("failed to get node %v: %v", node.Name, err)
			}
			return !hasDelayDeletionAnnotation(freshNode), nil
		})
		if err != nil && err != wait.ErrWaitTimeout {
			return errors.ToAutoscalerError(errors.ApiCallError, err)
		}
		if err == wait.ErrWaitTimeout {
			klog.Warningf("Delay node deletion timed out for node %v, delay deletion annotation wasn't removed within %v, this might slow down scale down.", node.Name, timeout)
		} else {
			klog.V(2).Infof("Annotation %s removed from node %v", DelayDeletionAnnotationPrefix, node.Name)
		}
	}
	return nil
}

func hasDelayDeletionAnnotation(node *apiv1.Node) bool {
	for annotation := range node.Annotations {
		if strings.HasPrefix(annotation, DelayDeletionAnnotationPrefix) {
			return true
		}
	}
	return false
}

func hasNoScaleDownAnnotation(node *apiv1.Node) bool {
	return node.Annotations[ScaleDownDisabledKey] == "true"
}

const (
	apiServerLabelKey   = "component"
	apiServerLabelValue = "kube-apiserver"
)


type patchStringValue struct {
	Op    string `json:"op"`
	Path  string `json:"path"`
	Value bool   `json:"value"`
}

func cordonWorkerNodeAndDeletePod(kubeclient kube_client.Interface, workerName string) {
	payload := []patchStringValue{{
		Op:    "replace",
		Path:  "/spec/unschedulable",
		Value: true,
	}}
	payloadBytes, _ := json.Marshal(payload)
	klog.V(1).Infof("Cordon node %s", workerName)
	kubeclient.CoreV1().Nodes().Patch(ctx.Background(), workerName, types.JSONPatchType, payloadBytes, metav1.PatchOptions{})
	pods, err := kubeclient.CoreV1().Pods("").List(ctx.Background(), metav1.ListOptions{})
	if err != nil {
		log.Fatal(err)
	}
	var gracePeriodSeconds int64 = 30
	for _, pod := range pods.Items {
		if pod.Spec.NodeName == workerName && pod.OwnerReferences[0].Kind != "DaemonSet" {
			// for _, volume := range pod.Spec.Volumes {
			// 	if volume.EmptyDir != nil {
			// 		klog.V(1).Infof("Evict pod %s", pod.Name)
			// 		// fmt.Println(pod.Name)
			// 		kubeclient.CoreV1().Pods(pod.Namespace).Delete(ctx.Background(), pod.Name, metav1.DeleteOptions{GracePeriodSeconds: &gracePeriodSeconds})
			// 	}
			// }
			kubeclient.CoreV1().Pods(pod.Namespace).Delete(ctx.Background(), pod.Name, metav1.DeleteOptions{GracePeriodSeconds: &gracePeriodSeconds})
		}
	}
}

func CheckWorkerNodeCanBeScaleDown(kubeclient kube_client.Interface, workerNodeName string) bool {
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
					fmt.Printf("If you want to scale down, you should evict pod %s in namespace %s "+
					"because your replicaset %s has only one replica", pod.Name, pod.Namespace,
					replicaset.Name)	
				canBeRemove = false
			}
			for _, volume := range pod.Spec.Volumes {
				if volume.EmptyDir != nil {
					klog.V(1).Infof("If you want to scale down, you should evict pod %s"+
						" in namespace %s because pod has local storage", pod.Name, pod.Namespace)
					fmt.Printf("If you want to scale down, you should evict pod %s"+
						" in namespace %s because pod has local storage", pod.Name, pod.Namespace)	
					canBeRemove = false
				}
			}
		}
	}
	fmt.Printf("can be remove: %v /n", canBeRemove)
	return canBeRemove
}
