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

package managementclustercontrollers

import (
	"context"
	"fmt"
	"testing"

	"github.com/go-logr/logr/testr"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/backend/pkg/listertesting"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	testSubscriptionID        = "00000000-0000-0000-0000-000000000000"
	testResourceGroup         = "rg"
	testDNSResourceGroup      = "dns-rg"
	testDNSZone               = "test.example.com"
	testCXSecretsKVURL        = "https://cx-kv.vault.azure.net/"
	testCXManagedIdentitiesKV = "https://mi-kv.vault.azure.net/"
	testCXMIClientID          = "c2bde1aa-d904-48cd-a728-9de33e3ddca9"
	testMaestroRestURL        = "http://maestro.maestro.svc.cluster.local:8000"
	testMaestroGRPCURL        = "maestro-grpc.maestro.svc.cluster.local:8090"
)

func testAKSResourceIDString(aksName string) string {
	return fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.ContainerService/managedClusters/%s", testSubscriptionID, testResourceGroup, aksName)
}

func testPublicDNSZoneResourceIDString() string {
	return fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Network/dnszones/%s", testSubscriptionID, testDNSResourceGroup, testDNSZone)
}

// managementClusterCmpOptions defines the comparison options for ManagementCluster
// assertions. Non-deterministic fields (Cosmos ETags, UIDs, condition timestamps,
// UUID-based resource IDs) are ignored so tests focus on meaningful state.
var managementClusterCmpOptions = append(
	api.CmpDiffOptions,
	cmpopts.IgnoreFields(api.ManagementCluster{}, "CosmosMetadata", "ResourceID"),
	cmpopts.IgnoreFields(api.Condition{}, "LastTransitionTime"),
)

// assertManagementClusterEqual compares two ManagementCluster objects ignoring
// non-deterministic fields (Cosmos ETags, UIDs, condition timestamps).
func assertManagementClusterEqual(t *testing.T, expected, got *api.ManagementCluster) {
	t.Helper()
	if diff := cmp.Diff(expected, got, managementClusterCmpOptions...); diff != "" {
		t.Errorf("ManagementCluster mismatch (-expected +got):\n%s", diff)
	}
}

// buildExpectedManagementCluster constructs the ManagementCluster that the sync
// controller is expected to produce from a provision shard with the given parameters.
// This is intentionally hand-built (not via ConvertCSManagementClusterToInternal)
// so the test catches conversion bugs. The resource ID name is expected to be a UUID
// and is not set here — callers should compare using managementClusterCmpOptions
// which ignores CosmosMetadata and ResourceID.
func buildExpectedManagementCluster(t *testing.T, shardID, consumerName, aksName string) *api.ManagementCluster {
	t.Helper()
	aksResourceID := api.Must(azcorearm.ParseResourceID(testAKSResourceIDString(aksName)))
	publicDNSZoneResourceID := api.Must(azcorearm.ParseResourceID(testPublicDNSZoneResourceIDString()))

	return &api.ManagementCluster{
		Spec: api.ManagementClusterSpec{
			SchedulingPolicy: api.ManagementClusterSchedulingPolicySchedulable,
		},
		Status: api.ManagementClusterStatus{
			AKSResourceID:                            aksResourceID,
			PublicDNSZoneResourceID:                  publicDNSZoneResourceID,
			CXSecretsKeyVaultURL:                     testCXSecretsKVURL,
			CXManagedIdentitiesKeyVaultURL:           testCXManagedIdentitiesKV,
			CXSecretsKeyVaultManagedIdentityClientID: testCXMIClientID,
			CSProvisionShardID:                       shardID,
			MaestroConfig: api.MaestroConfig{
				ConsumerName: consumerName,
				RESTAPIConfig: api.MaestroRESTAPIConfig{
					URL: testMaestroRestURL,
				},
				GRPCAPIConfig: api.MaestroGRPCAPIConfig{
					URL: testMaestroGRPCURL,
				},
			},
			Conditions: []api.Condition{
				{
					Type:   string(api.ManagementClusterConditionReady),
					Status: api.ConditionTrue,
					Reason: string(api.ManagementClusterConditionReasonProvisionShardActive),
				},
			},
		},
	}
}

