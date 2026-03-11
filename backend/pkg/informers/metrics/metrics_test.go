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
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"k8s.io/client-go/tools/cache"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type expectedMetric struct {
	name   string
	labels prometheus.Labels
	value  float64
}

type metricsTestCase struct {
	name        string
	dbResources func(t *testing.T) []any
	expected    []expectedMetric
}

func TestStorageMetrics(t *testing.T) {
	testCases := []metricsTestCase{
		clusterCountsTestCase(),
		nodePoolCountsTestCase(),
		externalAuthCountsTestCase(),
		activeOperationCountsTestCase(),
		subscriptionCountsTestCase(),
		controllerConditionsTestCase(),
		controllerWithoutDegradedConditionTestCase(),
		emptyInformerCacheTestCase(),
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(t.Context(), logr.Discard())

			mockDB, err := databasetesting.NewMockDBClientWithResources(ctx, tc.dbResources(t))
			require.NoError(t, err)

			backendInformers := informers.NewBackendInformersWithRelistDuration(ctx, mockDB.GlobalListers(), ptr.To(1*time.Second))
			go backendInformers.RunWithContext(ctx)
			waitForSync(t, ctx, backendInformers)

			reg := prometheus.NewPedanticRegistry()
			observer := NewStorageMetricsObserver(reg, backendInformers)
			go observer.Run(ctx)

			// eventually we want this to succeed while the observer goroutine is running
			require.EventuallyWithT(t, func(ct *assert.CollectT) {
				assertExpectedMetrics(ct, reg, tc.expected)
			}, 3*time.Second, 200*time.Millisecond)
		})
	}
}

func assertExpectedMetrics(t require.TestingT, reg *prometheus.Registry, expected []expectedMetric) {
	families, err := reg.Gather()
	require.NoError(t, err)
	for _, exp := range expected {
		found := false
		for _, mf := range families {
			if mf.GetName() != exp.name {
				continue
			}
			for _, m := range mf.GetMetric() {
				match := true
				for _, lp := range m.GetLabel() {
					if v, ok := exp.labels[lp.GetName()]; ok {
						if v != lp.GetValue() {
							match = false
							break
						}
					}
				}
				if match && len(m.GetLabel()) == len(exp.labels) {
					require.Equal(t, exp.value, m.GetGauge().GetValue(),
						"metric %s with labels %v", exp.name, exp.labels)
					found = true
				}
			}
		}
		require.True(t, found, "metric %s with labels %v not found", exp.name, exp.labels)
	}
}

func waitForSync(t *testing.T, ctx context.Context, bi informers.BackendInformers) {
	t.Helper()
	clusterInformer, _ := bi.Clusters()
	nodePoolInformer, _ := bi.NodePools()
	externalAuthInformer, _ := bi.ExternalAuths()
	activeOperationInformer, _ := bi.ActiveOperations()
	subscriptionInformer, _ := bi.Subscriptions()
	controllerInformer, _ := bi.Controllers()
	require.True(t, cache.WaitForCacheSync(ctx.Done(),
		clusterInformer.HasSynced,
		nodePoolInformer.HasSynced,
		externalAuthInformer.HasSynced,
		activeOperationInformer.HasSynced,
		subscriptionInformer.HasSynced,
		controllerInformer.HasSynced,
	), "timed out waiting for caches to sync")
}

// ---- Cluster counts test case ----

