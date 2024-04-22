package clusterstate

import (
	"fmt"
	"sync"
	"time"

	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"github.com/phongvu0403/secret-manager/clusterstate/api"
	"github.com/phongvu0403/secret-manager/clusterstate/utils"
	// "repair/metrics"
	// schedulerframework "k8s.io/kubernetes/pkg/scheduler/framework"

	// "k8s.io/autoscaler/cluster-autoscaler/metrics"
	// schedulerframework "k8s.io/kubernetes/pkg/scheduler/framework"

	klog "k8s.io/klog/v2"
)

const (
	// MaxNodeStartupTime is the maximum time from the moment the node is registered to the time the node is ready.
	MaxNodeStartupTime = 15 * time.Minute

	// MaxNodeGroupBackoffDuration is the maximum backoff duration for a NodeGroup after new nodes failed to start.
	MaxNodeGroupBackoffDuration = 30 * time.Minute

	// InitialNodeGroupBackoffDuration is the duration of first backoff after a new node failed to start.
	InitialNodeGroupBackoffDuration = 5 * time.Minute

	// NodeGroupBackoffResetTimeout is the time after last failed scale-up when the backoff duration is reset.
	NodeGroupBackoffResetTimeout = 3 * time.Hour
)

// ScaleUpRequest contains information about the requested node group scale up.
type ScaleUpRequest struct {
	// NodeGroup is the node group to be scaled up.
	// NodeGroup cloudprovider.NodeGroup
	// Time is the time when the request was submitted.
	Time time.Time
	// ExpectedAddTime is the time at which the request should be fulfilled.
	ExpectedAddTime time.Time
	// How much the node group is increased.
	Increase int
}

// ScaleDownRequest contains information about the requested node deletion.
type ScaleDownRequest struct {
	// NodeName is the name of the node to be deleted.
	// NodeName string
	// NodeGroup is the node group of the deleted node.
	// NodeGroup cloudprovider.NodeGroup
	// Time is the time when the node deletion was requested.
	Time time.Time
	// ExpectedDeleteTime is the time when the node is expected to be deleted.
	ExpectedDeleteTime time.Time
}

// ClusterStateRegistryConfig contains configuration information for ClusterStateRegistry.
type ClusterStateRegistryConfig struct {
	// Maximum percentage of unready nodes in total, if the number of unready nodes is higher than OkTotalUnreadyCount.
	MaxTotalUnreadyPercentage float64
	// Minimum number of nodes that must be unready for MaxTotalUnreadyPercentage to apply.
	// This is to ensure that in very small clusters (e.g. 2 nodes) a single node's failure doesn't disable autoscaling.
	OkTotalUnreadyCount int
	//  Maximum time CA waits for node to be provisioned
	MaxNodeProvisionTime time.Duration
}

// IncorrectNodeGroupSize contains information about how much the current size of the node group
// differs from the expected size. Prolonged, stable mismatch is an indication of quota
// or startup issues.
type IncorrectNodeGroupSize struct {
	// ExpectedSize is the size of the node group measured on the cloud provider side.
	ExpectedSize int
	// CurrentSize is the size of the node group measured on the kubernetes side.
	CurrentSize int
	// FirstObserved is the time when the given difference occurred.
	FirstObserved time.Time
}

// UnregisteredNode contains information about nodes that are present on the cluster provider side
// but failed to register in Kubernetes.
type UnregisteredNode struct {
	// Node is a dummy node that contains only the name of the node.
	Node *apiv1.Node
	// UnregisteredSince is the time when the node was first spotted.
	UnregisteredSince time.Time
}

// ScaleUpFailure contains information about a failure of a scale-up.
type ScaleUpFailure struct {
	// NodeGroup cloudprovider.NodeGroup
	// Reason metrics.FailedScaleUpReason
	Time   time.Time
}

