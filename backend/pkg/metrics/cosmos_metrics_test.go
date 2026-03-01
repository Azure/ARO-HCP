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

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/go-logr/logr"

	"k8s.io/client-go/tools/cache"
	"k8s.io/utils/ptr"

	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
)

func mustParseResourceID(t *testing.T, id string) *azcorearm.ResourceID {
	t.Helper()
	rid, err := azcorearm.ParseResourceID(id)
	require.NoError(t, err)
	return rid
}

type expectedMetric struct {
	name   string
	labels prometheus.Labels
	value  float64
}

// assertMetrics registers the collector in a fresh registry, gathers once,
// and checks all expected metric values.
func assertMetrics(t *testing.T, collector prometheus.Collector, expected []expectedMetric) {
	t.Helper()
	reg := prometheus.NewPedanticRegistry()
	reg.MustRegister(collector)
	families, err := reg.Gather()
	require.NoError(t, err)

	if expected == nil {
		for _, mf := range families {
			require.Empty(t, mf.GetMetric(), "expected no metrics for %s", mf.GetName())
		}
		return
	}

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

// waitForSync waits for all informer caches used by the collector to
// complete their initial list. It aborts if ctx is cancelled.
func waitForSync(t *testing.T, ctx context.Context, bi informers.BackendInformers) {
	t.Helper()
	clusterInformer, _ := bi.Clusters()
	nodePoolInformer, _ := bi.NodePools()
	externalAuthInformer, _ := bi.ExternalAuths()
	activeOperationInformer, _ := bi.ActiveOperations()
	subscriptionInformer, _ := bi.Subscriptions()
	controllerInformer, _ := bi.ClusterControllers()
	require.True(t, cache.WaitForCacheSync(ctx.Done(),
		clusterInformer.HasSynced,
		nodePoolInformer.HasSynced,
		externalAuthInformer.HasSynced,
		activeOperationInformer.HasSynced,
		subscriptionInformer.HasSynced,
		controllerInformer.HasSynced,
	), "timed out waiting for caches to sync")
}

type metricsTestCase struct {
	name string

	// seedDB populates the mock database with initial items.
	seedDB func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockDBClient)

	// expected lists the metric values to assert after collection.
	expected []expectedMetric
}

func TestCollect(t *testing.T) {
	testCases := []metricsTestCase{
		clusterCountsTestCase(),
		nodePoolCountsTestCase(),
		activeOperationCountsTestCase(),
		subscriptionCountsTestCase(),
		controllerConditionsTestCase(),
		emptyCountsTestCase(),
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			mockDB := databasetesting.NewMockDBClient()
			if tc.seedDB != nil {
				tc.seedDB(t, ctx, mockDB)
			}

			bi := informers.NewBackendInformersWithRelistDuration(ctx, mockDB.GlobalListers(), ptr.To(1*time.Second))
			go bi.RunWithContext(ctx)
			waitForSync(t, ctx, bi)

			collector := NewCosmosMetricsCollector(bi, logr.Discard())
			assertMetrics(t, collector, tc.expected)
		})
	}
}

// ---- Cluster counts test case ----

