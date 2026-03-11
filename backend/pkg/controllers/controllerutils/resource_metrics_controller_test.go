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
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"k8s.io/client-go/tools/cache"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
)

func newTestResourceMetricsController(t *testing.T, prefix string, extractor ResourceMetricsExtractor) *ResourceMetricsController {
	t.Helper()
	reg := prometheus.NewRegistry()

	ps := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: prefix + "_provision_state",
		Help: "Current provisioning state of the resource (value is always 1).",
	}, []string{"resource_id_hash", "phase"})
	ct := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: prefix + "_created_time_seconds",
		Help: "Unix timestamp when the resource was created.",
	}, []string{"resource_id_hash"})
	reg.MustRegister(ps, ct)

	return &ResourceMetricsController{
		name:           "TestMetrics",
		extractor:      extractor,
		provisionState: ps,
		createdTime:    ct,
	}
}

func TestResourceIDHash(t *testing.T) {
	t.Run("returns 16 hex characters", func(t *testing.T) {
		h := ResourceIDHash("test-resource")
		assert.Len(t, h, 16)
		for _, c := range h {
			assert.True(t, (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f'),
				"expected hex character, got %c", c)
		}
	})

	t.Run("is deterministic", func(t *testing.T) {
		h1 := ResourceIDHash("same-input")
		h2 := ResourceIDHash("same-input")
		assert.Equal(t, h1, h2)
	})

	t.Run("different inputs produce different hashes", func(t *testing.T) {
		h1 := ResourceIDHash("input-a")
		h2 := ResourceIDHash("input-b")
		assert.NotEqual(t, h1, h2)
	})
}

func TestClusterMetricsExtractor(t *testing.T) {
	now := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	cluster := &api.HCPOpenShiftCluster{
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				ID:         testParseResourceID(t, "/subscriptions/sub-1/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-1"),
				SystemData: &arm.SystemData{CreatedAt: &now},
			},
		},
		ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
			ProvisioningState: arm.ProvisioningStateProvisioning,
		},
	}

	e := &ClusterMetricsExtractor{}
	m, ok := e.Extract(cluster)
	require.True(t, ok)
	assert.Contains(t, m.ResourceID, "cluster-1")
	assert.Equal(t, arm.ProvisioningStateProvisioning, m.ProvisioningState)
	assert.Equal(t, &now, m.CreatedAt)
}

func TestNodePoolMetricsExtractor(t *testing.T) {
	now := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	np := &api.HCPOpenShiftClusterNodePool{
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				ID:         testParseResourceID(t, "/subscriptions/sub-1/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-1/nodePools/np-1"),
				SystemData: &arm.SystemData{CreatedAt: &now},
			},
		},
		Properties: api.HCPOpenShiftClusterNodePoolProperties{
			ProvisioningState: arm.ProvisioningStateSucceeded,
		},
	}

	e := &NodePoolMetricsExtractor{}
	m, ok := e.Extract(np)
	require.True(t, ok)
	assert.Contains(t, m.ResourceID, "np-1")
	assert.Equal(t, arm.ProvisioningStateSucceeded, m.ProvisioningState)
	assert.Equal(t, &now, m.CreatedAt)
}

func TestExternalAuthMetricsExtractor(t *testing.T) {
	now := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	ea := &api.HCPOpenShiftClusterExternalAuth{
		ProxyResource: arm.ProxyResource{
			Resource: arm.Resource{
				ID:         testParseResourceID(t, "/subscriptions/sub-1/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-1/externalAuths/ea-1"),
				SystemData: &arm.SystemData{CreatedAt: &now},
			},
		},
		Properties: api.HCPOpenShiftClusterExternalAuthProperties{
			ProvisioningState: arm.ProvisioningStateAccepted,
		},
	}

	e := &ExternalAuthMetricsExtractor{}
	m, ok := e.Extract(ea)
	require.True(t, ok)
	assert.Contains(t, m.ResourceID, "ea-1")
	assert.Equal(t, arm.ProvisioningStateAccepted, m.ProvisioningState)
	assert.Equal(t, &now, m.CreatedAt)
}

func TestExtractor_NilID(t *testing.T) {
	cluster := &api.HCPOpenShiftCluster{}
	e := &ClusterMetricsExtractor{}
	_, ok := e.Extract(cluster)
	assert.False(t, ok, "expected false for cluster with nil ID")
}