// ClusterStateRegistry is a structure to keep track the current state of the cluster.
type ClusterStateRegistry struct {
	sync.Mutex
	config             ClusterStateRegistryConfig
	scaleUpRequests    map[string]*ScaleUpRequest // nodeGroupName -> ScaleUpRequest
	scaleDownRequests  []*ScaleDownRequest
	nodes              []*apiv1.Node
	// nodeInfosForGroups map[string]*schedulerframework.NodeInfo
	// cloudProvider                      cloudprovider.CloudProvider
	perNodeGroupReadiness   map[string]Readiness
	totalReadiness          Readiness
	acceptableRanges        map[string]AcceptableRange
	incorrectNodeGroupSizes map[string]IncorrectNodeGroupSize
	unregisteredNodes       map[string]UnregisteredNode
	candidatesForScaleDown  map[string][]string
	//backoff                 backoff.Backoff
	lastStatus              *api.ClusterAutoscalerStatus
	lastScaleDownUpdateTime time.Time
	logRecorder             *utils.LogEventRecorder
	// cloudProviderNodeInstances         map[string][]cloudprovider.Instance
	// previousCloudProviderNodeInstances map[string][]cloudprovider.Instance
	// cloudProviderNodeInstancesCache    *utils.CloudProviderNodeInstancesCache
	interrupt chan struct{}

	// scaleUpFailures contains information about scale-up failures for each node group. It should be
	// cleared periodically to avoid unnecessary accumulation.
	scaleUpFailures map[string][]ScaleUpFailure
}

// NewClusterStateRegistry creates new ClusterStateRegistry.
func NewClusterStateRegistry(config ClusterStateRegistryConfig, logRecorder *utils.LogEventRecorder) *ClusterStateRegistry {
	emptyStatus := &api.ClusterAutoscalerStatus{
		ClusterwideConditions: make([]api.ClusterAutoscalerCondition, 0),
		NodeGroupStatuses:     make([]api.NodeGroupStatus, 0),
	}

	return &ClusterStateRegistry{
		scaleUpRequests:   make(map[string]*ScaleUpRequest),
		scaleDownRequests: make([]*ScaleDownRequest, 0),
		nodes:             make([]*apiv1.Node, 0),
		// cloudProvider:                   cloudProvider,
		config:                  config,
		perNodeGroupReadiness:   make(map[string]Readiness),
		acceptableRanges:        make(map[string]AcceptableRange),
		incorrectNodeGroupSizes: make(map[string]IncorrectNodeGroupSize),
		unregisteredNodes:       make(map[string]UnregisteredNode),
		candidatesForScaleDown:  make(map[string][]string),
		//backoff:                 backoff,
		lastStatus:  emptyStatus,
		logRecorder: logRecorder,
		// cloudProviderNodeInstancesCache: utils.NewCloudProviderNodeInstancesCache(cloudProvider),
		interrupt:       make(chan struct{}),
		scaleUpFailures: make(map[string][]ScaleUpFailure),
	}
}

//Start starts components running in background.
func (csr *ClusterStateRegistry) Start() {
	//csr.cloudProviderNodeInstancesCache.Start(csr.interrupt)
}

// Stop stops components running in background.
func (csr *ClusterStateRegistry) Stop() {
	close(csr.interrupt)
}

// IsClusterHealthy returns true if the cluster health is within the acceptable limits
func (csr *ClusterStateRegistry) IsClusterHealthy() bool {
	csr.Lock()
	defer csr.Unlock()

	totalUnready := csr.totalReadiness.Unready

	if totalUnready > csr.config.OkTotalUnreadyCount &&
		float64(totalUnready) > csr.config.MaxTotalUnreadyPercentage/100.0*float64(len(csr.nodes)) {
		return false
	}

	return true
}

// IsNodeGroupHealthy returns true if the node group health is within the acceptable limits
func (csr *ClusterStateRegistry) IsNodeGroupHealthy(nodeGroupName string) bool {
	acceptable, found := csr.acceptableRanges[nodeGroupName]
	if !found {
		klog.Warningf("Failed to find acceptable ranges for %v", nodeGroupName)
		return false
	}

	readiness, found := csr.perNodeGroupReadiness[nodeGroupName]
	if !found {
		// No nodes but target == 0 or just scaling up.
		if acceptable.CurrentTarget == 0 || (acceptable.MinNodes == 0 && acceptable.CurrentTarget > 0) {
			return true
		}
		klog.Warningf("Failed to find readiness information for %v", nodeGroupName)
		return false
	}

	unjustifiedUnready := 0
	// Too few nodes, something is missing. Below the expected node count.
	if readiness.Ready < acceptable.MinNodes {
		unjustifiedUnready += acceptable.MinNodes - readiness.Ready
	}
	// TODO: verify against max nodes as well.

	if unjustifiedUnready > csr.config.OkTotalUnreadyCount &&
		float64(unjustifiedUnready) > csr.config.MaxTotalUnreadyPercentage/100.0*
			float64(readiness.Ready+readiness.Unready+readiness.NotStarted) {
		return false
	}

	return true
}

