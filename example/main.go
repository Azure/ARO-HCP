// Copyright 2025 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package controllers

import (
	"context"
	"fmt"
	"time"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"
)

type Controller interface {
	Run(ctx context.Context, threadiness int)
}

const ClusterStatePending = "Pending"

type ListArguments struct {
	State string
}
type ClusterList struct {
	Items []Cluster
}

type GetArguments struct {
	Id string
}
type Cluster struct {
	Id    string
	State string
}

type clusterClient interface {
	AuthorizedFind(ctx context.Context, args ListArguments) (ClusterList, error)
	AuthorizedGet(ctx context.Context, args GetArguments) (Cluster, error)
}

// clusterKey is enough to uniquely identify a cluster
type clusterKey string

type AroHcpPendingClustersWorker struct {
	client clusterClient

	// queue is where incoming work is placed to de-dup and to allow "easy"
	// rate limited requeues on errors
	queue workqueue.TypedRateLimitingInterface[clusterKey]
}

func NewClusterProvisionerController(client clusterClient) Controller {
	c := &AroHcpPendingClustersWorker{
		client: client,
		queue: workqueue.NewTypedRateLimitingQueueWithConfig(
			workqueue.DefaultTypedControllerRateLimiter[clusterKey](),
			workqueue.TypedRateLimitingQueueConfig[clusterKey]{
				Name: "aro-hcp-pending-clusters",
			},
		),
	}

	return c
}

// processCluster attempts to provision a cluster, returning early when long-running asynchronous tasks are kicked off and
// providing the caller with:
// - how much time to wait before re-queueing, when waiting on an async task
// - whether we're done
// - whether we had an error
func (c *AroHcpPendingClustersWorker) processCluster(ctx context.Context, key clusterKey) (time.Duration, bool, error) {
	// the state of the cluster may have changed between when we queued this cluster to be processed and now, so
	// we must first get the current state and operate over it
	cluster, err := c.client.AuthorizedGet(ctx, GetArguments{
		Id: string(key),
	})
	if err != nil {
		return 0, false, fmt.Errorf("failed to get HCP cluster: %w", err)
	}

	if cluster.State != ClusterStatePending {
		// the cluster may have changed since we queued it, if we're no longer pending, there's nothing left to do
		return 0, true, nil
	}

	// call the current processing logic, adapting it to divulge how long to wait before re-queue, return its error

	return 0, true, nil
}

func (c *AroHcpPendingClustersWorker) queuePendingClusters(ctx context.Context) {
	logger := klog.FromContext(ctx)

	list, err := c.client.AuthorizedFind(ctx, ListArguments{State: ClusterStatePending})
	if err != nil {
		logger.Error(err, "failed to list HCP pending clusters")
		return
	}
	for _, cluster := range list.Items {
		c.queue.Add(clusterKey(cluster.Id))
	}
}

func (c *AroHcpPendingClustersWorker) Run(ctx context.Context, threadiness int) {
	// don't let panics crash the process
	defer utilruntime.HandleCrash()
	// make sure the work queue is shutdown which will trigger workers to end
	defer c.queue.ShutDown()
	logger := klog.FromContext(ctx)

	logger.Info("Starting AroHcpPendingClustersWorker controller")

	// start up your worker threads based on threadiness.  Some controllers
	// have multiple kinds of workers
	for i := 0; i < threadiness; i++ {
		// runWorker will loop until "something bad" happens.  The .Until will
		// then rekick the worker after one second
		go wait.UntilWithContext(ctx, c.runWorker, time.Second)
	}

	go wait.JitterUntilWithContext(ctx, c.queuePendingClusters, time.Minute, 0.1, true)

	logger.Info("Started workers")

	// wait until we're told to stop
	<-ctx.Done()
	logger.Info("Shutting down AroHcpPendingClustersWorker controller")
}

func (c *AroHcpPendingClustersWorker) runWorker(ctx context.Context) {
	// hot loop until we're told to stop.  processNextWorkItem will
	// automatically wait until there's work available, so we don't worry
	// about secondary waits
	for c.processNextWorkItem(ctx) {
	}
}

// processNextWorkItem deals with one item off the queue.  It returns false
// when it's time to quit.
func (c *AroHcpPendingClustersWorker) processNextWorkItem(ctx context.Context) bool {
	// Pull the next work item from queue.  It will be an object reference that we use to lookup
	// something in a cache
	ref, shutdown := c.queue.Get()
	if shutdown {
		return false
	}
	// you always have to indicate to the queue that you've completed a piece of
	// work
	defer c.queue.Done(ref)

	// Process the object reference.  This method will contain your "do stuff" logic
	requeueAfter, done, err := c.processCluster(ctx, ref)
	if done {
		// if you had no error, tell the queue to stop tracking history for your
		// item. This will reset things like failure counts for per-item rate
		// limiting
		c.queue.Forget(ref)
		return true
	}

	if requeueAfter > 0 {
		// if the processing knows it can't make progress now, we can open up space for the
		// queue workers to do other useful work before re-queueing this key and re-processing
		// it
		c.queue.AddAfter(ref, requeueAfter)
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
