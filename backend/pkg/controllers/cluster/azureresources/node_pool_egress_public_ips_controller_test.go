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

package azureresources

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armdeploymentstacks/v2"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/azureresourcesapi"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/azureresourcesapihelpers"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/azureresourcescosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/azureresourcescosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listers/azureresourceslisters"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type nodePoolEgressDesireLister struct {
	azureresourceslisters.DeploymentStackDesireLister
	desire *azureresourcesapi.DeploymentStackDesire
	err    error
}

func (lister *nodePoolEgressDesireLister) GetForCluster(context.Context, string, string, string, string) (*azureresourcesapi.DeploymentStackDesire, error) {
	if lister.err != nil {
		return nil, lister.err
	}
	if lister.desire == nil {
		return nil, cosmosstorageutils.NewNotFoundError()
	}
	return lister.desire, nil
}

type nodePoolEgressFixture struct {
	ctx          context.Context
	key          controllerutils.HCPClusterKey
	syncer       *nodePoolEgressPublicIPsSyncer
	cluster      *coreapi.Cluster
	spc          *coreapi.ServiceProviderCluster
	subscription *coreapi.Subscription
	lister       *nodePoolEgressDesireLister
	db           *azureresourcescosmosstoragetesting.MockAzureResourcesDBClient
}

