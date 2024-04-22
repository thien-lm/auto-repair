package main

import (
	goContext "context"
	"fmt"

	// "fmt"
	"errors"
	"time"

	"github.com/phongvu0403/secret-manager/core"
	corev1 "k8s.io/api/core/v1"

	// apierrors "k8s.io/apimachinery/pkg/api/errors"
	"github.com/phongvu0403/secret-manager/context"
	"github.com/phongvu0403/secret-manager/vcd"
	"github.com/phongvu0403/secret-manager/vcd/manipulation"

	// metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"github.com/vmware/go-vcloud-director/v2/govcd"
	"k8s.io/apimachinery/pkg/util/wait"
	nodeInformer "k8s.io/client-go/informers/core/v1"
	"k8s.io/client-go/kubernetes"
	nodeLister "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"

	//kube_util "github.com/phongvu0403/secret-manager/utils/kubernetes"
	scaler "github.com/phongvu0403/secret-manager/scaler"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type controller struct {
	clientset     kubernetes.Interface
	nodeLister    nodeLister.NodeLister
	// nsCacheSynced cache.InformerSynced
	queue         workqueue.RateLimitingInterface
	nodeInformer	  cache.SharedIndexInformer
}

func newController(clientset kubernetes.Interface, nodeInformer nodeInformer.NodeInformer) *controller {
	c := &controller{
		clientset:     clientset,
		nodeLister:    nodeInformer.Lister(),
		nodeInformer:  nodeInformer.Informer(),
		queue:         workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "lim"),
	}

	nodeInformer.Informer().AddEventHandlerWithResyncPeriod(
		cache.ResourceEventHandlerFuncs{
			UpdateFunc: c.handleUpdate,
		},
	5*time.Second)
	return c
}

func (c *controller) run(ch <-chan struct{}) {
	logger := logger()
	logger.Info("starting node auto repair controller")
	if !cache.WaitForCacheSync(ch, c.nodeInformer.HasSynced) {
		logger.Info("waiting for cache to be synced")
	}

	go wait.Until(c.worker, 30*time.Second, ch)

	<-ch
}

func (c *controller) worker() {
	for c.processItem() {

	}
}

func (c *controller) processItem() bool {
	logger := logger()
	logger.Info("starting controller")
	//wait until there is a new item in the working queue
	item, shutdown := c.queue.Get()
	if shutdown {
		return false
	}
	defer c.queue.Forget(item)

	err := c.handleNotReadyNode(item.(*corev1.Node))
	//ctx := context.Background()
	// nodeList, err := c.clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})

	if err != nil {
		logger.Info("unable to process nodes: " + err.Error())
		return false
	}

	return true
}

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
	
	fmt.Println("org is", host)
	goVcloudClient, err := goVCloudClientConfig.NewClient()
	return goVcloudClient, org, vdc, err
}

func (c *controller) handleNotReadyNode(node *corev1.Node) error{
	logger := logger()
		fmt.Printf("Node %s has been NotReady for more than 5 minutes", node.Name)
	if true {
		//fetch info to access to vmware platform
		goVcloudClient, org, vdc, err := c.createGoVCloudClient()
		if err != nil {
			return fmt.Errorf("failed to connect to vcd: %v", err)
		}
		//done fetch info
		err2 := manipulation.RebootVM(goVcloudClient, org, vdc, node.Name)
		if err2 != nil {
			return errors.New("failed to handle not ready node")
		}
	}
	//c.scaleNode(c.clientset, node)
	logger.Info("reboot node perform by auto repair controller was ran successfully")

	

	return nil
}



func (c *controller) scaleNode(kubeClient kubernetes.Interface, toBeRepairedNode *corev1.Node) {
	logger := logger()
	nodes, err := kubeClient.CoreV1().Nodes().List(goContext.Background(), metav1.ListOptions{})
	if err != nil {
		logger.Info("scale Node bugged")
	}

	accessToken := vcd.GetAccessToken(kubeClient)
	vpcID := vcd.GetVPCId(kubeClient)
	callbackURL := vcd.GetCallBackURL(kubeClient)
	domainAPI := GetDomainApiConformEnv(callbackURL)
	clusterIDPortal := GetClusterID(kubeClient)
	idCluster := GetIDCluster(domainAPI, vpcID, accessToken, clusterIDPortal)
	//(nodes []*apiv1.Node, kubeclient kube_client.Interface, accessToken, vpcID, idCluster, clusterIDPortal,callbackURL string, numberNodeScaleUp int) (*status.ScaleUpStatus, errors.AutorepairError)
	nodePointers := make([]*corev1.Node, len(nodes.Items))
	for i, node := range nodes.Items {
		nodePointers[i] = &node
	}
	scaler.ScaleUp( nodePointers ,kubeClient,  accessToken, vpcID, idCluster, clusterIDPortal, callbackURL, 1)
	//currentTime time.Time, kubeclient kube_client.Interface, accessToken, vpcID, idCluster, clusterIDPortal, callbackURL string
	logger.Info("debuLastTransitionTimegging scale down")
	scaler.ScaleDown(time.Now() , kubeClient, accessToken, vpcID, idCluster, clusterIDPortal, callbackURL, toBeRepairedNode.Name)

}
// 	kubeEventRecorder := kube_util.CreateEventRecorder(kubeClient)
// 	opts := createAutorepairOptions() 

// 	listerRegistryStopChannel := make(chan struct{})
// 	listerRegistry := kube_util.NewListerRegistryWithDefaultListers(kubeClient, listerRegistryStopChannel)
// 	autorepair, err := core.NewAutorepair(opts, kubeClient, kubeEventRecorder, listerRegistry)
// 	if err != nil {
// 		fmt.Errorf("Failed to create auto repair: %v", err)
// 	}
// 	err = autorepair.RunOnce(5*time.Minute, kubeClient, vpcID, accessToken, idCluster, clusterIDPortal, callbackURL)
// 	if err != nil {
// 		fmt.Printf("Failed to run autorepair : %v", err)
// 	}
// }

func createAutorepairOptions(namespace, clusterName, statusConfigMapName string) core.AutorepairOptions {
	autorepairingOpts := context.AutorepairingOptions{
		// CloudConfig:                      *cloudConfig,
		ConfigNamespace:                  namespace,
		ClusterName:                      clusterName,
		StatusConfigMapName:              statusConfigMapName,

	}

	return core.AutorepairOptions{
		AutorepairingOptions:   autorepairingOpts,
	}
}

func (c *controller) handleError(item *corev1.Node) {
	logger := logger()
	logger.Info("starting handle error for node")
}

func (c *controller) handleUpdate(oldObj, obj interface{}) {
	logger := logger()
	logger.Info("update handler was called")
	//oldNode := oldObj.(*corev1.Node)
	node := obj.(*corev1.Node)
	//check if node in not ready state for more than n minutes
	if isNodeNotReadyForTooLong(node) {
        c.queue.Add(node)
    }
}

func isNodeNotReadyForTooLong(node *corev1.Node) bool {
    for _, condition := range node.Status.Conditions {
        if condition.Type == corev1.NodeReady && condition.Status != corev1.ConditionTrue {
            lastTransitionTime := condition.LastTransitionTime.Time
            if time.Since(lastTransitionTime) > 30*time.Second {
                return true
            }
        }
    }
    return false
}