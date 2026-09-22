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
	"strings"
	"testing"

	"github.com/go-logr/logr"
	"github.com/go-logr/logr/funcr"
	"github.com/stretchr/testify/require"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/fleetapihelpers"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/fleetcosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const testStampIdentifier = "1"

func testContext(t *testing.T) context.Context {
	t.Helper()
	return utils.ContextWithLogger(t.Context(), logr.Discard())
}

func testContextWithLogger(t *testing.T, logger logr.Logger) context.Context {
	t.Helper()
	return utils.ContextWithLogger(t.Context(), logger)
}

func validRegisterOptions(t *testing.T, fleetDBClient *fleetcosmosstoragetesting.MockFleetDBClient) *RegisterOptions {
	t.Helper()
	return &RegisterOptions{
		registerOptions: &registerOptions{
			fleetDBClient:                              fleetDBClient,
			stampIdentifier:                            testStampIdentifier,
			stampResourceID:                            metadataapi.Must(fleetapihelpers.ToStampResourceID(testStampIdentifier)),
			managementClusterResourceID:                metadataapi.Must(fleetapihelpers.ToManagementClusterResourceID(testStampIdentifier)),
			schedulingPolicy:                           fleetapi.ManagementClusterSchedulingPolicySchedulable,
			aksResourceID:                              metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.ContainerService/managedClusters/aks-1")),
			publicDNSZoneResourceID:                    metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.Network/dnszones/example.com")),
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

func seededManagementClusterStatus() fleetapi.ManagementClusterStatus {
	return fleetapi.ManagementClusterStatus{
		AKSResourceID:                                        metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.ContainerService/managedClusters/aks-1")),
		PublicDNSZoneResourceID:                              metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.Network/dnszones/example.com")),
		HostedClustersSecretsKeyVaultURL:                     "https://kv-secrets.vault.azure.net",
		HostedClustersManagedIdentitiesKeyVaultURL:           "https://kv-mi.vault.azure.net",
		HostedClustersSecretsKeyVaultManagedIdentityClientID: "12345678-1234-1234-1234-123456789012",
		ClusterServiceProvisionShardID:                       ptr.To(metadataapi.Must(metadataapi.NewInternalID("/api/aro_hcp/v1alpha1/provision_shards/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"))),
		MaestroConsumerName:                                  "hcp-underlay-westus3-mgmt-1",
		MaestroRESTAPIURL:                                    "http://maestro.maestro.svc.cluster.local:8000",
		MaestroGRPCTarget:                                    "maestro-grpc.maestro.svc.cluster.local:8090",
		KubeApplierCosmosContainerName:                       "Manifests-MC-1",
		SharedIngressIPAddresses:                             []string{"203.0.113.10"},
	}
}

func TestRun(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		modify    func(t *testing.T, opts *RegisterOptions)
		seed      func(t *testing.T) []any
		verify    func(t *testing.T, client *fleetcosmosstoragetesting.MockFleetDBClient)
		expectErr string
	}{
		{
			name:   "create stamp and management cluster",
			modify: func(t *testing.T, opts *RegisterOptions) {},
			verify: func(t *testing.T, client *fleetcosmosstoragetesting.MockFleetDBClient) {
				ctx := testContext(t)
				stamp, err := client.Stamps().Get(ctx, testStampIdentifier)
				require.NoError(t, err)
				require.NotNil(t, stamp)

				managementCluster, err := client.Stamps().ManagementClusters(testStampIdentifier).Get(ctx, fleetapi.ManagementClusterResourceName)
				require.NoError(t, err)
				require.Equal(t, fleetapi.ManagementClusterSchedulingPolicySchedulable, managementCluster.Spec.SchedulingPolicy)
				require.Equal(t, "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.ContainerService/managedClusters/aks-1", managementCluster.Status.AKSResourceID.String())
				require.Equal(t, "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.Network/dnszones/example.com", managementCluster.Status.PublicDNSZoneResourceID.String())
				require.Equal(t, "https://kv-secrets.vault.azure.net", managementCluster.Status.HostedClustersSecretsKeyVaultURL)
				require.Equal(t, "https://kv-mi.vault.azure.net", managementCluster.Status.HostedClustersManagedIdentitiesKeyVaultURL)
				require.Equal(t, "12345678-1234-1234-1234-123456789012", managementCluster.Status.HostedClustersSecretsKeyVaultManagedIdentityClientID)
				require.Equal(t, "hcp-underlay-westus3-mgmt-1", managementCluster.Status.MaestroConsumerName)
				require.Equal(t, "http://maestro.maestro.svc.cluster.local:8000", managementCluster.Status.MaestroRESTAPIURL)
				require.Equal(t, "maestro-grpc.maestro.svc.cluster.local:8090", managementCluster.Status.MaestroGRPCTarget)
				require.Equal(t, "Manifests-MC-1", managementCluster.Status.KubeApplierCosmosContainerName)
			},
		},
		{
			name: "create stamp with auto-approve",
			modify: func(t *testing.T, opts *RegisterOptions) {
				opts.autoApprove = true
			},
			verify: func(t *testing.T, client *fleetcosmosstoragetesting.MockFleetDBClient) {
				ctx := testContext(t)
				stamp, err := client.Stamps().Get(ctx, testStampIdentifier)
				require.NoError(t, err)
				condition := apimeta.FindStatusCondition(stamp.Status.Conditions, string(fleetapi.StampConditionApproved))
				require.NotNil(t, condition)
				require.Equal(t, metav1.ConditionTrue, condition.Status)
				require.Equal(t, string(fleetapi.StampConditionReasonAutoApproved), condition.Reason)
			},
		},
		{
			name: "create stamp without auto-approve has no approved condition",
			modify: func(t *testing.T, opts *RegisterOptions) {
				opts.autoApprove = false
			},
			verify: func(t *testing.T, client *fleetcosmosstoragetesting.MockFleetDBClient) {
				ctx := testContext(t)
				stamp, err := client.Stamps().Get(ctx, testStampIdentifier)
				require.NoError(t, err)
				condition := apimeta.FindStatusCondition(stamp.Status.Conditions, string(fleetapi.StampConditionApproved))
				require.Nil(t, condition)
			},
		},
		{
			name: "update existing stamp preserves conditions",
			seed: func(t *testing.T) []any {
				stamp := &fleetapi.Stamp{
					CosmosMetadata: coreapi.CosmosMetadata{ResourceID: metadataapi.Must(fleetapihelpers.ToStampResourceID(testStampIdentifier)), PartitionKey: strings.ToLower(testStampIdentifier)},
					Status: fleetapi.StampStatus{
						Conditions: []metav1.Condition{
							{
								Type:   string(fleetapi.StampConditionApproved),
								Status: metav1.ConditionTrue,
								Reason: string(fleetapi.StampConditionReasonManuallyApproved),
							},
						},
					},
				}
				return []any{stamp}
			},
			modify: func(t *testing.T, opts *RegisterOptions) {
				opts.autoApprove = false
			},
			verify: func(t *testing.T, client *fleetcosmosstoragetesting.MockFleetDBClient) {
				ctx := testContext(t)
				stamp, err := client.Stamps().Get(ctx, testStampIdentifier)
				require.NoError(t, err)
				condition := apimeta.FindStatusCondition(stamp.Status.Conditions, string(fleetapi.StampConditionApproved))
				require.NotNil(t, condition)
				require.Equal(t, string(fleetapi.StampConditionReasonManuallyApproved), condition.Reason)
			},
		},
		{
			name: "update existing management cluster with same values succeeds",
			seed: func(t *testing.T) []any {
				stamp := &fleetapi.Stamp{
					CosmosMetadata: coreapi.CosmosMetadata{ResourceID: metadataapi.Must(fleetapihelpers.ToStampResourceID(testStampIdentifier)), PartitionKey: strings.ToLower(testStampIdentifier)},
				}
				managementCluster := &fleetapi.ManagementCluster{
					CosmosMetadata: coreapi.CosmosMetadata{ResourceID: metadataapi.Must(fleetapihelpers.ToManagementClusterResourceID(testStampIdentifier)), PartitionKey: strings.ToLower(testStampIdentifier)},
					Spec: fleetapi.ManagementClusterSpec{
						SchedulingPolicy: fleetapi.ManagementClusterSchedulingPolicySchedulable,
					},
					Status: seededManagementClusterStatus(),
				}
				return []any{stamp, managementCluster}
			},
			modify: func(t *testing.T, opts *RegisterOptions) {},
			verify: func(t *testing.T, client *fleetcosmosstoragetesting.MockFleetDBClient) {
				ctx := testContext(t)
				managementCluster, err := client.Stamps().ManagementClusters(testStampIdentifier).Get(ctx, fleetapi.ManagementClusterResourceName)
				require.NoError(t, err)
				require.Equal(t, fleetapi.ManagementClusterSchedulingPolicySchedulable, managementCluster.Spec.SchedulingPolicy)
			},
		},
		{
			name: "update existing management cluster preserves drifted immutable fields",
			seed: func(t *testing.T) []any {
				stamp := &fleetapi.Stamp{
					CosmosMetadata: coreapi.CosmosMetadata{ResourceID: metadataapi.Must(fleetapihelpers.ToStampResourceID(testStampIdentifier)), PartitionKey: strings.ToLower(testStampIdentifier)},
				}
				status := seededManagementClusterStatus()
				status.AKSResourceID = metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.ContainerService/managedClusters/original-aks"))
				status.PublicDNSZoneResourceID = metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.Network/dnszones/legacy.example.com"))
				status.HostedClustersSecretsKeyVaultURL = "https://kv-secrets-legacy.vault.azure.net"
				managementCluster := &fleetapi.ManagementCluster{
					CosmosMetadata: coreapi.CosmosMetadata{ResourceID: metadataapi.Must(fleetapihelpers.ToManagementClusterResourceID(testStampIdentifier)), PartitionKey: strings.ToLower(testStampIdentifier)},
					Spec: fleetapi.ManagementClusterSpec{
						SchedulingPolicy: fleetapi.ManagementClusterSchedulingPolicySchedulable,
					},
					Status: status,
				}
				return []any{stamp, managementCluster}
			},
			modify: func(t *testing.T, opts *RegisterOptions) {
				opts.aksResourceID = metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.ContainerService/managedClusters/different-aks"))
				opts.publicDNSZoneResourceID = metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.Network/dnszones/example.com"))
				opts.hostedClustersSecretsKeyVaultURL = "https://kv-secrets.vault.azure.net"
			},
			verify: func(t *testing.T, client *fleetcosmosstoragetesting.MockFleetDBClient) {
				ctx := testContext(t)
				managementCluster, err := client.Stamps().ManagementClusters(testStampIdentifier).Get(ctx, fleetapi.ManagementClusterResourceName)
				require.NoError(t, err)
				require.Equal(t, "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.ContainerService/managedClusters/original-aks", managementCluster.Status.AKSResourceID.String())
				require.Equal(t, "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.Network/dnszones/legacy.example.com", managementCluster.Status.PublicDNSZoneResourceID.String())
				require.Equal(t, "https://kv-secrets-legacy.vault.azure.net", managementCluster.Status.HostedClustersSecretsKeyVaultURL)
				require.Equal(t, "/api/aro_hcp/v1alpha1/provision_shards/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", managementCluster.Status.ClusterServiceProvisionShardID.String())
				require.Equal(t, []string{"203.0.113.10"}, managementCluster.Status.SharedIngressIPAddresses)
			},
		},
		{
			name: "update existing management cluster preserves scheduling policy",
			seed: func(t *testing.T) []any {
				stamp := &fleetapi.Stamp{
					CosmosMetadata: coreapi.CosmosMetadata{ResourceID: metadataapi.Must(fleetapihelpers.ToStampResourceID(testStampIdentifier)), PartitionKey: strings.ToLower(testStampIdentifier)},
				}
				managementCluster := &fleetapi.ManagementCluster{
					CosmosMetadata: coreapi.CosmosMetadata{ResourceID: metadataapi.Must(fleetapihelpers.ToManagementClusterResourceID(testStampIdentifier)), PartitionKey: strings.ToLower(testStampIdentifier)},
					Spec: fleetapi.ManagementClusterSpec{
						SchedulingPolicy: fleetapi.ManagementClusterSchedulingPolicyUnschedulable,
					},
					Status: seededManagementClusterStatus(),
				}
				return []any{stamp, managementCluster}
			},
			modify: func(t *testing.T, opts *RegisterOptions) {
				opts.schedulingPolicy = fleetapi.ManagementClusterSchedulingPolicySchedulable
			},
			verify: func(t *testing.T, client *fleetcosmosstoragetesting.MockFleetDBClient) {
				ctx := testContext(t)
				managementCluster, err := client.Stamps().ManagementClusters(testStampIdentifier).Get(ctx, fleetapi.ManagementClusterResourceName)
				require.NoError(t, err)
				require.Equal(t, fleetapi.ManagementClusterSchedulingPolicyUnschedulable, managementCluster.Spec.SchedulingPolicy)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := testContext(t)

			var client *fleetcosmosstoragetesting.MockFleetDBClient
			var err error
			if tt.seed != nil {
				client, err = fleetcosmosstoragetesting.NewMockFleetDBClientWithResources(ctx, tt.seed(t))
				require.NoError(t, err)
			} else {
				client = fleetcosmosstoragetesting.NewMockFleetDBClient()
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

func TestApplyMutableStatusToManagementCluster(t *testing.T) {
	t.Parallel()

	t.Run("backfills empty kube-applier cosmos container name", func(t *testing.T) {
		t.Parallel()
		opts := validRegisterOptions(t, fleetcosmosstoragetesting.NewMockFleetDBClient())
		managementCluster := &fleetapi.ManagementCluster{
			Status: fleetapi.ManagementClusterStatus{
				ClusterServiceProvisionShardID: ptr.To(metadataapi.Must(metadataapi.NewInternalID("/api/aro_hcp/v1alpha1/provision_shards/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"))),
				SharedIngressIPAddresses:       []string{"203.0.113.10"},
			},
		}

		opts.applyMutableStatusToManagementCluster(managementCluster)

		require.Equal(t, "Manifests-MC-1", managementCluster.Status.KubeApplierCosmosContainerName)
		require.Equal(t, "/api/aro_hcp/v1alpha1/provision_shards/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", managementCluster.Status.ClusterServiceProvisionShardID.String())
		require.Equal(t, []string{"203.0.113.10"}, managementCluster.Status.SharedIngressIPAddresses)
	})

	t.Run("does not overwrite existing kube-applier cosmos container name", func(t *testing.T) {
		t.Parallel()
		opts := validRegisterOptions(t, fleetcosmosstoragetesting.NewMockFleetDBClient())
		opts.kubeApplierCosmosContainerName = "Manifests-MC-configured"
		managementCluster := &fleetapi.ManagementCluster{
			Status: fleetapi.ManagementClusterStatus{
				KubeApplierCosmosContainerName: "Manifests-MC-persisted",
			},
		}

		opts.applyMutableStatusToManagementCluster(managementCluster)

		require.Equal(t, "Manifests-MC-persisted", managementCluster.Status.KubeApplierCosmosContainerName)
	})
}

func TestWarnImmutableManagementClusterDrift(t *testing.T) {
	t.Parallel()

	t.Run("emits warning when configured immutable value differs", func(t *testing.T) {
		t.Parallel()
		var logs []string
		logger := funcr.New(func(prefix, args string) {
			logs = append(logs, prefix+" "+args)
		}, funcr.Options{})
		ctx := testContextWithLogger(t, logger)

		client := fleetcosmosstoragetesting.NewMockFleetDBClient()
		opts := validRegisterOptions(t, client)
		existing := &fleetapi.ManagementCluster{
			Status: seededManagementClusterStatus(),
		}
		existing.Status.PublicDNSZoneResourceID = metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.Network/dnszones/legacy.example.com"))

		opts.warnImmutableManagementClusterDrift(ctx, existing)

		require.Len(t, logs, 1)
		require.Contains(t, logs[0], "configured immutable field differs from persisted value; retaining persisted value")
		require.Contains(t, logs[0], "status.publicDNSZoneResourceID")
		require.Contains(t, logs[0], "legacy.example.com")
		require.Contains(t, logs[0], "example.com")
	})

	t.Run("does not emit warning when configured values match", func(t *testing.T) {
		t.Parallel()
		var logs []string
		logger := funcr.New(func(prefix, args string) {
			logs = append(logs, prefix+" "+args)
		}, funcr.Options{})
		ctx := testContextWithLogger(t, logger)

		client := fleetcosmosstoragetesting.NewMockFleetDBClient()
		opts := validRegisterOptions(t, client)
		existing := &fleetapi.ManagementCluster{
			Status: seededManagementClusterStatus(),
		}

		opts.warnImmutableManagementClusterDrift(ctx, existing)

		require.Empty(t, logs)
	})
}
