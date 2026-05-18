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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/fleet"
	"github.com/Azure/ARO-HCP/internal/database"
	dblisters "github.com/Azure/ARO-HCP/internal/database/listers"
	"github.com/Azure/ARO-HCP/internal/database/listertesting"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	testSubscriptionID                                       = "00000000-0000-0000-0000-000000000000"
	testResourceGroup                                        = "rg"
	testDNSResourceGroup                                     = "dns-rg"
	testDNSZone                                              = "test.example.com"
	testHostedClustersSecretsKeyVaultURL                     = "https://cx-kv.vault.azure.net/"
	testHostedClustersManagedIdentitiesKeyVaultURL           = "https://mi-kv.vault.azure.net/"
	testHostedClustersSecretsKeyVaultManagedIdentityClientID = "c2bde1aa-d904-48cd-a728-9de33e3ddca9"
	testMaestroRestURL                                       = "http://maestro.maestro.svc.cluster.local:8000"
	testMaestroGRPCURL                                       = "maestro-grpc.maestro.svc.cluster.local:8090"

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

var managementClusterCmpOptions = append(
	api.CmpDiffOptions,
	cmpopts.IgnoreFields(fleet.ManagementCluster{}, "CosmosMetadata", "ResourceID"),
	cmpopts.IgnoreFields(metav1.Condition{}, "LastTransitionTime"),
)

func assertManagementClusterEqual(t *testing.T, expected, got *fleet.ManagementCluster) {
	t.Helper()
	if diff := cmp.Diff(expected, got, managementClusterCmpOptions...); diff != "" {
		t.Errorf("ManagementCluster mismatch (-expected +got):\n%s", diff)
	}
}

func testProvisionShardHREF(shardID string) string {
	return "/api/aro_hcp/v1alpha1/provision_shards/" + shardID
}

func buildExpectedManagementCluster(t *testing.T, shardID, consumerName, aksName string) *fleet.ManagementCluster {
	t.Helper()
	aksResourceID := api.Must(azcorearm.ParseResourceID(testAKSResourceIDString(aksName)))
	publicDNSZoneResourceID := api.Must(azcorearm.ParseResourceID(testPublicDNSZoneResourceIDString()))
	return &fleet.ManagementCluster{
		Spec: fleet.ManagementClusterSpec{
			SchedulingPolicy: fleet.ManagementClusterSchedulingPolicySchedulable,
		},
		Status: fleet.ManagementClusterStatus{
			AKSResourceID:                                        aksResourceID,
			PublicDNSZoneResourceID:                              publicDNSZoneResourceID,
			HostedClustersSecretsKeyVaultURL:                     testHostedClustersSecretsKeyVaultURL,
			HostedClustersManagedIdentitiesKeyVaultURL:           testHostedClustersManagedIdentitiesKeyVaultURL,
			HostedClustersSecretsKeyVaultManagedIdentityClientID: testHostedClustersSecretsKeyVaultManagedIdentityClientID,
			ClusterServiceProvisionShardID:                       ptr.To(api.Must(api.NewInternalID(testProvisionShardHREF(shardID)))),
			MaestroConsumerName:                                  consumerName,
			MaestroRESTAPIURL:                                    testMaestroRestURL,
			MaestroGRPCTarget:                                    testMaestroGRPCURL,
			Conditions: []metav1.Condition{
				{
					Type:   string(fleet.ManagementClusterConditionReady),
					Status: metav1.ConditionTrue,
					Reason: string(fleet.ManagementClusterConditionReasonProvisionShardActive),
				},
			},
		},
	}
}

func getManagementCluster(t *testing.T, ctx context.Context, client database.FleetDBClient, stampIdentifier string) *fleet.ManagementCluster {
	t.Helper()
	mc, err := client.Stamps().ManagementClusters(stampIdentifier).Get(ctx, fleet.ManagementClusterResourceName)
	require.NoError(t, err)
	return mc
}

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
			CxSecretsKeyVaultUrl(testHostedClustersSecretsKeyVaultURL).
			CxManagedIdentitiesKeyVaultUrl(testHostedClustersManagedIdentitiesKeyVaultURL).
			CxSecretsKeyVaultManagedIdentityClientId(testHostedClustersSecretsKeyVaultManagedIdentityClientID),
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

type errorManagementClusterLister struct {
	listertesting.SliceManagementClusterLister
	err error
}

