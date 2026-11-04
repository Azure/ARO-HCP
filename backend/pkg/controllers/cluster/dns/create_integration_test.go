// Copyright 2026 Microsoft Corporation
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package dns_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/blang/semver/v4"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"
	clocktesting "k8s.io/utils/clock/testing"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/cluster/creation"
	clusterdns "github.com/Azure/ARO-HCP/backend/pkg/controllers/cluster/dns"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/validationutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/fleetapihelpers"
	"github.com/Azure/ARO-HCP/internal/apitesting/coreapitesting"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/billingcosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/fleetcosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/informers/fleetinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/fleetlisters"
	"github.com/Azure/ARO-HCP/internal/ocm"
)

func TestCreatePersistsClaimedDNSBeforeDispatch(t *testing.T) {
	for _, prefix := range []string{"", "payments"} {
		t.Run("prefix="+prefix, func(t *testing.T) {
			for _, loseResponse := range []bool{false, true} {
				t.Run(fmt.Sprintf("lost-response=%t", loseResponse), func(t *testing.T) {
					ctx := context.Background()
					pending := metadataapi.Must(metadataapi.NewInternalID(testClusterServiceIDStr))
					cluster := coreapitesting.MinimumValidClusterTestCase()
					id := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName))
					cluster.ID = id
					cluster.Name = testClusterName
					cluster.Type = id.ResourceType.String()
					cluster.CosmosMetadata = coreapi.CosmosMetadata{ResourceID: id, PartitionKey: testSubscriptionID}
					cluster.CustomerProperties.DNS.BaseDomainPrefix = prefix
					cluster.ServiceProviderProperties.ClusterServiceID = nil
					cluster.ServiceProviderProperties.PendingClusterServiceID = &pending
					managementID := metadataapi.Must(fleetapihelpers.ToManagementClusterResourceID("1"))
					spc := &coreapi.ServiceProviderCluster{CosmosMetadata: coreapi.CosmosMetadata{ResourceID: metadataapi.Must(azcorearm.ParseResourceID(id.String() + "/serviceProviderClusters/default")), PartitionKey: testSubscriptionID}}
					spc.Spec.ManagementClusterResourceID = managementID
					spc.Spec.ControlPlaneVersion.DesiredVersion = ptr.To(semver.MustParse("4.20.0"))
					// Satisfy the policy preflight so this test exercises DNS allocation and dispatch.
					meta.SetStatusCondition(&spc.Status.Validations, metav1.Condition{
						Type:   validationutils.StorageAccountNetworkAccessValidationName,
						Status: metav1.ConditionTrue,
						Reason: "AsExpected",
					})
					subscriptionID := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID))
					subscription := &coreapi.Subscription{CosmosMetadata: coreapi.CosmosMetadata{ResourceID: subscriptionID, PartitionKey: testSubscriptionID}, Properties: &coreapi.SubscriptionProperties{TenantId: ptr.To("11111111-1111-1111-1111-111111111111")}}
					management := &fleetapi.ManagementCluster{CosmosMetadata: coreapi.CosmosMetadata{ResourceID: managementID, PartitionKey: "1"}, Status: fleetapi.ManagementClusterStatus{PublicDNSZoneResourceID: metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/dns/providers/Microsoft.Network/dnsZones/example.com")), ClusterServiceProvisionShardID: ptr.To(metadataapi.Must(metadataapi.NewInternalID("/api/aro_hcp/v1alpha1/provision_shards/" + testProvisionShardID)))}}
					db, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, spc, subscription})
					require.NoError(t, err)
					fleet := fleetcosmosstoragetesting.NewMockFleetDBClient()
					indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
					claims := fleetlisters.NewDNSClaimLister(indexer)
					client := ocm.NewMockClusterServiceClientSpec(gomock.NewController(t))
					var created *arohcpv1alpha1.Cluster
					client.EXPECT().ListClusters(gomock.Any()).DoAndReturn(func(string) ocm.ClusterListIterator {
						var clusters []*arohcpv1alpha1.Cluster
						if created != nil {
							clusters = append(clusters, created)
						}
						return ocm.NewSimpleClusterListIterator(clusters, nil)
					}).AnyTimes()
					wantPrefix := prefix
					if len(wantPrefix) == 0 {
						wantPrefix = "cluster"
					}
					dispatches := 0
					client.EXPECT().PostCluster(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, builder *arohcpv1alpha1.ClusterBuilder) (*arohcpv1alpha1.Cluster, error) {
						dispatches++
						// Inspect the database at the instant of dispatch, not only afterward.
						stored, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).Get(ctx, testClusterName)
						require.NoError(t, err)
						request, err := builder.HREF(testClusterServiceIDStr).Build()
						require.NoError(t, err)
						require.Equal(t, wantPrefix, request.DomainPrefix())
						require.Equal(t, stored.CustomerProperties.DNS.BaseDomainPrefix, request.DomainPrefix())
						require.Equal(t, stored.ServiceProviderProperties.DNS.BaseDomain, request.DNS().BaseDomain())
						require.Regexp(t, `^[a-z0-9]{5}\.example\.com$`, request.DNS().BaseDomain())
						provider, err := db.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
						require.NoError(t, err)
						require.Equal(t, stored.ServiceProviderProperties.DNS.BaseDomain, provider.Spec.DesiredDNSBaseDomain)
						claim, err := claims.Get(ctx, request.DNS().BaseDomain())
						require.NoError(t, err)
						require.Equal(t, cluster.ServiceProviderProperties.ClusterUID, claim.ClusterUUID)
						require.False(t, claim.CreatedAt.IsZero())
						require.Equal(t, testProvisionShardID, request.ProvisionShardID())
						created = request
						if loseResponse {
							return nil, fmt.Errorf("response lost after CS accepted create")
						}
						return request, nil
					}).Times(1)
					for i := 0; i < 6; i++ {
						items, err := fleet.GlobalListers().DNSClaims().List(ctx, nil)
						require.NoError(t, err)
						for _, item := range items.Items(ctx) {
							require.NoError(t, indexer.Add(item))
						}
						require.NoError(t, items.GetError())
						// Reconstruct on every iteration: no in-memory allocation state survives.
						ci := coreinformers.NewBackendInformers(ctx, db.ResourcesGlobalListers(), db, billingcosmosstoragetesting.NewMockBillingDBClient().BillingGlobalListers())
						fi := fleetinformers.NewFleetInformers(ctx, fleet.GlobalListers(), fleet)
						clusterInformer, _ := ci.Clusters()
						storedCluster, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).Get(ctx, testClusterName)
						require.NoError(t, err)
						require.NoError(t, clusterInformer.GetIndexer().Add(storedCluster))
						providerInformer, _ := ci.ServiceProviderClusters()
						storedProvider, err := db.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
						require.NoError(t, err)
						require.NoError(t, providerInformer.GetIndexer().Add(storedProvider))
						subscriptionInformer, _ := ci.Subscriptions()
						require.NoError(t, subscriptionInformer.GetIndexer().Add(subscription))
						managementInformer, _ := fi.ManagementClusters()
						require.NoError(t, managementInformer.GetIndexer().Add(management))
						claimInformer, _ := fi.DNSClaims()
						for _, obj := range indexer.List() {
							require.NoError(t, claimInformer.GetIndexer().Add(obj))
						}
						allocator := clusterdns.NewDNSAllocationController(db, fleet, ci, fi, client, clocktesting.NewFakePassiveClock(time.Unix(10000, 0)))
						require.NoError(t, allocator.SyncOnce(ctx, controllerutils.HCPClusterKey{SubscriptionID: testSubscriptionID, ResourceGroupName: testResourceGroupName, HCPClusterName: testClusterName}))
						storedCluster, err = db.HCPClusters(testSubscriptionID, testResourceGroupName).Get(ctx, testClusterName)
						require.NoError(t, err)
						require.NoError(t, clusterInformer.GetIndexer().Update(storedCluster))
						_, managementLister := fi.ManagementClusters()
						syncer := creation.NewClusterClusterServiceCreateController(db, client, managementLister, ci, false)
						err = syncer.SyncOnce(ctx, controllerutils.HCPClusterKey{SubscriptionID: testSubscriptionID, ResourceGroupName: testResourceGroupName, HCPClusterName: testClusterName})
						if err != nil {
							require.True(t, loseResponse)
							require.Contains(t, err.Error(), "response lost")
						}
						if created != nil && !loseResponse {
							stored, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).Get(ctx, testClusterName)
							require.NoError(t, err)
							require.NotNil(t, stored.ServiceProviderProperties.ClusterServiceID, "do not defer successful POST adoption")
						}
					}
					require.Equal(t, 1, dispatches)
					stored, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).Get(ctx, testClusterName)
					require.NoError(t, err)
					require.NotNil(t, stored.ServiceProviderProperties.ClusterServiceID)
					require.Nil(t, stored.ServiceProviderProperties.PendingClusterServiceID)
					require.Equal(t, created.DNS().BaseDomain(), stored.ServiceProviderProperties.DNS.BaseDomain)
				})
			}
		})
	}
}

const (
	testSubscriptionID      = "00000000-0000-0000-0000-000000000000"
	testResourceGroupName   = "test-rg"
	testClusterName         = "test-cluster"
	testClusterServiceIDStr = "/api/aro_hcp/v1alpha1/clusters/abc123"
	testProvisionShardID    = "shard-abc123"
)
