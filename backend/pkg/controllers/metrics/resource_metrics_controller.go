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

package metrics

import (
	"context"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
)

type resourceStateMetricsObject interface {
	ResourceID() *azcorearm.ResourceID
	ProvisioningState() coreapi.ProvisioningState
	CreatedAt() *time.Time
}

type resourceStateMetricsHandler[T resourceStateMetricsObject] struct {
	provisionState *prometheus.GaugeVec
	createdTime    *prometheus.GaugeVec
}

func newResourceStateMetricsHandler[T resourceStateMetricsObject](
	r prometheus.Registerer,
	provisionMetricName string,
	provisionHelp string,
	createdMetricName string,
	createdHelp string,
) *resourceStateMetricsHandler[T] {
	h := &resourceStateMetricsHandler[T]{
		provisionState: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: provisionMetricName,
			Help: provisionHelp,
		}, []string{"resource_id", "subscription_id", "phase"}),
		createdTime: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: createdMetricName,
			Help: createdHelp,
		}, []string{"resource_id", "subscription_id"}),
	}
	r.MustRegister(h.provisionState, h.createdTime)
	return h
}

func (h *resourceStateMetricsHandler[T]) Sync(_ context.Context, obj T) {
	resourceIDValue := obj.ResourceID()
	resourceID := resourceIDMetricLabel(resourceIDValue)
	if len(resourceID) == 0 {
		return
	}
	subscriptionID := subscriptionIDMetricLabel(resourceIDValue)

	deleteSelector := prometheus.Labels{"resource_id": resourceID}
	h.provisionState.DeletePartialMatch(deleteSelector)
	h.provisionState.With(prometheus.Labels{
		"resource_id":     resourceID,
		"subscription_id": subscriptionID,
		"phase":           phaseMetricLabel(obj.ProvisioningState()),
	}).Set(1.0)

	createdTimeLabels := prometheus.Labels{
		"resource_id":     resourceID,
		"subscription_id": subscriptionID,
	}
	createdAt := obj.CreatedAt()
	if createdAt != nil && !createdAt.IsZero() {
		h.createdTime.With(createdTimeLabels).Set(float64(createdAt.Unix()))
	} else {
		h.createdTime.DeletePartialMatch(deleteSelector)
	}
}

func (h *resourceStateMetricsHandler[T]) Delete(key string) {
	if len(key) == 0 {
		return
	}

	deleteSelector := prometheus.Labels{"resource_id": key}
	h.provisionState.DeletePartialMatch(deleteSelector)
	h.createdTime.DeletePartialMatch(deleteSelector)
}

type clusterMetricsObject struct {
	*coreapi.Cluster
}

func (o clusterMetricsObject) ResourceID() *azcorearm.ResourceID {
	if o.Cluster == nil {
		return nil
	}
	return o.ID
}

func (o clusterMetricsObject) ProvisioningState() coreapi.ProvisioningState {
	if o.Cluster == nil {
		return ""
	}
	return o.ServiceProviderProperties.ProvisioningState
}

func (o clusterMetricsObject) CreatedAt() *time.Time {
	if o.Cluster == nil || o.SystemData == nil {
		return nil
	}
	return o.SystemData.CreatedAt
}

type clusterMetricsHandler struct {
	*resourceStateMetricsHandler[clusterMetricsObject]
}

// NewClusterMetricsHandler creates a metrics handler for cluster metrics.
func NewClusterMetricsHandler(r prometheus.Registerer) Handler[*coreapi.Cluster] {
	return &clusterMetricsHandler{
		resourceStateMetricsHandler: newResourceStateMetricsHandler[clusterMetricsObject](
			r,
			"backend_cluster_provision_state",
			"Current provisioning state of the cluster (value is always 1).",
			"backend_cluster_created_time_seconds",
			"Unix timestamp when the cluster was created.",
		),
	}
}

func (h *clusterMetricsHandler) Sync(ctx context.Context, cluster *coreapi.Cluster) {
	h.resourceStateMetricsHandler.Sync(ctx, clusterMetricsObject{cluster})
}

type nodePoolMetricsObject struct {
	*coreapi.ClusterNodePool
}

func (o nodePoolMetricsObject) ResourceID() *azcorearm.ResourceID {
	if o.ClusterNodePool == nil {
		return nil
	}
	return o.ID
}

func (o nodePoolMetricsObject) ProvisioningState() coreapi.ProvisioningState {
	if o.ClusterNodePool == nil {
		return ""
	}
	return o.Properties.ProvisioningState
}

func (o nodePoolMetricsObject) CreatedAt() *time.Time {
	if o.ClusterNodePool == nil || o.SystemData == nil {
		return nil
	}
	return o.SystemData.CreatedAt
}

type nodePoolMetricsHandler struct {
	*resourceStateMetricsHandler[nodePoolMetricsObject]
}

// NewNodePoolMetricsHandler creates a metrics handler for node pool metrics.
func NewNodePoolMetricsHandler(r prometheus.Registerer) Handler[*coreapi.ClusterNodePool] {
	return &nodePoolMetricsHandler{
		newResourceStateMetricsHandler[nodePoolMetricsObject](
			r,
			"backend_nodepool_provision_state",
			"Current provisioning state of the node pool (value is always 1).",
			"backend_nodepool_created_time_seconds",
			"Unix timestamp when the node pool was created.",
		),
	}
}

func (h *nodePoolMetricsHandler) Sync(ctx context.Context, nodePool *coreapi.ClusterNodePool) {
	h.resourceStateMetricsHandler.Sync(ctx, nodePoolMetricsObject{nodePool})
}

type externalAuthMetricsObject struct {
	*coreapi.ClusterExternalAuth
}

func (o externalAuthMetricsObject) ResourceID() *azcorearm.ResourceID {
	if o.ClusterExternalAuth == nil {
		return nil
	}
	return o.ID
}

func (o externalAuthMetricsObject) ProvisioningState() coreapi.ProvisioningState {
	if o.ClusterExternalAuth == nil {
		return ""
	}
	return o.Properties.ProvisioningState
}

func (o externalAuthMetricsObject) CreatedAt() *time.Time {
	if o.ClusterExternalAuth == nil || o.SystemData == nil {
		return nil
	}
	return o.SystemData.CreatedAt
}

type externalAuthMetricsHandler struct {
	*resourceStateMetricsHandler[externalAuthMetricsObject]
}

// NewExternalAuthMetricsHandler creates a metrics handler for external auth metrics.
func NewExternalAuthMetricsHandler(r prometheus.Registerer) Handler[*coreapi.ClusterExternalAuth] {
	return &externalAuthMetricsHandler{
		newResourceStateMetricsHandler[externalAuthMetricsObject](
			r,
			"backend_externalauth_provision_state",
			"Current provisioning state of the external auth (value is always 1).",
			"backend_externalauth_created_time_seconds",
			"Unix timestamp when the external auth was created.",
		),
	}
}

func (h *externalAuthMetricsHandler) Sync(ctx context.Context, externalAuth *coreapi.ClusterExternalAuth) {
	h.resourceStateMetricsHandler.Sync(ctx, externalAuthMetricsObject{externalAuth})
}