func newNodePoolEgressFixture(t *testing.T) *nodePoolEgressFixture {
	t.Helper()
	cluster := newTestCluster(false)
	cluster.Location = "eastus"
	cluster.ServiceProviderProperties.ClusterServiceID = ptr.To(metadataapi.Must(metadataapi.NewInternalID("/api/aro_hcp/v1alpha1/clusters/cs-cluster")))
	spc := newTestServiceProviderCluster(coreapi.AzureReference{AzureResource: testManagedResourceGroupID(t)})
	spc.Status.ServingCABundle = "unrelated-ca"
	subscription := newTestSubscription(ptr.To(testTenantID))
	lister := &nodePoolEgressDesireLister{}
	db := azureresourcescosmosstoragetesting.NewMockAzureResourcesDBClient()
	resourcesDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(t.Context(), []any{cluster, spc})
	require.NoError(t, err)
	spc, err = resourcesDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(t.Context(), coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	return &nodePoolEgressFixture{
		ctx:     utils.ContextWithLogger(context.Background(), testr.New(t)),
		key:     controllerutils.HCPClusterKey{SubscriptionID: testSubscriptionID, ResourceGroupName: testResourceGroupName, HCPClusterName: testClusterName},
		cluster: cluster, spc: spc, subscription: subscription, lister: lister, db: db,
		syncer: &nodePoolEgressPublicIPsSyncer{
			resourcesDBClient:            resourcesDB,
			clusterLister:                &corelistertesting.SliceClusterLister{Clusters: []*coreapi.Cluster{cluster}},
			serviceProviderClusterLister: &corelistertesting.SliceServiceProviderClusterLister{ServiceProviderClusters: []*coreapi.ServiceProviderCluster{spc}},
			subscriptionLister:           &corelistertesting.SliceSubscriptionLister{Subscriptions: []*coreapi.Subscription{subscription}},
			deploymentStackDesireLister:  lister,
			azureResourcesDBClient:       db,
		},
	}
}

func (fixture *nodePoolEgressFixture) refresh(t *testing.T) *azureresourcesapi.DeploymentStackDesire {
	t.Helper()
	crud, err := fixture.db.DeploymentStackDesiresForCluster(fixture.key.SubscriptionID, fixture.key.ResourceGroupName, fixture.key.HCPClusterName)
	require.NoError(t, err)
	desire, err := crud.Get(fixture.ctx, azureresourcesapi.NodePoolEgressPublicIPsDeploymentStackDesireName)
	require.NoError(t, err)
	fixture.lister.desire = desire
	fixture.refreshSPC(t)
	return desire
}

func (fixture *nodePoolEgressFixture) refreshSPC(t *testing.T) {
	t.Helper()
	stored, err := fixture.syncer.resourcesDBClient.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(fixture.ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	fixture.spc = stored
	fixture.syncer.serviceProviderClusterLister = &corelistertesting.SliceServiceProviderClusterLister{ServiceProviderClusters: []*coreapi.ServiceProviderCluster{stored}}
}

func TestNodePoolEgressPublicIPsTemplate(t *testing.T) {
	fixture := newNodePoolEgressFixture(t)
	require.NoError(t, fixture.syncer.SyncOnce(fixture.ctx, fixture.key))
	desire := fixture.refresh(t)
	assert.Equal(t, "node-pool-egress-public-ips", desire.ResourceID.Name)
	assert.Equal(t, testManagedResourceGroupID(t).String()+"/providers/Microsoft.Resources/deploymentStacks/egress-public-ips", desire.Spec.TargetResourceID.String())
	assert.Equal(t, testSubscriptionID, desire.PartitionKey)
	assert.Equal(t, azureresourcesapi.DeploymentStackDesireActionTypeApply, desire.Spec.Action)
	assert.Equal(t, azureresourcesapi.DeploymentStackDesireAuthenticationTypeFPA, desire.Spec.Authentication.Type)
	require.NotNil(t, desire.Spec.Authentication.FirstPartyApplication)
	assert.Equal(t, testTenantID, desire.Spec.Authentication.FirstPartyApplication.TenantID)
	assert.Equal(t, armdeploymentstacks.UnmanageActionResourceModeDetach, *desire.Spec.Stack.Properties.ActionOnUnmanage.Resources)
	assert.Equal(t, armdeploymentstacks.DenySettingsModeNone, *desire.Spec.Stack.Properties.DenySettings.Mode)
	data, err := json.Marshal(desire.Spec.Stack.Properties.Template)
	require.NoError(t, err)
	var template struct {
		Resources []map[string]any `json:"resources"`
	}
	require.NoError(t, json.Unmarshal(data, &template))
	require.Len(t, template.Resources, 9)
	for index, resource := range template.Resources {
		assert.Equal(t, map[string]any{
			"type":       "Microsoft.Network/publicIPAddresses",
			"apiVersion": "2024-05-01",
			"name":       fmt.Sprintf("cs-cluster-%d", index),
			"location":   "eastus",
			"sku":        map[string]any{"name": "Standard"},
			"properties": map[string]any{"publicIPAddressVersion": "IPv4", "publicIPAllocationMethod": "Static", "idleTimeoutInMinutes": float64(4)},
		}, resource)
	}
	assert.NotContains(t, string(data), "loadBalancers")
	assert.NotContains(t, string(data), "ipConfiguration")
	assert.NotContains(t, string(data), "ipAddress\"")
	assert.Empty(t, desire.Status)
	before := fixture.db.GetAllDocuments()
	require.NoError(t, fixture.syncer.SyncOnce(fixture.ctx, fixture.key))
	assert.Equal(t, before, fixture.db.GetAllDocuments(), "unchanged intent must not write")
}

func TestNodePoolEgressPublicIPsNeedsWork(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*coreapi.Cluster)
		want   bool
	}{
		{"legacy outbound", func(cluster *coreapi.Cluster) { cluster.CustomerProperties.Platform.OutboundType = "" }, true},
		{"load balancer", func(cluster *coreapi.Cluster) {
			cluster.CustomerProperties.Platform.OutboundType = metadataapi.OutboundTypeLoadBalancer
		}, true},
		{"UDR", func(cluster *coreapi.Cluster) {
			cluster.CustomerProperties.Platform.OutboundType = metadataapi.OutboundType("UserDefinedRouting")
		}, false},
		{"deleting", func(cluster *coreapi.Cluster) {
			cluster.ServiceProviderProperties.DeletionTimestamp = ptr.To(metav1.Now())
		}, false},
		{"missing resource ID", func(cluster *coreapi.Cluster) { cluster.ID = nil }, false},
		{"missing CS ID", func(cluster *coreapi.Cluster) { cluster.ServiceProviderProperties.ClusterServiceID = nil }, false},
		{"empty CS ID", func(cluster *coreapi.Cluster) {
			cluster.ServiceProviderProperties.ClusterServiceID = new(metadataapi.InternalID)
		}, false},
		{"blank location", func(cluster *coreapi.Cluster) { cluster.Location = " " }, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newNodePoolEgressFixture(t)
			test.change(fixture.cluster)
			require.Equal(t, test.want, fixture.syncer.needsWork(fixture.cluster))
		})
	}
}