// getManagementClusterByShardID retrieves a management cluster from the mock CRUD by
// listing all records and finding the one with the matching CSProvisionShardID.
func getManagementClusterByShardID(t *testing.T, ctx context.Context, crud database.ManagementClusterCRUD, shardID string) *api.ManagementCluster {
	t.Helper()
	iter, err := crud.List(ctx, nil)
	require.NoError(t, err)
	for _, mc := range iter.Items(ctx) {
		if mc.Status.CSProvisionShardID == shardID {
			return mc
		}
	}
	require.NoError(t, iter.GetError())
	t.Fatalf("no management cluster found for shard ID %q", shardID)
	return nil
}

// errorManagementClusterLister always returns an error.
type errorManagementClusterLister struct {
	err error
}

func (e *errorManagementClusterLister) List(_ context.Context) ([]*api.ManagementCluster, error) {
	return nil, e.err
}

func (e *errorManagementClusterLister) Get(_ context.Context, _ string) (*api.ManagementCluster, error) {
	return nil, e.err
}

func (e *errorManagementClusterLister) GetByCSProvisionShardID(_ context.Context, _ string) (*api.ManagementCluster, error) {
	return nil, e.err
}

// buildTestProvisionShard creates a provision shard with an AKS name that follows
// the {env}-{region}-mgmt-{stamp} pattern required by ConvertCSManagementClusterToInternal.
// status should be "active", "maintenance", or "offline".
func buildTestProvisionShard(t *testing.T, shardID, consumerName, aksName, status string) *arohcpv1alpha1.ProvisionShard {
	t.Helper()
	shard, err := arohcpv1alpha1.NewProvisionShard().
		ID(shardID).
		Status(status).
		Topology("shared").
		AzureShard(arohcpv1alpha1.NewAzureShard().
			AksManagementClusterResourceId(testAKSResourceIDString(aksName)).
			PublicDnsZoneResourceId(testPublicDNSZoneResourceIDString()).
			CxSecretsKeyVaultUrl(testCXSecretsKVURL).
			CxManagedIdentitiesKeyVaultUrl(testCXManagedIdentitiesKV).
			CxSecretsKeyVaultManagedIdentityClientId(testCXMIClientID),
		).
		MaestroConfig(
			arohcpv1alpha1.NewProvisionShardMaestroConfig().
				ConsumerName(consumerName).
				RestApiConfig(arohcpv1alpha1.NewProvisionShardMaestroRestApiConfig().
					Url(testMaestroRestURL)).
				GrpcApiConfig(arohcpv1alpha1.NewProvisionShardMaestroGrpcApiConfig().
					Url(testMaestroGRPCURL)),
		).
		Build()
	require.NoError(t, err)
	return shard
}

