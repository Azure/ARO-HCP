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
			name: "cluster being deleted",
			clusters: []*coreapi.Cluster{newTestCluster(
				withExperimentalAutoNode,
				withAutoNodeIdentity,
				func(c *coreapi.Cluster) {
					c.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: time.Now()}
				},
			)},
			serviceProviderClusters: []*coreapi.ServiceProviderCluster{newTestServiceProviderCluster(
				withResolvedAutoNodeClientID(testResolvedClientID),
			)},
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
