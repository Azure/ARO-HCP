package controllers

import (
	"context"
	"fmt"
	"time"

	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"
)

type Controller interface {
	Run(ctx context.Context, threadiness int) error
}

type hcpClusterKey struct {
	subscriptionID  string
	resourceGroupID string
	hcpClusterID    string
}

type clusterServiceToCustomerSync struct {
	cosmosClient database.DBClient

	clusterServiceClient ocm.ClusterServiceClientSpec

	// queue is where incoming work is placed to de-dup and to allow "easy"
	// rate limited requeues on errors
	queue workqueue.TypedRateLimitingInterface[hcpClusterKey]
}

func NewClusterServiceToCustomerSyncController(cosmosClient database.DBClient, clusterServiceClient ocm.ClusterServiceClientSpec) Controller {
	c := &clusterServiceToCustomerSync{
		cosmosClient:         cosmosClient,
		clusterServiceClient: clusterServiceClient,
		queue: workqueue.NewTypedRateLimitingQueueWithConfig(
			workqueue.DefaultTypedControllerRateLimiter[hcpClusterKey](),
			workqueue.TypedRateLimitingQueueConfig[hcpClusterKey]{
				Name: "cluster-service-customer-sync",
			},
		),
	}

	return c
}

func (c *clusterServiceToCustomerSync) synchronizeHCPCluster(ctx context.Context, key hcpClusterKey) error {
	cosmosHCPCluster, err := c.cosmosClient.HCPClusters().Get(ctx, key.subscriptionID, key.resourceGroupID, key.hcpClusterID)
	if err != nil {
		return fmt.Errorf("failed to get HCP cluster: %w", err)
	}

	csHCPCluster, err := c.clusterServiceClient.GetCluster(ctx, cosmosHCPCluster.InternalID)
	if err != nil {
		return fmt.Errorf("failed to get HCP cluster: %w", err)
	}

	// TODO create HCP cluster from csHCPCluster

	c.cosmosClient.UpdateHCPCluster(ctx)
	return nil
}

func (c *clusterServiceToCustomerSync) queueAllHCPClusters(ctx context.Context) {
}

func (c *clusterServiceToCustomerSync) Run(ctx context.Context, threadiness int) error {
	// don't let panics crash the process
	defer utilruntime.HandleCrash()
	// make sure the work queue is shutdown which will trigger workers to end
	defer c.queue.ShutDown()
	logger := klog.FromContext(ctx)

	logger.Info("Starting <NAME> controller")

	// start up your worker threads based on threadiness.  Some controllers
	// have multiple kinds of workers
	for i := 0; i < threadiness; i++ {
		// runWorker will loop until "something bad" happens.  The .Until will
		// then rekick the worker after one second
		go wait.UntilWithContext(ctx, c.runWorker, time.Second)
	}

	go wait.JitterUntilWithContext(ctx, c.queueAllHCPClusters, time.Minute, 0.1, true)

	logger.Info("Started workers")

	// wait until we're told to stop
	<-ctx.Done()
	logger.Info("Shutting down <NAME> controller")

	return nil
}

func (c *clusterServiceToCustomerSync) runWorker(ctx context.Context) {
	// hot loop until we're told to stop.  processNextWorkItem will
	// automatically wait until there's work available, so we don't worry
	// about secondary waits
	for c.processNextWorkItem(ctx) {
	}
}

// processNextWorkItem deals with one item off the queue.  It returns false
// when it's time to quit.
func (c *clusterServiceToCustomerSync) processNextWorkItem(ctx context.Context) bool {
	// Pull the next work item from queue.  It will be an object reference that we use to lookup
	// something in a cache
	ref, shutdown := c.queue.Get()
	if shutdown {
		return false
	}
	// you always have to indicate to the queue that you've completed a piece of
	// work
	defer c.queue.Done(ref)

	// Process the object reference.  This method will contains your "do stuff" logic
	err := c.synchronizeHCPCluster(ctx, ref)
	if err == nil {
		// if you had no error, tell the queue to stop tracking history for your
		// item. This will reset things like failure counts for per-item rate
		// limiting
		c.queue.Forget(ref)
		return true
	}

	// there was a failure so be sure to report it.  This method allows for
	// pluggable error handling which can be used for things like
	// cluster-monitoring
	utilruntime.HandleErrorWithContext(ctx, err, "Error syncing; requeuing for later retry", "objectReference", ref)

	// since we failed, we should requeue the item to work on later.  This
	// method will add a backoff to avoid hotlooping on particular items
	// (they're probably still not going to work right away) and overall
	// controller protection (everything I've done is broken, this controller
	// needs to calm down or it can starve other useful work) cases.
	c.queue.AddRateLimited(ref)

	return true
}
