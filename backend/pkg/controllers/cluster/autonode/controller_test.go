// Copyright 2026 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package autonode

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	hyperv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/backend/pkg/kubeapplierhelpers"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/fleetapihelpers"
	"github.com/Azure/ARO-HCP/internal/apihelpers/kubeapplierapihelpers"
	"github.com/Azure/ARO-HCP/internal/azure"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/kubeappliercosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/fleetlistertesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/kubeapplierlistertesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	testSub          = "test-sub"
	testRG           = "test-rg"
	testClusterName  = "test-cluster"
	testClusterID    = "11111111111111111111111111111111"
	testEnvID        = "test-env"
	testDomainPrefix = "test-domprefix"
	testStampID      = "mc1"
	testClientID     = "22222222-2222-2222-2222-222222222222"
)

func testKey() controllerutils.HCPClusterKey {
	return controllerutils.HCPClusterKey{SubscriptionID: testSub, ResourceGroupName: testRG, HCPClusterName: testClusterName}
}

func testMgmtClusterResourceID() *azcorearm.ResourceID {
	return metadataapi.Must(fleetapihelpers.ToManagementClusterResourceID(testStampID))
}

func testClusterResourceID() *azcorearm.ResourceID {
	return metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSub + "/resourceGroups/" + testRG +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName,
	))
}

func newTestCluster(opts ...func(*coreapi.Cluster)) *coreapi.Cluster {
	resourceID := testClusterResourceID()
	csID := metadataapi.Must(metadataapi.NewInternalID("/api/aro_hcp/v1alpha1/clusters/" + testClusterID))
	cluster := &coreapi.Cluster{
		CosmosMetadata: coreapi.CosmosMetadata{ResourceID: resourceID, PartitionKey: strings.ToLower(resourceID.SubscriptionID)},
		TrackedResource: coreapi.TrackedResource{
			Resource: coreapi.Resource{ID: resourceID},
		},
		CustomerProperties: coreapi.ClusterCustomerProperties{
			DNS: coreapi.CustomerDNSProfile{BaseDomainPrefix: testDomainPrefix},
		},
		ServiceProviderProperties: coreapi.ClusterServiceProviderProperties{
			ClusterServiceID: &csID,
		},
	}
	for _, opt := range opts {
		opt(cluster)
	}
	return cluster
}

func testAutoNodeIdentityResourceID() *azcorearm.ResourceID {
	return metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSub + "/resourceGroups/" + testRG +
			"/providers/Microsoft.ManagedIdentity/userAssignedIdentities/autonode-identity",
	))
}

func withExperimentalAutoNode(c *coreapi.Cluster) {
	c.ServiceProviderProperties.ExperimentalFeatures.AutoNode = coreapi.AutoNode
}

func newTestServiceProviderCluster(opts ...func(*coreapi.ServiceProviderCluster)) *coreapi.ServiceProviderCluster {
	serviceProviderClusterResourceID := metadataapi.Must(azcorearm.ParseResourceID(
		testClusterResourceID().String() + "/" + coreapi.ServiceProviderClusterResourceTypeName + "/" + coreapi.ServiceProviderClusterResourceName,
	))
	spc := &coreapi.ServiceProviderCluster{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   serviceProviderClusterResourceID,
			PartitionKey: strings.ToLower(testSub),
		},
		Status: coreapi.ServiceProviderClusterStatus{
			ManagementClusterResourceID: testMgmtClusterResourceID(),
		},
	}
	for _, opt := range opts {
		opt(spc)
	}
	return spc
}

func seedHostedClusterReadDesire(t *testing.T, ctx context.Context, mockKubeApplier *kubeappliercosmosstoragetesting.MockKubeApplierDBClient) {
	t.Helper()
	hc := &hyperv1beta1.HostedCluster{}
	raw, err := json.Marshal(hc)
	require.NoError(t, err)
	rdResourceIDStr := kubeapplierapihelpers.ToClusterScopedReadDesireResourceIDString(
		testSub, testRG, testClusterName, kubeapplierhelpers.ReadDesireNameReadonlyHostedCluster,
	)
	readDesire := &kubeapplierapi.ReadDesire{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   metadataapi.Must(azcorearm.ParseResourceID(rdResourceIDStr)),
			PartitionKey: strings.ToLower(testMgmtClusterResourceID().String()),
		},
		Status: kubeapplierapi.ReadDesireStatus{
			KubeContent: &runtime.RawExtension{Raw: raw},
		},
	}
	readDesireCRUD, err := mockKubeApplier.ReadDesiresForCluster(testSub, testRG, testClusterName)
	require.NoError(t, err)
	_, err = readDesireCRUD.Create(ctx, readDesire, nil)
	require.NoError(t, err)
}