func TestSyncOnce(t *testing.T) {
	tests := []struct {
		name                string
		setup               func(t *testing.T, ctrl *gomock.Controller) (cs ocm.ClusterServiceClientSpec, crud database.ManagementClusterCRUD, lister listers.ManagementClusterLister)
		expectedErrorSubstr string
		validate            func(t *testing.T, ctx context.Context, crud database.ManagementClusterCRUD)
	}{
		{
			name: "no shards syncs successfully",
			setup: func(t *testing.T, ctrl *gomock.Controller) (ocm.ClusterServiceClientSpec, database.ManagementClusterCRUD, listers.ManagementClusterLister) {
				t.Helper()
				mockCS := ocm.NewMockClusterServiceClientSpec(ctrl)
				mockCS.EXPECT().ListProvisionShards().Return(ocm.NewSimpleProvisionShardListIterator(nil, nil))
				return mockCS, databasetesting.NewMockDBClient().ManagementClusters(), &listertesting.SliceManagementClusterLister{}
			},
		},
		{
			name: "CS list error is propagated",
			setup: func(t *testing.T, ctrl *gomock.Controller) (ocm.ClusterServiceClientSpec, database.ManagementClusterCRUD, listers.ManagementClusterLister) {
				t.Helper()
				mockCS := ocm.NewMockClusterServiceClientSpec(ctrl)
				mockCS.EXPECT().ListProvisionShards().Return(ocm.NewSimpleProvisionShardListIterator(nil, fmt.Errorf("list failed")))
				return mockCS, databasetesting.NewMockDBClient().ManagementClusters(), &listertesting.SliceManagementClusterLister{}
			},
			expectedErrorSubstr: "list failed",
		},
		{
			name: "new management cluster is created",
			setup: func(t *testing.T, ctrl *gomock.Controller) (ocm.ClusterServiceClientSpec, database.ManagementClusterCRUD, listers.ManagementClusterLister) {
				t.Helper()
				mockCS := ocm.NewMockClusterServiceClientSpec(ctrl)
				shard := buildTestProvisionShard(t, "test-shard-id", "test-consumer", "test-westus3-mgmt-1", "active")
				mockCS.EXPECT().ListProvisionShards().Return(
					ocm.NewSimpleProvisionShardListIterator([]*arohcpv1alpha1.ProvisionShard{shard}, nil),
				)
				return mockCS, databasetesting.NewMockDBClient().ManagementClusters(), &listertesting.SliceManagementClusterLister{}
			},
			validate: func(t *testing.T, ctx context.Context, crud database.ManagementClusterCRUD) {
				t.Helper()
				doc := getManagementClusterByShardID(t, ctx, crud, "test-shard-id")
				assert.NoError(t, uuid.Validate(doc.ResourceID.Name), "resource ID name should be a valid UUID")
				expected := buildExpectedManagementCluster(t, "test-shard-id", "test-consumer", "test-westus3-mgmt-1")
				assertManagementClusterEqual(t, expected, doc)
			},
		},
		{
			name: "existing management cluster is updated when SchedulingPolicy changed",
			setup: func(t *testing.T, ctrl *gomock.Controller) (ocm.ClusterServiceClientSpec, database.ManagementClusterCRUD, listers.ManagementClusterLister) {
				t.Helper()
				mockCS := ocm.NewMockClusterServiceClientSpec(ctrl)
				mockCRUD := databasetesting.NewMockDBClient().ManagementClusters()

				shard := buildTestProvisionShard(t, "test-shard-id", "test-consumer", "test-westus3-mgmt-1", "active")
				existing, err := ocm.ConvertCSManagementClusterToInternal(shard)
				require.NoError(t, err)
				existing.Spec.SchedulingPolicy = api.ManagementClusterSchedulingPolicyUnschedulable
				_, err = mockCRUD.Create(t.Context(), existing, nil)
				require.NoError(t, err)

				mockCS.EXPECT().ListProvisionShards().Return(
					ocm.NewSimpleProvisionShardListIterator([]*arohcpv1alpha1.ProvisionShard{shard}, nil),
				)
				return mockCS, mockCRUD, &listertesting.SliceManagementClusterLister{ManagementClusters: []*api.ManagementCluster{existing}}
			},
			validate: func(t *testing.T, ctx context.Context, crud database.ManagementClusterCRUD) {
				t.Helper()
				doc := getManagementClusterByShardID(t, ctx, crud, "test-shard-id")
				expected := buildExpectedManagementCluster(t, "test-shard-id", "test-consumer", "test-westus3-mgmt-1")
				assertManagementClusterEqual(t, expected, doc)
			},
		},
		{
			name: "unchanged management cluster is not replaced",
			setup: func(t *testing.T, ctrl *gomock.Controller) (ocm.ClusterServiceClientSpec, database.ManagementClusterCRUD, listers.ManagementClusterLister) {
				t.Helper()
				mockCS := ocm.NewMockClusterServiceClientSpec(ctrl)
				mockCRUD := database.NewMockManagementClusterCRUD(ctrl)

				shard := buildTestProvisionShard(t, "test-shard-id", "test-consumer", "test-westus3-mgmt-1", "active")
				existing, err := ocm.ConvertCSManagementClusterToInternal(shard)
				require.NoError(t, err)

				mockCS.EXPECT().ListProvisionShards().Return(
					ocm.NewSimpleProvisionShardListIterator([]*arohcpv1alpha1.ProvisionShard{shard}, nil),
				)
				// No Replace or Create calls expected — gomock will fail if either is called
				return mockCS, mockCRUD, &listertesting.SliceManagementClusterLister{ManagementClusters: []*api.ManagementCluster{existing}}
			},
		},
		{
			name: "conversion error is collected and reported",
			setup: func(t *testing.T, ctrl *gomock.Controller) (ocm.ClusterServiceClientSpec, database.ManagementClusterCRUD, listers.ManagementClusterLister) {
				t.Helper()
				mockCS := ocm.NewMockClusterServiceClientSpec(ctrl)
				badShard, err := arohcpv1alpha1.NewProvisionShard().
					ID("bad-shard").
					AzureShard(arohcpv1alpha1.NewAzureShard().
						AksManagementClusterResourceId(testAKSResourceIDString("test-westus3-mgmt-bad")).
						PublicDnsZoneResourceId(testPublicDNSZoneResourceIDString()).
						CxSecretsKeyVaultUrl(testCXSecretsKVURL).
						CxManagedIdentitiesKeyVaultUrl(testCXManagedIdentitiesKV).
						CxSecretsKeyVaultManagedIdentityClientId(testCXMIClientID),
					).
					Build()
				require.NoError(t, err)
				mockCS.EXPECT().ListProvisionShards().Return(
					ocm.NewSimpleProvisionShardListIterator([]*arohcpv1alpha1.ProvisionShard{badShard}, nil),
				)
				return mockCS, databasetesting.NewMockDBClient().ManagementClusters(), &listertesting.SliceManagementClusterLister{}
			},
			expectedErrorSubstr: "no maestro config",
		},
		{
			name: "multiple shards are all created",
			setup: func(t *testing.T, ctrl *gomock.Controller) (ocm.ClusterServiceClientSpec, database.ManagementClusterCRUD, listers.ManagementClusterLister) {
				t.Helper()
				mockCS := ocm.NewMockClusterServiceClientSpec(ctrl)

				shard1 := buildTestProvisionShard(t, "shard-1", "consumer-1", "test-westus3-mgmt-1", "active")
				shard2 := buildTestProvisionShard(t, "shard-2", "consumer-2", "test-eastus-mgmt-1", "active")

				mockCS.EXPECT().ListProvisionShards().Return(
					ocm.NewSimpleProvisionShardListIterator([]*arohcpv1alpha1.ProvisionShard{shard1, shard2}, nil),
				)
				return mockCS, databasetesting.NewMockDBClient().ManagementClusters(), &listertesting.SliceManagementClusterLister{}
			},
			validate: func(t *testing.T, ctx context.Context, crud database.ManagementClusterCRUD) {
				t.Helper()
				doc1 := getManagementClusterByShardID(t, ctx, crud, "shard-1")
				expected1 := buildExpectedManagementCluster(t, "shard-1", "consumer-1", "test-westus3-mgmt-1")
				assertManagementClusterEqual(t, expected1, doc1)

				doc2 := getManagementClusterByShardID(t, ctx, crud, "shard-2")
				expected2 := buildExpectedManagementCluster(t, "shard-2", "consumer-2", "test-eastus-mgmt-1")
				assertManagementClusterEqual(t, expected2, doc2)
			},
		},
		{
			name: "Create DB failure is collected and reported",
			setup: func(t *testing.T, ctrl *gomock.Controller) (ocm.ClusterServiceClientSpec, database.ManagementClusterCRUD, listers.ManagementClusterLister) {
				t.Helper()
				mockCS := ocm.NewMockClusterServiceClientSpec(ctrl)
				mockCRUD := database.NewMockManagementClusterCRUD(ctrl)

				shard := buildTestProvisionShard(t, "test-shard-id", "test-consumer", "test-westus3-mgmt-1", "active")
				mockCS.EXPECT().ListProvisionShards().Return(
					ocm.NewSimpleProvisionShardListIterator([]*arohcpv1alpha1.ProvisionShard{shard}, nil),
				)
				mockCRUD.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Nil()).Return(nil, fmt.Errorf("cosmos create failed"))
				return mockCS, mockCRUD, &listertesting.SliceManagementClusterLister{}
			},
			expectedErrorSubstr: "cosmos create failed",
		},
		{
			name: "Replace DB failure is collected and reported",
			setup: func(t *testing.T, ctrl *gomock.Controller) (ocm.ClusterServiceClientSpec, database.ManagementClusterCRUD, listers.ManagementClusterLister) {
				t.Helper()
				mockCS := ocm.NewMockClusterServiceClientSpec(ctrl)
				mockCRUD := database.NewMockManagementClusterCRUD(ctrl)

				// Existing has SchedulingPolicy: Unschedulable, CS returns Schedulable (active shard) → triggers Replace
				shard := buildTestProvisionShard(t, "test-shard-id", "test-consumer", "test-westus3-mgmt-1", "active")
				existing, err := ocm.ConvertCSManagementClusterToInternal(shard)
				require.NoError(t, err)
				existing.Spec.SchedulingPolicy = api.ManagementClusterSchedulingPolicyUnschedulable

				mockCS.EXPECT().ListProvisionShards().Return(
					ocm.NewSimpleProvisionShardListIterator([]*arohcpv1alpha1.ProvisionShard{shard}, nil),
				)
				mockCRUD.EXPECT().Replace(gomock.Any(), gomock.Any(), gomock.Nil()).Return(nil, fmt.Errorf("cosmos replace failed"))
				return mockCS, mockCRUD, &listertesting.SliceManagementClusterLister{ManagementClusters: []*api.ManagementCluster{existing}}
			},
			expectedErrorSubstr: "cosmos replace failed",
		},
		{
			name: "existing cluster condition transitions from Ready=False to Ready=True",
			setup: func(t *testing.T, ctrl *gomock.Controller) (ocm.ClusterServiceClientSpec, database.ManagementClusterCRUD, listers.ManagementClusterLister) {
				t.Helper()
				mockCS := ocm.NewMockClusterServiceClientSpec(ctrl)
				mockCRUD := databasetesting.NewMockDBClient().ManagementClusters()

				// Create an existing cluster with Ready=False (as if it was previously in maintenance)
				maintenanceShard := buildTestProvisionShard(t, "test-shard-id", "test-consumer", "test-westus3-mgmt-1", "maintenance")
				existing, err := ocm.ConvertCSManagementClusterToInternal(maintenanceShard)
				require.NoError(t, err)
				_, err = mockCRUD.Create(t.Context(), existing, nil)
				require.NoError(t, err)

				// Now CS returns the shard as active
				activeShard := buildTestProvisionShard(t, "test-shard-id", "test-consumer", "test-westus3-mgmt-1", "active")
				mockCS.EXPECT().ListProvisionShards().Return(
					ocm.NewSimpleProvisionShardListIterator([]*arohcpv1alpha1.ProvisionShard{activeShard}, nil),
				)
				return mockCS, mockCRUD, &listertesting.SliceManagementClusterLister{ManagementClusters: []*api.ManagementCluster{existing}}
			},
			validate: func(t *testing.T, ctx context.Context, crud database.ManagementClusterCRUD) {
				t.Helper()
				doc := getManagementClusterByShardID(t, ctx, crud, "test-shard-id")
				assert.Equal(t, api.ManagementClusterSchedulingPolicySchedulable, doc.Spec.SchedulingPolicy, "should be schedulable")
				// Verify Ready condition transitioned to True
				var readyCond *api.Condition
				for i := range doc.Status.Conditions {
					if doc.Status.Conditions[i].Type == string(api.ManagementClusterConditionReady) {
						readyCond = &doc.Status.Conditions[i]
						break
					}
				}
				require.NotNil(t, readyCond, "Ready condition must exist")
				assert.Equal(t, api.ConditionTrue, readyCond.Status, "Ready condition should be True")
				assert.Equal(t, string(api.ManagementClusterConditionReasonProvisionShardActive), readyCond.Reason)
			},
		},
		{
			name: "lister Get non-404 error is collected and reported",
			setup: func(t *testing.T, ctrl *gomock.Controller) (ocm.ClusterServiceClientSpec, database.ManagementClusterCRUD, listers.ManagementClusterLister) {
				t.Helper()
				mockCS := ocm.NewMockClusterServiceClientSpec(ctrl)
				shard := buildTestProvisionShard(t, "test-shard-id", "test-consumer", "test-westus3-mgmt-1", "active")
				mockCS.EXPECT().ListProvisionShards().Return(
					ocm.NewSimpleProvisionShardListIterator([]*arohcpv1alpha1.ProvisionShard{shard}, nil),
				)
				return mockCS, databasetesting.NewMockDBClient().ManagementClusters(), &errorManagementClusterLister{err: fmt.Errorf("lister internal error")}
			},
			expectedErrorSubstr: "lister internal error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := utils.ContextWithLogger(t.Context(), testr.New(t))
			ctrl := gomock.NewController(t)
			cs, crud, lister := tt.setup(t, ctrl)

			c := &managementClusterSyncController{
				name:                    "test",
				clusterServiceClient:    cs,
				managementClusterCRUD:   crud,
				managementClusterLister: lister,
			}

			err := c.SyncOnce(ctx, nil)
			if len(tt.expectedErrorSubstr) > 0 {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedErrorSubstr)
			} else {
				require.NoError(t, err)
			}
			if tt.validate != nil {
				tt.validate(t, ctx, crud)
			}
		})
	}
}
