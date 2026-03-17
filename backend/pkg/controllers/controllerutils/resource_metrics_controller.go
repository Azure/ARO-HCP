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
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// resourceIDHashNamespace is a fixed UUID namespace for deterministic hashing
// of resource IDs into metric labels.
var resourceIDHashNamespace = uuid.MustParse("a3e4c5f6-7b8d-4e2a-9f1c-0d3e5a7b9c1d")

// ResourceIDHash returns a deterministic UUID-based hash of the resource ID
// string. The input is lowercased to ensure consistent hashes regardless of
// input casing. This uses the same uuid.NewSHA1 technique as
// arm.ResourceIDToCosmosID, but with a separate namespace to avoid collisions.
func ResourceIDHash(resourceID string) string {
	return uuid.NewSHA1(resourceIDHashNamespace, []byte(strings.ToLower(resourceID))).String()
}

// ResourceMetricsHandler defines the interface for resource-specific metric
// handling. Implementations emit and clean up Prometheus metrics for a
// specific resource type.
type ResourceMetricsHandler[T any] interface {
	Sync(ctx context.Context, obj T)
	Delete(key string)
}

// ResourceMetricsController[T] reacts to informer events and maintains
// per-resource Prometheus gauge metrics using a level-driven approach.
// On each sync it reads the current state of the resource and passes the
// typed object to the handler. The controller handles workqueue boilerplate
// and type-safe object extraction via generics.
//
// T is the concrete resource type stored in the informer (e.g.
// *api.HCPOpenShiftCluster, *api.HCPOpenShiftClusterNodePool).
type ResourceMetricsController[T any] struct {
	name    string
	indexer cache.Indexer
	queue   workqueue.TypedRateLimitingInterface[string]
	handler ResourceMetricsHandler[T]
}

// NewResourceMetricsController creates a generic, type-safe metrics controller.
// The handler's Sync method is called with the typed resource object on each sync.
// The handler's Delete method is called with the store key when the resource is
// removed from the informer cache.
func NewResourceMetricsController[T any](
	name string,
	informer cache.SharedIndexInformer,
	handler ResourceMetricsHandler[T],
) *ResourceMetricsController[T] {
	c := &ResourceMetricsController[T]{
		name:    name,
		indexer: informer.GetIndexer(),
		handler: handler,
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
		c.handler.Delete(key)
		return nil
	}

	typed, ok := obj.(T)
	if !ok {
		logger := utils.LoggerFromContext(ctx)
		logger.Error(nil, "Unexpected object type in indexer", "key", key, "controller", c.name)
		c.handler.Delete(key)
		return nil
	}

	c.handler.Sync(ctx, typed)
	return nil
}

// clusterMetricsHandler implements ResourceMetricsHandler for HCPOpenShiftCluster.
// Not thread-safe; relies on the controller running with threadiness=1.
type clusterMetricsHandler struct {
	provisionState *prometheus.GaugeVec
	createdTime    *prometheus.GaugeVec
	lastPhase      map[string]string
}

// NewClusterMetricsHandler creates a ResourceMetricsHandler for cluster metrics.
func NewClusterMetricsHandler(r prometheus.Registerer) ResourceMetricsHandler[*api.HCPOpenShiftCluster] {
	h := &clusterMetricsHandler{
		provisionState: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "backend_cluster_provision_state",
			Help: "Current provisioning state of the cluster (value is always 1).",
		}, []string{"resource_id_hash", "phase"}),
		createdTime: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "backend_cluster_created_time_seconds",
			Help: "Unix timestamp when the cluster was created.",
		}, []string{"resource_id_hash"}),
		lastPhase: make(map[string]string),
	}
	r.MustRegister(h.provisionState, h.createdTime)
	return h
}

func (h *clusterMetricsHandler) Sync(ctx context.Context, cluster *api.HCPOpenShiftCluster) {
	if cluster.ID == nil {
		return
	}
	resourceID := strings.ToLower(cluster.ID.String())
	hash := ResourceIDHash(resourceID)
	phase := PhaseLabel(cluster.ServiceProviderProperties.ProvisioningState)

	if oldPhase, ok := h.lastPhase[hash]; ok && oldPhase != phase {
		h.provisionState.Delete(prometheus.Labels{"resource_id_hash": hash, "phase": oldPhase})
	}
	h.provisionState.With(prometheus.Labels{
		"resource_id_hash": hash,
		"phase":            phase,
	}).Set(1.0)
	h.lastPhase[hash] = phase

	createdTimeLabels := prometheus.Labels{"resource_id_hash": hash}
	if cluster.SystemData != nil && cluster.SystemData.CreatedAt != nil && !cluster.SystemData.CreatedAt.IsZero() {
		h.createdTime.With(createdTimeLabels).Set(float64(cluster.SystemData.CreatedAt.Unix()))
	} else {
		h.createdTime.Delete(createdTimeLabels)
	}

	logger := utils.LoggerFromContext(ctx)
	logValues := append(
		utils.LogValues{"resource_id_hash", hash},
		utils.LogValues{}.AddLogValuesForResourceID(cluster.ID)...)
	logger.V(1).Info("Cluster metrics synced", logValues...)
}

func (h *clusterMetricsHandler) Delete(key string) {
	hash := ResourceIDHash(key)
	if oldPhase, ok := h.lastPhase[hash]; ok {
		h.provisionState.Delete(prometheus.Labels{"resource_id_hash": hash, "phase": oldPhase})
		delete(h.lastPhase, hash)
	}
	h.createdTime.Delete(prometheus.Labels{"resource_id_hash": hash})
}