func clusterCountsTestCase() metricsTestCase {
	const (
		subscriptionID    = "sub-1"
		resourceGroupName = "test-rg"
	)
	newCluster := func(t *testing.T, name string, state arm.ProvisioningState) *api.HCPOpenShiftCluster {
		t.Helper()
		clusterResourceID := api.Must(azcorearm.ParseResourceID(
			"/subscriptions/" + subscriptionID +
				"/resourceGroups/" + resourceGroupName +
				"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + name,
		))
		internalID, err := api.NewInternalID("/api/clusters_mgmt/v1/clusters/" + name)
		require.NoError(t, err)
		return &api.HCPOpenShiftCluster{
			TrackedResource: arm.TrackedResource{
				Resource: arm.Resource{
					ID:   clusterResourceID,
					Name: name,
					Type: api.ClusterResourceType.String(),
					SystemData: &arm.SystemData{
						CreatedAt: ptr.To(time.Now()),
					},
				},
				Location: "eastus",
			},
			ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
				ProvisioningState: state,
				ClusterServiceID:  internalID,
			},
		}
	}
	return metricsTestCase{
		name: "cluster counts by provisioning state",
		dbResources: func(t *testing.T) []any {
			return []any{
				newCluster(t, "cluster-1", arm.ProvisioningStateSucceeded),
				newCluster(t, "cluster-2", arm.ProvisioningStateSucceeded),
				newCluster(t, "cluster-3", arm.ProvisioningStateDeleting),
			}
		},
		expected: []expectedMetric{
			{"backend_resource_provider_objects", prometheus.Labels{"resource_type": api.ClusterResourceType.String(), "provisioning_state": "Succeeded"}, 2},
			{"backend_resource_provider_objects", prometheus.Labels{"resource_type": api.ClusterResourceType.String(), "provisioning_state": "Deleting"}, 1},
		},
	}
}

// ---- NodePool counts test case ----

func nodePoolCountsTestCase() metricsTestCase {
	const (
		subscriptionID    = "sub-1"
		resourceGroupName = "test-rg"
		clusterName       = "cluster-1"
	)
	newNodePool := func(t *testing.T, name string, state arm.ProvisioningState) *api.HCPOpenShiftClusterNodePool {
		t.Helper()
		npResourceID := api.Must(azcorearm.ParseResourceID(
			"/subscriptions/" + subscriptionID +
				"/resourceGroups/" + resourceGroupName +
				"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + clusterName +
				"/nodePools/" + name,
		))
		internalID, err := api.NewInternalID("/api/clusters_mgmt/v1/clusters/" + clusterName)
		require.NoError(t, err)
		return &api.HCPOpenShiftClusterNodePool{
			TrackedResource: arm.TrackedResource{
				Resource: arm.Resource{
					ID:   npResourceID,
					Name: name,
					Type: api.NodePoolResourceType.String(),
				},
				Location: "eastus",
			},
			Properties: api.HCPOpenShiftClusterNodePoolProperties{
				ProvisioningState: state,
				Replicas:          3,
			},
			ServiceProviderProperties: api.HCPOpenShiftClusterNodePoolServiceProviderProperties{
				ClusterServiceID: internalID,
			},
		}
	}
	return metricsTestCase{
		name: "nodepool counts by provisioning state",
		dbResources: func(t *testing.T) []any {
			return []any{
				newNodePool(t, "np-1", arm.ProvisioningStateSucceeded),
				newNodePool(t, "np-2", arm.ProvisioningStateSucceeded),
				newNodePool(t, "np-3", arm.ProvisioningStateDeleting),
			}
		},
		expected: []expectedMetric{
			{"backend_resource_provider_objects", prometheus.Labels{"resource_type": api.NodePoolResourceType.String(), "provisioning_state": "Succeeded"}, 2},
			{"backend_resource_provider_objects", prometheus.Labels{"resource_type": api.NodePoolResourceType.String(), "provisioning_state": "Deleting"}, 1},
		},
	}
}

// ---- ExternalAuth counts test case ----

