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

package register

import (
	"context"
	"testing"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/require"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/fleet"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const testStampIdentifier = "1"

func testContext(t *testing.T) context.Context {
	t.Helper()
	return utils.ContextWithLogger(t.Context(), logr.Discard())
}

func validRegisterOptions(t *testing.T, fleetDBClient *databasetesting.MockFleetDBClient) *RegisterOptions {
	t.Helper()
	return &RegisterOptions{
		registerOptions: &registerOptions{
			fleetDBClient:                              fleetDBClient,
			stampIdentifier:                            testStampIdentifier,
			stampResourceID:                            api.Must(fleet.ToStampResourceID(testStampIdentifier)),
			managementClusterResourceID:                api.Must(fleet.ToManagementClusterResourceID(testStampIdentifier)),
			schedulingPolicy:                           fleet.ManagementClusterSchedulingPolicySchedulable,
			aksResourceID:                              api.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.ContainerService/managedClusters/aks-1")),
			publicDNSZoneResourceID:                    api.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.Network/dnszones/example.com")),
			hostedClustersSecretsKeyVaultURL:           "https://kv-secrets.vault.azure.net",
			hostedClustersManagedIdentitiesKeyVaultURL: "https://kv-mi.vault.azure.net",
			hostedClustersSecretsKeyVaultManagedIdentityClientID: "12345678-1234-1234-1234-123456789012",
			maestroConsumerName:            "hcp-underlay-westus3-mgmt-1",
			maestroRESTAPIURL:              "http://maestro.maestro.svc.cluster.local:8000",
			maestroGRPCTarget:              "maestro-grpc.maestro.svc.cluster.local:8090",
			kubeApplierCosmosContainerName: "Manifests-MC-1",
		},
	}
}

func TestRun(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		modify    func(t *testing.T, opts *RegisterOptions)
		seed      func(t *testing.T) []any
		verify    func(t *testing.T, client *databasetesting.MockFleetDBClient)
		expectErr string
	}{
		{
			name:   "create stamp and management cluster",
			modify: func(t *testing.T, opts *RegisterOptions) {},
			verify: func(t *testing.T, client *databasetesting.MockFleetDBClient) {
				ctx := testContext(t)
				stamp, err := client.Stamps().Get(ctx, testStampIdentifier)
				require.NoError(t, err)
				require.NotNil(t, stamp)

				managementCluster, err := client.Stamps().ManagementClusters(testStampIdentifier).Get(ctx, fleet.ManagementClusterResourceName)
				require.NoError(t, err)
				require.Equal(t, fleet.ManagementClusterSchedulingPolicySchedulable, managementCluster.Spec.SchedulingPolicy)
				require.Equal(t, "hcp-underlay-westus3-mgmt-1", managementCluster.Status.MaestroConsumerName)
			},
		},
		{
			name: "create stamp with auto-approve",
			modify: func(t *testing.T, opts *RegisterOptions) {
				opts.autoApprove = true
			},
			verify: func(t *testing.T, client *databasetesting.MockFleetDBClient) {
				ctx := testContext(t)
				stamp, err := client.Stamps().Get(ctx, testStampIdentifier)
				require.NoError(t, err)
				condition := apimeta.FindStatusCondition(stamp.Status.Conditions, string(fleet.StampConditionApproved))
				require.NotNil(t, condition)
				require.Equal(t, metav1.ConditionTrue, condition.Status)
				require.Equal(t, string(fleet.StampConditionReasonAutoApproved), condition.Reason)
			},
		},
		{
			name: "create stamp without auto-approve has no approved condition",
			modify: func(t *testing.T, opts *RegisterOptions) {
				opts.autoApprove = false
			},
			verify: func(t *testing.T, client *databasetesting.MockFleetDBClient) {
				ctx := testContext(t)
				stamp, err := client.Stamps().Get(ctx, testStampIdentifier)
				require.NoError(t, err)
				condition := apimeta.FindStatusCondition(stamp.Status.Conditions, string(fleet.StampConditionApproved))
				require.Nil(t, condition)
			},
		},
		{
			name: "update existing stamp preserves conditions",
			seed: func(t *testing.T) []any {
				stamp := &fleet.Stamp{
					CosmosMetadata: api.CosmosMetadata{ResourceID: api.Must(fleet.ToStampResourceID(testStampIdentifier))},
					ResourceID:     api.Must(fleet.ToStampResourceID(testStampIdentifier)),
					Status: fleet.StampStatus{
						Conditions: []metav1.Condition{
							{
								Type:   string(fleet.StampConditionApproved),
								Status: metav1.ConditionTrue,
								Reason: string(fleet.StampConditionReasonManuallyApproved),
							},
						},
					},
				}
				return []any{stamp}
			},
			modify: func(t *testing.T, opts *RegisterOptions) {
				opts.autoApprove = false
			},
			verify: func(t *testing.T, client *databasetesting.MockFleetDBClient) {
				ctx := testContext(t)
				stamp, err := client.Stamps().Get(ctx, testStampIdentifier)
				require.NoError(t, err)
				condition := apimeta.FindStatusCondition(stamp.Status.Conditions, string(fleet.StampConditionApproved))
				require.NotNil(t, condition)
				require.Equal(t, string(fleet.StampConditionReasonManuallyApproved), condition.Reason)
			},
		},
		{
			name: "update existing management cluster with same values succeeds",
			seed: func(t *testing.T) []any {
				stamp := &fleet.Stamp{
					CosmosMetadata: api.CosmosMetadata{ResourceID: api.Must(fleet.ToStampResourceID(testStampIdentifier))},
					ResourceID:     api.Must(fleet.ToStampResourceID(testStampIdentifier)),
				}
				managementCluster := &fleet.ManagementCluster{
					CosmosMetadata: api.CosmosMetadata{ResourceID: api.Must(fleet.ToManagementClusterResourceID(testStampIdentifier))},
					ResourceID:     api.Must(fleet.ToManagementClusterResourceID(testStampIdentifier)),
					Spec: fleet.ManagementClusterSpec{
						SchedulingPolicy: fleet.ManagementClusterSchedulingPolicySchedulable,
					},
					Status: fleet.ManagementClusterStatus{
						AKSResourceID:                                        api.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.ContainerService/managedClusters/aks-1")),
						PublicDNSZoneResourceID:                              api.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.Network/dnszones/example.com")),
						HostedClustersSecretsKeyVaultURL:                     "https://kv-secrets.vault.azure.net",
						HostedClustersManagedIdentitiesKeyVaultURL:           "https://kv-mi.vault.azure.net",
						HostedClustersSecretsKeyVaultManagedIdentityClientID: "12345678-1234-1234-1234-123456789012",
						ClusterServiceProvisionShardID:                       ptr.To(api.Must(api.NewInternalID("/api/aro_hcp/v1alpha1/provision_shards/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"))),
						MaestroConsumerName:                                  "hcp-underlay-westus3-mgmt-1",
						MaestroRESTAPIURL:                                    "http://maestro.maestro.svc.cluster.local:8000",
						MaestroGRPCTarget:                                    "maestro-grpc.maestro.svc.cluster.local:8090",
						KubeApplierCosmosContainerName:                       "Manifests-MC-1",
					},
				}
				return []any{stamp, managementCluster}
			},
			modify: func(t *testing.T, opts *RegisterOptions) {},
			verify: func(t *testing.T, client *databasetesting.MockFleetDBClient) {
				ctx := testContext(t)
				managementCluster, err := client.Stamps().ManagementClusters(testStampIdentifier).Get(ctx, fleet.ManagementClusterResourceName)
				require.NoError(t, err)
				require.Equal(t, fleet.ManagementClusterSchedulingPolicySchedulable, managementCluster.Spec.SchedulingPolicy)
			},
		},
		{
			name: "update existing management cluster rejects changed immutable field",
			seed: func(t *testing.T) []any {
				stamp := &fleet.Stamp{
					CosmosMetadata: api.CosmosMetadata{ResourceID: api.Must(fleet.ToStampResourceID(testStampIdentifier))},
					ResourceID:     api.Must(fleet.ToStampResourceID(testStampIdentifier)),
				}
				managementCluster := &fleet.ManagementCluster{
					CosmosMetadata: api.CosmosMetadata{ResourceID: api.Must(fleet.ToManagementClusterResourceID(testStampIdentifier))},
					ResourceID:     api.Must(fleet.ToManagementClusterResourceID(testStampIdentifier)),
					Spec: fleet.ManagementClusterSpec{
						SchedulingPolicy: fleet.ManagementClusterSchedulingPolicySchedulable,
					},
					Status: fleet.ManagementClusterStatus{
						AKSResourceID:                                        api.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.ContainerService/managedClusters/original-aks")),
						PublicDNSZoneResourceID:                              api.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.Network/dnszones/example.com")),
						HostedClustersSecretsKeyVaultURL:                     "https://kv-secrets.vault.azure.net",
						HostedClustersManagedIdentitiesKeyVaultURL:           "https://kv-mi.vault.azure.net",
						HostedClustersSecretsKeyVaultManagedIdentityClientID: "12345678-1234-1234-1234-123456789012",
						ClusterServiceProvisionShardID:                       ptr.To(api.Must(api.NewInternalID("/api/aro_hcp/v1alpha1/provision_shards/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"))),
						MaestroConsumerName:                                  "hcp-underlay-westus3-mgmt-1",
						MaestroRESTAPIURL:                                    "http://maestro.maestro.svc.cluster.local:8000",
						MaestroGRPCTarget:                                    "maestro-grpc.maestro.svc.cluster.local:8090",
						KubeApplierCosmosContainerName:                       "Manifests-MC-1",
					},
				}
				return []any{stamp, managementCluster}
			},
			modify: func(t *testing.T, opts *RegisterOptions) {
				opts.aksResourceID = api.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.ContainerService/managedClusters/different-aks"))
			},
			expectErr: "replace validation failed",
		},
		{
			name: "update existing management cluster allows scheduling policy change",
			seed: func(t *testing.T) []any {
				stamp := &fleet.Stamp{
					CosmosMetadata: api.CosmosMetadata{ResourceID: api.Must(fleet.ToStampResourceID(testStampIdentifier))},
					ResourceID:     api.Must(fleet.ToStampResourceID(testStampIdentifier)),
				}
				managementCluster := &fleet.ManagementCluster{
					CosmosMetadata: api.CosmosMetadata{ResourceID: api.Must(fleet.ToManagementClusterResourceID(testStampIdentifier))},
					ResourceID:     api.Must(fleet.ToManagementClusterResourceID(testStampIdentifier)),
					Spec: fleet.ManagementClusterSpec{
						SchedulingPolicy: fleet.ManagementClusterSchedulingPolicySchedulable,
					},
					Status: fleet.ManagementClusterStatus{
						AKSResourceID:                                        api.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.ContainerService/managedClusters/aks-1")),
						PublicDNSZoneResourceID:                              api.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.Network/dnszones/example.com")),
						HostedClustersSecretsKeyVaultURL:                     "https://kv-secrets.vault.azure.net",
						HostedClustersManagedIdentitiesKeyVaultURL:           "https://kv-mi.vault.azure.net",
						HostedClustersSecretsKeyVaultManagedIdentityClientID: "12345678-1234-1234-1234-123456789012",
						ClusterServiceProvisionShardID:                       ptr.To(api.Must(api.NewInternalID("/api/aro_hcp/v1alpha1/provision_shards/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"))),
						MaestroConsumerName:                                  "hcp-underlay-westus3-mgmt-1",
						MaestroRESTAPIURL:                                    "http://maestro.maestro.svc.cluster.local:8000",
						MaestroGRPCTarget:                                    "maestro-grpc.maestro.svc.cluster.local:8090",
						KubeApplierCosmosContainerName:                       "Manifests-MC-1",
					},
				}
				return []any{stamp, managementCluster}
			},
			modify: func(t *testing.T, opts *RegisterOptions) {
				opts.schedulingPolicy = fleet.ManagementClusterSchedulingPolicyUnschedulable
			},
			verify: func(t *testing.T, client *databasetesting.MockFleetDBClient) {
				ctx := testContext(t)
				managementCluster, err := client.Stamps().ManagementClusters(testStampIdentifier).Get(ctx, fleet.ManagementClusterResourceName)
				require.NoError(t, err)
				require.Equal(t, fleet.ManagementClusterSchedulingPolicyUnschedulable, managementCluster.Spec.SchedulingPolicy)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := testContext(t)

			var client *databasetesting.MockFleetDBClient
			var err error
			if tt.seed != nil {
				client, err = databasetesting.NewMockFleetDBClientWithResources(ctx, tt.seed(t))
				require.NoError(t, err)
			} else {
				client = databasetesting.NewMockFleetDBClient()
			}

			opts := validRegisterOptions(t, client)
			tt.modify(t, opts)

			err = opts.Run(ctx)

			if len(tt.expectErr) > 0 {
				require.ErrorContains(t, err, tt.expectErr)
				return
			}
			require.NoError(t, err)
			if tt.verify != nil {
				tt.verify(t, client)
			}
		})
	}
}
