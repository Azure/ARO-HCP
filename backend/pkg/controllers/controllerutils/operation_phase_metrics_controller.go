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
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

var labelNames = []string{"operation_id_hash", "resource_type", "operation_type", "phase"}

type operationMetricLabels struct {
	Hash          string
	ResourceType  string
	OperationType string
	Phase         string
}

func (l operationMetricLabels) prometheusLabels() prometheus.Labels {
	return prometheus.Labels{
		"operation_id_hash": l.Hash,
		"resource_type":     l.ResourceType,
		"operation_type":    l.OperationType,
		"phase":             l.Phase,
	}
}

// OperationPhaseMetricsController reacts to informer events and maintains
// per-operation Prometheus gauge metrics. It follows the workqueue pattern
// used by other controllers in this codebase.
type OperationPhaseMetricsController struct {
	name    string
	indexer cache.Indexer
	queue   workqueue.TypedRateLimitingInterface[string]

	phaseInfo          *prometheus.GaugeVec
	startTime          *prometheus.GaugeVec
	lastTransitionTime *prometheus.GaugeVec

	mu    sync.Mutex
	known map[string]operationMetricLabels
}

// NewOperationPhaseMetricsController creates an OperationPhaseMetricsController
// that watches the given informer for operation changes and updates Prometheus
// metrics via a workqueue.
func NewOperationPhaseMetricsController(
	r prometheus.Registerer,
	informer cache.SharedIndexInformer,
) *OperationPhaseMetricsController {
	phaseInfo := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "backend_resource_operation_phase_info",
		Help: "Current phase of each operation (value is always 1).",
	}, labelNames)
	startTime := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "backend_resource_operation_start_time_seconds",
		Help: "Unix timestamp when the operation started.",
	}, labelNames)
	lastTransitionTime := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "backend_resource_operation_last_transition_time_seconds",
		Help: "Unix timestamp when the operation last changed phase.",
	}, labelNames)
	r.MustRegister(phaseInfo, startTime, lastTransitionTime)

	c := &OperationPhaseMetricsController{
		name:               "OperationPhaseMetrics",
		indexer:            informer.GetIndexer(),
		phaseInfo:          phaseInfo,
		startTime:          startTime,
		lastTransitionTime: lastTransitionTime,
		known:              make(map[string]operationMetricLabels),
		queue: workqueue.NewTypedRateLimitingQueueWithConfig(
			workqueue.DefaultTypedControllerRateLimiter[string](),
			workqueue.TypedRateLimitingQueueConfig[string]{
				Name: "OperationPhaseMetrics",
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

func (c *OperationPhaseMetricsController) enqueue(obj interface{}) {
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
func (c *OperationPhaseMetricsController) Run(ctx context.Context, threadiness int) {
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

func (c *OperationPhaseMetricsController) runWorker(ctx context.Context) {
	for c.processNextWorkItem(ctx) {
	}
}

func (c *OperationPhaseMetricsController) processNextWorkItem(ctx context.Context) bool {
	key, shutdown := c.queue.Get()
	if shutdown {
		return false
	}
	defer c.queue.Done(key)

	ReconcileTotal.WithLabelValues(c.name).Inc()
	err := c.syncOperation(ctx, key)
	if err == nil {
		c.queue.Forget(key)
		return true
	}

	utilruntime.HandleErrorWithContext(ctx, err, "Error syncing operation phase metrics", "key", key)
	c.queue.AddRateLimited(key)
	return true
}

// syncOperation processes a single operation key: sets metrics if the operation
// exists, or deletes metrics if it has been removed from the store.
func (c *OperationPhaseMetricsController) syncOperation(ctx context.Context, key string) error {
	obj, exists, err := c.indexer.GetByKey(key)
	if err != nil {
		return err
	}

	if !exists {
		c.deleteMetrics(key)
		return nil
	}

	op, ok := obj.(*api.Operation)
	if !ok {
		return fmt.Errorf("expected *api.Operation, got %T", obj)
	}

	if op.OperationID == nil {
		return nil
	}

	c.setMetrics(ctx, key, op)
	return nil
}

func (c *OperationPhaseMetricsController) setMetrics(ctx context.Context, key string, op *api.Operation) {
	labels := operationMetricLabels{
		Hash:          OperationIDHash(op.OperationID.Name),
		ResourceType:  ResourceTypeFromExternalID(op.ExternalID),
		OperationType: OperationTypeLabel(op.Request),
		Phase:         PhaseLabel(op.Status),
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	old, hadOld := c.known[key]
	c.known[key] = labels

	// If labels changed (e.g. phase transition), delete old metric series.
	if hadOld && old != labels {
		c.deleteGaugeValues(old)
	}

	promLabels := labels.prometheusLabels()
	c.phaseInfo.With(promLabels).Set(1.0)

	if !op.StartTime.IsZero() {
		c.startTime.With(promLabels).Set(float64(op.StartTime.Unix()))
	}
	if !op.LastTransitionTime.IsZero() {
		c.lastTransitionTime.With(promLabels).Set(float64(op.LastTransitionTime.Unix()))
	}

	if !hadOld {
		logger := utils.LoggerFromContext(ctx)
		logger.Info("Operation tracked",
			utils.LogValues{}.
				AddOperationID(op.OperationID.Name).
				AddResourceID(externalIDString(op.ExternalID)).
				AddCorrelationRequestID(op.CorrelationRequestID)...)
	}
}

func (c *OperationPhaseMetricsController) deleteMetrics(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	old, ok := c.known[key]
	if ok {
		delete(c.known, key)
		c.deleteGaugeValues(old)
	}
}

func (c *OperationPhaseMetricsController) deleteGaugeValues(labels operationMetricLabels) {
	promLabels := labels.prometheusLabels()
	c.phaseInfo.Delete(promLabels)
	c.startTime.Delete(promLabels)
	c.lastTransitionTime.Delete(promLabels)
}

func externalIDString(id *azcorearm.ResourceID) string {
	if id == nil {
		return ""
	}
	return id.String()
}

// OperationIDHash returns the first 16 hex characters of the SHA-256 hash of name.
func OperationIDHash(name string) string {
	h := sha256.Sum256([]byte(name))
	return fmt.Sprintf("%x", h[:8])
}

// PhaseLabel returns the lowercased provisioning state string for use as a metric label.
func PhaseLabel(status arm.ProvisioningState) string {
	return strings.ToLower(string(status))
}

// ResourceTypeFromExternalID derives a resource type label from an ExternalID.
func ResourceTypeFromExternalID(externalID *azcorearm.ResourceID) string {
	if externalID == nil {
		return "unknown"
	}
	rt := externalID.ResourceType.String()
	switch {
	case strings.EqualFold(rt, api.ClusterResourceType.String()):
		return "cluster"
	case strings.EqualFold(rt, api.NodePoolResourceType.String()):
		return "nodepool"
	case strings.EqualFold(rt, api.ExternalAuthResourceType.String()):
		return "externalauth"
	default:
		return "unknown"
	}
}

// OperationTypeLabel returns the lowercased operation request string for use as a metric label.
func OperationTypeLabel(request api.OperationRequest) string {
	return strings.ToLower(string(request))
}