func clusterCountsTestCase() metricsTestCase {
	const (
		subscriptionID    = "sub-1"
		resourceGroupName = "test-rg"
	)
	return metricsTestCase{
		name: "cluster counts by provisioning state",
		seedDB: func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockDBClient) {
			t.Helper()
			newCluster := func(name string, state arm.ProvisioningState) *api.HCPOpenShiftCluster {
				clusterResourceID := mustParseResourceID(t,
					"/subscriptions/"+subscriptionID+
						"/resourceGroups/"+resourceGroupName+
						"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/"+name)
				internalID, err := api.NewInternalID("/api/clusters_mgmt/v1/clusters/" + name)
				require.NoError(t, err)
				return &api.HCPOpenShiftCluster{
					TrackedResource: arm.TrackedResource{
						Resource: arm.Resource{
							ID:   clusterResourceID,
							Name: name,
							Type: api.ClusterResourceType.String(),
						},
						Location: "eastus",
					},
					ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
						ProvisioningState: state,
						ClusterServiceID:  internalID,
					},
				}
			}

			clusterCRUD := mockDB.HCPClusters(subscriptionID, resourceGroupName)
			_, err := clusterCRUD.Create(ctx, newCluster("cluster-1", arm.ProvisioningStateSucceeded), nil)
			require.NoError(t, err)
			_, err = clusterCRUD.Create(ctx, newCluster("cluster-2", arm.ProvisioningStateSucceeded), nil)
			require.NoError(t, err)
			_, err = clusterCRUD.Create(ctx, newCluster("cluster-3", arm.ProvisioningStateDeleting), nil)
			require.NoError(t, err)
		},
		expected: []expectedMetric{
			{"backend_hcpopenshiftclusters", prometheus.Labels{"provisioning_state": "Succeeded"}, 2},
			{"backend_hcpopenshiftclusters", prometheus.Labels{"provisioning_state": "Deleting"}, 1},
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
	return metricsTestCase{
		name: "nodepool counts by provisioning state",
		seedDB: func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockDBClient) {
			t.Helper()
			newNodePool := func(name string, state arm.ProvisioningState) *api.HCPOpenShiftClusterNodePool {
				npResourceID := mustParseResourceID(t,
					"/subscriptions/"+subscriptionID+
						"/resourceGroups/"+resourceGroupName+
						"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/"+clusterName+
						"/nodePools/"+name)
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

			npCRUD := mockDB.HCPClusters(subscriptionID, resourceGroupName).NodePools(clusterName)
			_, err := npCRUD.Create(ctx, newNodePool("np-1", arm.ProvisioningStateSucceeded), nil)
			require.NoError(t, err)
			_, err = npCRUD.Create(ctx, newNodePool("np-2", arm.ProvisioningStateSucceeded), nil)
			require.NoError(t, err)
			_, err = npCRUD.Create(ctx, newNodePool("np-3", arm.ProvisioningStateDeleting), nil)
			require.NoError(t, err)
		},
		expected: []expectedMetric{
			{"backend_nodepools", prometheus.Labels{"provisioning_state": "Succeeded"}, 2},
			{"backend_nodepools", prometheus.Labels{"provisioning_state": "Deleting"}, 1},
		},
	}
}

// ---- Active operation counts test case ----

func activeOperationCountsTestCase() metricsTestCase {
	const subscriptionID = "sub-1"
	return metricsTestCase{
		name: "active operation counts by provisioning state",
		seedDB: func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockDBClient) {
			t.Helper()
			newOperation := func(opName string, status arm.ProvisioningState) *api.Operation {
				operationID := mustParseResourceID(t,
					"/subscriptions/"+subscriptionID+
						"/providers/Microsoft.RedHatOpenShift/locations/eastus/hcpOperationStatuses/"+opName)
				externalID := mustParseResourceID(t,
					"/subscriptions/"+subscriptionID+
						"/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-1")
				resourceID := mustParseResourceID(t,
					"/subscriptions/"+subscriptionID+
						"/providers/Microsoft.RedHatOpenShift/hcpOperationStatuses/"+opName)
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

			opCRUD := mockDB.Operations(subscriptionID)
			_, err := opCRUD.Create(ctx, newOperation("op-1", arm.ProvisioningStateAccepted), nil)
			require.NoError(t, err)
			_, err = opCRUD.Create(ctx, newOperation("op-2", arm.ProvisioningStateAccepted), nil)
			require.NoError(t, err)
			_, err = opCRUD.Create(ctx, newOperation("op-3", arm.ProvisioningStateProvisioning), nil)
			require.NoError(t, err)
		},
		expected: []expectedMetric{
			{"backend_active_operations", prometheus.Labels{"provisioning_state": "Accepted"}, 2},
			{"backend_active_operations", prometheus.Labels{"provisioning_state": "Provisioning"}, 1},
		},
	}
}

// ---- Subscription counts test case ----