// newTestSyncer builds an autoNodeSyncer wired against slice/DB-backed
// listers and a mock kube-applier client registry, mirroring the pattern
// used by the backups package's SyncOnce tests.
func newTestSyncer(
	clusters []*coreapi.Cluster,
	serviceProviderClusters []*coreapi.ServiceProviderCluster,
	mockClients *kubeappliercosmosstoragetesting.MockKubeApplierDBClients,
) *autoNodeSyncer {
	mcLister := &fleetlistertesting.SliceManagementClusterLister{
		ManagementClusters: []*fleetapi.ManagementCluster{{CosmosMetadata: coreapi.CosmosMetadata{ResourceID: testMgmtClusterResourceID()}}},
	}
	return &autoNodeSyncer{
		clusterLister:                       &corelistertesting.SliceClusterLister{Clusters: clusters},
		serviceProviderClusterLister:        &corelistertesting.SliceServiceProviderClusterLister{ServiceProviderClusters: serviceProviderClusters},
		readDesireLister:                    &kubeapplierlistertesting.DBReadDesireLister{Clients: mockClients, Lister: mcLister},
		applyDesireLister:                   &kubeapplierlistertesting.DBApplyDesireLister{Clients: mockClients, Lister: mcLister},
		kubeApplierDBClients:                mockClients,
		hostedClusterNamespaceEnvIdentifier: testEnvID,
	}
}