func externalAuthCountsTestCase() metricsTestCase {
	const (
		subscriptionID    = "sub-1"
		resourceGroupName = "test-rg"
		clusterName       = "cluster-1"
	)
	newExternalAuth := func(t *testing.T, name string, state arm.ProvisioningState) *api.HCPOpenShiftClusterExternalAuth {
		t.Helper()
		resourceID := api.Must(azcorearm.ParseResourceID(
			"/subscriptions/" + subscriptionID +
				"/resourceGroups/" + resourceGroupName +
				"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + clusterName +
				"/externalAuths/" + name,
		))
		return &api.HCPOpenShiftClusterExternalAuth{
			ProxyResource: arm.NewProxyResource(resourceID),
			Properties: api.HCPOpenShiftClusterExternalAuthProperties{
				ProvisioningState: state,
			},
		}
	}
	return metricsTestCase{
		name: "external auth counts by provisioning state",
		dbResources: func(t *testing.T) []any {
			return []any{
				newExternalAuth(t, "ea-1", arm.ProvisioningStateSucceeded),
				newExternalAuth(t, "ea-2", arm.ProvisioningStateSucceeded),
				newExternalAuth(t, "ea-3", arm.ProvisioningStateAwaitingSecret),
			}
		},
		expected: []expectedMetric{
			{"backend_resource_provider_objects", prometheus.Labels{"resource_type": api.ExternalAuthResourceType.String(), "provisioning_state": "Succeeded"}, 2},
			{"backend_resource_provider_objects", prometheus.Labels{"resource_type": api.ExternalAuthResourceType.String(), "provisioning_state": "AwaitingSecret"}, 1},
			// Verify pre-initialization: states with zero items must be explicitly set to 0
			{"backend_resource_provider_objects", prometheus.Labels{"resource_type": api.ExternalAuthResourceType.String(), "provisioning_state": "Failed"}, 0},
		},
	}
}

// ---- Active operation counts test case ----

func activeOperationCountsTestCase() metricsTestCase {
	const subscriptionID = "sub-1"
	newOperation := func(t *testing.T, opName string, status arm.ProvisioningState) *api.Operation {
		t.Helper()
		operationID := api.Must(azcorearm.ParseResourceID(
			"/subscriptions/" + subscriptionID +
				"/providers/Microsoft.RedHatOpenShift/locations/eastus/hcpOperationStatuses/" + opName,
		))
		externalID := api.Must(azcorearm.ParseResourceID(
			"/subscriptions/" + subscriptionID +
				"/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-1",
		))
		resourceID := api.Must(azcorearm.ParseResourceID(
			"/subscriptions/" + subscriptionID +
				"/providers/Microsoft.RedHatOpenShift/hcpOperationStatuses/" + opName,
		))
		now := time.Now().UTC()
		return &api.Operation{
			CosmosMetadata: api.CosmosMetadata{
				ResourceID: resourceID,
			},
			ResourceID:         resourceID,
			OperationID:        operationID,
			ExternalID:         externalID,
			Request:            api.OperationRequestCreate,
			Status:             status,
			StartTime:          now,
			LastTransitionTime: now,
		}
	}
	return metricsTestCase{
		name: "active operation counts by provisioning state",
		dbResources: func(t *testing.T) []any {
			t.Helper()
			return []any{
				newOperation(t, "op-1", arm.ProvisioningStateAccepted),
				newOperation(t, "op-2", arm.ProvisioningStateAccepted),
				newOperation(t, "op-3", arm.ProvisioningStateProvisioning),
				newOperation(t, "op-4", arm.ProvisioningStateSucceeded),
			}
		},
		expected: []expectedMetric{
			{"backend_resource_provider_objects", prometheus.Labels{"resource_type": api.OperationStatusResourceType.String(), "provisioning_state": "Accepted"}, 2},
			{"backend_resource_provider_objects", prometheus.Labels{"resource_type": api.OperationStatusResourceType.String(), "provisioning_state": "Provisioning"}, 1},
		},
	}
}

// ---- Subscription counts test case ----