func subscriptionCountsTestCase() metricsTestCase {
	return metricsTestCase{
		name: "subscription counts by state",
		seedDB: func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockDBClient) {
			t.Helper()
			newSubscription := func(id string, state arm.SubscriptionState) *arm.Subscription {
				resourceID := mustParseResourceID(t, "/subscriptions/"+id)
				return &arm.Subscription{
					CosmosMetadata: arm.CosmosMetadata{
						ResourceID: resourceID,
					},
					ResourceID: resourceID,
					State:      state,
				}
			}

			_, err := mockDB.Subscriptions().Create(ctx, newSubscription("sub-1", arm.SubscriptionStateRegistered), nil)
			require.NoError(t, err)
			_, err = mockDB.Subscriptions().Create(ctx, newSubscription("sub-2", arm.SubscriptionStateRegistered), nil)
			require.NoError(t, err)
			_, err = mockDB.Subscriptions().Create(ctx, newSubscription("sub-3", arm.SubscriptionStateWarned), nil)
			require.NoError(t, err)
		},
		expected: []expectedMetric{
			{"backend_subscriptions", prometheus.Labels{"state": "Registered"}, 2},
			{"backend_subscriptions", prometheus.Labels{"state": "Warned"}, 1},
		},
	}
}

// ---- Controller conditions test case ----

func controllerConditionsTestCase() metricsTestCase {
	const (
		subscriptionID    = "sub-1"
		resourceGroupName = "test-rg"
		clusterName       = "cluster-1"
	)
	return metricsTestCase{
		name: "controller condition counts",
		seedDB: func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockDBClient) {
			t.Helper()
			newController := func(name string, degraded bool) *api.Controller {
				resourceID, err := azcorearm.ParseResourceID(
					"/subscriptions/" + subscriptionID +
						"/resourceGroups/" + resourceGroupName +
						"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + clusterName +
						"/hcpOpenShiftControllers/" + name)
				require.NoError(t, err)

				status := api.ControllerStatus{
					Conditions: []api.Condition{
						{
							Type:               "Degraded",
							Status:             api.ConditionFalse,
							Reason:             "NoErrors",
							Message:            "As expected.",
							LastTransitionTime: time.Now(),
						},
					},
				}
				if degraded {
					status.Conditions[0].Status = api.ConditionTrue
					status.Conditions[0].Reason = "Failed"
					status.Conditions[0].Message = "Controller is degraded"
				}

				return &api.Controller{
					CosmosMetadata: api.CosmosMetadata{
						ResourceID: resourceID,
					},
					ResourceID: resourceID,
					Status:     status,
				}
			}

			controllerCRUD := mockDB.HCPClusters(subscriptionID, resourceGroupName).Controllers(clusterName)
			_, err := controllerCRUD.Create(ctx, newController("cluster-validation", false), nil)
			require.NoError(t, err)
			_, err = controllerCRUD.Create(ctx, newController("cluster-matching", true), nil)
			require.NoError(t, err)
			_, err = controllerCRUD.Create(ctx, newController("nodepool-sync", false), nil)
			require.NoError(t, err)
		},
		expected: []expectedMetric{
			{"backend_controller_status_conditions", prometheus.Labels{"resource_type": api.ClusterResourceTypeName, "controller_name": "cluster-validation", "condition": "Degraded", "status": "False"}, 1},
			{"backend_controller_status_conditions", prometheus.Labels{"resource_type": api.ClusterResourceTypeName, "controller_name": "cluster-matching", "condition": "Degraded", "status": "True"}, 1},
			{"backend_controller_status_conditions", prometheus.Labels{"resource_type": api.ClusterResourceTypeName, "controller_name": "nodepool-sync", "condition": "Degraded", "status": "False"}, 1},
		},
	}
}

// ---- Empty caches test case ----

func emptyCountsTestCase() metricsTestCase {
	return metricsTestCase{
		name:     "empty caches emit no metrics",
		seedDB:   nil,
		expected: nil,
	}
}
