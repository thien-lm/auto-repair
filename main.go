package main

import (
	"flag"
	"time"
	//"time"

	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

func main() {
	logger := logger()
	kubeconfig := flag.String("kubeconfig", "/home/thien/Desktop/tailieutoanhoc/be/dpqibt3c-kubeconfig", "location to your kubeconfig file")
	config, err := clientcmd.BuildConfigFromFlags("", *kubeconfig)
	if err != nil {
		logger.Info("unable to build config from flags")
		config, err = rest.InClusterConfig()
		if err != nil {
			logger.Error(err, "unable to get  incluster config")
		}
	}
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		logger.Error(err, "unable to create clientset")
	}

	ch := make(chan struct{})
	informers := informers.NewSharedInformerFactory(clientset, 10*time.Second)
	c := newController(clientset,  informers.Core().V1().Nodes())
	informers.Start(ch)
	c.run(ch)
}