func TestAutoNodeSyncer_SyncOnce_NotReady(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))

	tests := []struct {
		name                    string
		clusters                []*coreapi.Cluster
		serviceProviderClusters []*coreapi.ServiceProviderCluster
		seedReadDesire          bool
	}{
		{
			name:     "cluster not found",
			clusters: nil,
		},
		{
			// A management cluster was never assigned, so nothing could have
			// been delivered (creation is gated on ManagementClusterResourceID
			// != nil) - this must stay a no-op even though the cluster is
			// deleting. Contrast with
			// TestAutoNodeSyncer_SyncOnce_TearsDownOnDeletion, which covers the
			// case where a desire was actually created before deletion.
			name: "cluster being deleted before a management cluster was assigned",
			clusters: []*coreapi.Cluster{newTestCluster(
				withExperimentalAutoNode,
				withAutoNodeIdentity,
				func(c *coreapi.Cluster) {
					c.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: time.Now()}
				},
			)},
			serviceProviderClusters: []*coreapi.ServiceProviderCluster{newTestServiceProviderCluster(
				withResolvedAutoNodeClientID(testResolvedClientID),
				func(spc *coreapi.ServiceProviderCluster) {
					spc.Status.ManagementClusterResourceID = nil
				},
			)},
		},
		{
			name: "cluster being deleted where AutoNode was never enabled",
			clusters: []*coreapi.Cluster{newTestCluster(
				func(c *coreapi.Cluster) {
					c.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: time.Now()}
				},
			)},
			serviceProviderClusters: []*coreapi.ServiceProviderCluster{newTestServiceProviderCluster()},
		},
		{
			name:                    "ServiceProviderCluster not found",
			clusters:                []*coreapi.Cluster{newTestCluster(withExperimentalAutoNode, withAutoNodeIdentity)},
			serviceProviderClusters: nil,
		},
		{
			name:     "AutoNode not requested",
			clusters: []*coreapi.Cluster{newTestCluster()},
			serviceProviderClusters: []*coreapi.ServiceProviderCluster{newTestServiceProviderCluster(
				withResolvedAutoNodeClientID(testResolvedClientID),
			)},
		},
		{
			name: "AutoNode requested but identity not yet resolved",
			clusters: []*coreapi.Cluster{newTestCluster(
				withExperimentalAutoNode,
				withAutoNodeIdentity,
			)},
			serviceProviderClusters: []*coreapi.ServiceProviderCluster{newTestServiceProviderCluster()},
		},
		{
			name:     "no management cluster resource ID yet",
			clusters: []*coreapi.Cluster{newTestCluster(withExperimentalAutoNode, withAutoNodeIdentity)},
			serviceProviderClusters: []*coreapi.ServiceProviderCluster{newTestServiceProviderCluster(
				withResolvedAutoNodeClientID(testResolvedClientID),
				func(spc *coreapi.ServiceProviderCluster) {
					spc.Status.ManagementClusterResourceID = nil
				},
			)},
		},
		{
			name: "no ClusterServiceID yet",
			clusters: []*coreapi.Cluster{newTestCluster(
				withExperimentalAutoNode,
				withAutoNodeIdentity,
				func(c *coreapi.Cluster) {
					c.ServiceProviderProperties.ClusterServiceID = nil
				},
			)},
			serviceProviderClusters: []*coreapi.ServiceProviderCluster{newTestServiceProviderCluster(
				withResolvedAutoNodeClientID(testResolvedClientID),
			)},
		},
		{
			name: "no domain prefix yet",
			clusters: []*coreapi.Cluster{newTestCluster(
				withExperimentalAutoNode,
				withAutoNodeIdentity,
				func(c *coreapi.Cluster) {
					c.CustomerProperties.DNS.BaseDomainPrefix = ""
				},
			)},
			serviceProviderClusters: []*coreapi.ServiceProviderCluster{newTestServiceProviderCluster(
				withResolvedAutoNodeClientID(testResolvedClientID),
			)},
		},
		{
			name:     "HostedCluster ReadDesire not yet observed",
			clusters: []*coreapi.Cluster{newTestCluster(withExperimentalAutoNode, withAutoNodeIdentity)},
			serviceProviderClusters: []*coreapi.ServiceProviderCluster{newTestServiceProviderCluster(
				withResolvedAutoNodeClientID(testResolvedClientID),
			)},
			seedReadDesire: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockKubeApplier := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClient()
			mockClients := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClients()
			mockClients.Register(testMgmtClusterResourceID(), mockKubeApplier)

			if tt.seedReadDesire {
				seedHostedClusterReadDesire(t, ctx, mockKubeApplier)
			}

			syncer := newTestSyncer(tt.clusters, tt.serviceProviderClusters, mockClients)
			err := syncer.SyncOnce(ctx, testKey())
			require.NoError(t, err)

			applyDesireCRUD, err := mockKubeApplier.ApplyDesiresForCluster(testSub, testRG, testClusterName)
			require.NoError(t, err)
			_, err = applyDesireCRUD.Get(ctx, autoNodeApplyDesireName)
			assert.Error(t, err, "no ApplyDesire should have been written")
		})
	}
}

// TestAutoNodeSyncer_SyncOnce_CreatesApplyDesire pins that the AFEC-gated
// ExperimentalFeatures.AutoNode trigger drives delivery once the cluster's
// "autonode" data-plane identity has resolved, and that the resulting
// ApplyDesire is well-formed and idempotent.
func TestAutoNodeSyncer_SyncOnce_CreatesApplyDesire(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))

	mockKubeApplier := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClient()
	mockClients := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClients()
	mockClients.Register(testMgmtClusterResourceID(), mockKubeApplier)

	seedHostedClusterReadDesire(t, ctx, mockKubeApplier)

	syncer := newTestSyncer(
		[]*coreapi.Cluster{newTestCluster(withExperimentalAutoNode, withAutoNodeIdentity)},
		[]*coreapi.ServiceProviderCluster{newTestServiceProviderCluster(withResolvedAutoNodeClientID(testClientID))},
		mockClients,
	)

	require.NoError(t, syncer.SyncOnce(ctx, testKey()))

	applyDesireCRUD, err := mockKubeApplier.ApplyDesiresForCluster(testSub, testRG, testClusterName)
	require.NoError(t, err)
	applyDesire, err := applyDesireCRUD.Get(ctx, autoNodeApplyDesireName)
	require.NoError(t, err, "expected ApplyDesire to have been created")

	assert.Equal(t, kubeapplierapi.ApplyDesireTypeServerSideApply, applyDesire.Spec.Type)
	assert.Equal(t, AutoNodeEnablerControllerName, applyDesire.Tags[kubeapplierapi.TagControllerName])

	expectedTarget := controllerutils.HostedClusterTarget(testEnvID, testClusterID, testDomainPrefix)
	assert.Equal(t, expectedTarget, applyDesire.Spec.TargetItem)

	require.NotNil(t, applyDesire.Spec.ServerSideApply)
	require.NotNil(t, applyDesire.Spec.ServerSideApply.FieldManager)
	assert.Equal(t, autoNodeFieldManager, *applyDesire.Spec.ServerSideApply.FieldManager)

	var payload partialHostedCluster
	require.NoError(t, json.Unmarshal(applyDesire.Spec.ServerSideApply.KubeContent.Raw, &payload))
	assert.Equal(t, "HostedCluster", payload.Kind)
	assert.Equal(t, expectedTarget.Name, payload.Metadata.Name)
	assert.Equal(t, expectedTarget.Namespace, payload.Metadata.Namespace)
	assert.Equal(t, "Karpenter", payload.Spec.AutoNode.Provisioner.Name)
	assert.Equal(t, "Azure", payload.Spec.AutoNode.Provisioner.Karpenter.Platform)
	assert.Equal(t, testClientID, payload.Spec.AutoNode.Provisioner.Karpenter.Azure.ClientID)

	// Re-running SyncOnce should be idempotent: EnsureApplyDesire only
	// writes on drift, so this must succeed without error and produce the
	// same desire.
	require.NoError(t, syncer.SyncOnce(ctx, testKey()))
	again, err := applyDesireCRUD.Get(ctx, autoNodeApplyDesireName)
	require.NoError(t, err)
	assert.Equal(t, applyDesire.Spec, again.Spec)
}