func TestNodePoolEgressPublicIPsPrerequisites(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*nodePoolEgressFixture)
	}{
		{"missing cluster", func(f *nodePoolEgressFixture) { f.syncer.clusterLister = &corelistertesting.SliceClusterLister{} }},
		{"UDR", func(f *nodePoolEgressFixture) {
			f.cluster.CustomerProperties.Platform.OutboundType = metadataapi.OutboundType("UserDefinedRouting")
		}},
		{"missing resource ID", func(f *nodePoolEgressFixture) { f.cluster.ID = nil }},
		{"missing CS ID", func(f *nodePoolEgressFixture) { f.cluster.ServiceProviderProperties.ClusterServiceID = nil }},
		{"empty CS ID", func(f *nodePoolEgressFixture) {
			f.cluster.ServiceProviderProperties.ClusterServiceID = new(metadataapi.InternalID)
		}},
		{"missing location", func(f *nodePoolEgressFixture) { f.cluster.Location = "" }},
		{"missing SPC", func(f *nodePoolEgressFixture) {
			f.syncer.serviceProviderClusterLister = &corelistertesting.SliceServiceProviderClusterLister{}
		}},
		{"pending MRG only", func(f *nodePoolEgressFixture) {
			f.spc.Status.AzureResources.ManagedResourceGroup = coreapi.AzureReference{PendingAzureResource: testManagedResourceGroupID(t)}
		}},
		{"missing subscription", func(f *nodePoolEgressFixture) {
			f.syncer.subscriptionLister = &corelistertesting.SliceSubscriptionLister{}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newNodePoolEgressFixture(t)
			test.change(fixture)
			require.NoError(t, fixture.syncer.SyncOnce(fixture.ctx, fixture.key))
			assert.Empty(t, fixture.db.GetAllDocuments())
		})
	}
	t.Run("cluster prerequisite becomes available on retry", func(t *testing.T) {
		fixture := newNodePoolEgressFixture(t)
		fixture.cluster.Location = ""
		require.NoError(t, fixture.syncer.SyncOnce(fixture.ctx, fixture.key))
		require.Empty(t, fixture.db.GetAllDocuments())
		fixture.cluster.Location = "eastus"
		require.NoError(t, fixture.syncer.SyncOnce(fixture.ctx, fixture.key))
		fixture.refresh(t)
	})
	t.Run("prerequisite becomes available on retry", func(t *testing.T) {
		fixture := newNodePoolEgressFixture(t)
		fixture.subscription.Properties.TenantId = nil
		require.ErrorContains(t, fixture.syncer.SyncOnce(fixture.ctx, fixture.key), "has no tenant ID")
		fixture.subscription.Properties.TenantId = ptr.To(testTenantID)
		require.NoError(t, fixture.syncer.SyncOnce(fixture.ctx, fixture.key))
		fixture.refresh(t)
	})
	t.Run("confirmed scope mismatch", func(t *testing.T) {
		fixture := newNodePoolEgressFixture(t)
		fixture.cluster.CustomerProperties.Platform.ManagedResourceGroup = "another-mrg"
		require.ErrorContains(t, fixture.syncer.SyncOnce(fixture.ctx, fixture.key), "does not match")
		assert.Empty(t, fixture.db.GetAllDocuments())
	})
}

func TestNodePoolEgressPublicIPsMissingTenant(t *testing.T) {
	for _, change := range []func(*coreapi.Subscription){
		func(subscription *coreapi.Subscription) { subscription.Properties = nil },
		func(subscription *coreapi.Subscription) { subscription.Properties.TenantId = nil },
		func(subscription *coreapi.Subscription) { subscription.Properties.TenantId = ptr.To(" ") },
	} {
		fixture := newNodePoolEgressFixture(t)
		change(fixture.subscription)
		require.ErrorContains(t, fixture.syncer.SyncOnce(fixture.ctx, fixture.key), "has no tenant ID")
		require.Empty(t, fixture.db.GetAllDocuments())
	}
}

