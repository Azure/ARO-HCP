package clusteractuator

import (
	"context"
	"fmt"
	"time"

	"github.com/Azure/ARO-HCP/internal/database"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/workqueue"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/klog/v2"
)

type ClusterActuator struct {
	dbClient database.DBClient

	// relistingQueue contains marshalled resourceIDs retrieved by listing all the active clusters available.
	// We do this on an interval to ensure our status never more stale than bounded set of minutes
	relistingQueue workqueue.TypedRateLimitingInterface[string]
	// recentlyModifiedQueue contains marshalled resourceIDs found by investigating the changefeed.
	// This allows us to prioritize cluster instances that were created of modified recently so we can take action.
	recentlyModifiedQueue workqueue.TypedRateLimitingInterface[string]
	// changedInMaestroQueue contains marshalled resourceIDs found by watching Maestro for changes.
	// This allows us to prioritize cluster instances that were created of modified recently so we can take action.
	changedInMaestroQueue workqueue.TypedRateLimitingInterface[string]
}

func NewClusterActuator(cluster database.DBClient) *ClusterActuator {
	return &ClusterActuator{
		dbClient: cluster,
		relistingQueue: workqueue.NewTypedRateLimitingQueueWithConfig(
			workqueue.DefaultTypedControllerRateLimiter[string](),
			workqueue.TypedRateLimitingQueueConfig[string]{
				Name: "cluster-actuator-relist-queue",
			},
		),
		recentlyModifiedQueue: workqueue.NewTypedRateLimitingQueueWithConfig(
			workqueue.DefaultTypedControllerRateLimiter[string](),
			workqueue.TypedRateLimitingQueueConfig[string]{
				Name: "cluster-actuator-recently-modified-queue",
			},
		),
		changedInMaestroQueue: workqueue.NewTypedRateLimitingQueueWithConfig(
			workqueue.DefaultTypedControllerRateLimiter[string](),
			workqueue.TypedRateLimitingQueueConfig[string]{
				Name: "cluster-actuator-changed-in-maestro-queue",
			},
		),
	}
}

func (c *ClusterActuator) Reconcile(ctx context.Context, resourceIDString string) error {
	logger := klog.FromContext(ctx)

	resourceID, err := azcorearm.ParseResourceID(resourceIDString)
	if err != nil {
		// requeuing will not help. Report the error and return
		return nil
	}
	_, cluster, err := c.dbClient.GetResourceDoc(ctx, resourceID)
	if err != nil {
		return fmt.Errorf("failed to get cluster '%s': %w", resourceID, err)
	}
	logger.Info("Reconciling cluster", "cluster", cluster.ResourceID)

	// check to see if we need to check this based on last time we inspected (ConcurrentMap), if so, do

	// check to see if it has changed since the last time we reconciled (we'll have a ConcurrentMap for this), if so do

	// if we have no reason to check this one, then return

	// reach out to cluster service to check the state, if we need to set cluster service, do it

	// reach out to maesto to check the state

	// use the cluster service state, superceded the maestro state to determine the resulting state

	// write the resulting state to cosmos

	return nil
}

func (c *ClusterActuator) queueAllActiveClusters(ctx context.Context) {
	activeSubscriptions, err := getAllActiveSubscriptions(ctx, c.dbClient)
	if err != nil {
		utilruntime.HandleError(err)
		return
	}
	for _, _ = range activeSubscriptions {
		// TODO figure out how to list all the clusters for a subscription. Maybe we can list all clusters for all subscriptions?
	}
}

// Run begins watching and syncing.
func (c *ClusterActuator) Run(ctx context.Context, numWorkers int) {
	// don't let panics crash the process
	defer utilruntime.HandleCrash()
	// make sure the work queue is shutdown which will trigger workers to end
	defer c.relistingQueue.ShutDown()
	defer c.recentlyModifiedQueue.ShutDown()
	defer c.changedInMaestroQueue.ShutDown()

	logger := klog.FromContext(ctx)
	logger.Info("Starting controller", "controller", "ClusterActuator")
	defer logger.Info("Shutting down controller", "controller", "ClusterActuator")

	// every duration, queue all the active cluster too inspect them again.
	go wait.UntilWithContext(ctx, c.queueAllActiveClusters, 10*time.Minute)

	for i := 0; i < numWorkers; i++ {
		// UntilWithContext will re-invoke worker after a one second pause. This creates a warm loop until the context is cancelled.
		// it also adds crash protection
		go wait.UntilWithContext(ctx, waitFuncFor(c.worker, c.relistingQueue), time.Second)
		go wait.UntilWithContext(ctx, waitFuncFor(c.worker, c.recentlyModifiedQueue), time.Second)
		go wait.UntilWithContext(ctx, waitFuncFor(c.worker, c.changedInMaestroQueue), time.Second)
	}

	<-ctx.Done()
}

// waitFuncFor provides a func that is compatible with wait.UntilWithContext
func waitFuncFor(workerFn func(ctx context.Context, queue workqueue.TypedRateLimitingInterface[string]), queue workqueue.TypedRateLimitingInterface[string]) func(ctx context.Context) {
	return func(ctx context.Context) {
		workerFn(ctx, queue)
	}
}

// worker runs a worker thread that just dequeues items, processes them, and marks them done.
// It enforces that the syncHandler is never invoked concurrently with the same key.
func (c *ClusterActuator) worker(ctx context.Context, queue workqueue.TypedRateLimitingInterface[string]) {
	for c.processNextWorkItem(ctx, queue) {
	}
}

func (c *ClusterActuator) processNextWorkItem(ctx context.Context, queue workqueue.TypedRateLimitingInterface[string]) bool {
	// Pull the next work item from queue.  It will be an object reference that we use to lookup
	// something in a cache
	key, shutdown := queue.Get()
	if shutdown {
		return false
	}
	// you always have to indicate to the queue that you've completed a piece of
	// work
	defer queue.Done(key)

	err := c.Reconcile(ctx, key)
	if err == nil {
		// if you had no error, tell the queue to stop tracking history for your
		// item. This will reset things like failure counts for per-item rate
		// limiting
		queue.Forget(key)
		return true
	}

	// there was a failure so be sure to report it.  This method allows for
	// pluggable error handling which can be used for things like
	// cluster-monitoring
	utilruntime.HandleErrorWithContext(ctx, err, "Error syncing; requeuing for later retry", "objectReference", key)

	// since we failed, we should requeue the item to work on later.  This
	// method will add a backoff to avoid hotlooping on particular items
	// (they're probably still not going to work right away) and overall
	// controller protection (everything I've done is broken, this controller
	// needs to calm down or it can starve other useful work) cases.
	queue.AddRateLimited(key)
	return true
}