func (csr *ClusterStateRegistry) getProvisionedAndTargetSizesForNodeGroup(nodeGroupName string) (provisioned, target int, ok bool) {
	if len(csr.acceptableRanges) == 0 {
		klog.Warningf("AcceptableRanges have not been populated yet. Skip checking")
		return 0, 0, false
	}

	acceptable, found := csr.acceptableRanges[nodeGroupName]
	if !found {
		klog.Warningf("Failed to find acceptable ranges for %v", nodeGroupName)
		return 0, 0, false
	}
	target = acceptable.CurrentTarget

	readiness, found := csr.perNodeGroupReadiness[nodeGroupName]
	if !found {
		// No need to warn if node group has size 0 (was scaled to 0 before).
		if acceptable.MinNodes != 0 {
			klog.Warningf("Failed to find readiness information for %v", nodeGroupName)
		}
		return 0, target, true
	}
	provisioned = readiness.Registered - readiness.NotStarted

	return provisioned, target, true
}

func (csr *ClusterStateRegistry) areThereUpcomingNodesInNodeGroup(nodeGroupName string) bool {
	provisioned, target, ok := csr.getProvisionedAndTargetSizesForNodeGroup(nodeGroupName)
	if !ok {
		return false
	}
	return target > provisioned
}

// IsNodeGroupAtTargetSize returns true if the number of nodes provisioned in the group is equal to the target number of nodes.
func (csr *ClusterStateRegistry) IsNodeGroupAtTargetSize(nodeGroupName string) bool {
	provisioned, target, ok := csr.getProvisionedAndTargetSizesForNodeGroup(nodeGroupName)
	if !ok {
		return false
	}
	return target == provisioned
}

// IsNodeGroupScalingUp returns true if the node group is currently scaling up.
func (csr *ClusterStateRegistry) IsNodeGroupScalingUp(nodeGroupName string) bool {
	if !csr.areThereUpcomingNodesInNodeGroup(nodeGroupName) {
		return false
	}
	_, found := csr.scaleUpRequests[nodeGroupName]
	return found
}

// AcceptableRange contains information about acceptable size of a node group.
type AcceptableRange struct {
	// MinNodes is the minimum number of nodes in the group.
	MinNodes int
	// MaxNodes is the maximum number of nodes in the group.
	MaxNodes int
	// CurrentTarget is the current target size of the group.
	CurrentTarget int
}


// Readiness contains readiness information about a group of nodes.
type Readiness struct {
	// Number of ready nodes.
	Ready int
	// Number of unready nodes that broke down after they started.
	Unready int
	// Number of nodes that are being currently deleted. They exist in K8S but
	// are not included in NodeGroup.TargetSize().
	Deleted int
	// Number of nodes that are not yet fully started.
	NotStarted int
	// Number of all registered nodes in the group (ready/unready/deleted/etc).
	Registered int
	// Number of nodes that failed to register within a reasonable limit.
	LongUnregistered int
	// Number of nodes that haven't yet registered.
	Unregistered int
	// Time when the readiness was measured.
	Time time.Time
}

func (csr *ClusterStateRegistry) updateUnregisteredNodes(unregisteredNodes []UnregisteredNode) {
	result := make(map[string]UnregisteredNode)
	for _, unregistered := range unregisteredNodes {
		if prev, found := csr.unregisteredNodes[unregistered.Node.Name]; found {
			result[unregistered.Node.Name] = prev
		} else {
			result[unregistered.Node.Name] = unregistered
		}
	}
	csr.unregisteredNodes = result
}

//GetUnregisteredNodes returns a list of all unregistered nodes.
func (csr *ClusterStateRegistry) GetUnregisteredNodes() []UnregisteredNode {
	csr.Lock()
	defer csr.Unlock()

	result := make([]UnregisteredNode, 0, len(csr.unregisteredNodes))
	for _, unregistered := range csr.unregisteredNodes {
		result = append(result, unregistered)
	}
	return result
}