func TestNodePoolEgressPublicIPsReservedID(t *testing.T) {
	fixture := newNodePoolEgressFixture(t)
	client := ocm.NewMockClusterServiceClientSpec(gomock.NewController(t))
	client.EXPECT().ListClusters(gomock.Any()).Return(ocm.NewSimpleClusterListIterator(nil, nil))
	fixture.syncer.clustersServiceClient = client
	fixture.cluster.ServiceProviderProperties.PendingClusterServiceID = fixture.cluster.ServiceProviderProperties.ClusterServiceID
	fixture.cluster.ServiceProviderProperties.ClusterServiceID = nil
	require.True(t, fixture.syncer.needsWork(fixture.cluster))
	require.NoError(t, fixture.syncer.SyncOnce(fixture.ctx, fixture.key))
	desire := fixture.refresh(t)
	expected, err := NodePoolEgressPublicIPsDesiredSpec(fixture.cluster, fixture.spc, fixture.subscription)
	require.NoError(t, err)
	require.NotNil(t, expected)
	expectedHash, err := azureresourcesapihelpers.DeploymentStackDesireSpecHash(*expected)
	require.NoError(t, err)
	actualHash, err := azureresourcesapihelpers.DeploymentStackDesireSpecHash(desire.Spec)
	require.NoError(t, err)
	require.Equal(t, expectedHash, actualHash)
	fixture.cluster.ServiceProviderProperties.ClusterServiceID = fixture.cluster.ServiceProviderProperties.PendingClusterServiceID
	fixture.cluster.ServiceProviderProperties.PendingClusterServiceID = nil
	before := fixture.db.GetAllDocuments()
	require.NoError(t, fixture.syncer.SyncOnce(fixture.ctx, fixture.key))
	require.Equal(t, before, fixture.db.GetAllDocuments(), "confirming the reserved ID must not change intent")
}

func TestNodePoolEgressPublicIPsAdoptionBeforeReservation(t *testing.T) {
	for _, scenario := range []string{"adopt", "lookup fails", "conflicting reservation"} {
		t.Run(scenario, func(t *testing.T) {
			fixture := newNodePoolEgressFixture(t)
			if scenario == "conflicting reservation" {
				require.NoError(t, fixture.syncer.SyncOnce(fixture.ctx, fixture.key))
				fixture.refresh(t)
			}
			before := fixture.spc.DeepCopy()
			documents := fixture.db.GetAllDocuments()
			fixture.cluster.ServiceProviderProperties.PendingClusterServiceID = fixture.cluster.ServiceProviderProperties.ClusterServiceID
			fixture.cluster.ServiceProviderProperties.ClusterServiceID = nil
			adoptedID := metadataapi.Must(metadataapi.NewInternalID("/api/aro_hcp/v1alpha1/clusters/adopted-cluster"))
			found, err := arohcpv1alpha1.NewCluster().ID(adoptedID.ID()).HREF(adoptedID.String()).Azure(arohcpv1alpha1.NewAzure().
				SubscriptionID(strings.ToLower(fixture.key.SubscriptionID)).ResourceGroupName(strings.ToLower(fixture.key.ResourceGroupName)).
				ResourceName(strings.ToLower(fixture.key.HCPClusterName)).TenantID(testTenantID).
				ManagedResourceGroupName(fixture.cluster.CustomerProperties.Platform.ManagedResourceGroup)).Build()
			require.NoError(t, err)
			client := ocm.NewMockClusterServiceClientSpec(gomock.NewController(t))
			var lookupErr error
			if scenario == "lookup fails" {
				lookupErr = errors.New("discovery unavailable")
			}
			client.EXPECT().ListClusters(gomock.Any()).Return(ocm.NewSimpleClusterListIterator([]*arohcpv1alpha1.Cluster{found}, lookupErr))
			fixture.syncer.clustersServiceClient = client
			err = fixture.syncer.SyncOnce(fixture.ctx, fixture.key)
			switch scenario {
			case "lookup fails":
				require.ErrorContains(t, err, "discovery unavailable")
			case "conflicting reservation":
				require.ErrorContains(t, err, "reserved node-pool public IP identities conflict")
			default:
				require.NoError(t, err)
			}
			require.Equal(t, documents, fixture.db.GetAllDocuments(), "pending identity must not publish intent before adoption")
			fixture.refreshSPC(t)
			require.Equal(t, before, fixture.spc)
			if scenario == "conflicting reservation" {
				return
			}
			require.Empty(t, fixture.spc.Status.ReservedNodePoolEgressPublicIPResourceIDs)
			fixture.cluster.ServiceProviderProperties.ClusterServiceID = &adoptedID
			fixture.cluster.ServiceProviderProperties.PendingClusterServiceID = nil
			require.NoError(t, fixture.syncer.SyncOnce(fixture.ctx, fixture.key))
			fixture.refresh(t)
			for index, reserved := range fixture.spc.Status.ReservedNodePoolEgressPublicIPResourceIDs {
				require.Equal(t, fmt.Sprintf("adopted-cluster-%d", index), reserved.Name)
			}
		})
	}
}

