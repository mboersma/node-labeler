// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT license.

package main

import (
	"errors"
	"flag"
	"time"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog"
)

type Controller struct {
	kubeclientset kubernetes.Interface
	indexer       cache.Indexer
	queue         workqueue.RateLimitingInterface
	informer      cache.Controller
}

func NewController(kubeclientset kubernetes.Interface, queue workqueue.RateLimitingInterface, indexer cache.Indexer, informer cache.Controller) *Controller {
	return &Controller{
		kubeclientset: kubeclientset,
		informer:      informer,
		indexer:       indexer,
		queue:         queue,
	}
}

func (c *Controller) processNextItem() bool {
	// Wait until there is a new item in the working queue
	key, quit := c.queue.Get()
	if quit {
		return false
	}
	// Tell the queue that we are done with processing this key. This unblocks the key for other workers
	// This allows safe parallel processing because two pods with the same key are never processed in
	// parallel.
	defer c.queue.Done(key)

	// Invoke the method containing the business logic
	err := c.syncToStdout(key.(string))
	// Handle the error if something went wrong during the execution of the business logic
	c.handleErr(err, key)
	return true
}

// syncToStdout is the business logic of the controller. In this controller it simply prints
// information about the pod to stdout. In case an error happened, it has to simply return the error.
// The retry logic should not be part of the business logic.
func (c *Controller) syncToStdout(key string) error {
	obj, exists, err := c.indexer.GetByKey(key)
	if err != nil {
		klog.Errorf("getting object by key %s: %v", key, err)
		return err
	}

	if exists {
		node := obj.(*v1.Node)

		klog.Infof("syncing Node %s\n", node.GetName())

		masterLabels := map[string]string{
			"kubernetes.azure.com/role":      "master",
			"kubernetes.io/role":             "master",
			"node-role.kubernetes.io/master": "",
		}

		if needsLabeling(node, masterLabels) {
			labels := node.GetLabels()
			for k, v := range masterLabels {
				labels[k] = v
			}
			node.SetLabels(labels)
			// TODO: replace this with PATCH
			if _, err := c.kubeclientset.CoreV1().Nodes().Update(node); err != nil {
				klog.Error(err)
			}
			return nil
		}

		agentLabels := map[string]string{
			"kubernetes.azure.com/role":     "agent",
			"kubernetes.io/role":            "agent",
			"node-role.kubernetes.io/agent": "",
		}

		if needsLabeling(node, agentLabels) {
			labels := node.GetLabels()
			for k, v := range agentLabels {
				labels[k] = v
			}
			node.SetLabels(labels)
			// TODO: replace this with PATCH
			// TODO: strategic merge patch to update labels
			if _, err := c.kubeclientset.CoreV1().Nodes().Update(node); err != nil {
				klog.Error(err)
			}
			return nil
		}
	}

	return nil
}

func needsLabeling(node *v1.Node, labels map[string]string) bool {
	// check to see if the node has any--but not all--of the specified labels
	matched := 0
	for key, value := range labels {
		for key1, value1 := range node.GetLabels() {
			if key == key1 && value == value1 {
				matched++
			}
		}
	}

	return (matched > 0 && matched < len(labels))
}

// handleErr checks if an error happened and makes sure we will retry later.
func (c *Controller) handleErr(err error, key interface{}) {
	if err == nil {
		// Forget about the #AddRateLimited history of the key on every successful synchronization.
		// This ensures that future processing of updates for this key is not delayed because of
		// an outdated error history.
		c.queue.Forget(key)
		return
	}

	// This controller retries 5 times if something goes wrong. After that, it stops trying.
	if c.queue.NumRequeues(key) < 5 {
		klog.Infof("Error syncing node %v: %v", key, err)

		// Re-enqueue the key rate limited. Based on the rate limiter on the
		// queue and the re-enqueue history, the key will be processed later again.
		c.queue.AddRateLimited(key)
		return
	}

	c.queue.Forget(key)
	// Report to an external entity that, even after several retries, we could not successfully process this key
	runtime.HandleError(err)
	klog.Infof("Dropping node %q out of the queue: %v", key, err)
}

func (c *Controller) Run(threadiness int, stopCh chan struct{}) {
	defer runtime.HandleCrash()
	defer c.queue.ShutDown()

	klog.Info("Starting Node controller")

	go c.informer.Run(stopCh)

	// Wait for all involved caches to be synced, before processing items from the queue is started
	if !cache.WaitForCacheSync(stopCh, c.informer.HasSynced) {
		runtime.HandleError(errors.New("timeout waiting for cache sync"))
		return
	}

	for i := 0; i < threadiness; i++ {
		go wait.Until(c.runWorker, time.Second, stopCh)
	}

	<-stopCh
	klog.Info("Stopping Node controller")
}

func (c *Controller) runWorker() {
	for c.processNextItem() {
	}
}

func main() {
	klogFlags := flag.NewFlagSet("klog", flag.ExitOnError)
	klog.InitFlags(klogFlags)
	logtostderr := klogFlags.Lookup("logtostderr")
	logtostderr.Value.Set("true")

	// use the in-cluster Kubernetes config
	config, err := rest.InClusterConfig()
	if err != nil {
		klog.Fatal(err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		klog.Fatal(err)
	}

	nodeWatcher := cache.NewListWatchFromClient(
		clientset.CoreV1().RESTClient(), "nodes", v1.NamespaceAll, fields.Everything())

	queue := workqueue.NewRateLimitingQueue(workqueue.DefaultControllerRateLimiter())

	indexer, informer := cache.NewIndexerInformer(nodeWatcher, &v1.Node{}, 0, cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			if key, err := cache.MetaNamespaceKeyFunc(obj); err == nil {
				queue.Add(key)
			}
		},
		UpdateFunc: func(old interface{}, new interface{}) {
			if key, err := cache.MetaNamespaceKeyFunc(new); err == nil {
				queue.Add(key)
			}
		},
	}, cache.Indexers{})

	controller := NewController(clientset, queue, indexer, informer)

	stop := make(chan struct{})
	defer close(stop)
	go controller.Run(1, stop)

	select {}
}
