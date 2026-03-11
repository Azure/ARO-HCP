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
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// ResourceIDHash returns the first 16 hex characters of the SHA-256 hash of
// the resource ID string. This anonymizes the resource identity in metric
// labels while remaining deterministic for correlation.
func ResourceIDHash(resourceID string) string {
	h := sha256.Sum256([]byte(resourceID))
	return fmt.Sprintf("%x", h[:8])
}

// ResourceMetricsExtractor extracts metric-relevant fields from a resource
// stored in the informer cache. Each resource type (cluster, nodepool,
// externalauth) implements this to map its fields to a common representation.
type ResourceMetricsExtractor interface {
	Extract(obj interface{}) (*ResourceMetrics, bool)
}

// ResourceMetrics holds the fields needed to emit metrics for a resource.
type ResourceMetrics struct {
	// ResourceID is the lowercased ARM resource ID string, used for
	// hash computation and logging. Not exposed directly in metric labels.
	ResourceID string
	// ProvisioningState is the current provisioning state of the resource.
	ProvisioningState arm.ProvisioningState
	// CreatedAt is the ARM SystemData creation timestamp. May be nil.
	CreatedAt *time.Time
}

// ResourceMetricsController reacts to informer events and maintains
// per-resource Prometheus gauge metrics using a level-driven approach.
// On each sync it reads the current state of the resource and sets metrics
// accordingly, with no in-memory state tracking beyond the informer cache.
//
// Metrics emitted per resource:
//   - {prefix}_provision_state{resource_id_hash, phase} = 1
//   - {prefix}_created_time_seconds{resource_id_hash} = unix timestamp
//
// The resource_id_hash label is a truncated SHA-256 hash of the ARM resource
// ID, anonymizing customer identifiers while remaining deterministic. The
// hash-to-ID mapping is logged at V(1) for correlation.
//
// Note: per-phase timestamp metrics (e.g. {prefix}_provisioned_time) are not
// currently possible because the resource objects in CosmosDB do not store
// per-phase transition timestamps. See ARO-25109 for tracking.
type ResourceMetricsController struct {
	name      string
	indexer   cache.Indexer
	extractor ResourceMetricsExtractor
	queue     workqueue.TypedRateLimitingInterface[string]

	provisionState *prometheus.GaugeVec
	createdTime    *prometheus.GaugeVec
}