func TestNodePoolEgressPublicIPsExistingClusterAndStatus(t *testing.T) {
	fixture := newNodePoolEgressFixture(t)
	fixture.cluster.CustomerProperties.Platform.OutboundType = metadataapi.OutboundTypeLoadBalancer
	fixture.cluster.ServiceProviderProperties.ProvisioningState = coreapi.ProvisioningStateSucceeded
	require.NoError(t, fixture.syncer.SyncOnce(fixture.ctx, fixture.key))
	desire := fixture.refresh(t)
	desire.Status = azureresourcesapi.DeploymentStackDesireStatus{
		ObservedSpecHash: "executor-hash",
		ObservedAt:       ptr.To(metav1.NewTime(time.Now())),
		Conditions:       []metav1.Condition{{Type: azureresourcesapi.DeploymentStackDesireConditionTypeSuccessfullyApplied, Status: metav1.ConditionTrue}},
		Stack:            &armdeploymentstacks.DeploymentStack{ID: ptr.To(desire.Spec.TargetResourceID.String())},
	}
	crud, err := fixture.db.DeploymentStackDesiresForCluster(testSubscriptionID, testResourceGroupName, testClusterName)
	require.NoError(t, err)
	_, err = crud.Replace(fixture.ctx, desire, nil)
	require.NoError(t, err)
	previous := fixture.refresh(t).DeepCopy()
	fixture.subscription.Properties.TenantId = ptr.To("updated-tenant")
	require.NoError(t, fixture.syncer.SyncOnce(fixture.ctx, fixture.key))
	updated := fixture.refresh(t)
	assert.Equal(t, "updated-tenant", updated.Spec.Authentication.FirstPartyApplication.TenantID)
	assert.Equal(t, previous.Status, updated.Status, "producer must preserve executor-owned status")
	assert.Equal(t, testTenantID, previous.Spec.Authentication.FirstPartyApplication.TenantID, "cached object must not be mutated")
	assert.Equal(t, previous.Spec.TargetResourceID, updated.Spec.TargetResourceID)
}

func TestNodePoolEgressPublicIPsConflictsAndTarget(t *testing.T) {
	fixture := newNodePoolEgressFixture(t)
	require.NoError(t, fixture.syncer.SyncOnce(fixture.ctx, fixture.key))
	fixture.refreshSPC(t)
	err := fixture.syncer.SyncOnce(fixture.ctx, fixture.key)
	require.True(t, cosmosstorageutils.IsConflictError(err), "stale absent cache must requeue a create conflict: %v", err)
	stale := fixture.refresh(t).DeepCopy()
	fixture.subscription.Properties.TenantId = ptr.To("tenant-two")
	require.NoError(t, fixture.syncer.SyncOnce(fixture.ctx, fixture.key))
	fixture.lister.desire = stale
	err = fixture.syncer.SyncOnce(fixture.ctx, fixture.key)
	require.True(t, cosmosstorageutils.IsPreconditionFailedError(err), "stale replacement must requeue: %v", err)
	fixture.refresh(t)
	require.NoError(t, fixture.syncer.SyncOnce(fixture.ctx, fixture.key))
	fixture.lister.desire.Spec.TargetResourceID = testManagedResourceGroupID(t)
	before := fixture.db.GetAllDocuments()
	require.ErrorContains(t, fixture.syncer.SyncOnce(fixture.ctx, fixture.key), "immutable")
	assert.Equal(t, before, fixture.db.GetAllDocuments())
	fixture.lister.err = errors.New("cache unavailable")
	require.ErrorContains(t, fixture.syncer.SyncOnce(fixture.ctx, fixture.key), "cache unavailable")
}