func TestExtractor_WrongType(t *testing.T) {
	e := &ClusterMetricsExtractor{}
	_, ok := e.Extract("not a cluster")
	assert.False(t, ok, "expected false for wrong type")
}

func TestSetMetrics_SetsProvisionStateAndCreatedTime(t *testing.T) {
	now := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	resourceID := "/subscriptions/sub-1/resourcegroups/rg/providers/microsoft.redhatopenshift/hcpopenshiftclusters/cluster-1"
	hash := ResourceIDHash(resourceID)

	c := newTestResourceMetricsController(t, "backend_cluster", &ClusterMetricsExtractor{})
	c.setMetrics(context.Background(), &ResourceMetrics{
		ResourceID:        resourceID,
		ProvisioningState: arm.ProvisioningStateProvisioning,
		CreatedAt:         &now,
	})

	assert.Equal(t, 1, testutil.CollectAndCount(c.provisionState))
	assert.Equal(t, 1, testutil.CollectAndCount(c.createdTime))

	expected := fmt.Sprintf(`# HELP backend_cluster_provision_state Current provisioning state of the resource (value is always 1).
# TYPE backend_cluster_provision_state gauge
backend_cluster_provision_state{phase="provisioning",resource_id_hash="%s"} 1
`, hash)

	err := testutil.CollectAndCompare(c.provisionState, strings.NewReader(expected))
	require.NoError(t, err)
}

func TestResourceMetrics_PhaseTransitionDeletesOldSeries(t *testing.T) {
	now := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	resourceID := "/subscriptions/sub-1/resourcegroups/rg/providers/microsoft.redhatopenshift/hcpopenshiftclusters/cluster-1"
	hash := ResourceIDHash(resourceID)
	c := newTestResourceMetricsController(t, "backend_cluster", &ClusterMetricsExtractor{})

	c.setMetrics(context.Background(), &ResourceMetrics{
		ResourceID:        resourceID,
		ProvisioningState: arm.ProvisioningStateAccepted,
		CreatedAt:         &now,
	})
	assert.Equal(t, 1, testutil.CollectAndCount(c.provisionState))

	c.setMetrics(context.Background(), &ResourceMetrics{
		ResourceID:        resourceID,
		ProvisioningState: arm.ProvisioningStateProvisioning,
		CreatedAt:         &now,
	})
	assert.Equal(t, 1, testutil.CollectAndCount(c.provisionState), "should be 1 after phase transition, not 2")

	expected := fmt.Sprintf(`# HELP backend_cluster_provision_state Current provisioning state of the resource (value is always 1).
# TYPE backend_cluster_provision_state gauge
backend_cluster_provision_state{phase="provisioning",resource_id_hash="%s"} 1
`, hash)
	err := testutil.CollectAndCompare(c.provisionState, strings.NewReader(expected))
	require.NoError(t, err)
}

func TestSetMetrics_NilCreatedAt(t *testing.T) {
	c := newTestResourceMetricsController(t, "backend_cluster", &ClusterMetricsExtractor{})
	c.setMetrics(context.Background(), &ResourceMetrics{
		ResourceID:        "/subscriptions/sub-1/resourcegroups/rg/providers/microsoft.redhatopenshift/hcpopenshiftclusters/cluster-1",
		ProvisioningState: arm.ProvisioningStateAccepted,
		CreatedAt:         nil,
	})

	assert.Equal(t, 1, testutil.CollectAndCount(c.provisionState))
	assert.Equal(t, 0, testutil.CollectAndCount(c.createdTime), "should not emit created_time when CreatedAt is nil")
}

func TestSetMetrics_DeletesStaleCreatedTime(t *testing.T) {
	now := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	resourceID := "/subscriptions/sub-1/resourcegroups/rg/providers/microsoft.redhatopenshift/hcpopenshiftclusters/cluster-1"
	c := newTestResourceMetricsController(t, "backend_cluster", &ClusterMetricsExtractor{})

	c.setMetrics(context.Background(), &ResourceMetrics{
		ResourceID:        resourceID,
		ProvisioningState: arm.ProvisioningStateAccepted,
		CreatedAt:         &now,
	})
	assert.Equal(t, 1, testutil.CollectAndCount(c.createdTime))

	c.setMetrics(context.Background(), &ResourceMetrics{
		ResourceID:        resourceID,
		ProvisioningState: arm.ProvisioningStateAccepted,
		CreatedAt:         nil,
	})
	assert.Equal(t, 0, testutil.CollectAndCount(c.createdTime), "stale created_time should be deleted")
}

