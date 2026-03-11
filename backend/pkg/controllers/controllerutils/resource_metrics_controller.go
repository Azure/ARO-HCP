// Copyright 2026 Microsoft Corporation
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

package controllerutils

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// ResourceIDHash returns the first 16 hex characters of the SHA-256 hash of
// the resource ID string. This anonymizes the resource identity in metric
// labels while remaining deterministic for correlation.
func ResourceIDHash(resourceID string) string {
	h := sha256.Sum256([]byte(resourceID))
	return fmt.Sprintf("%x", h[:8])
}

// ResourceMetricsController[T] reacts to informer events and maintains
// per-resource Prometheus gauge metrics using a level-driven approach.
// On each sync it reads the current state of the resource and passes the
// typed object to the provided handler functions. The controller handles
// workqueue boilerplate and type-safe object extraction via generics.
//
// T is the concrete resource type stored in the informer (e.g.
// *api.HCPOpenShiftCluster, *api.HCPOpenShiftClusterNodePool).
type ResourceMetricsController[T any] struct {
	name          string
	indexer       cache.Indexer
	queue         workqueue.TypedRateLimitingInterface[string]
	syncMetrics   func(ctx context.Context, obj T)
	deleteMetrics func(key string)
}

// NewResourceMetricsController creates a generic, type-safe metrics controller.
// The syncMetrics function is called with the typed resource object on each sync.
// The deleteMetrics function is called with the store key when the resource is
// removed from the informer cache.
func NewResourceMetricsController[T any](
	name string,
	informer cache.SharedIndexInformer,
	syncMetrics func(ctx context.Context, obj T),
	deleteMetrics func(key string),
) *ResourceMetricsController[T] {
	c := &ResourceMetricsController[T]{
		name:          name,
		indexer:       informer.GetIndexer(),
		syncMetrics:   syncMetrics,
		deleteMetrics: deleteMetrics,
		queue: workqueue.NewTypedRateLimitingQueueWithConfig(
			workqueue.DefaultTypedControllerRateLimiter[string](),
			workqueue.TypedRateLimitingQueueConfig[string]{
				Name: name,
			},
		),
	}

	_, err := informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    c.enqueue,
		UpdateFunc: func(_, newObj interface{}) { c.enqueue(newObj) },
		DeleteFunc: c.enqueue,
	})
	if err != nil {
		panic(err)
	}

	return c
}

func (c *ResourceMetricsController[T]) enqueue(obj interface{}) {
	key, err := cache.DeletionHandlingMetaNamespaceKeyFunc(obj)
	if err != nil {
		logger := utils.DefaultLogger()
		logger = logger.WithValues(utils.LogValues{}.AddControllerName(c.name)...)
		logger.Error(err, "failed to compute key")
		return
	}
	c.queue.Add(key)
}

// Run starts the controller workers and blocks until ctx is cancelled.
func (c *ResourceMetricsController[T]) Run(ctx context.Context, threadiness int) {
	defer utilruntime.HandleCrash()
	defer c.queue.ShutDown()

	logger := utils.LoggerFromContext(ctx)
	logger = logger.WithValues(utils.LogValues{}.AddControllerName(c.name)...)
	ctx = utils.ContextWithLogger(ctx, logger)
	logger.Info("Starting")

	for i := 0; i < threadiness; i++ {
		go wait.UntilWithContext(ctx, c.runWorker, time.Second)
	}

	logger.Info("Started workers")
	<-ctx.Done()
	logger.Info("Shutting down")
}

func (c *ResourceMetricsController[T]) runWorker(ctx context.Context) {
	for c.processNextWorkItem(ctx) {
	}
}

func (c *ResourceMetricsController[T]) processNextWorkItem(ctx context.Context) bool {
	key, shutdown := c.queue.Get()
	if shutdown {
		return false
	}
	defer c.queue.Done(key)

	ReconcileTotal.WithLabelValues(c.name).Inc()
	err := c.syncResource(ctx, key)
	if err == nil {
		c.queue.Forget(key)
		return true
	}

	utilruntime.HandleErrorWithContext(ctx, err, "Error syncing resource metrics", "key", key)
	c.queue.AddRateLimited(key)
	return true
}

// syncResource reads the current state of the resource from the informer cache
// and calls the typed handler function (level-driven).
func (c *ResourceMetricsController[T]) syncResource(ctx context.Context, key string) error {
	obj, exists, err := c.indexer.GetByKey(key)
	if err != nil {
		return err
	}

	if !exists {
		c.deleteMetrics(key)
		return nil
	}

	typed, ok := obj.(T)
	if !ok {
		c.deleteMetrics(key)
		return nil
	}

	c.syncMetrics(ctx, typed)
	return nil
}

