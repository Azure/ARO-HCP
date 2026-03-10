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

	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
)

// OperationListSource abstracts operation listing for the collector.
// Satisfied by listers.OperationLister, listers.ActiveOperationLister, or any type with List.
type OperationListSource interface {
	List(ctx context.Context) ([]*api.Operation, error)
}

var (
	operationPhaseInfoDesc = prometheus.NewDesc(
		"backend_resource_operation_phase_info",
		"Current phase of each operation (value is always 1).",
		[]string{"operation_id_hash", "resource_type", "operation_type", "phase"},
		nil,
	)
	operationStartTimeDesc = prometheus.NewDesc(
		"backend_resource_operation_start_time_seconds",
		"Unix timestamp when the operation started.",
		[]string{"operation_id_hash", "resource_type", "operation_type", "phase"},
		nil,
	)
	operationLastTransitionTimeDesc = prometheus.NewDesc(
		"backend_resource_operation_last_transition_time_seconds",
		"Unix timestamp when the operation last changed phase.",
		[]string{"operation_id_hash", "resource_type", "operation_type", "phase"},
		nil,
	)
)

// OperationPhaseCollector is a custom prometheus.Collector that emits
// per-operation phase metrics on each Prometheus scrape.
type OperationPhaseCollector struct {
	lister OperationListSource
	logger logr.Logger
	mu     sync.Mutex
	seen   map[string]struct{}
}

// NewOperationPhaseCollector creates and registers an OperationPhaseCollector.
func NewOperationPhaseCollector(r prometheus.Registerer, lister OperationListSource, logger logr.Logger) *OperationPhaseCollector {
	c := &OperationPhaseCollector{
		lister: lister,
		logger: logger.WithName("OperationPhaseCollector"),
		seen:   make(map[string]struct{}),
	}
	r.MustRegister(c)
	return c
}

// Describe implements the prometheus.Collector interface.
func (c *OperationPhaseCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- operationPhaseInfoDesc
	ch <- operationStartTimeDesc
	ch <- operationLastTransitionTimeDesc
}

// Collect implements the prometheus.Collector interface.
func (c *OperationPhaseCollector) Collect(ch chan<- prometheus.Metric) {
	ops, err := c.lister.List(context.Background())
	if err != nil {
		c.logger.Error(err, "failed to list operations")
		return
	}

	currentHashes := make(map[string]struct{}, len(ops))

	for _, op := range ops {
		if op.OperationID == nil {
			continue
		}

		hash := OperationIDHash(op.OperationID.Name)
		resourceType := ResourceTypeFromExternalID(op.ExternalID)
		operationType := OperationTypeLabel(op.Request)
		phase := PhaseLabel(op.Status)

		currentHashes[hash] = struct{}{}

		// Log first-time hash-to-resource mapping.
		var firstSeen bool
		c.mu.Lock()
		if _, ok := c.seen[hash]; !ok {
			c.seen[hash] = struct{}{}
			firstSeen = true
		}
		c.mu.Unlock()

		if firstSeen {
			c.logger.Info("Operation tracked",
				"operationIDHash", hash,
				"operationID", op.OperationID.Name,
				"resourceID", externalIDString(op.ExternalID),
				"correlationID", op.CorrelationRequestID,
				"operationType", string(op.Request),
				"phase", string(op.Status),
			)
		}

		ch <- prometheus.MustNewConstMetric(
			operationPhaseInfoDesc,
			prometheus.GaugeValue,
			1.0,
			hash, resourceType, operationType, phase,
		)

		if !op.StartTime.IsZero() {
			ch <- prometheus.MustNewConstMetric(
				operationStartTimeDesc,
				prometheus.GaugeValue,
				float64(op.StartTime.Unix()),
				hash, resourceType, operationType, phase,
			)
		}

		if !op.LastTransitionTime.IsZero() {
			ch <- prometheus.MustNewConstMetric(
				operationLastTransitionTimeDesc,
				prometheus.GaugeValue,
				float64(op.LastTransitionTime.Unix()),
				hash, resourceType, operationType, phase,
			)
		}
	}

	// Clean seen hashes for operations no longer in the cache.
	c.mu.Lock()
	for hash := range c.seen {
		if _, ok := currentHashes[hash]; !ok {
			delete(c.seen, hash)
		}
	}
	c.mu.Unlock()
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