// GetStatus returns ClusterAutoscalerStatus with the current cluster autoscaler status.
func (csr *ClusterStateRegistry) GetStatus(now time.Time) *api.ClusterAutoscalerStatus {
	result := &api.ClusterAutoscalerStatus{
		ClusterwideConditions: make([]api.ClusterAutoscalerCondition, 0),
		NodeGroupStatuses:     make([]api.NodeGroupStatus, 0),
	}
	
	result.ClusterwideConditions = append(result.ClusterwideConditions,
		buildHealthStatusClusterwide(csr.IsClusterHealthy(), csr.totalReadiness))
	result.ClusterwideConditions = append(result.ClusterwideConditions,
		buildScaleUpStatusClusterwide(result.NodeGroupStatuses, csr.totalReadiness))
	result.ClusterwideConditions = append(result.ClusterwideConditions,
		buildScaleDownStatusClusterwide(csr.candidatesForScaleDown, csr.lastScaleDownUpdateTime))

	updateLastTransition(csr.lastStatus, result)
	csr.lastStatus = result
	return result
}

// GetClusterReadiness returns current readiness stats of cluster
func (csr *ClusterStateRegistry) GetClusterReadiness() Readiness {
	return csr.totalReadiness
}

func buildHealthStatusNodeGroup(isReady bool, readiness Readiness, acceptable AcceptableRange, minSize, maxSize int) api.ClusterAutoscalerCondition {
	condition := api.ClusterAutoscalerCondition{
		Type: api.ClusterAutoscalerHealth,
		Message: fmt.Sprintf("ready=%d unready=%d notStarted=%d longNotStarted=0 registered=%d longUnregistered=%d cloudProviderTarget=%d (minSize=%d, maxSize=%d)",
			readiness.Ready,
			readiness.Unready,
			readiness.NotStarted,
			readiness.Registered,
			readiness.LongUnregistered,
			acceptable.CurrentTarget,
			minSize,
			maxSize),
		LastProbeTime: metav1.Time{Time: readiness.Time},
	}
	if isReady {
		condition.Status = api.ClusterAutoscalerHealthy
	} else {
		condition.Status = api.ClusterAutoscalerUnhealthy
	}
	return condition
}

func buildScaleUpStatusNodeGroup(isScaleUpInProgress bool, isSafeToScaleUp bool, readiness Readiness, acceptable AcceptableRange) api.ClusterAutoscalerCondition {
	condition := api.ClusterAutoscalerCondition{
		Type: api.ClusterAutoscalerScaleUp,
		Message: fmt.Sprintf("ready=%d cloudProviderTarget=%d",
			readiness.Ready,
			acceptable.CurrentTarget),
		LastProbeTime: metav1.Time{Time: readiness.Time},
	}
	if isScaleUpInProgress {
		condition.Status = api.ClusterAutoscalerInProgress
	} else if !isSafeToScaleUp {
		condition.Status = api.ClusterAutoscalerBackoff
	} else {
		condition.Status = api.ClusterAutoscalerNoActivity
	}
	return condition
}

func buildScaleDownStatusNodeGroup(candidates []string, lastProbed time.Time) api.ClusterAutoscalerCondition {
	condition := api.ClusterAutoscalerCondition{
		Type:          api.ClusterAutoscalerScaleDown,
		Message:       fmt.Sprintf("candidates=%d", len(candidates)),
		LastProbeTime: metav1.Time{Time: lastProbed},
	}
	if len(candidates) > 0 {
		condition.Status = api.ClusterAutoscalerCandidatesPresent
	} else {
		condition.Status = api.ClusterAutoscalerNoCandidates
	}
	return condition
}

func buildHealthStatusClusterwide(isReady bool, readiness Readiness) api.ClusterAutoscalerCondition {
	condition := api.ClusterAutoscalerCondition{
		Type: api.ClusterAutoscalerHealth,
		Message: fmt.Sprintf("ready=%d unready=%d notStarted=%d longNotStarted=0 registered=%d longUnregistered=%d",
			readiness.Ready,
			readiness.Unready,
			readiness.NotStarted,
			readiness.Registered,
			readiness.LongUnregistered,
		),
		LastProbeTime: metav1.Time{Time: readiness.Time},
	}
	if isReady {
		condition.Status = api.ClusterAutoscalerHealthy
	} else {
		condition.Status = api.ClusterAutoscalerUnhealthy
	}
	return condition
}

