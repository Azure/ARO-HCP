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

	testShardID  = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	testShardID2 = "11111111-2222-3333-4444-555555555555"
	testShardID3 = "22222222-3333-4444-5555-666666666666"
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
func testProvisionShardHREF(shardID string) string {
	return "/api/aro_hcp/v1alpha1/provision_shards/" + shardID
}

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
			CSProvisionShardID:                       api.Must(api.NewInternalID(testProvisionShardHREF(shardID))),
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

// getManagementCluster retrieves a management cluster by name from the mock CRUD.
func getManagementCluster(t *testing.T, ctx context.Context, dbClient database.DBClient, name string) *api.ManagementCluster {
	t.Helper()
	mc, err := dbClient.ManagementClusters(testSubscriptionID, testResourceGroup).Get(ctx, name)
	require.NoError(t, err)
	return mc
}

// buildTestProvisionShard creates a provision shard with an AKS name that follows
// the {env}-{region}-mgmt-{stamp} pattern required by ConvertCSManagementClusterToInternal.
// status should be "active", "maintenance", or "offline".
func buildTestProvisionShard(t *testing.T, shardID, consumerName, aksName, status string) *arohcpv1alpha1.ProvisionShard {
	t.Helper()
	shard, err := arohcpv1alpha1.NewProvisionShard().
		ID(shardID).
		HREF(testProvisionShardHREF(shardID)).
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

// mockDBClientWithCRUD wraps a gomock ManagementClusterCRUD into a database.DBClient
// for tests that need to assert specific CRUD method expectations.
type mockDBClientWithCRUD struct {
	database.DBClient
	crud database.ManagementClusterCRUD
}

func (m *mockDBClientWithCRUD) ManagementClusters(_, _ string) database.ManagementClusterCRUD {
	return m.crud
}

// errorManagementClusterLister always returns an error from Get.
type errorManagementClusterLister struct {
	listertesting.SliceManagementClusterLister
	err error
}

func (e *errorManagementClusterLister) Get(_ context.Context, _, _, _ string) (*api.ManagementCluster, error) {
	return nil, e.err
}

func TestSyncOnce(t *testing.T) {
	tests := []struct {
		name                string
		setup               func(t *testing.T, ctrl *gomock.Controller) (ocm.ClusterServiceClientSpec, database.DBClient, listers.ManagementClusterLister)
		expectedErrorSubstr string
		validate            func(t *testing.T, ctx context.Context, dbClient database.DBClient)
	}{
		{
			name: "no shards syncs successfully",
			setup: func(t *testing.T, ctrl *gomock.Controller) (ocm.ClusterServiceClientSpec, database.DBClient, listers.ManagementClusterLister) {
				t.Helper()
				mockCS := ocm.NewMockClusterServiceClientSpec(ctrl)
				mockCS.EXPECT().ListProvisionShards().Return(ocm.NewSimpleProvisionShardListIterator(nil, nil))
				return mockCS, databasetesting.NewMockDBClient(), &listertesting.SliceManagementClusterLister{}
			},
		},
		{
			name: "CS list error is propagated",
			setup: func(t *testing.T, ctrl *gomock.Controller) (ocm.ClusterServiceClientSpec, database.DBClient, listers.ManagementClusterLister) {
				t.Helper()
				mockCS := ocm.NewMockClusterServiceClientSpec(ctrl)
				mockCS.EXPECT().ListProvisionShards().Return(ocm.NewSimpleProvisionShardListIterator(nil, fmt.Errorf("list failed")))
				return mockCS, databasetesting.NewMockDBClient(), &listertesting.SliceManagementClusterLister{}
			},
			expectedErrorSubstr: "list failed",
		},
		{
			name: "new management cluster is created",
			setup: func(t *testing.T, ctrl *gomock.Controller) (ocm.ClusterServiceClientSpec, database.DBClient, listers.ManagementClusterLister) {
				t.Helper()
				mockCS := ocm.NewMockClusterServiceClientSpec(ctrl)
				shard := buildTestProvisionShard(t, testShardID, "test-consumer", "test-westus3-mgmt-1", "active")
				mockCS.EXPECT().ListProvisionShards().Return(
					ocm.NewSimpleProvisionShardListIterator([]*arohcpv1alpha1.ProvisionShard{shard}, nil),
				)
				return mockCS, databasetesting.NewMockDBClient(), &listertesting.SliceManagementClusterLister{}
			},
			validate: func(t *testing.T, ctx context.Context, dbClient database.DBClient) {
				t.Helper()
				doc := getManagementCluster(t, ctx, dbClient, "test-westus3-mgmt-1")
				assert.Equal(t, "test-westus3-mgmt-1", doc.ResourceID.Name, "resource ID name should be the AKS cluster name")
				expected := buildExpectedManagementCluster(t, testShardID, "test-consumer", "test-westus3-mgmt-1")
				assertManagementClusterEqual(t, expected, doc)
			},
		},
		{
			name: "existing management cluster is updated when SchedulingPolicy changed",
			setup: func(t *testing.T, ctrl *gomock.Controller) (ocm.ClusterServiceClientSpec, database.DBClient, listers.ManagementClusterLister) {
				t.Helper()
				mockCS := ocm.NewMockClusterServiceClientSpec(ctrl)
				mockDB := databasetesting.NewMockDBClient()

				shard := buildTestProvisionShard(t, testShardID, "test-consumer", "test-westus3-mgmt-1", "active")
				existing, err := ocm.ConvertCSManagementClusterToInternal(shard)
				require.NoError(t, err)
				existingResourceID := api.Must(api.ToManagementClusterResourceID(testSubscriptionID, testResourceGroup, "test-westus3-mgmt-1"))
				existing.ResourceID = existingResourceID
				existing.CosmosMetadata.ResourceID = existingResourceID
				existing.Spec.SchedulingPolicy = api.ManagementClusterSchedulingPolicyUnschedulable
				_, err = mockDB.ManagementClusters(testSubscriptionID, testResourceGroup).Create(t.Context(), existing, nil)
				require.NoError(t, err)

				mockCS.EXPECT().ListProvisionShards().Return(
					ocm.NewSimpleProvisionShardListIterator([]*arohcpv1alpha1.ProvisionShard{shard}, nil),
				)
				return mockCS, mockDB, &listertesting.SliceManagementClusterLister{ManagementClusters: []*api.ManagementCluster{existing}}
			},
			validate: func(t *testing.T, ctx context.Context, dbClient database.DBClient) {
				t.Helper()
				doc := getManagementCluster(t, ctx, dbClient, "test-westus3-mgmt-1")
				expected := buildExpectedManagementCluster(t, testShardID, "test-consumer", "test-westus3-mgmt-1")
				assertManagementClusterEqual(t, expected, doc)
			},
		},
		{
			name: "unchanged management cluster is not replaced",
			setup: func(t *testing.T, ctrl *gomock.Controller) (ocm.ClusterServiceClientSpec, database.DBClient, listers.ManagementClusterLister) {
				t.Helper()
				mockCS := ocm.NewMockClusterServiceClientSpec(ctrl)
				mockCRUD := database.NewMockManagementClusterCRUD(ctrl)

				shard := buildTestProvisionShard(t, testShardID, "test-consumer", "test-westus3-mgmt-1", "active")
				existing, err := ocm.ConvertCSManagementClusterToInternal(shard)
				require.NoError(t, err)
				existingResourceID := api.Must(api.ToManagementClusterResourceID(testSubscriptionID, testResourceGroup, "test-westus3-mgmt-1"))
				existing.ResourceID = existingResourceID
				existing.CosmosMetadata.ResourceID = existingResourceID

				mockCS.EXPECT().ListProvisionShards().Return(
					ocm.NewSimpleProvisionShardListIterator([]*arohcpv1alpha1.ProvisionShard{shard}, nil),
				)
				// Lister returns existing cluster; no CRUD calls expected — gomock will fail if Create or Replace is called
				return mockCS, &mockDBClientWithCRUD{crud: mockCRUD}, &listertesting.SliceManagementClusterLister{ManagementClusters: []*api.ManagementCluster{existing}}
			},
		},
		{
			name: "conversion error is collected and reported",
			setup: func(t *testing.T, ctrl *gomock.Controller) (ocm.ClusterServiceClientSpec, database.DBClient, listers.ManagementClusterLister) {
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
				return mockCS, databasetesting.NewMockDBClient(), &listertesting.SliceManagementClusterLister{}
			},
			expectedErrorSubstr: "provision shard has empty HREF",
		},
		{
			name: "multiple shards are all created",
			setup: func(t *testing.T, ctrl *gomock.Controller) (ocm.ClusterServiceClientSpec, database.DBClient, listers.ManagementClusterLister) {
				t.Helper()
				mockCS := ocm.NewMockClusterServiceClientSpec(ctrl)

				shard1 := buildTestProvisionShard(t, testShardID2, "consumer-1", "test-westus3-mgmt-1", "active")
				shard2 := buildTestProvisionShard(t, testShardID3, "consumer-2", "test-eastus-mgmt-1", "active")

				mockCS.EXPECT().ListProvisionShards().Return(
					ocm.NewSimpleProvisionShardListIterator([]*arohcpv1alpha1.ProvisionShard{shard1, shard2}, nil),
				)
				return mockCS, databasetesting.NewMockDBClient(), &listertesting.SliceManagementClusterLister{}
			},
			validate: func(t *testing.T, ctx context.Context, dbClient database.DBClient) {
				t.Helper()
				doc1 := getManagementCluster(t, ctx, dbClient, "test-westus3-mgmt-1")
				expected1 := buildExpectedManagementCluster(t, testShardID2, "consumer-1", "test-westus3-mgmt-1")
				assertManagementClusterEqual(t, expected1, doc1)

				doc2 := getManagementCluster(t, ctx, dbClient, "test-eastus-mgmt-1")
				expected2 := buildExpectedManagementCluster(t, testShardID3, "consumer-2", "test-eastus-mgmt-1")
				assertManagementClusterEqual(t, expected2, doc2)
			},
		},
		{
			name: "Create DB failure is collected and reported",
			setup: func(t *testing.T, ctrl *gomock.Controller) (ocm.ClusterServiceClientSpec, database.DBClient, listers.ManagementClusterLister) {
				t.Helper()
				mockCS := ocm.NewMockClusterServiceClientSpec(ctrl)
				mockCRUD := database.NewMockManagementClusterCRUD(ctrl)

				shard := buildTestProvisionShard(t, testShardID, "test-consumer", "test-westus3-mgmt-1", "active")
				mockCS.EXPECT().ListProvisionShards().Return(
					ocm.NewSimpleProvisionShardListIterator([]*arohcpv1alpha1.ProvisionShard{shard}, nil),
				)
				mockCRUD.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Nil()).Return(nil, fmt.Errorf("cosmos create failed"))
				return mockCS, &mockDBClientWithCRUD{crud: mockCRUD}, &listertesting.SliceManagementClusterLister{}
			},
			expectedErrorSubstr: "cosmos create failed",
		},
		{
			name: "Replace DB failure is collected and reported",
			setup: func(t *testing.T, ctrl *gomock.Controller) (ocm.ClusterServiceClientSpec, database.DBClient, listers.ManagementClusterLister) {
				t.Helper()
				mockCS := ocm.NewMockClusterServiceClientSpec(ctrl)
				mockCRUD := database.NewMockManagementClusterCRUD(ctrl)

				// Existing has SchedulingPolicy: Unschedulable, CS returns Schedulable (active shard) → triggers Replace
				shard := buildTestProvisionShard(t, testShardID, "test-consumer", "test-westus3-mgmt-1", "active")
				existing, err := ocm.ConvertCSManagementClusterToInternal(shard)
				require.NoError(t, err)
				existingResourceID := api.Must(api.ToManagementClusterResourceID(testSubscriptionID, testResourceGroup, "test-westus3-mgmt-1"))
				existing.ResourceID = existingResourceID
				existing.CosmosMetadata.ResourceID = existingResourceID
				existing.Spec.SchedulingPolicy = api.ManagementClusterSchedulingPolicyUnschedulable

				mockCS.EXPECT().ListProvisionShards().Return(
					ocm.NewSimpleProvisionShardListIterator([]*arohcpv1alpha1.ProvisionShard{shard}, nil),
				)
				mockCRUD.EXPECT().Replace(gomock.Any(), gomock.Any(), gomock.Nil()).Return(nil, fmt.Errorf("cosmos replace failed"))
				return mockCS, &mockDBClientWithCRUD{crud: mockCRUD}, &listertesting.SliceManagementClusterLister{ManagementClusters: []*api.ManagementCluster{existing}}
			},
			expectedErrorSubstr: "cosmos replace failed",
		},
		{
			name: "existing cluster condition transitions from Ready=False to Ready=True",
			setup: func(t *testing.T, ctrl *gomock.Controller) (ocm.ClusterServiceClientSpec, database.DBClient, listers.ManagementClusterLister) {
				t.Helper()
				mockCS := ocm.NewMockClusterServiceClientSpec(ctrl)
				mockDB := databasetesting.NewMockDBClient()

				// Create an existing cluster with Ready=False (as if it was previously in maintenance)
				maintenanceShard := buildTestProvisionShard(t, testShardID, "test-consumer", "test-westus3-mgmt-1", "maintenance")
				existing, err := ocm.ConvertCSManagementClusterToInternal(maintenanceShard)
				require.NoError(t, err)
				existingResourceID := api.Must(api.ToManagementClusterResourceID(testSubscriptionID, testResourceGroup, "test-westus3-mgmt-1"))
				existing.ResourceID = existingResourceID
				existing.CosmosMetadata.ResourceID = existingResourceID
				_, err = mockDB.ManagementClusters(testSubscriptionID, testResourceGroup).Create(t.Context(), existing, nil)
				require.NoError(t, err)

				// Now CS returns the shard as active
				activeShard := buildTestProvisionShard(t, testShardID, "test-consumer", "test-westus3-mgmt-1", "active")
				mockCS.EXPECT().ListProvisionShards().Return(
					ocm.NewSimpleProvisionShardListIterator([]*arohcpv1alpha1.ProvisionShard{activeShard}, nil),
				)
				return mockCS, mockDB, &listertesting.SliceManagementClusterLister{ManagementClusters: []*api.ManagementCluster{existing}}
			},
			validate: func(t *testing.T, ctx context.Context, dbClient database.DBClient) {
				t.Helper()
				doc := getManagementCluster(t, ctx, dbClient, "test-westus3-mgmt-1")
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
			setup: func(t *testing.T, ctrl *gomock.Controller) (ocm.ClusterServiceClientSpec, database.DBClient, listers.ManagementClusterLister) {
				t.Helper()
				mockCS := ocm.NewMockClusterServiceClientSpec(ctrl)
				shard := buildTestProvisionShard(t, testShardID, "test-consumer", "test-westus3-mgmt-1", "active")
				mockCS.EXPECT().ListProvisionShards().Return(
					ocm.NewSimpleProvisionShardListIterator([]*arohcpv1alpha1.ProvisionShard{shard}, nil),
				)
				return mockCS, databasetesting.NewMockDBClient(), &errorManagementClusterLister{err: fmt.Errorf("lister internal error")}
			},
			expectedErrorSubstr: "lister internal error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := utils.ContextWithLogger(t.Context(), testr.New(t))
			ctrl := gomock.NewController(t)
			cs, dbClient, lister := tt.setup(t, ctrl)

			c := &managementClusterSyncController{
				name:                    "test",
				clusterServiceClient:    cs,
				dbClient:                dbClient,
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
				tt.validate(t, ctx, dbClient)
			}
		})
	}
}