// NewClusterMetricsHandler creates sync and delete functions for cluster metrics.
func NewClusterMetricsHandler(r prometheus.Registerer) (func(context.Context, *api.HCPOpenShiftCluster), func(string)) {
	provisionState := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "backend_cluster_provision_state",
		Help: "Current provisioning state of the cluster (value is always 1).",
	}, []string{"resource_id_hash", "phase"})
	createdTime := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "backend_cluster_created_time_seconds",
		Help: "Unix timestamp when the cluster was created.",
	}, []string{"resource_id_hash"})
	r.MustRegister(provisionState, createdTime)

	syncFunc := func(ctx context.Context, cluster *api.HCPOpenShiftCluster) {
		if cluster.ID == nil {
			return
		}
		resourceID := strings.ToLower(cluster.ID.String())
		hash := ResourceIDHash(resourceID)
		phase := PhaseLabel(cluster.ServiceProviderProperties.ProvisioningState)

		partialMatch := prometheus.Labels{"resource_id_hash": hash}
		provisionState.DeletePartialMatch(partialMatch)
		provisionState.With(prometheus.Labels{
			"resource_id_hash": hash,
			"phase":            phase,
		}).Set(1.0)

		createdTimeLabels := prometheus.Labels{"resource_id_hash": hash}
		if cluster.SystemData != nil && cluster.SystemData.CreatedAt != nil && !cluster.SystemData.CreatedAt.IsZero() {
			createdTime.With(createdTimeLabels).Set(float64(cluster.SystemData.CreatedAt.Unix()))
		} else {
			createdTime.Delete(createdTimeLabels)
		}

		logger := utils.LoggerFromContext(ctx)
		logValues := append(
			utils.LogValues{"resource_id_hash", hash},
			utils.LogValues{}.AddLogValuesForResourceID(cluster.ID)...)
		logger.V(1).Info("Cluster metrics synced", logValues...)
	}

	deleteFunc := func(key string) {
		hash := ResourceIDHash(strings.ToLower(key))
		partialMatch := prometheus.Labels{"resource_id_hash": hash}
		provisionState.DeletePartialMatch(partialMatch)
		createdTime.DeletePartialMatch(partialMatch)
	}

	return syncFunc, deleteFunc
}

// NewNodePoolMetricsHandler creates sync and delete functions for nodepool metrics.
func NewNodePoolMetricsHandler(r prometheus.Registerer) (func(context.Context, *api.HCPOpenShiftClusterNodePool), func(string)) {
	provisionState := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "backend_nodepool_provision_state",
		Help: "Current provisioning state of the node pool (value is always 1).",
	}, []string{"resource_id_hash", "phase"})
	createdTime := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "backend_nodepool_created_time_seconds",
		Help: "Unix timestamp when the node pool was created.",
	}, []string{"resource_id_hash"})
	r.MustRegister(provisionState, createdTime)

	syncFunc := func(ctx context.Context, np *api.HCPOpenShiftClusterNodePool) {
		if np.ID == nil {
			return
		}
		resourceID := strings.ToLower(np.ID.String())
		hash := ResourceIDHash(resourceID)
		phase := PhaseLabel(np.Properties.ProvisioningState)

		partialMatch := prometheus.Labels{"resource_id_hash": hash}
		provisionState.DeletePartialMatch(partialMatch)
		provisionState.With(prometheus.Labels{
			"resource_id_hash": hash,
			"phase":            phase,
		}).Set(1.0)

		createdTimeLabels := prometheus.Labels{"resource_id_hash": hash}
		if np.SystemData != nil && np.SystemData.CreatedAt != nil && !np.SystemData.CreatedAt.IsZero() {
			createdTime.With(createdTimeLabels).Set(float64(np.SystemData.CreatedAt.Unix()))
		} else {
			createdTime.Delete(createdTimeLabels)
		}

		logger := utils.LoggerFromContext(ctx)
		logValues := append(
			utils.LogValues{"resource_id_hash", hash},
			utils.LogValues{}.AddLogValuesForResourceID(np.ID)...)
		logger.V(1).Info("NodePool metrics synced", logValues...)
	}

	deleteFunc := func(key string) {
		hash := ResourceIDHash(strings.ToLower(key))
		partialMatch := prometheus.Labels{"resource_id_hash": hash}
		provisionState.DeletePartialMatch(partialMatch)
		createdTime.DeletePartialMatch(partialMatch)
	}

	return syncFunc, deleteFunc
}

// NewExternalAuthMetricsHandler creates sync and delete functions for externalauth metrics.
func NewExternalAuthMetricsHandler(r prometheus.Registerer) (func(context.Context, *api.HCPOpenShiftClusterExternalAuth), func(string)) {
	provisionState := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "backend_externalauth_provision_state",
		Help: "Current provisioning state of the external auth (value is always 1).",
	}, []string{"resource_id_hash", "phase"})
	createdTime := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "backend_externalauth_created_time_seconds",
		Help: "Unix timestamp when the external auth was created.",
	}, []string{"resource_id_hash"})
	r.MustRegister(provisionState, createdTime)

	syncFunc := func(ctx context.Context, ea *api.HCPOpenShiftClusterExternalAuth) {
		if ea.ID == nil {
			return
		}
		resourceID := strings.ToLower(ea.ID.String())
		hash := ResourceIDHash(resourceID)
		phase := PhaseLabel(ea.Properties.ProvisioningState)

		partialMatch := prometheus.Labels{"resource_id_hash": hash}
		provisionState.DeletePartialMatch(partialMatch)
		provisionState.With(prometheus.Labels{
			"resource_id_hash": hash,
			"phase":            phase,
		}).Set(1.0)

		createdTimeLabels := prometheus.Labels{"resource_id_hash": hash}
		if ea.SystemData != nil && ea.SystemData.CreatedAt != nil && !ea.SystemData.CreatedAt.IsZero() {
			createdTime.With(createdTimeLabels).Set(float64(ea.SystemData.CreatedAt.Unix()))
		} else {
			createdTime.Delete(createdTimeLabels)
		}

		logger := utils.LoggerFromContext(ctx)
		logValues := append(
			utils.LogValues{"resource_id_hash", hash},
			utils.LogValues{}.AddLogValuesForResourceID(ea.ID)...)
		logger.V(1).Info("ExternalAuth metrics synced", logValues...)
	}

	deleteFunc := func(key string) {
		hash := ResourceIDHash(strings.ToLower(key))
		partialMatch := prometheus.Labels{"resource_id_hash": hash}
		provisionState.DeletePartialMatch(partialMatch)
		createdTime.DeletePartialMatch(partialMatch)
	}

	return syncFunc, deleteFunc
}