// TestAutoNodeSyncer_SyncOnce_TearsDownOnDeletion pins the fix for the
// cluster-deletion deadlock: once an AutoNode ApplyDesire has been created,
// setting DeletionTimestamp must purge it in a single SyncOnce call. Reaching
// NotFound after exactly one call is itself the regression guard against the
// Type=Delete-flip hazard: kubeapplierhelpers.EnsureApplyDesireRemoved
// provably needs >=2 syncs to reach purge (it returns (false, nil) on the
// first, flip-only call - see desire_deletion.go), so a single-call purge
// proves PurgeApplyDesire (a plain document delete) was used instead.
func TestAutoNodeSyncer_SyncOnce_TearsDownOnDeletion(t *testing.T) {
	setup := func(t *testing.T) (*autoNodeSyncer, *kubeappliercosmosstoragetesting.MockKubeApplierDBClient, context.Context) {
		t.Helper()
		ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
		mockKubeApplier := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClient()
		mockClients := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClients()
		mockClients.Register(testMgmtClusterResourceID(), mockKubeApplier)
		seedHostedClusterReadDesire(t, ctx, mockKubeApplier)

		syncer := newTestSyncer(
			[]*coreapi.Cluster{newTestCluster(withExperimentalAutoNode, withAutoNodeIdentity)},
			[]*coreapi.ServiceProviderCluster{newTestServiceProviderCluster(withResolvedAutoNodeClientID(testClientID))},
			mockClients,
		)
		require.NoError(t, syncer.SyncOnce(ctx, testKey()))

		applyDesireCRUD, err := mockKubeApplier.ApplyDesiresForCluster(testSub, testRG, testClusterName)
		require.NoError(t, err)
		_, err = applyDesireCRUD.Get(ctx, autoNodeApplyDesireName)
		require.NoError(t, err, "precondition: ApplyDesire must exist before exercising teardown")

		return syncer, mockKubeApplier, ctx
	}

	assertPurged := func(t *testing.T, ctx context.Context, mockKubeApplier *kubeappliercosmosstoragetesting.MockKubeApplierDBClient) {
		t.Helper()
		applyDesireCRUD, err := mockKubeApplier.ApplyDesiresForCluster(testSub, testRG, testClusterName)
		require.NoError(t, err)
		_, err = applyDesireCRUD.Get(ctx, autoNodeApplyDesireName)
		assert.Error(t, err, "ApplyDesire should have been purged")

		// Belt-and-braces: confirm no document was ever left as Type=Delete
		// (which would indicate EnsureApplyDesireRemoved's flip step ran).
		docs := mockKubeApplier.GetAllDocuments()
		for name, raw := range docs {
			var desire kubeapplierapi.ApplyDesire
			if err := json.Unmarshal(raw, &desire); err != nil {
				continue // not an ApplyDesire document (e.g. a ReadDesire)
			}
			assert.NotEqual(t, kubeapplierapi.ApplyDesireTypeDelete, desire.Spec.Type,
				"document %s should never be flipped to Type=Delete", name)
		}
	}

	t.Run("purges in a single SyncOnce after DeletionTimestamp is set", func(t *testing.T) {
		syncer, mockKubeApplier, ctx := setup(t)

		syncer.clusterLister = &corelistertesting.SliceClusterLister{Clusters: []*coreapi.Cluster{newTestCluster(
			withExperimentalAutoNode,
			withAutoNodeIdentity,
			func(c *coreapi.Cluster) {
				c.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: time.Now()}
			},
		)}}

		require.NoError(t, syncer.SyncOnce(ctx, testKey()))
		assertPurged(t, ctx, mockKubeApplier)

		// Idempotency: a second sync after the purge must not error (NotFound
		// is a no-op).
		require.NoError(t, syncer.SyncOnce(ctx, testKey()))
	})

	// The following cases are the actual regression guard the gate-reordering
	// in SyncOnce exists to protect: each simulates a gate that has gone false
	// after the desire was created, and asserts teardown still proceeds.
	t.Run("purges even when ClusterServiceID has been cleared", func(t *testing.T) {
		syncer, mockKubeApplier, ctx := setup(t)

		syncer.clusterLister = &corelistertesting.SliceClusterLister{Clusters: []*coreapi.Cluster{newTestCluster(
			withExperimentalAutoNode,
			withAutoNodeIdentity,
			func(c *coreapi.Cluster) {
				c.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: time.Now()}
				c.ServiceProviderProperties.ClusterServiceID = nil
			},
		)}}

		require.NoError(t, syncer.SyncOnce(ctx, testKey()))
		assertPurged(t, ctx, mockKubeApplier)
	})

	t.Run("purges even when the HostedCluster ReadDesire has already been reaped", func(t *testing.T) {
		syncer, mockKubeApplier, ctx := setup(t)

		// Simulate the ReadDesire being gone by pointing the syncer at a fresh
		// mock client registry with no seeded ReadDesire, while the ApplyDesire
		// created during setup remains in the original registry.
		mockClients := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClients()
		mockClients.Register(testMgmtClusterResourceID(), mockKubeApplier)
		mcLister := syncer.readDesireLister.(*kubeapplierlistertesting.DBReadDesireLister).Lister
		syncer.readDesireLister = &kubeapplierlistertesting.DBReadDesireLister{Clients: mockClients, Lister: mcLister}

		readDesireCRUD, err := mockKubeApplier.ReadDesiresForCluster(testSub, testRG, testClusterName)
		require.NoError(t, err)
		require.NoError(t, readDesireCRUD.Delete(ctx, kubeapplierhelpers.ReadDesireNameReadonlyHostedCluster))

		syncer.clusterLister = &corelistertesting.SliceClusterLister{Clusters: []*coreapi.Cluster{newTestCluster(
			withExperimentalAutoNode,
			withAutoNodeIdentity,
			func(c *coreapi.Cluster) {
				c.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: time.Now()}
			},
		)}}

		require.NoError(t, syncer.SyncOnce(ctx, testKey()))
		assertPurged(t, ctx, mockKubeApplier)
	})

	t.Run("purges even when the resolved client ID has been cleared with a RetrievalError", func(t *testing.T) {
		syncer, mockKubeApplier, ctx := setup(t)

		syncer.clusterLister = &corelistertesting.SliceClusterLister{Clusters: []*coreapi.Cluster{newTestCluster(
			withExperimentalAutoNode,
			withAutoNodeIdentity,
			func(c *coreapi.Cluster) {
				c.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: time.Now()}
			},
		)}}
		syncer.serviceProviderClusterLister = &corelistertesting.SliceServiceProviderClusterLister{
			ServiceProviderClusters: []*coreapi.ServiceProviderCluster{newTestServiceProviderCluster(
				func(spc *coreapi.ServiceProviderCluster) {
					spc.Status.DataPlaneOperatorsManagedIdentities.Identities = map[string]*coreapi.ServiceProviderClusterDataPlaneOperatorManagedIdentity{
						strings.ToLower(testAutoNodeIdentityResourceID().String()): {
							ResourceID:     testAutoNodeIdentityResourceID(),
							RetrievalError: ptr.To("failed to get identity"),
						},
					}
				},
			)},
		}

		require.NoError(t, syncer.SyncOnce(ctx, testKey()))
		assertPurged(t, ctx, mockKubeApplier)
	})

	t.Run("does not purge a deleting cluster's ApplyDesire when AutoNode was never enabled on it", func(t *testing.T) {
		syncer, mockKubeApplier, ctx := setup(t)

		// A cluster without AutoNode enabled should never have had an
		// AutoNode ApplyDesire delivered in the first place, but the
		// pre-existing desire from setup() stands in for one left behind by
		// a prior, unrelated controller run. needsDeletionWork must not key
		// off DeletionTimestamp + ManagementClusterResourceID alone, or it
		// would purge indiscriminately on every deleting cluster fleet-wide.
		syncer.clusterLister = &corelistertesting.SliceClusterLister{Clusters: []*coreapi.Cluster{newTestCluster(
			withAutoNodeIdentity,
			func(c *coreapi.Cluster) {
				c.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: time.Now()}
			},
		)}}

		require.NoError(t, syncer.SyncOnce(ctx, testKey()))

		applyDesireCRUD, err := mockKubeApplier.ApplyDesiresForCluster(testSub, testRG, testClusterName)
		require.NoError(t, err)
		_, err = applyDesireCRUD.Get(ctx, autoNodeApplyDesireName)
		require.NoError(t, err, "ApplyDesire should not have been purged for a non-AutoNode cluster")
	})
}

