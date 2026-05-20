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

package maestro

import (
	"context"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	workv1 "open-cluster-management.io/api/work/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

type MaestroMetrics struct {
	operationsTotal   *prometheus.CounterVec
	errorsTotal       *prometheus.CounterVec
	operationDuration *prometheus.HistogramVec
	eventsPublished   prometheus.Counter
	eventsSubscribed  prometheus.Counter
}

func NewMaestroMetrics(r prometheus.Registerer) *MaestroMetrics {
	m := &MaestroMetrics{
		operationsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "maestro_grpc_operations_total",
				Help: "Total number of Maestro GRPC operations by operation type",
			},
			[]string{"operation"},
		),
		errorsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "maestro_grpc_errors_total",
				Help: "Total number of failed Maestro GRPC operations (dropped events)",
			},
			[]string{"operation"},
		),
		operationDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "maestro_grpc_operation_duration_seconds",
				Help:    "Duration of Maestro GRPC operations in seconds",
				Buckets: prometheus.DefBuckets,
			},
			[]string{"operation"},
		),
		eventsPublished: prometheus.NewCounter(
			prometheus.CounterOpts{
				Name: "maestro_grpc_events_published_total",
				Help: "Total number of events published to Maestro (Create, Patch, Delete operations)",
			},
		),
		eventsSubscribed: prometheus.NewCounter(
			prometheus.CounterOpts{
				Name: "maestro_grpc_events_subscribed_total",
				Help: "Total number of events subscribed from Maestro (Get, List operations)",
			},
		),
	}

	r.MustRegister(
		m.operationsTotal,
		m.errorsTotal,
		m.operationDuration,
		m.eventsPublished,
		m.eventsSubscribed,
	)

	// Pre-initialize all operation label values so metrics appear even with zero values
	operations := []string{"create", "get", "delete", "patch", "list"}
	for _, op := range operations {
		m.operationsTotal.WithLabelValues(op)
		m.errorsTotal.WithLabelValues(op)
		m.operationDuration.WithLabelValues(op)
	}

	return m
}

type instrumentedMaestroClient struct {
	client  Client
	metrics *MaestroMetrics
}

func NewInstrumentedMaestroClient(client Client, metrics *MaestroMetrics) Client {
	return &instrumentedMaestroClient{
		client:  client,
		metrics: metrics,
	}
}

// observe records metrics for a completed operation.
// Uses defer-friendly design to ensure metrics are always recorded even on panics.
func (c *instrumentedMaestroClient) observe(operation string, start time.Time, err error, eventType string) {
	c.metrics.operationDuration.WithLabelValues(operation).Observe(time.Since(start).Seconds())

	if err != nil {
		c.metrics.errorsTotal.WithLabelValues(operation).Inc()
	} else {
		switch eventType {
		case "published":
			c.metrics.eventsPublished.Inc()
		case "subscribed":
			c.metrics.eventsSubscribed.Inc()
		}
	}
}

func (c *instrumentedMaestroClient) Create(ctx context.Context, manifestWork *workv1.ManifestWork, opts metav1.CreateOptions) (*workv1.ManifestWork, error) {
	operation := "create"
	start := time.Now()
	c.metrics.operationsTotal.WithLabelValues(operation).Inc()

	result, err := c.client.Create(ctx, manifestWork, opts)
	c.observe(operation, start, err, "published")

	return result, err
}

func (c *instrumentedMaestroClient) Get(ctx context.Context, name string, opts metav1.GetOptions) (*workv1.ManifestWork, error) {
	operation := "get"
	start := time.Now()
	c.metrics.operationsTotal.WithLabelValues(operation).Inc()

	result, err := c.client.Get(ctx, name, opts)
	c.observe(operation, start, err, "subscribed")

	return result, err
}

func (c *instrumentedMaestroClient) Delete(ctx context.Context, name string, opts metav1.DeleteOptions) error {
	operation := "delete"
	start := time.Now()
	c.metrics.operationsTotal.WithLabelValues(operation).Inc()

	err := c.client.Delete(ctx, name, opts)
	c.observe(operation, start, err, "published")

	return err
}

func (c *instrumentedMaestroClient) Patch(ctx context.Context, name string, pt types.PatchType, data []byte, opts metav1.PatchOptions, subresources ...string) (*workv1.ManifestWork, error) {
	operation := "patch"
	start := time.Now()
	c.metrics.operationsTotal.WithLabelValues(operation).Inc()

	result, err := c.client.Patch(ctx, name, pt, data, opts, subresources...)
	c.observe(operation, start, err, "published")

	return result, err
}

func (c *instrumentedMaestroClient) List(ctx context.Context, opts metav1.ListOptions) (*workv1.ManifestWorkList, error) {
	operation := "list"
	start := time.Now()
	c.metrics.operationsTotal.WithLabelValues(operation).Inc()

	result, err := c.client.List(ctx, opts)
	c.observe(operation, start, err, "subscribed")

	return result, err
}