func buildScaleUpStatusClusterwide(nodeGroupStatuses []api.NodeGroupStatus, readiness Readiness) api.ClusterAutoscalerCondition {
	isScaleUpInProgress := false
	for _, nodeGroupStatuses := range nodeGroupStatuses {
		for _, condition := range nodeGroupStatuses.Conditions {
			if condition.Type == api.ClusterAutoscalerScaleUp &&
				condition.Status == api.ClusterAutoscalerInProgress {
				isScaleUpInProgress = true
			}
		}
	}

	condition := api.ClusterAutoscalerCondition{
		Type: api.ClusterAutoscalerScaleUp,
		Message: fmt.Sprintf("ready=%d registered=%d",
			readiness.Ready,
			readiness.Registered),
		LastProbeTime: metav1.Time{Time: readiness.Time},
	}
	if isScaleUpInProgress {
		condition.Status = api.ClusterAutoscalerInProgress
	} else {
		condition.Status = api.ClusterAutoscalerNoActivity
	}
	return condition
}

func buildScaleDownStatusClusterwide(candidates map[string][]string, lastProbed time.Time) api.ClusterAutoscalerCondition {
	totalCandidates := 0
	for _, val := range candidates {
		totalCandidates += len(val)
	}
	condition := api.ClusterAutoscalerCondition{
		Type:          api.ClusterAutoscalerScaleDown,
		Message:       fmt.Sprintf("candidates=%d", totalCandidates),
		LastProbeTime: metav1.Time{Time: lastProbed},
	}
	if totalCandidates > 0 {
		condition.Status = api.ClusterAutoscalerCandidatesPresent
	} else {
		condition.Status = api.ClusterAutoscalerNoCandidates
	}
	return condition
}

func updateLastTransition(oldStatus, newStatus *api.ClusterAutoscalerStatus) {
	newStatus.ClusterwideConditions = updateLastTransitionSingleList(
		oldStatus.ClusterwideConditions, newStatus.ClusterwideConditions)
	updatedNgStatuses := make([]api.NodeGroupStatus, 0)
	for _, ngStatus := range newStatus.NodeGroupStatuses {
		oldConds := make([]api.ClusterAutoscalerCondition, 0)
		// for _, oldNgStatus := range oldStatus.NodeGroupStatuses {
		// 	if ngStatus.ProviderID == oldNgStatus.ProviderID {
		// 		oldConds = oldNgStatus.Conditions
		// 		break
		// 	}
		// }
		newConds := updateLastTransitionSingleList(oldConds, ngStatus.Conditions)
		updatedNgStatuses = append(
			updatedNgStatuses,
			api.NodeGroupStatus{
				// ProviderID: ngStatus.ProviderID,
				Conditions: newConds,
			})
	}
	newStatus.NodeGroupStatuses = updatedNgStatuses
}

func updateLastTransitionSingleList(oldConds, newConds []api.ClusterAutoscalerCondition) []api.ClusterAutoscalerCondition {
	result := make([]api.ClusterAutoscalerCondition, 0)
	// We have ~3 conditions, so O(n**2) is good enough
	for _, condition := range newConds {
		condition.LastTransitionTime = condition.LastProbeTime
		for _, oldCondition := range oldConds {
			if condition.Type == oldCondition.Type {
				if condition.Status == oldCondition.Status {
					condition.LastTransitionTime = oldCondition.LastTransitionTime
				}
				break
			}
		}
		result = append(result, condition)
	}
	return result
}

// GetIncorrectNodeGroupSize gets IncorrectNodeGroupSizeInformation for the given node group.
func (csr *ClusterStateRegistry) GetIncorrectNodeGroupSize(nodeGroupName string) *IncorrectNodeGroupSize {
	result, found := csr.incorrectNodeGroupSizes[nodeGroupName]
	if !found {
		return nil
	}
	return &result
}


// PeriodicCleanup performs clean-ups that should be done periodically, e.g.
// each Autoscaler loop.
func (csr *ClusterStateRegistry) PeriodicCleanup() {
	// Clear the scale-up failures info so they don't accumulate.
	csr.clearScaleUpFailures()
}

// clearScaleUpFailures clears the scale-up failures map.
func (csr *ClusterStateRegistry) clearScaleUpFailures() {
	csr.Lock()
	defer csr.Unlock()
	csr.scaleUpFailures = make(map[string][]ScaleUpFailure)
}

// GetScaleUpFailures returns the scale-up failures map.
func (csr *ClusterStateRegistry) GetScaleUpFailures() map[string][]ScaleUpFailure {
	csr.Lock()
	defer csr.Unlock()
	result := make(map[string][]ScaleUpFailure)
	for nodeGroupId, failures := range csr.scaleUpFailures {
		result[nodeGroupId] = failures
	}
	return result
}