func (e *errorManagementClusterLister) Get(_ context.Context, _ string) (*fleet.ManagementCluster, error) {
	return nil, e.err
}

func TestSyncOnce(t *testing.T) {
	tests := []struct {
		name                string
		setup               func(t *testing.T, ctrl *gomock.Controller) (ocm.ClusterServiceClientSpec, *databasetesting.MockFleetDBClient, dblisters.StampLister, dblisters.ManagementClusterLister)
		expectedErrorSubstr string
		validate            func(t *testing.T, ctx context.Context, client *databasetesting.MockFleetDBClient)
	}{
		{
			name: "no shards syncs successfully",
			setup: func(t *testing.T, ctrl *gomock.Controller) (ocm.ClusterServiceClientSpec, *databasetesting.MockFleetDBClient, dblisters.StampLister, dblisters.ManagementClusterLister) {
				t.Helper()
				mockCS := ocm.NewMockClusterServiceClientSpec(ctrl)
				mockCS.EXPECT().ListProvisionShards().Return(ocm.NewSimpleProvisionShardListIterator(nil, nil))
				return mockCS, databasetesting.NewMockFleetDBClient(), &listertesting.SliceStampLister{}, &listertesting.SliceManagementClusterLister{}
			},
		},
		{
			name: "CS list error is propagated",
			setup: func(t *testing.T, ctrl *gomock.Controller) (ocm.ClusterServiceClientSpec, *databasetesting.MockFleetDBClient, dblisters.StampLister, dblisters.ManagementClusterLister) {
				t.Helper()
				mockCS := ocm.NewMockClusterServiceClientSpec(ctrl)
				mockCS.EXPECT().ListProvisionShards().Return(ocm.NewSimpleProvisionShardListIterator(nil, fmt.Errorf("list failed")))
				return mockCS, databasetesting.NewMockFleetDBClient(), &listertesting.SliceStampLister{}, &listertesting.SliceManagementClusterLister{}
			},
			expectedErrorSubstr: "list failed",
		},
		{
			name: "new management cluster is created",
			setup: func(t *testing.T, ctrl *gomock.Controller) (ocm.ClusterServiceClientSpec, *databasetesting.MockFleetDBClient, dblisters.StampLister, dblisters.ManagementClusterLister) {
				t.Helper()
				mockCS := ocm.NewMockClusterServiceClientSpec(ctrl)
				shard := buildTestProvisionShard(t, testShardID, "test-consumer", "test-westus3-mgmt-1", "active")
				mockCS.EXPECT().ListProvisionShards().Return(
					ocm.NewSimpleProvisionShardListIterator([]*arohcpv1alpha1.ProvisionShard{shard}, nil),
				)
				return mockCS, databasetesting.NewMockFleetDBClient(), &listertesting.SliceStampLister{}, &listertesting.SliceManagementClusterLister{}
			},
			validate: func(t *testing.T, ctx context.Context, client *databasetesting.MockFleetDBClient) {
				t.Helper()
				doc := getManagementCluster(t, ctx, client, "1")
				assert.Equal(t, fleet.ManagementClusterResourceName, doc.ResourceID.Name, "resource name should be 'default'")
				assert.Equal(t, "1", doc.ResourceID.Parent.Name, "parent name should be the stamp identifier")
				expected := buildExpectedManagementCluster(t, testShardID, "test-consumer", "test-westus3-mgmt-1")
				assertManagementClusterEqual(t, expected, doc)
			},
		},
		{
			name: "existing management cluster is updated when SchedulingPolicy changed",
			setup: func(t *testing.T, ctrl *gomock.Controller) (ocm.ClusterServiceClientSpec, *databasetesting.MockFleetDBClient, dblisters.StampLister, dblisters.ManagementClusterLister) {
				t.Helper()
				mockCS := ocm.NewMockClusterServiceClientSpec(ctrl)
				fleetClient := databasetesting.NewMockFleetDBClient()

				shard := buildTestProvisionShard(t, testShardID, "test-consumer", "test-westus3-mgmt-1", "active")
				existing, err := ocm.ConvertCSManagementClusterToInternal(shard)
				require.NoError(t, err)
				existing.Spec.SchedulingPolicy = fleet.ManagementClusterSchedulingPolicyUnschedulable
				_, err = fleetClient.Stamps().ManagementClusters("1").Create(t.Context(), existing, nil)
				require.NoError(t, err)

				mockCS.EXPECT().ListProvisionShards().Return(
					ocm.NewSimpleProvisionShardListIterator([]*arohcpv1alpha1.ProvisionShard{shard}, nil),
				)
				return mockCS, fleetClient, &listertesting.SliceStampLister{}, &listertesting.SliceManagementClusterLister{ManagementClusters: []*fleet.ManagementCluster{existing}}
			},
			validate: func(t *testing.T, ctx context.Context, client *databasetesting.MockFleetDBClient) {
				t.Helper()
				doc := getManagementCluster(t, ctx, client, "1")
				expected := buildExpectedManagementCluster(t, testShardID, "test-consumer", "test-westus3-mgmt-1")
				assertManagementClusterEqual(t, expected, doc)
			},
		},
		{
			name: "unchanged management cluster is not replaced",
			setup: func(t *testing.T, ctrl *gomock.Controller) (ocm.ClusterServiceClientSpec, *databasetesting.MockFleetDBClient, dblisters.StampLister, dblisters.ManagementClusterLister) {
				t.Helper()
				mockCS := ocm.NewMockClusterServiceClientSpec(ctrl)
				fleetClient := databasetesting.NewMockFleetDBClient()

				shard := buildTestProvisionShard(t, testShardID, "test-consumer", "test-westus3-mgmt-1", "active")
				existing, err := ocm.ConvertCSManagementClusterToInternal(shard)
				require.NoError(t, err)

				mockCS.EXPECT().ListProvisionShards().Return(
					ocm.NewSimpleProvisionShardListIterator([]*arohcpv1alpha1.ProvisionShard{shard}, nil),
				)
				return mockCS, fleetClient, &listertesting.SliceStampLister{}, &listertesting.SliceManagementClusterLister{ManagementClusters: []*fleet.ManagementCluster{existing}}
			},
		},
		{
			name: "conversion error is collected and reported",
			setup: func(t *testing.T, ctrl *gomock.Controller) (ocm.ClusterServiceClientSpec, *databasetesting.MockFleetDBClient, dblisters.StampLister, dblisters.ManagementClusterLister) {
				t.Helper()
				mockCS := ocm.NewMockClusterServiceClientSpec(ctrl)
				badShard, err := arohcpv1alpha1.NewProvisionShard().
					ID("bad-shard").
					AzureShard(arohcpv1alpha1.NewAzureShard().
						AksManagementClusterResourceId(testAKSResourceIDString("test-westus3-mgmt-bad")).
						PublicDnsZoneResourceId(testPublicDNSZoneResourceIDString()).
						CxSecretsKeyVaultUrl(testHostedClustersSecretsKeyVaultURL).
						CxManagedIdentitiesKeyVaultUrl(testHostedClustersManagedIdentitiesKeyVaultURL).
						CxSecretsKeyVaultManagedIdentityClientId(testHostedClustersSecretsKeyVaultManagedIdentityClientID),
					).
					Build()
				require.NoError(t, err)
				mockCS.EXPECT().ListProvisionShards().Return(
					ocm.NewSimpleProvisionShardListIterator([]*arohcpv1alpha1.ProvisionShard{badShard}, nil),
				)
				return mockCS, databasetesting.NewMockFleetDBClient(), &listertesting.SliceStampLister{}, &listertesting.SliceManagementClusterLister{}
			},
			expectedErrorSubstr: "provision shard has empty HREF",
		},
		{
			name: "multiple shards are all created",
			setup: func(t *testing.T, ctrl *gomock.Controller) (ocm.ClusterServiceClientSpec, *databasetesting.MockFleetDBClient, dblisters.StampLister, dblisters.ManagementClusterLister) {
				t.Helper()
				mockCS := ocm.NewMockClusterServiceClientSpec(ctrl)

				shard1 := buildTestProvisionShard(t, testShardID2, "consumer-1", "test-westus3-mgmt-1", "active")
				shard2 := buildTestProvisionShard(t, testShardID3, "consumer-2", "test-eastus-mgmt-2", "active")

				mockCS.EXPECT().ListProvisionShards().Return(
					ocm.NewSimpleProvisionShardListIterator([]*arohcpv1alpha1.ProvisionShard{shard1, shard2}, nil),
				)
				return mockCS, databasetesting.NewMockFleetDBClient(), &listertesting.SliceStampLister{}, &listertesting.SliceManagementClusterLister{}
			},
			validate: func(t *testing.T, ctx context.Context, client *databasetesting.MockFleetDBClient) {
				t.Helper()
				doc1 := getManagementCluster(t, ctx, client, "1")
				expected1 := buildExpectedManagementCluster(t, testShardID2, "consumer-1", "test-westus3-mgmt-1")
				assertManagementClusterEqual(t, expected1, doc1)

				doc2 := getManagementCluster(t, ctx, client, "2")
				expected2 := buildExpectedManagementCluster(t, testShardID3, "consumer-2", "test-eastus-mgmt-2")
				assertManagementClusterEqual(t, expected2, doc2)
			},
		},
		{
			name: "existing cluster condition transitions from Ready=False to Ready=True",
			setup: func(t *testing.T, ctrl *gomock.Controller) (ocm.ClusterServiceClientSpec, *databasetesting.MockFleetDBClient, dblisters.StampLister, dblisters.ManagementClusterLister) {
				t.Helper()
				mockCS := ocm.NewMockClusterServiceClientSpec(ctrl)
				fleetClient := databasetesting.NewMockFleetDBClient()

				maintenanceShard := buildTestProvisionShard(t, testShardID, "test-consumer", "test-westus3-mgmt-1", "maintenance")
				existing, err := ocm.ConvertCSManagementClusterToInternal(maintenanceShard)
				require.NoError(t, err)
				_, err = fleetClient.Stamps().ManagementClusters("1").Create(t.Context(), existing, nil)
				require.NoError(t, err)

				activeShard := buildTestProvisionShard(t, testShardID, "test-consumer", "test-westus3-mgmt-1", "active")
				mockCS.EXPECT().ListProvisionShards().Return(
					ocm.NewSimpleProvisionShardListIterator([]*arohcpv1alpha1.ProvisionShard{activeShard}, nil),
				)
				return mockCS, fleetClient, &listertesting.SliceStampLister{}, &listertesting.SliceManagementClusterLister{ManagementClusters: []*fleet.ManagementCluster{existing}}
			},
			validate: func(t *testing.T, ctx context.Context, client *databasetesting.MockFleetDBClient) {
				t.Helper()
				doc := getManagementCluster(t, ctx, client, "1")
				assert.Equal(t, fleet.ManagementClusterSchedulingPolicySchedulable, doc.Spec.SchedulingPolicy, "should be schedulable")
				var readyCond *metav1.Condition
				for i := range doc.Status.Conditions {
					if doc.Status.Conditions[i].Type == string(fleet.ManagementClusterConditionReady) {
						readyCond = &doc.Status.Conditions[i]
						break
					}
				}
				require.NotNil(t, readyCond, "Ready condition must exist")
				assert.Equal(t, metav1.ConditionTrue, readyCond.Status, "Ready condition should be True")
				assert.Equal(t, string(fleet.ManagementClusterConditionReasonProvisionShardActive), readyCond.Reason)
			},
		},
		{
			name: "lister Get non-404 error is collected and reported",
			setup: func(t *testing.T, ctrl *gomock.Controller) (ocm.ClusterServiceClientSpec, *databasetesting.MockFleetDBClient, dblisters.StampLister, dblisters.ManagementClusterLister) {
				t.Helper()
				mockCS := ocm.NewMockClusterServiceClientSpec(ctrl)
				shard := buildTestProvisionShard(t, testShardID, "test-consumer", "test-westus3-mgmt-1", "active")
				mockCS.EXPECT().ListProvisionShards().Return(
					ocm.NewSimpleProvisionShardListIterator([]*arohcpv1alpha1.ProvisionShard{shard}, nil),
				)
				return mockCS, databasetesting.NewMockFleetDBClient(), &listertesting.SliceStampLister{}, &errorManagementClusterLister{err: fmt.Errorf("lister internal error")}
			},
			expectedErrorSubstr: "lister internal error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := utils.ContextWithLogger(t.Context(), testr.New(t))
			ctrl := gomock.NewController(t)
			cs, fleetClient, sLister, mcLister := tt.setup(t, ctrl)

			c := &managementClusterMigrationController{
				name:                    "test",
				clusterServiceClient:    cs,
				fleetDBClient:           fleetClient,
				stampLister:             sLister,
				managementClusterLister: mcLister,
			}

			err := c.SyncOnce(ctx, nil)
			if len(tt.expectedErrorSubstr) > 0 {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedErrorSubstr)
			} else {
				require.NoError(t, err)
			}
			if tt.validate != nil {
				tt.validate(t, ctx, fleetClient)
			}
		})
	}
}