func TestNodePoolEgressPublicIPsDeletion(t *testing.T) {
	fixture := newNodePoolEgressFixture(t)
	require.NoError(t, fixture.syncer.SyncOnce(fixture.ctx, fixture.key))
	fixture.refreshSPC(t)
	reserved := fixture.spc.DeepCopy()
	fixture.cluster.ServiceProviderProperties.DeletionTimestamp = ptr.To(metav1.Now())
	fixture.cluster.ServiceProviderProperties.ClusterServiceID = nil
	require.False(t, fixture.syncer.needsWork(fixture.cluster), "deletion must run before the apply predicate")
	fixture.syncer.serviceProviderClusterLister = nil
	fixture.syncer.subscriptionLister = nil
	fixture.syncer.deploymentStackDesireLister = nil
	require.NoError(t, fixture.syncer.SyncOnce(fixture.ctx, fixture.key))
	crud, err := fixture.db.DeploymentStackDesiresForCluster(testSubscriptionID, testResourceGroupName, testClusterName)
	require.NoError(t, err)
	_, err = crud.Get(fixture.ctx, azureresourcesapi.NodePoolEgressPublicIPsDeploymentStackDesireName)
	assert.True(t, cosmosstorageutils.IsNotFoundError(err))
	require.NoError(t, fixture.syncer.SyncOnce(fixture.ctx, fixture.key), "cleanup is idempotent")
	fixture.refreshSPC(t)
	require.Equal(t, reserved, fixture.spc, "Cosmos desire cleanup must not remove reserved identities")
	assert.NotNil(t, fixture.spc.Status.AzureResources.ManagedResourceGroup.AzureResource, "existing MRG cleanup remains responsible for Azure deletion")
}

type reservationCheckedAzureDB struct {
	azureresourcescosmosstorage.AzureResourcesDBClient
	beforePublish func() error
}

func (db *reservationCheckedAzureDB) DeploymentStackDesiresForCluster(subscriptionID, resourceGroupName, clusterName string) (cosmosstorageutils.ResourceCRUD[azureresourcesapi.DeploymentStackDesire, *azureresourcesapi.DeploymentStackDesire], error) {
	if err := db.beforePublish(); err != nil {
		return nil, err
	}
	return db.AzureResourcesDBClient.DeploymentStackDesiresForCluster(subscriptionID, resourceGroupName, clusterName)
}

type reservationFailingDB struct {
	corecosmosstorage.ResourcesDBClient
	err error
}

func (db *reservationFailingDB) ServiceProviderClusters(subscriptionID, resourceGroupName, clusterName string) cosmosstorageutils.ResourceCRUD[coreapi.ServiceProviderCluster, *coreapi.ServiceProviderCluster] {
	return &reservationFailingCRUD{ResourceCRUD: db.ResourcesDBClient.ServiceProviderClusters(subscriptionID, resourceGroupName, clusterName), err: db.err}
}

type reservationFailingCRUD struct {
	cosmosstorageutils.ResourceCRUD[coreapi.ServiceProviderCluster, *coreapi.ServiceProviderCluster]
	err error
}

func (crud *reservationFailingCRUD) Replace(context.Context, *coreapi.ServiceProviderCluster, *azcosmos.ItemOptions) (*coreapi.ServiceProviderCluster, error) {
	return nil, crud.err
}

