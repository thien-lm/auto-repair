package main

import (
	goContext "context"
	"fmt"

	// "fmt"
	"errors"
	"time"

	//"github.com/phongvu0403/secret-manager/core"
	corev1 "k8s.io/api/core/v1"

	// apierrors "k8s.io/apimachinery/pkg/api/errors"
	//"github.com/phongvu0403/secret-manager/context"
	"github.com/phongvu0403/secret-manager/vcd"
	"github.com/phongvu0403/secret-manager/vcd/manipulation"

	// metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"github.com/vmware/go-vcloud-director/v2/govcd"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/wait"
	nodeInformer "k8s.io/client-go/informers/core/v1"
	"k8s.io/client-go/kubernetes"
	nodeLister "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"

	//kube_util "github.com/phongvu0403/secret-manager/utils/kubernetes"
	"sync"

	scaler "github.com/phongvu0403/secret-manager/scaler"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
     maxRebootRetry int = 3
	 waitingTimeForNotReady time.Duration = 5*time.Minute
     maxReplaceNodeRetry int = 1
	 retryDuration time.Duration = 10*time.Second
)

type controller struct {
	clientset     kubernetes.Interface
	nodeLister    nodeLister.NodeLister
	queue         workqueue.RateLimitingInterface
	nodeInformer	  cache.SharedIndexInformer
	//handle concurrent request to access workqueue
	enqueueMap   map[string]struct{}
	lock         sync.Mutex
}

func newController(clientset kubernetes.Interface, nodeInformer nodeInformer.NodeInformer) *controller {
	c := &controller{
		clientset:     clientset,
		nodeLister:    nodeInformer.Lister(),
		nodeInformer:  nodeInformer.Informer(),
		queue:         workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "lim"),
		enqueueMap:   make(map[string]struct{}),
	}

	nodeInformer.Informer().AddEventHandler(
		cache.ResourceEventHandlerFuncs{
			UpdateFunc: c.handleUpdate,
		},
	)
	return c
}

func (c *controller) run(ch <-chan struct{}) {
	logger := logger()
	logger.Info("starting node auto repair controller")
	if !cache.WaitForCacheSync(ch, c.nodeInformer.HasSynced) {
		logger.Info("waiting for cache to be synced")
	}

	go wait.Until(c.worker, 1*time.Second, ch)

	<-ch
}

func (c *controller) worker() {
	for c.processItem() {

	}
}