const testResolvedClientID = "33333333-3333-3333-3333-333333333333"

func withAutoNodeIdentity(c *coreapi.Cluster) {
	c.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.DataPlaneOperators = map[string]*azcorearm.ResourceID{
		string(azure.ClusterOperatorIdentifierAutoNode): testAutoNodeIdentityResourceID(),
	}
}

func withResolvedAutoNodeClientID(clientID string) func(*coreapi.ServiceProviderCluster) {
	return func(spc *coreapi.ServiceProviderCluster) {
		spc.Status.DataPlaneOperatorsManagedIdentities.Identities = map[string]*coreapi.ServiceProviderClusterDataPlaneOperatorManagedIdentity{
			strings.ToLower(testAutoNodeIdentityResourceID().String()): {
				ResourceID: testAutoNodeIdentityResourceID(),
				ClientID:   ptr.To(clientID),
			},
		}
	}
}

func TestAutoNodeKarpenterClientID(t *testing.T) {
	tests := []struct {
		name         string
		clusterOpt   func(*coreapi.Cluster)
		spcOpt       func(*coreapi.ServiceProviderCluster)
		wantResolved string
		wantOK       bool
	}{
		{
			name:         "resolved",
			clusterOpt:   withAutoNodeIdentity,
			spcOpt:       withResolvedAutoNodeClientID(testResolvedClientID),
			wantResolved: testResolvedClientID,
			wantOK:       true,
		},
		{
			name:       "identity not configured on cluster",
			clusterOpt: func(*coreapi.Cluster) {},
			spcOpt:     withResolvedAutoNodeClientID(testResolvedClientID),
			wantOK:     false,
		},
		{
			name:       "identity configured but not yet resolved",
			clusterOpt: withAutoNodeIdentity,
			spcOpt:     func(*coreapi.ServiceProviderCluster) {},
			wantOK:     false,
		},
		{
			name:       "resolution failed (RetrievalError set)",
			clusterOpt: withAutoNodeIdentity,
			spcOpt: func(spc *coreapi.ServiceProviderCluster) {
				spc.Status.DataPlaneOperatorsManagedIdentities.Identities = map[string]*coreapi.ServiceProviderClusterDataPlaneOperatorManagedIdentity{
					strings.ToLower(testAutoNodeIdentityResourceID().String()): {
						ResourceID:     testAutoNodeIdentityResourceID(),
						RetrievalError: ptr.To("failed to get identity"),
					},
				}
			},
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cluster := newTestCluster(tt.clusterOpt)
			spc := newTestServiceProviderCluster(tt.spcOpt)
			got, ok := autoNodeKarpenterClientID(cluster, spc)
			assert.Equal(t, tt.wantOK, ok)
			if tt.wantOK {
				assert.Equal(t, tt.wantResolved, got)
			}
		})
	}
}