func TestSyncResource_DeletesMetricsWhenExtractorReturnsFalse(t *testing.T) {
	indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
	mockExtractor := &alwaysFalseExtractor{}
	c := newTestResourceMetricsController(t, "backend_cluster", mockExtractor)
	c.indexer = indexer

	// Add a valid cluster so the indexer has something to return.
	cluster := &api.HCPOpenShiftCluster{
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				ID: testParseResourceID(t, "/subscriptions/sub-1/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-1"),
			},
		},
	}
	require.NoError(t, indexer.Add(cluster))

	key, err := cache.MetaNamespaceKeyFunc(cluster)
	require.NoError(t, err)

	err = c.syncResource(context.Background(), key)
	assert.NoError(t, err)
	assert.Equal(t, 0, testutil.CollectAndCount(c.provisionState), "expected 0 metrics when extractor returns false")
}

type alwaysFalseExtractor struct{}

func (e *alwaysFalseExtractor) Extract(obj interface{}) (*ResourceMetrics, bool) {
	return nil, false
}

func TestSyncResource_SetsMetricsFromIndexer(t *testing.T) {
	now := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	cluster := &api.HCPOpenShiftCluster{
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				ID:         testParseResourceID(t, "/subscriptions/sub-1/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-1"),
				SystemData: &arm.SystemData{CreatedAt: &now},
			},
		},
		ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
			ProvisioningState: arm.ProvisioningStateSucceeded,
		},
	}

	indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
	require.NoError(t, indexer.Add(cluster))

	c := newTestResourceMetricsController(t, "backend_cluster", &ClusterMetricsExtractor{})
	c.indexer = indexer

	key, err := cache.MetaNamespaceKeyFunc(cluster)
	require.NoError(t, err)

	err = c.syncResource(context.Background(), key)
	assert.NoError(t, err)
	assert.Equal(t, 1, testutil.CollectAndCount(c.provisionState))
	assert.Equal(t, 1, testutil.CollectAndCount(c.createdTime))
}

func TestSyncResource_DeletesMetricsWhenResourceRemoved(t *testing.T) {
	now := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	cluster := &api.HCPOpenShiftCluster{
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				ID:         testParseResourceID(t, "/subscriptions/sub-1/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-1"),
				SystemData: &arm.SystemData{CreatedAt: &now},
			},
		},
		ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
			ProvisioningState: arm.ProvisioningStateSucceeded,
		},
	}

	indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
	require.NoError(t, indexer.Add(cluster))

	c := newTestResourceMetricsController(t, "backend_cluster", &ClusterMetricsExtractor{})
	c.indexer = indexer

	key, err := cache.MetaNamespaceKeyFunc(cluster)
	require.NoError(t, err)

	err = c.syncResource(context.Background(), key)
	require.NoError(t, err)
	assert.Equal(t, 1, testutil.CollectAndCount(c.provisionState))

	require.NoError(t, indexer.Delete(cluster))

	err = c.syncResource(context.Background(), key)
	assert.NoError(t, err)
	assert.Equal(t, 0, testutil.CollectAndCount(c.provisionState), "expected 0 after deletion")
	assert.Equal(t, 0, testutil.CollectAndCount(c.createdTime))
}

func TestDeleteMetrics_CleansUpByHash(t *testing.T) {
	now := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	resourceID := "/subscriptions/sub-1/resourcegroups/rg/providers/microsoft.redhatopenshift/hcpopenshiftclusters/cluster-1"
	c := newTestResourceMetricsController(t, "backend_cluster", &ClusterMetricsExtractor{})

	c.setMetrics(context.Background(), &ResourceMetrics{
		ResourceID:        resourceID,
		ProvisioningState: arm.ProvisioningStateSucceeded,
		CreatedAt:         &now,
	})
	assert.Equal(t, 1, testutil.CollectAndCount(c.provisionState))

	// deleteMetrics takes the store key (which equals resourceID) and hashes it internally.
	c.deleteMetrics(resourceID)
	assert.Equal(t, 0, testutil.CollectAndCount(c.provisionState))
	assert.Equal(t, 0, testutil.CollectAndCount(c.createdTime))
}