func (c *controller) processItem() bool {
	logger := logger()
	logger.Info("finding node to repair ...")
	//wait until there is a new item in the working queue
	item, shutdown := c.queue.Get()
	if shutdown {
		return false
	}
	defer c.queue.Forget(item)

	err := c.handleNotReadyNode(item.(*corev1.Node))

	if err != nil {
		logger.Info("unable to process nodes: " + err.Error())
		c.handleError()
		return false
	}
	return true
}
// create vcloud client to access infra
func (c *controller) createGoVCloudClient() (*govcd.VCDClient, string, string, error) {
	kubeClient := c.clientset
	userName := vcd.GetUserName(kubeClient)
	password := vcd.GetPassword(kubeClient)
	host     := vcd.GetHost(kubeClient)
	org 	:= vcd.GetORG(kubeClient)
	vdc     := vcd.GetVDC(kubeClient)

	goVCloudClientConfig := vcd.Config{
		User: userName,
		Password: password,
		Href: fmt.Sprintf("%v/api", host),
		Org: org,
		VDC: vdc,
	}
	
	goVcloudClient, err := goVCloudClientConfig.NewClient()
	return goVcloudClient, org, vdc, err
}
//main logic to handle not ready node
//1. reboot
//2. replace
func (c *controller) handleNotReadyNode(node *corev1.Node) error {
	logger := logger()
	logger.Info("found node has been in 'NotReady' state for more than x minutes: ", "node" , node.Name)
	//trying to reboot first
	for i:= 0; i < maxRebootRetry; i++ {
		logger.Info("trying to reboot ", "total times: ", i + 1)
		//fetch info to access to vmware platform
		logger.Info("trying to reboot node: ", "node" , node.Name)
		goVcloudClient, org, vdc, err := c.createGoVCloudClient()
		if err != nil {
			return fmt.Errorf("failed to connect to vcd: %v", err)
		}
		//reboot vm in infra
		err2 := manipulation.RebootVM(goVcloudClient, org, vdc, node.Name)
		if err2 != nil {
			return errors.New("failed to handle not ready node")
		}
		isNodeReady := c.checkNodeReadyStatusAfterRepairing(node)
		if isNodeReady {
			logger.Info("repair node perform by auto repair controller was ran successfully")
			delete(c.enqueueMap,node.Name) //only mark that node can be re-process (repair again) if the auto scaler success to process it before
			return nil
		}
	}
	//trying to replace node
	for i:= 0; i < maxReplaceNodeRetry; i++ {
		logger.Info("trying to replace node ","node", node.Name, "total times: ", i + 1)
		c.replaceNode(c.clientset, node)
		isNodeReady := c.checkAllNodesReadyStatus()
		if isNodeReady {
			logger.Info("replace node perform by auto repair controller was ran successfully")
			delete(c.enqueueMap,node.Name) //only mark that node can be re-process (repair again) if the auto scaler success to process it before
			return nil
		}
	}
	return errors.New("max retries reached, can not check if node is ready")
}
//replace node, either create then remove or remove then create
func (c *controller) replaceNode(kubeClient kubernetes.Interface, toBeRepairedNode *corev1.Node) error{
	logger := logger()
	nodes, err := kubeClient.CoreV1().Nodes().List(goContext.Background(), metav1.ListOptions{})
	if err != nil {
		logger.Info("scale Node failed")
		return errors.New("auto scaler should not perform any action")
	}
	//fetch information of cluster to scale up/down 
	accessToken := vcd.GetAccessToken(kubeClient)
	vpcID := vcd.GetVPCId(kubeClient)
	callbackURL := vcd.GetCallBackURL(kubeClient)
	domainAPI := GetDomainApiConformEnv(callbackURL)
	clusterIDPortal := GetClusterID(kubeClient)
	idCluster := GetIDCluster(domainAPI, vpcID, accessToken, clusterIDPortal)
	nodePointers := make([]*corev1.Node, len(nodes.Items))

	for i, node := range nodes.Items {
		nodePointers[i] = &node
	}
	//fetch state of auto scaler to decide remove or create node first
	maxAutoScalerNode := vcd.GetMaxSize(kubeClient)
	minAutoScalerNode := vcd.GetMinSize(kubeClient)
	currentNodeList, _ := c.nodeLister.List(labels.Everything())
	currentSize := len(currentNodeList)
	//based on max and min size of auto scaler, perform corresponding action
	if !scaler.CheckWorkerNodeCanBeScaleDown(kubeClient, toBeRepairedNode.Name) {
		logger.Info("node should not be deleted, auto repair do nothing")
		return errors.New("auto repairer can not delete node")
	} else if currentSize >= minAutoScalerNode && currentSize < maxAutoScalerNode{
		logger.Info("auto repairing controller is trying to create new node")
		_, errSU := scaler.ScaleUp( nodePointers ,kubeClient,  accessToken, vpcID, idCluster, clusterIDPortal, callbackURL, 1)
		if errSU != nil {
			return errSU
		}
		logger.Info("auto repairing controller successfully created new node")
		time.Sleep(10 * time.Second)
		logger.Info("auto repairing controller is trying to remove node: ", "node", toBeRepairedNode.Name)
		_, errSD := scaler.ScaleDown(time.Now() , kubeClient, accessToken, vpcID, idCluster, clusterIDPortal, callbackURL, toBeRepairedNode.Name)
		if errSD != nil {
			return errSD
		}
		logger.Info("auto repairing controller removed node: ", "node", toBeRepairedNode.Name)
		return nil
	} else if currentSize >= maxAutoScalerNode {
		logger.Info("auto repairing controller is trying to remove node: ", "node", toBeRepairedNode.Name)
		_, errSD:= scaler.ScaleDown(time.Now() , kubeClient, accessToken, vpcID, idCluster, clusterIDPortal, callbackURL, toBeRepairedNode.Name)
		if errSD != nil {
			return errSD
		}
		time.Sleep(10 * time.Second)		
		logger.Info("auto repairing controller is trying to create new node")
		_, errSU :=  scaler.ScaleUp( nodePointers ,kubeClient,  accessToken, vpcID, idCluster, clusterIDPortal, callbackURL, 1)
		if errSU != nil {
			return errSU
		}
		logger.Info("auto repairing controller successfully created new node")
		return nil
	} else {
		logger.Info("node is under auto scaler lower limit, auto repairer should not do any thing")
		return errors.New("auto scaler should not perform any action")
	}
}
//idk what to do
func (c *controller) handleError() {
	logger := logger()
	logger.Info("starting handle error for node")
}
//handle event each time node resource update
func (c *controller) handleUpdate(oldObj, obj interface{}) {
	newNode := obj.(*corev1.Node)
	if isNodeNotReadyForTooLong(newNode) {
		c.lock.Lock()
		defer c.lock.Unlock()
		if _, ok := c.enqueueMap[newNode.Name]; !ok {
			c.queue.Add(newNode)
			c.enqueueMap[newNode.Name] = struct{}{} //mark that node can't be enqueue anymore before it can be processed
		}
	}
}
//check if node is in not ready state for the limited time
func isNodeNotReadyForTooLong(node *corev1.Node) bool {
    for _, condition := range node.Status.Conditions {
        if condition.Type == corev1.NodeReady && condition.Status != corev1.ConditionTrue {
            lastTransitionTime := condition.LastTransitionTime.Time
            if time.Since(lastTransitionTime) >= waitingTimeForNotReady {
                return true
            }
        }
    }
    return false
}
//check status of rebooted node in cluster 
//this method trying to fetch node status from Kube API server each 10s
func (c *controller) checkNodeReadyStatusAfterRepairing(node *corev1.Node) bool {
	logger := logger()
	logger.Info("Rebooted node in infrastructure, waiting for Ready state in kubernetes")
	maxRetry := 10
	//retryCount := 0
	for retryCount := 0; retryCount < maxRetry; retryCount++ {
		newNodeState, _ := c.nodeLister.Get(node.Name)
		for _, condition := range newNodeState.Status.Conditions {
        	if condition.Type == corev1.NodeReady && condition.Status == corev1.ConditionTrue {
				logger.Info("node is healthy now, have a good day !")
				time.Sleep(retryDuration)
            	return true
			}
	}
	logger.Info("can not determine if node is healthy, retry after 10 seconds")
	time.Sleep(retryDuration)
	}
	return false
}
//check status of all nodes in cluster
//this method trying to fetch all nodes status from Kube API server each 10s
func (c *controller) checkAllNodesReadyStatus() bool {
	logger := logger()
	maxRetry := 10
	//retryCount := 0
	for retryCount := 0; retryCount < maxRetry; retryCount++ {
		nodeList, _ := c.nodeLister.List(labels.Everything())
		for _, node := range nodeList {
		for _, condition := range node.Status.Conditions {
        	if condition.Type == corev1.NodeReady && condition.Status == corev1.ConditionTrue {
				logger.Info("all nodes is healthy now, have a good day !")
				time.Sleep(retryDuration)
            	return true
			}
		}
		}
	logger.Info("can not determine if all nodes is healthy, retry after 10 seconds")
	time.Sleep(retryDuration)
	}
	return false
}