func TestNodePoolEgressPublicIPsReservationOrdering(t *testing.T) {
	fixture := newNodePoolEgressFixture(t)
	cached := fixture.spc
	spcCRUD := fixture.syncer.resourcesDBClient.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName)
	publicationErr := errors.New("publication unavailable")
	publications := 0
	fixture.syncer.azureResourcesDBClient = &reservationCheckedAzureDB{AzureResourcesDBClient: fixture.db, beforePublish: func() error {
		stored, err := spcCRUD.Get(fixture.ctx, coreapi.ServiceProviderClusterResourceName)
		require.NoError(t, err)
		require.Len(t, stored.Status.ReservedNodePoolEgressPublicIPResourceIDs, 9, "reservation must be durable before any desire write")
		publications++
		return publicationErr
	}}
	require.ErrorIs(t, fixture.syncer.SyncOnce(fixture.ctx, fixture.key), publicationErr)
	require.Equal(t, 1, publications)
	require.Empty(t, fixture.db.GetAllDocuments())
	require.Empty(t, cached.Status.ReservedNodePoolEgressPublicIPResourceIDs, "cached SPC must not be mutated")
	fixture.refreshSPC(t)
	reserved := fixture.spc.DeepCopy()
	require.Equal(t, "unrelated-ca", reserved.Status.ServingCABundle)
	copy := reserved.DeepCopy()
	copy.Status.ReservedNodePoolEgressPublicIPResourceIDs[0].Name = "changed-copy"
	require.Equal(t, "cs-cluster-0", reserved.Status.ReservedNodePoolEgressPublicIPResourceIDs[0].Name)
	publicationErr = nil
	require.NoError(t, fixture.syncer.SyncOnce(fixture.ctx, fixture.key))
	desire := fixture.refresh(t)
	before := fixture.db.GetAllDocuments()
	fixture.cluster.ServiceProviderProperties.ClusterServiceID = ptr.To(metadataapi.Must(metadataapi.NewInternalID("/api/aro_hcp/v1alpha1/clusters/changed-id")))
	desire.Status.ObservedSpecHash = "executor-only"
	require.True(t, fixture.syncer.needsWork(fixture.cluster))
	require.NoError(t, fixture.syncer.SyncOnce(fixture.ctx, fixture.key))
	require.Equal(t, before, fixture.db.GetAllDocuments(), "reserved names and unchanged intent must not be rewritten")
	fixture.refreshSPC(t)
	require.Equal(t, reserved, fixture.spc, "executor status changes must not write SPC")
	fixture.subscription.Properties.TenantId = ptr.To("updated-tenant")
	require.NoError(t, fixture.syncer.SyncOnce(fixture.ctx, fixture.key))
	require.Equal(t, "updated-tenant", fixture.refresh(t).Spec.Authentication.FirstPartyApplication.TenantID)
	require.Equal(t, reserved, fixture.spc)
}

func TestNodePoolEgressPublicIPsReservationWriteFailure(t *testing.T) {
	for _, conflict := range []bool{false, true} {
		t.Run(fmt.Sprintf("conflict=%t", conflict), func(t *testing.T) {
			fixture := newNodePoolEgressFixture(t)
			db := fixture.syncer.resourcesDBClient
			if conflict {
				updated := fixture.spc.DeepCopy()
				updated.Status.ServingCABundle = "new-ca"
				_, err := db.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Replace(fixture.ctx, updated, nil)
				require.NoError(t, err)
			} else {
				fixture.syncer.resourcesDBClient = &reservationFailingDB{ResourcesDBClient: db, err: errors.New("reservation unavailable")}
			}
			fixture.syncer.azureResourcesDBClient = &reservationCheckedAzureDB{AzureResourcesDBClient: fixture.db, beforePublish: func() error {
				t.Fatal("failed reservation must not reach publication")
				return nil
			}}
			err := fixture.syncer.SyncOnce(fixture.ctx, fixture.key)
			if conflict {
				require.True(t, cosmosstorageutils.IsPreconditionFailedError(err))
			} else {
				require.ErrorContains(t, err, "reservation unavailable")
			}
			require.Empty(t, fixture.db.GetAllDocuments())
			fixture.syncer.resourcesDBClient = db
			fixture.syncer.azureResourcesDBClient = fixture.db
			fixture.refreshSPC(t)
			require.Empty(t, fixture.spc.Status.ReservedNodePoolEgressPublicIPResourceIDs)
			require.NoError(t, fixture.syncer.SyncOnce(fixture.ctx, fixture.key))
			fixture.refresh(t)
			if conflict {
				require.Equal(t, "new-ca", fixture.spc.Status.ServingCABundle)
			}
		})
	}
}