// nodePoolMetricsHandler implements ResourceMetricsHandler for HCPOpenShiftClusterNodePool.
// Not thread-safe; relies on the controller running with threadiness=1.
type nodePoolMetricsHandler struct {
	provisionState *prometheus.GaugeVec
	createdTime    *prometheus.GaugeVec
	lastPhase      map[string]string
}

// NewNodePoolMetricsHandler creates a ResourceMetricsHandler for nodepool metrics.
func NewNodePoolMetricsHandler(r prometheus.Registerer) ResourceMetricsHandler[*api.HCPOpenShiftClusterNodePool] {
	h := &nodePoolMetricsHandler{
		provisionState: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "backend_nodepool_provision_state",
			Help: "Current provisioning state of the node pool (value is always 1).",
		}, []string{"resource_id_hash", "phase"}),
		createdTime: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "backend_nodepool_created_time_seconds",
			Help: "Unix timestamp when the node pool was created.",
		}, []string{"resource_id_hash"}),
		lastPhase: make(map[string]string),
	}
	r.MustRegister(h.provisionState, h.createdTime)
	return h
}

func (h *nodePoolMetricsHandler) Sync(ctx context.Context, np *api.HCPOpenShiftClusterNodePool) {
	if np.ID == nil {
		return
	}
	resourceID := strings.ToLower(np.ID.String())
	hash := ResourceIDHash(resourceID)
	phase := PhaseLabel(np.Properties.ProvisioningState)

	if oldPhase, ok := h.lastPhase[hash]; ok && oldPhase != phase {
		h.provisionState.Delete(prometheus.Labels{"resource_id_hash": hash, "phase": oldPhase})
	}
	h.provisionState.With(prometheus.Labels{
		"resource_id_hash": hash,
		"phase":            phase,
	}).Set(1.0)
	h.lastPhase[hash] = phase

	createdTimeLabels := prometheus.Labels{"resource_id_hash": hash}
	if np.SystemData != nil && np.SystemData.CreatedAt != nil && !np.SystemData.CreatedAt.IsZero() {
		h.createdTime.With(createdTimeLabels).Set(float64(np.SystemData.CreatedAt.Unix()))
	} else {
		h.createdTime.Delete(createdTimeLabels)
	}

	logger := utils.LoggerFromContext(ctx)
	logValues := append(
		utils.LogValues{"resource_id_hash", hash},
		utils.LogValues{}.AddLogValuesForResourceID(np.ID)...)
	logger.V(1).Info("NodePool metrics synced", logValues...)
}

func (h *nodePoolMetricsHandler) Delete(key string) {
	hash := ResourceIDHash(key)
	if oldPhase, ok := h.lastPhase[hash]; ok {
		h.provisionState.Delete(prometheus.Labels{"resource_id_hash": hash, "phase": oldPhase})
		delete(h.lastPhase, hash)
	}
	h.createdTime.Delete(prometheus.Labels{"resource_id_hash": hash})
}

// externalAuthMetricsHandler implements ResourceMetricsHandler for HCPOpenShiftClusterExternalAuth.
// Not thread-safe; relies on the controller running with threadiness=1.
type externalAuthMetricsHandler struct {
	provisionState *prometheus.GaugeVec
	createdTime    *prometheus.GaugeVec
	lastPhase      map[string]string
}

// NewExternalAuthMetricsHandler creates a ResourceMetricsHandler for externalauth metrics.
func NewExternalAuthMetricsHandler(r prometheus.Registerer) ResourceMetricsHandler[*api.HCPOpenShiftClusterExternalAuth] {
	h := &externalAuthMetricsHandler{
		provisionState: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "backend_externalauth_provision_state",
			Help: "Current provisioning state of the external auth (value is always 1).",
		}, []string{"resource_id_hash", "phase"}),
		createdTime: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "backend_externalauth_created_time_seconds",
			Help: "Unix timestamp when the external auth was created.",
		}, []string{"resource_id_hash"}),
		lastPhase: make(map[string]string),
	}
	r.MustRegister(h.provisionState, h.createdTime)
	return h
}

func (h *externalAuthMetricsHandler) Sync(ctx context.Context, ea *api.HCPOpenShiftClusterExternalAuth) {
	if ea.ID == nil {
		return
	}
	resourceID := strings.ToLower(ea.ID.String())
	hash := ResourceIDHash(resourceID)
	phase := PhaseLabel(ea.Properties.ProvisioningState)

	if oldPhase, ok := h.lastPhase[hash]; ok && oldPhase != phase {
		h.provisionState.Delete(prometheus.Labels{"resource_id_hash": hash, "phase": oldPhase})
	}
	h.provisionState.With(prometheus.Labels{
		"resource_id_hash": hash,
		"phase":            phase,
	}).Set(1.0)
	h.lastPhase[hash] = phase

	createdTimeLabels := prometheus.Labels{"resource_id_hash": hash}
	if ea.SystemData != nil && ea.SystemData.CreatedAt != nil && !ea.SystemData.CreatedAt.IsZero() {
		h.createdTime.With(createdTimeLabels).Set(float64(ea.SystemData.CreatedAt.Unix()))
	} else {
		h.createdTime.Delete(createdTimeLabels)
	}

	logger := utils.LoggerFromContext(ctx)
	logValues := append(
		utils.LogValues{"resource_id_hash", hash},
		utils.LogValues{}.AddLogValuesForResourceID(ea.ID)...)
	logger.V(1).Info("ExternalAuth metrics synced", logValues...)
}

func (h *externalAuthMetricsHandler) Delete(key string) {
	hash := ResourceIDHash(key)
	if oldPhase, ok := h.lastPhase[hash]; ok {
		h.provisionState.Delete(prometheus.Labels{"resource_id_hash": hash, "phase": oldPhase})
		delete(h.lastPhase, hash)
	}
	h.createdTime.Delete(prometheus.Labels{"resource_id_hash": hash})
}