// NewResourceMetricsController creates a ResourceMetricsController that watches
// the given informer and emits metrics with the given prefix (e.g. "backend_cluster",
// "backend_nodepool", "backend_externalauth").
func NewResourceMetricsController(
	name string,
	prefix string,
	r prometheus.Registerer,
	informer cache.SharedIndexInformer,
	extractor ResourceMetricsExtractor,
) *ResourceMetricsController {
	provisionState := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: prefix + "_provision_state",
		Help: "Current provisioning state of the resource (value is always 1).",
	}, []string{"resource_id_hash", "phase"})
	createdTime := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: prefix + "_created_time_seconds",
		Help: "Unix timestamp when the resource was created.",
	}, []string{"resource_id_hash"})
	r.MustRegister(provisionState, createdTime)

	c := &ResourceMetricsController{
		name:           name,
		indexer:        informer.GetIndexer(),
		extractor:      extractor,
		provisionState: provisionState,
		createdTime:    createdTime,
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

func (c *ResourceMetricsController) enqueue(obj interface{}) {
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
func (c *ResourceMetricsController) Run(ctx context.Context, threadiness int) {
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

func (c *ResourceMetricsController) runWorker(ctx context.Context) {
	for c.processNextWorkItem(ctx) {
	}
}

func (c *ResourceMetricsController) processNextWorkItem(ctx context.Context) bool {
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
// and sets metrics accordingly (level-driven).
func (c *ResourceMetricsController) syncResource(ctx context.Context, key string) error {
	obj, exists, err := c.indexer.GetByKey(key)
	if err != nil {
		return err
	}

	if !exists {
		c.deleteMetrics(key)
		return nil
	}

	metrics, ok := c.extractor.Extract(obj)
	if !ok {
		c.deleteMetrics(key)
		return nil
	}

	c.setMetrics(ctx, metrics)
	return nil
}

func (c *ResourceMetricsController) setMetrics(ctx context.Context, m *ResourceMetrics) {
	hash := ResourceIDHash(m.ResourceID)
	phase := PhaseLabel(m.ProvisioningState)

	// Level-driven: delete existing series, then set current state.
	partialMatch := prometheus.Labels{"resource_id_hash": hash}
	c.provisionState.DeletePartialMatch(partialMatch)

	c.provisionState.With(prometheus.Labels{
		"resource_id_hash": hash,
		"phase":            phase,
	}).Set(1.0)

	createdTimeLabels := prometheus.Labels{"resource_id_hash": hash}
	if m.CreatedAt != nil && !m.CreatedAt.IsZero() {
		c.createdTime.With(createdTimeLabels).Set(float64(m.CreatedAt.Unix()))
	} else {
		c.createdTime.Delete(createdTimeLabels)
	}

	logger := utils.LoggerFromContext(ctx)
	logValues := append(
		utils.LogValues{"resource_id_hash", hash},
		utils.LogValues{}.AddLogValuesForResourceIDString(m.ResourceID)...)
	logger.V(1).Info("Resource metrics synced", logValues...)
}

// deleteMetrics removes all metric series for the resource identified by the
// given store key. The store key is the lowercased resource ID string (from
// GetObjectMeta().Name), which is hashed to match the resource_id_hash label.
func (c *ResourceMetricsController) deleteMetrics(key string) {
	hash := ResourceIDHash(key)
	partialMatch := prometheus.Labels{"resource_id_hash": hash}
	c.provisionState.DeletePartialMatch(partialMatch)
	c.createdTime.DeletePartialMatch(partialMatch)
}

// ClusterMetricsExtractor extracts metrics from HCPOpenShiftCluster resources.
type ClusterMetricsExtractor struct{}

func (e *ClusterMetricsExtractor) Extract(obj interface{}) (*ResourceMetrics, bool) {
	cluster, ok := obj.(*api.HCPOpenShiftCluster)
	if !ok {
		return nil, false
	}
	if cluster.ID == nil {
		return nil, false
	}
	m := &ResourceMetrics{
		ResourceID:        strings.ToLower(cluster.ID.String()),
		ProvisioningState: cluster.ServiceProviderProperties.ProvisioningState,
	}
	if cluster.SystemData != nil {
		m.CreatedAt = cluster.SystemData.CreatedAt
	}
	return m, true
}

// NodePoolMetricsExtractor extracts metrics from HCPOpenShiftClusterNodePool resources.
type NodePoolMetricsExtractor struct{}

func (e *NodePoolMetricsExtractor) Extract(obj interface{}) (*ResourceMetrics, bool) {
	np, ok := obj.(*api.HCPOpenShiftClusterNodePool)
	if !ok {
		return nil, false
	}
	if np.ID == nil {
		return nil, false
	}
	m := &ResourceMetrics{
		ResourceID:        strings.ToLower(np.ID.String()),
		ProvisioningState: np.Properties.ProvisioningState,
	}
	if np.SystemData != nil {
		m.CreatedAt = np.SystemData.CreatedAt
	}
	return m, true
}

// ExternalAuthMetricsExtractor extracts metrics from HCPOpenShiftClusterExternalAuth resources.
type ExternalAuthMetricsExtractor struct{}

func (e *ExternalAuthMetricsExtractor) Extract(obj interface{}) (*ResourceMetrics, bool) {
	ea, ok := obj.(*api.HCPOpenShiftClusterExternalAuth)
	if !ok {
		return nil, false
	}
	if ea.ID == nil {
		return nil, false
	}
	m := &ResourceMetrics{
		ResourceID:        strings.ToLower(ea.ID.String()),
		ProvisioningState: ea.Properties.ProvisioningState,
	}
	if ea.SystemData != nil {
		m.CreatedAt = ea.SystemData.CreatedAt
	}
	return m, true
}