func TestNodePoolEgressPublicIPsDraftReservationBackfill(t *testing.T) {
	for _, matching := range []bool{true, false} {
		t.Run(fmt.Sprintf("matching=%t", matching), func(t *testing.T) {
			fixture := newNodePoolEgressFixture(t)
			require.NoError(t, fixture.syncer.SyncOnce(fixture.ctx, fixture.key))
			desire := fixture.refresh(t)
			crud, err := fixture.db.DeploymentStackDesiresForCluster(testSubscriptionID, testResourceGroupName, testClusterName)
			require.NoError(t, err)
			desire.Status.ObservedSpecHash = "executor-status"
			if !matching {
				desire.Spec.Stack.Properties.Template = map[string]any{"changed": true}
			}
			_, err = crud.Replace(fixture.ctx, desire, nil)
			require.NoError(t, err)
			fixture.refresh(t)
			spcCRUD := fixture.syncer.resourcesDBClient.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName)
			unreserved := fixture.spc.DeepCopy()
			unreserved.Status.ReservedNodePoolEgressPublicIPResourceIDs = nil
			_, err = spcCRUD.Replace(fixture.ctx, unreserved, nil)
			require.NoError(t, err)
			fixture.refreshSPC(t)
			before := fixture.db.GetAllDocuments()
			storedSPC := fixture.spc.DeepCopy()
			if matching {
				fixture.syncer.azureResourcesDBClient = &reservationCheckedAzureDB{AzureResourcesDBClient: fixture.db, beforePublish: func() error {
					persisted, err := spcCRUD.Get(fixture.ctx, coreapi.ServiceProviderClusterResourceName)
					require.NoError(t, err)
					require.Len(t, persisted.Status.ReservedNodePoolEgressPublicIPResourceIDs, 9)
					return nil
				}}
				require.NoError(t, fixture.syncer.SyncOnce(fixture.ctx, fixture.key))
				fixture.refreshSPC(t)
				require.Len(t, fixture.spc.Status.ReservedNodePoolEgressPublicIPResourceIDs, 9)
			} else {
				require.ErrorContains(t, fixture.syncer.SyncOnce(fixture.ctx, fixture.key), "does not match unreserved")
				fixture.refreshSPC(t)
				require.Equal(t, storedSPC, fixture.spc)
			}
			require.Equal(t, before, fixture.db.GetAllDocuments(), "backfill must not rewrite existing intent or executor status")
		})
	}
}

func TestNodePoolEgressPublicIPsInvalidReservations(t *testing.T) {
	for _, invalid := range []string{"count", "nil", "duplicate", "scope", "type"} {
		t.Run(invalid, func(t *testing.T) {
			fixture := newNodePoolEgressFixture(t)
			require.NoError(t, fixture.syncer.SyncOnce(fixture.ctx, fixture.key))
			fixture.refresh(t)
			changed := fixture.spc.DeepCopy()
			switch invalid {
			case "count":
				changed.Status.ReservedNodePoolEgressPublicIPResourceIDs = changed.Status.ReservedNodePoolEgressPublicIPResourceIDs[:8]
			case "nil":
				changed.Status.ReservedNodePoolEgressPublicIPResourceIDs[0] = nil
			case "duplicate":
				changed.Status.ReservedNodePoolEgressPublicIPResourceIDs[0] = changed.Status.ReservedNodePoolEgressPublicIPResourceIDs[1]
			case "scope":
				changed.Status.ReservedNodePoolEgressPublicIPResourceIDs[0] = metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/other/resourceGroups/other/providers/Microsoft.Network/publicIPAddresses/other"))
			case "type":
				changed.Status.ReservedNodePoolEgressPublicIPResourceIDs[0] = metadataapi.Must(azcorearm.ParseResourceID(testManagedResourceGroupID(t).String() + "/providers/Microsoft.Network/loadBalancers/other"))
			}
			spcCRUD := fixture.syncer.resourcesDBClient.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName)
			_, err := spcCRUD.Replace(fixture.ctx, changed, nil)
			require.NoError(t, err)
			fixture.refreshSPC(t)
			beforeSPC := fixture.spc.DeepCopy()
			beforeDesires := fixture.db.GetAllDocuments()
			require.Error(t, fixture.syncer.SyncOnce(fixture.ctx, fixture.key))
			fixture.refreshSPC(t)
			require.Equal(t, beforeSPC, fixture.spc, "invalid reservations must not be silently replaced")
			require.Equal(t, beforeDesires, fixture.db.GetAllDocuments())
		})
	}
}