func subscriptionCountsTestCase() metricsTestCase {
	newSubscription := func(t *testing.T, id string, state arm.SubscriptionState) *arm.Subscription {
		t.Helper()
		resourceID := api.Must(azcorearm.ParseResourceID(
			"/subscriptions/" + id,
		))
		return &arm.Subscription{
			CosmosMetadata: arm.CosmosMetadata{
				ResourceID: resourceID,
			},
			ResourceID: resourceID,
			State:      state,
		}
	}
	return metricsTestCase{
		name: "subscription counts by state",
		dbResources: func(t *testing.T) []any {
			t.Helper()
			return []any{
				newSubscription(t, "sub-1", arm.SubscriptionStateRegistered),
				newSubscription(t, "sub-2", arm.SubscriptionStateRegistered),
				newSubscription(t, "sub-3", arm.SubscriptionStateWarned),
			}
		},
		expected: []expectedMetric{
			{"backend_resource_provider_objects", prometheus.Labels{"resource_type": azcorearm.SubscriptionResourceType.String(), "provisioning_state": "Registered"}, 2},
			{"backend_resource_provider_objects", prometheus.Labels{"resource_type": azcorearm.SubscriptionResourceType.String(), "provisioning_state": "Warned"}, 1},
		},
	}
}

// ---- Controller conditions test case ----

func controllerConditionsTestCase() metricsTestCase {
	const (
		subscriptionID    = "sub-1"
		resourceGroupName = "test-rg"
	)
	newControllerClusterController := func(t *testing.T, clusterName string, name string, degradedStatus api.ConditionStatus) *api.Controller {
		t.Helper()
		resourceID := api.Must(azcorearm.ParseResourceID(
			"/subscriptions/" + subscriptionID +
				"/resourceGroups/" + resourceGroupName +
				"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + clusterName +
				"/hcpOpenShiftControllers/" + name,
		))

		status := api.ControllerStatus{
			Conditions: []api.Condition{
				{
					Type:               controllerutils.ConditionTypeDegraded,
					Status:             degradedStatus,
					Reason:             string(degradedStatus),
					Message:            string(degradedStatus),
					LastTransitionTime: time.Now(),
				},
			},
		}

		return &api.Controller{
			CosmosMetadata: api.CosmosMetadata{
				ResourceID: resourceID,
			},
			ResourceID: resourceID,
			Status:     status,
		}
	}
	return metricsTestCase{
		name: "controller condition counts",
		dbResources: func(t *testing.T) []any {
			t.Helper()
			return []any{
				newControllerClusterController(t, "cluster-1", "cluster-validation", api.ConditionFalse),
				newControllerClusterController(t, "cluster-1", "cluster-matching", api.ConditionTrue),
				newControllerClusterController(t, "cluster-1", "nodepool-sync", api.ConditionFalse),

				newControllerClusterController(t, "cluster-2", "cluster-validation", api.ConditionTrue),
				newControllerClusterController(t, "cluster-2", "cluster-matching", api.ConditionTrue),
				newControllerClusterController(t, "cluster-2", "nodepool-sync", api.ConditionTrue),

				newControllerClusterController(t, "cluster-3", "cluster-validation", api.ConditionUnknown),
				newControllerClusterController(t, "cluster-3", "cluster-matching", api.ConditionUnknown),
				newControllerClusterController(t, "cluster-3", "nodepool-sync", api.ConditionUnknown),
			}
		},
		expected: []expectedMetric{
			{"backend_controller_status_conditions", prometheus.Labels{"controller_name": "cluster-validation", "condition": "Degraded", "status": "True"}, 1},
			{"backend_controller_status_conditions", prometheus.Labels{"controller_name": "cluster-validation", "condition": "Degraded", "status": "False"}, 1},
			{"backend_controller_status_conditions", prometheus.Labels{"controller_name": "cluster-validation", "condition": "Degraded", "status": "Unknown"}, 1},

			{"backend_controller_status_conditions", prometheus.Labels{"controller_name": "cluster-matching", "condition": "Degraded", "status": "True"}, 2},
			{"backend_controller_status_conditions", prometheus.Labels{"controller_name": "cluster-matching", "condition": "Degraded", "status": "Unknown"}, 1},

			{"backend_controller_status_conditions", prometheus.Labels{"controller_name": "nodepool-sync", "condition": "Degraded", "status": "True"}, 1},
			{"backend_controller_status_conditions", prometheus.Labels{"controller_name": "nodepool-sync", "condition": "Degraded", "status": "False"}, 1},
			{"backend_controller_status_conditions", prometheus.Labels{"controller_name": "nodepool-sync", "condition": "Degraded", "status": "Unknown"}, 1},
		},
	}
}

// ---- Controller without Degraded condition test case ----

func controllerWithoutDegradedConditionTestCase() metricsTestCase {
	const (
		subscriptionID    = "sub-1"
		resourceGroupName = "test-rg"
	)
	newControllerClusterController := func(t *testing.T, clusterName string, name string, conditions []api.Condition) *api.Controller {
		t.Helper()
		resourceID := api.Must(azcorearm.ParseResourceID(
			"/subscriptions/" + subscriptionID +
				"/resourceGroups/" + resourceGroupName +
				"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + clusterName +
				"/hcpOpenShiftControllers/" + name,
		))

		return &api.Controller{
			CosmosMetadata: api.CosmosMetadata{
				ResourceID: resourceID,
			},
			ResourceID: resourceID,
			Status: api.ControllerStatus{
				Conditions: conditions,
			},
		}
	}
	return metricsTestCase{
		name: "controllers without degraded condition are skipped",
		dbResources: func(t *testing.T) []any {
			t.Helper()
			return []any{
				// Controller with no conditions at all
				newControllerClusterController(t, "cluster-1", "no-conditions", nil),
				// Controller with a non-Degraded condition only
				newControllerClusterController(t, "cluster-1", "other-condition", []api.Condition{
					{
						Type:    "Ready",
						Status:  api.ConditionTrue,
						Reason:  "AllGood",
						Message: "Ready",
					},
				}),
				// Controller with a Degraded condition (should be counted)
				newControllerClusterController(t, "cluster-1", "has-degraded", []api.Condition{
					{
						Type:    controllerutils.ConditionTypeDegraded,
						Status:  api.ConditionFalse,
						Reason:  "NoErrors",
						Message: "As expected.",
					},
				}),
			}
		},
		expected: []expectedMetric{
			// Only "has-degraded" should appear in metrics
			{"backend_controller_status_conditions", prometheus.Labels{"controller_name": "has-degraded", "condition": "Degraded", "status": "True"}, 0},
			{"backend_controller_status_conditions", prometheus.Labels{"controller_name": "has-degraded", "condition": "Degraded", "status": "False"}, 1},
			{"backend_controller_status_conditions", prometheus.Labels{"controller_name": "has-degraded", "condition": "Degraded", "status": "Unknown"}, 0},
		},
	}
}

// ---- Empty informer cache test case ----

func emptyInformerCacheTestCase() metricsTestCase {
	return metricsTestCase{
		name: "empty informer cache produces zero counts",
		dbResources: func(t *testing.T) []any {
			t.Helper()
			return []any{} // no resources at all
		},
		expected: []expectedMetric{
			// Clusters: all provisioning states should be zero
			{"backend_resource_provider_objects", prometheus.Labels{"resource_type": api.ClusterResourceType.String(), "provisioning_state": "Succeeded"}, 0},
			{"backend_resource_provider_objects", prometheus.Labels{"resource_type": api.ClusterResourceType.String(), "provisioning_state": "Failed"}, 0},
			// NodePools: zero
			{"backend_resource_provider_objects", prometheus.Labels{"resource_type": api.NodePoolResourceType.String(), "provisioning_state": "Succeeded"}, 0},
			// Subscriptions: zero
			{"backend_resource_provider_objects", prometheus.Labels{"resource_type": azcorearm.SubscriptionResourceType.String(), "provisioning_state": "Registered"}, 0},
		},
	}
}
