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

package clusterresources

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/kubeapplierapihelpers"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/kubeappliercosmosstoragetesting"
	fleetlistertesting "github.com/Azure/ARO-HCP/internal/database/listertesting/fleetlistertesting"
	kubeapplierlistertesting "github.com/Azure/ARO-HCP/internal/database/listertesting/kubeapplierlistertesting"
	"github.com/Azure/ARO-HCP/internal/restmapper"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// classifiableResources is one kube object per desire name classifyClusterResource
// can produce. It is the input to the coverage test below, which is the only
// thing keeping the destruct chain's claim sets honest: a new resource type
// added to classifyClusterResource but not assigned to a step would otherwise
// only surface as a log line on a cluster that is already deleting.
//
// Add a fixture here whenever classifyClusterResource learns a new desire name.
var classifiableResources = []string{
	`{"apiVersion":"cluster.open-cluster-management.io/v1","kind":"ManagedCluster","metadata":{"name":"my-hc"}}`,
	`{"apiVersion":"hypershift.openshift.io/v1beta1","kind":"HostedCluster","metadata":{"name":"my-hc","namespace":"ocm-env-abc"}}`,
	`{"apiVersion":"hypershift.openshift.io/v1beta1","kind":"NodePool","metadata":{"name":"q2e1p3b8m8a3s6l-np-2dz967","namespace":"ocm-env-abc"},"spec":{"clusterName":"q2e1p3b8m8a3s6l"}}`,
	`{"apiVersion":"v1","kind":"Namespace","metadata":{"name":"ocm-env-abc"}}`,
	`{"apiVersion":"v1","kind":"Namespace","metadata":{"name":"ocm-env-abc-cp","labels":{"hypershift.openshift.io/cluster":"abc"}}}`,
	`{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"default-ingress","namespace":"ocm-env-abc"}}`,
	`{"apiVersion":"v1","kind":"Secret","metadata":{"name":"pull-secret","namespace":"ocm-env-abc"}}`,
	`{"apiVersion":"multitenancy.acn.azure.com/v1alpha1","kind":"PodNetwork","metadata":{"name":"pn-abc123"}}`,
	`{"apiVersion":"multitenancy.acn.azure.com/v1alpha1","kind":"PodNetworkInstance","metadata":{"name":"pni-abc123","namespace":"ocm-env-abc-cp"}}`,
	`{"apiVersion":"secret-sync.x-k8s.io/v1alpha1","kind":"SecretSync","metadata":{"name":"bound-sa-signing-key","namespace":"ocm-env-abc"}}`,
	`{"apiVersion":"secret-sync.x-k8s.io/v1alpha1","kind":"SecretSync","metadata":{"name":"default-ingress-cert","namespace":"ocm-env-abc"}}`,
	`{"apiVersion":"secret-sync.x-k8s.io/v1alpha1","kind":"SecretSync","metadata":{"name":"kube-apiserver-server-cert","namespace":"ocm-env-abc"}}`,
	`{"apiVersion":"secrets-store.csi.x-k8s.io/v1","kind":"SecretProviderClass","metadata":{"name":"bound-sa-signing-key","namespace":"ocm-env-abc"}}`,
	`{"apiVersion":"secrets-store.csi.x-k8s.io/v1","kind":"SecretProviderClass","metadata":{"name":"default-ingress-cert","namespace":"ocm-env-abc"}}`,
	`{"apiVersion":"secrets-store.csi.x-k8s.io/v1","kind":"SecretProviderClass","metadata":{"name":"kube-apiserver-server-cert","namespace":"ocm-env-abc"}}`,
}

// TestApplyDesireDestructChainCoversEveryDesireName pins the invariant the
// destruct chain depends on: every desire name the controller can create is
// claimed by exactly one step, and no step claims a name that cannot exist.
//
// An unclaimed name is the dangerous direction — the chain would report itself
// drained while the desire is still live, so the object is never deleted and the
// cluster leaks it. A name claimed twice is milder but still wrong: two steps
// would race over the same desire.
func TestApplyDesireDestructChainCoversEveryDesireName(t *testing.T) {
	t.Parallel()

	stepClaims := map[string]desireNameSet{
		"managed-cluster":          managedClusterNames,
		"ingress-manifests":        ingressManifestDesireNames,
		"hosted-cluster":           hostedClusterDesireNames,
		"swift-podnetworkinstance": swiftPodNetworkInstanceDesireNames,
		"swift-podnetwork":         swiftPodNetworkDesireNames,
		"cascade-covered":          cascadeCoveredDesireNames,
		"namespaces":               namespaceDesireNames,
	}
	stepNames := make(map[string]bool)
	for _, step := range applyDesireRemovalChain {
		require.False(t, stepNames[step.name()], "step name %q must be unique", step.name())
		stepNames[step.name()] = true
		require.Contains(t, stepClaims, step.name(), "every removal step must have claim coverage")
	}
	require.Len(t, stepNames, len(stepClaims), "every tested claim set must have a step in the chain")

	// Keyed by the lower-cased name, since that is what a step matches on; the
	// value keeps the original casing so failures name the constant.
	classified := make(map[string]string)
	for _, resource := range classifiableResources {
		var obj unstructured.Unstructured
		require.NoError(t, json.Unmarshal([]byte(resource), &obj), "failed to unmarshal fixture %s", resource)

		result, err := classifyClusterResource(&obj)
		require.NoError(t, err, "fixture should classify: %s", resource)
		classified[strings.ToLower(result.desireName)] = result.desireName
	}
	claimed := claimedDesireNames()
	require.Len(t, claimed, len(classified), "the runtime claim registry must cover exactly the classified names")
	for name := range classified {
		assert.True(t, claimed.has(name), "runtime registry must claim %s without logging an unclaimed desire", name)
	}

	t.Run("every classified name is claimed by exactly one step", func(t *testing.T) {
		t.Parallel()
		for _, desireName := range classified {
			var claimedBy []string
			for stepName, claims := range stepClaims {
				if claims.has(desireName) {
					claimedBy = append(claimedBy, stepName)
				}
			}
			assert.Len(t, claimedBy, 1,
				"desire name %q should be claimed by exactly one destruct step, got %v", desireName, claimedBy)
		}
	})

	t.Run("no step claims a name classifyClusterResource cannot produce", func(t *testing.T) {
		t.Parallel()
		for stepName, claims := range stepClaims {
			for desireName := range claims {
				assert.Contains(t, classified, desireName,
					"step %q claims %q, which classifyClusterResource never produces "+
						"(dead entry, or classifiableResources is missing a fixture)", stepName, desireName)
			}
		}
	})
}

// TestApplyDesireDestructChainOrdering pins the ordering rules the chain exists
// to enforce. Most of them protect a finalizer that reaches outside the object
// being deleted, so getting the order wrong leaks Azure state rather than just
// being slow. Document-only cleanup runs first to stop reconciliation before
// any waited-on deletion begins.
func TestApplyDesireDestructChainOrdering(t *testing.T) {
	t.Parallel()

	indexOf := func(want applyDesireRemovalStep) int {
		for i, step := range applyDesireRemovalChain {
			if step.name() == want.name() {
				return i
			}
		}
		return -1
	}

	managedCluster := indexOf(managedClusterRemovalStep{})
	ingress := indexOf(ingressManifestRemovalStep{})
	hostedCluster := indexOf(hostedClusterRemovalStep{})
	podNetworkInstance := indexOf(swiftPodNetworkInstanceRemovalStep{})
	podNetwork := indexOf(swiftPodNetworkRemovalStep{})
	cascadeCovered := indexOf(cascadeCoveredRemovalStep{})
	namespaces := indexOf(namespacesRemovalStep{})

	require.NotEqual(t, -1, managedCluster, "managed-cluster step must be in the chain")
	require.NotEqual(t, -1, ingress, "ingress-manifests step must be in the chain")
	require.NotEqual(t, -1, hostedCluster, "hosted-cluster step must be in the chain")
	require.NotEqual(t, -1, podNetworkInstance, "swift-podnetworkinstance step must be in the chain")
	require.NotEqual(t, -1, podNetwork, "swift-podnetwork step must be in the chain")
	require.NotEqual(t, -1, cascadeCovered, "cascade-covered step must be in the chain")
	require.NotEqual(t, -1, namespaces, "namespaces step must be in the chain")

	assert.Equal(t, 0, cascadeCovered, "document-only cleanup must run before any waited-on step")
	assert.Less(t, managedCluster, ingress, "ManagedCluster cleanup must finish before ingress configuration is removed")
	assert.Less(t, ingress, hostedCluster, "shared-namespace ingress manifests must be removed before HostedCluster teardown")
	assert.Less(t, hostedCluster, namespaces,
		"HostedCluster must be deleted before its namespaces: its finalizer deprovisions Azure "+
			"infrastructure and needs the control plane namespace intact while it runs")
	assert.Less(t, hostedCluster, podNetworkInstance,
		"the PodNetworkInstance provides pod networking for the control plane, so it must outlive "+
			"the HostedCluster teardown")
	assert.Less(t, podNetworkInstance, podNetwork,
		"SWIFT resources are destroyed in reverse order of creation: PodNetwork reports InUse "+
			"while an instance still references it")
	assert.Less(t, podNetworkInstance, namespaces,
		"the PodNetworkInstance is namespaced and its finalizer releases Azure networking state, "+
			"so leaving it to the namespace cascade would wedge the namespace in Terminating")
	assert.Less(t, cascadeCovered, namespaces,
		"cascade-covered desires must stop being reconciled before their namespace is deleted, "+
			"otherwise kube-applier retries an apply into a Terminating namespace that can never succeed")
}

// TestPodNetworkIsNeverLeftToTheCascade guards the specific mistake that the
// cascade-covered set invites. PodNetwork is cluster-scoped, so no namespace
// delete can ever reclaim it; dropping its document rather than deleting the
// object would strand the PodNetwork and its Azure subnet delegation for good.
func TestPodNetworkIsNeverLeftToTheCascade(t *testing.T) {
	t.Parallel()

	for _, desireName := range []string{DesireNamePodNetwork, DesireNamePodNetworkInstance} {
		assert.False(t, cascadeCoveredDesireNames.has(desireName),
			"%s holds Azure state behind a finalizer and must be deleted by its own step, "+
				"not dropped and left to the namespace cascade", desireName)
	}

	mapping, err := restmapper.Mapper.RESTMapping(
		schema.GroupKind{Group: "multitenancy.acn.azure.com", Kind: "PodNetwork"}, "v1alpha1")
	require.NoError(t, err, "PodNetwork should be resolvable")
	assert.Equal(t, meta.RESTScopeNameRoot, mapping.Scope.Name(),
		"this test exists because PodNetwork is cluster-scoped; if that changed, revisit whether "+
			"swiftPodNetworkRemovalStep is still needed")
}

func newOwnedClusterDesire(name string) *kubeapplierapi.ApplyDesire {
	resourceIDStr := kubeapplierapihelpers.ToClusterScopedApplyDesireResourceIDString(
		testSubscriptionID, testResourceGroupName, testClusterName, name,
	)
	return newOwnedDesire(resourceIDStr, name)
}

func newOwnedNodePoolDesire(nodePoolName, name string) *kubeapplierapi.ApplyDesire {
	resourceIDStr := kubeapplierapihelpers.ToNodePoolScopedApplyDesireResourceIDString(
		testSubscriptionID, testResourceGroupName, testClusterName, nodePoolName, name,
	)
	return newOwnedDesire(resourceIDStr, name)
}

func newOwnedDesire(resourceIDStr, name string) *kubeapplierapi.ApplyDesire {
	return &kubeapplierapi.ApplyDesire{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   metadataapi.Must(azcorearm.ParseResourceID(resourceIDStr)),
			PartitionKey: strings.ToLower(testManagementClusterResourceID.String()),
		},
		Tags: map[string]string{kubeapplierapi.TagControllerName: ClusterResourcesControllerName},
		Spec: kubeapplierapi.ApplyDesireSpec{
			ManagementCluster: testManagementClusterResourceID,
			Type:              kubeapplierapi.ApplyDesireTypeServerSideApply,
			TargetItem: kubeapplierapi.ResourceReference{
				Group: "", Version: "v1", Resource: "configmaps",
				Name: name, Namespace: "ns",
			},
			ServerSideApply: &kubeapplierapi.ServerSideApplyConfig{
				KubeContent: &runtime.RawExtension{Raw: []byte(`{}`)},
			},
		},
	}
}

// destructFixture wires a controller against mock Cosmos storage and hands back
// the CRUD handles the assertions read through.
type destructFixture struct {
	controller            *clusterResourcesController
	clusterCRUD           applyDesireCRUD
	nodePoolCRUD          applyDesireCRUD
	mockKubeApplierClient *kubeappliercosmosstoragetesting.MockKubeApplierDBClient
}

type applyDesireCRUD = interface {
	Get(ctx context.Context, name string) (*kubeapplierapi.ApplyDesire, error)
}

func newDestructFixture(t *testing.T, ctx context.Context, nodePoolName string, desires ...*kubeapplierapi.ApplyDesire) *destructFixture {
	t.Helper()

	mockKubeApplierClient := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClient()
	mockClients := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClients()
	mockClients.Register(testManagementClusterResourceID, mockKubeApplierClient)

	clusterCRUD, err := mockKubeApplierClient.ApplyDesiresForCluster(testSubscriptionID, testResourceGroupName, testClusterName)
	require.NoError(t, err, "cluster-scoped CRUD")
	nodePoolCRUD, err := mockKubeApplierClient.ApplyDesiresForNodePool(testSubscriptionID, testResourceGroupName, testClusterName, nodePoolName)
	require.NoError(t, err, "nodepool-scoped CRUD")

	for _, desire := range desires {
		crud := clusterCRUD
		if strings.Contains(strings.ToLower(desire.ResourceID.String()), "/nodepools/") {
			crud = nodePoolCRUD
		}
		_, err := crud.Create(ctx, desire, nil)
		require.NoError(t, err, "seed desire %s", desire.ResourceID.Name)
	}

	mcLister := &fleetlistertesting.SliceManagementClusterLister{
		ManagementClusters: []*fleetapi.ManagementCluster{
			{CosmosMetadata: coreapi.CosmosMetadata{ResourceID: testManagementClusterResourceID}},
		},
	}

	return &destructFixture{
		controller: &clusterResourcesController{
			kubeApplierDBClients: mockClients,
			applyDesireLister:    &kubeapplierlistertesting.DBApplyDesireLister{Clients: mockClients, Lister: mcLister},
			readDesireLister:     &kubeapplierlistertesting.DBReadDesireLister{Clients: mockClients, Lister: mcLister},
		},
		clusterCRUD:           clusterCRUD,
		nodePoolCRUD:          nodePoolCRUD,
		mockKubeApplierClient: mockKubeApplierClient,
	}
}

// newDestructFixtureWithReadDesires seeds both ApplyDesires and ReadDesires.
// Use this when testing ReadDesire cleanup paths.
func newDestructFixtureWithReadDesires(t *testing.T, ctx context.Context, nodePoolName string, applyDesires []*kubeapplierapi.ApplyDesire, readDesireNames ...string) *destructFixture {
	t.Helper()

	fixture := newDestructFixture(t, ctx, nodePoolName, applyDesires...)

	// Create ReadDesires for the specified names
	clusterReadDesireCRUD, err := fixture.mockKubeApplierClient.ReadDesiresForCluster(testSubscriptionID, testResourceGroupName, testClusterName)
	require.NoError(t, err, "cluster-scoped ReadDesire CRUD")

	nodePoolReadDesireCRUD, err := fixture.mockKubeApplierClient.ReadDesiresForNodePool(testSubscriptionID, testResourceGroupName, testClusterName, nodePoolName)
	require.NoError(t, err, "nodepool-scoped ReadDesire CRUD")

	for _, name := range readDesireNames {
		var resourceIDStr string

		// Determine scope based on name
		isNodePoolScoped := name == DesireNameNodePool
		if isNodePoolScoped {
			resourceIDStr = kubeapplierapihelpers.ToNodePoolScopedReadDesireResourceIDString(
				testSubscriptionID, testResourceGroupName, testClusterName, nodePoolName, name,
			)
		} else {
			resourceIDStr = kubeapplierapihelpers.ToClusterScopedReadDesireResourceIDString(
				testSubscriptionID, testResourceGroupName, testClusterName, name,
			)
		}

		readDesire := &kubeapplierapi.ReadDesire{
			CosmosMetadata: coreapi.CosmosMetadata{
				ResourceID:   metadataapi.Must(azcorearm.ParseResourceID(resourceIDStr)),
				PartitionKey: strings.ToLower(testManagementClusterResourceID.String()),
			},
			Tags: map[string]string{kubeapplierapi.TagControllerName: ClusterResourcesControllerName},
			Spec: kubeapplierapi.ReadDesireSpec{
				ManagementCluster: testManagementClusterResourceID,
				TargetItem: kubeapplierapi.ResourceReference{
					Group: "", Version: "v1", Resource: "configmaps",
					Name: name, Namespace: "ns",
				},
			},
		}

		if isNodePoolScoped {
			_, err := nodePoolReadDesireCRUD.Create(ctx, readDesire, nil)
			require.NoError(t, err, "seed ReadDesire %s", name)
		} else {
			_, err := clusterReadDesireCRUD.Create(ctx, readDesire, nil)
			require.NoError(t, err, "seed ReadDesire %s", name)
		}
	}

	return fixture
}

// TestDeleteAllOwnedApplyDesires walks the chain and pins the behaviours that
// distinguish it from the old "delete every document" cleanup: each waited-on
// step blocks everything behind it until deletion completes. Desires covered by
// HostedCluster or namespace cleanup have their documents dropped immediately.
func TestDeleteAllOwnedApplyDesires(t *testing.T) {
	t.Parallel()

	const testNodePoolName = "np-1"

	// markDeleted stands in for the kube-applier having completed a delete.
	// EqualFold because ResourceID.Name comes back lower-cased.
	markDeleted := func(desires []*kubeapplierapi.ApplyDesire, names ...string) {
		for _, desire := range desires {
			for _, name := range names {
				if !strings.EqualFold(desire.ResourceID.Name, name) {
					continue
				}
				desire.Spec.Type = kubeapplierapi.ApplyDesireTypeDelete
				desire.Spec.ServerSideApply = nil
				desire.Status.Conditions = []metav1.Condition{
					{Type: kubeapplierapi.ConditionTypeSuccessfullyDeleted, Status: metav1.ConditionTrue},
				}
			}
		}
	}

	seedDesires := func() []*kubeapplierapi.ApplyDesire {
		return []*kubeapplierapi.ApplyDesire{
			newOwnedClusterDesire(DesireNameHostedCluster),
			newOwnedClusterDesire(DesireNamePodNetworkInstance),
			newOwnedClusterDesire(DesireNamePodNetwork),
			newOwnedClusterDesire(DesireNameOCPPullSecret),
			newOwnedClusterDesire(DesireNameHostedClusterNamespace),
			newOwnedClusterDesire(DesireNameControlPlaneNamespace),
			newOwnedNodePoolDesire(testNodePoolName, DesireNameNodePool),
		}
	}

	downstreamOfHostedCluster := []string{
		DesireNamePodNetworkInstance, DesireNamePodNetwork,
		DesireNameHostedClusterNamespace, DesireNameControlPlaneNamespace,
	}

	ingressNames := []string{
		DesireNameDefaultIngressConfigMap,
		DesireNameDefaultIngressWildcardCertSecretProviderClass,
		DesireNameDefaultIngressWildcardCertSecretSync,
	}
	seedIngressDesires := func() []*kubeapplierapi.ApplyDesire {
		desires := seedDesires()
		for _, name := range ingressNames {
			desire := newOwnedClusterDesire(name)
			desire.Spec.TargetItem.Namespace = "open-cluster-management-policies"
			desires = append(desires, desire)
		}
		return desires
	}

	t.Run("waits for ManagedCluster deletion before touching ingress or HostedCluster", func(t *testing.T) {
		t.Parallel()
		ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
		desires := append(seedIngressDesires(), newOwnedClusterDesire(DesireNameManagedCluster))
		fixture := newDestructFixture(t, ctx, testNodePoolName, desires...)

		err := fixture.controller.deleteAllOwnedApplyDesires(ctx, testKey(), testManagementClusterResourceID)
		require.NoError(t, err, "start ManagedCluster deletion")
		managedCluster, err := fixture.clusterCRUD.Get(ctx, DesireNameManagedCluster)
		require.NoError(t, err, "ManagedCluster desire should remain while deletion is pending")
		assert.Equal(t, kubeapplierapi.ApplyDesireTypeDelete, managedCluster.Spec.Type, "ManagedCluster must be explicitly deleted")
		assert.Nil(t, managedCluster.Spec.ServerSideApply, "ManagedCluster apply configuration must be cleared")
		_, err = fixture.nodePoolCRUD.Get(ctx, DesireNameNodePool)
		assert.True(t, cosmosstorageutils.IsNotFoundError(err), "NodePool reconciliation must stop before ManagedCluster deletion completes, got %v", err)
		for _, name := range append([]string{DesireNameHostedCluster}, ingressNames...) {
			desire, err := fixture.clusterCRUD.Get(ctx, name)
			require.NoError(t, err, "%s should remain while ManagedCluster deletion is pending", name)
			assert.Equal(t, kubeapplierapi.ApplyDesireTypeServerSideApply, desire.Spec.Type, "%s should not start deleting yet", name)
		}
	})

	for _, pendingName := range ingressNames {
		t.Run("waits for ingress deletion of "+pendingName, func(t *testing.T) {
			t.Parallel()
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
			desires := append(seedIngressDesires(), newOwnedClusterDesire(DesireNameManagedCluster))
			markDeleted(desires, DesireNameManagedCluster)
			fixture := newDestructFixtureWithReadDesires(t, ctx, testNodePoolName, desires, ingressNames...)

			err := fixture.controller.deleteAllOwnedApplyDesires(ctx, testKey(), testManagementClusterResourceID)
			require.NoError(t, err, "start ingress deletion after ManagedCluster cleanup")
			_, err = fixture.clusterCRUD.Get(ctx, DesireNameManagedCluster)
			assert.True(t, cosmosstorageutils.IsNotFoundError(err), "completed ManagedCluster desire should be purged, got %v", err)
			readCRUD, err := fixture.mockKubeApplierClient.ReadDesiresForCluster(testSubscriptionID, testResourceGroupName, testClusterName)
			require.NoError(t, err, "get ingress ReadDesire CRUD")
			applyCRUD, err := fixture.mockKubeApplierClient.ApplyDesiresForCluster(testSubscriptionID, testResourceGroupName, testClusterName)
			require.NoError(t, err, "get ingress ApplyDesire CRUD")
			for _, name := range ingressNames {
				desire, err := applyCRUD.Get(ctx, name)
				require.NoError(t, err, "%s should remain until Kubernetes confirms deletion", name)
				assert.Equal(t, kubeapplierapi.ApplyDesireTypeDelete, desire.Spec.Type, "%s must be explicitly deleted from the shared namespace", name)
				assert.Nil(t, desire.Spec.ServerSideApply, "%s apply configuration must be cleared", name)
				_, err = readCRUD.Get(ctx, name)
				require.NoError(t, err, "%s observation must remain while deletion is pending", name)
				if name != pendingName {
					markDeleted([]*kubeapplierapi.ApplyDesire{desire}, name)
					_, err = applyCRUD.Replace(ctx, desire, nil)
					require.NoError(t, err, "report ingress %s deleted", name)
				}
			}

			// Even a single remaining ingress object must block the entire tail.
			err = fixture.controller.deleteAllOwnedApplyDesires(ctx, testKey(), testManagementClusterResourceID)
			require.NoError(t, err, "reconcile partial ingress deletion")
			for _, name := range ingressNames {
				_, applyErr := applyCRUD.Get(ctx, name)
				_, readErr := readCRUD.Get(ctx, name)
				if name == pendingName {
					require.NoError(t, applyErr, "pending ingress %s must retain its ApplyDesire", name)
					require.NoError(t, readErr, "pending ingress %s must retain its ReadDesire", name)
				} else {
					assert.True(t, cosmosstorageutils.IsNotFoundError(applyErr), "deleted ingress %s ApplyDesire should be purged, got %v", name, applyErr)
					assert.True(t, cosmosstorageutils.IsNotFoundError(readErr), "deleted ingress %s ReadDesire should be purged, got %v", name, readErr)
				}
			}
			for _, name := range append([]string{DesireNameHostedCluster}, downstreamOfHostedCluster...) {
				desire, err := applyCRUD.Get(ctx, name)
				require.NoError(t, err, "%s should remain while ingress %s is pending", name, pendingName)
				assert.Equal(t, kubeapplierapi.ApplyDesireTypeServerSideApply, desire.Spec.Type, "%s must wait for every ingress deletion", name)
			}

			pending, err := applyCRUD.Get(ctx, pendingName)
			require.NoError(t, err, "get final pending ingress desire")
			markDeleted([]*kubeapplierapi.ApplyDesire{pending}, pendingName)
			_, err = applyCRUD.Replace(ctx, pending, nil)
			require.NoError(t, err, "report final ingress deletion")
			err = fixture.controller.deleteAllOwnedApplyDesires(ctx, testKey(), testManagementClusterResourceID)
			require.NoError(t, err, "continue after all ingress objects are deleted")
			_, err = applyCRUD.Get(ctx, pendingName)
			assert.True(t, cosmosstorageutils.IsNotFoundError(err), "final ingress ApplyDesire should be purged, got %v", err)
			_, err = readCRUD.Get(ctx, pendingName)
			assert.True(t, cosmosstorageutils.IsNotFoundError(err), "final ingress ReadDesire should be purged, got %v", err)
			hostedCluster, err := applyCRUD.Get(ctx, DesireNameHostedCluster)
			require.NoError(t, err, "HostedCluster desire should remain until deletion completes")
			assert.Equal(t, kubeapplierapi.ApplyDesireTypeDelete, hostedCluster.Spec.Type, "HostedCluster deletion should start after ingress cleanup")
		})
	}

	t.Run("drops NodePool and configuration desires and starts HostedCluster deletion on the first pass", func(t *testing.T) {
		t.Parallel()
		ctx := utils.ContextWithLogger(context.Background(), testr.New(t))

		desires := seedDesires()

		fixture := newDestructFixture(t, ctx, testNodePoolName, desires...)

		err := fixture.controller.deleteAllOwnedApplyDesires(ctx, testKey(), testManagementClusterResourceID)
		require.NoError(t, err, "deleteAllOwnedApplyDesires should succeed")

		_, err = fixture.nodePoolCRUD.Get(ctx, DesireNameNodePool)
		assert.True(t, cosmosstorageutils.IsNotFoundError(err), "NodePool ApplyDesire should be dropped without waiting for deletion status, got %v", err)
		_, err = fixture.clusterCRUD.Get(ctx, DesireNameOCPPullSecret)
		assert.True(t, cosmosstorageutils.IsNotFoundError(err), "configuration ApplyDesire should be dropped on the first pass, got %v", err)

		hostedCluster, err := fixture.clusterCRUD.Get(ctx, DesireNameHostedCluster)
		require.NoError(t, err, "HostedCluster desire should still exist while its delete is pending")
		assert.Equal(t, kubeapplierapi.ApplyDesireTypeDelete, hostedCluster.Spec.Type,
			"HostedCluster desire should be flipped to Delete")

		for _, name := range downstreamOfHostedCluster {
			desire, err := fixture.clusterCRUD.Get(ctx, name)
			require.NoError(t, err, "%s should be untouched while the HostedCluster is still deleting", name)
			assert.Equal(t, kubeapplierapi.ApplyDesireTypeServerSideApply, desire.Spec.Type,
				"%s should not be marked for deletion yet", name)
		}
	})

	t.Run("waits for namespace cleanup of an orphan NodePool when HostedCluster is absent", func(t *testing.T) {
		t.Parallel()
		ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
		fixture := newDestructFixtureWithReadDesires(t, ctx, testNodePoolName, []*kubeapplierapi.ApplyDesire{
			newOwnedNodePoolDesire(testNodePoolName, DesireNameNodePool),
			newOwnedClusterDesire(DesireNameHostedClusterNamespace),
			newOwnedClusterDesire(DesireNameControlPlaneNamespace),
		}, DesireNameNodePool)

		// The controller can request namespace cleanup, but cannot declare it
		// complete until kube-applier observes the namespaces gone. This does
		// not simulate Kubernetes GC or HyperShift's NodePool finalizer.
		for range 2 {
			err := fixture.controller.deleteAllOwnedApplyDesires(ctx, testKey(), testManagementClusterResourceID)
			require.NoError(t, err, "reconcile orphan NodePool cleanup without HostedCluster")
			_, err = fixture.nodePoolCRUD.Get(ctx, DesireNameNodePool)
			assert.True(t, cosmosstorageutils.IsNotFoundError(err), "orphan NodePool must stop being applied, got %v", err)
			for _, name := range []string{DesireNameHostedClusterNamespace, DesireNameControlPlaneNamespace} {
				desire, err := fixture.clusterCRUD.Get(ctx, name)
				require.NoError(t, err, "%s must remain pending until namespace cleanup completes", name)
				assert.Equal(t, kubeapplierapi.ApplyDesireTypeDelete, desire.Spec.Type, "%s must request deletion", name)
			}
			readCRUD, err := fixture.mockKubeApplierClient.ReadDesiresForNodePool(testSubscriptionID, testResourceGroupName, testClusterName, testNodePoolName)
			require.NoError(t, err, "get orphan NodePool ReadDesire CRUD")
			_, err = readCRUD.Get(ctx, DesireNameNodePool)
			require.NoError(t, err, "orphan NodePool observation must remain until namespace cleanup completes")
		}
	})

	// The SWIFT resources hold Azure networking state behind finalizers, so the
	// chain has to wait them out rather than drop their documents and let the
	// namespace cascade run. PodNetwork in particular is cluster-scoped, so no
	// cascade would ever reach it.
	t.Run("waits out the PodNetworkInstance before deleting the PodNetwork", func(t *testing.T) {
		t.Parallel()
		ctx := utils.ContextWithLogger(context.Background(), testr.New(t))

		desires := seedDesires()
		markDeleted(desires, DesireNameHostedCluster)

		fixture := newDestructFixture(t, ctx, testNodePoolName, desires...)

		err := fixture.controller.deleteAllOwnedApplyDesires(ctx, testKey(), testManagementClusterResourceID)
		require.NoError(t, err, "deleteAllOwnedApplyDesires should succeed")

		podNetworkInstance, err := fixture.clusterCRUD.Get(ctx, DesireNamePodNetworkInstance)
		require.NoError(t, err, "PodNetworkInstance should still exist while its finalizer runs")
		assert.Equal(t, kubeapplierapi.ApplyDesireTypeDelete, podNetworkInstance.Spec.Type,
			"PodNetworkInstance should be deleted through the kube-applier, not dropped")

		podNetwork, err := fixture.clusterCRUD.Get(ctx, DesireNamePodNetwork)
		require.NoError(t, err, "PodNetwork should not be touched yet")
		assert.Equal(t, kubeapplierapi.ApplyDesireTypeServerSideApply, podNetwork.Spec.Type,
			"PodNetwork stays InUse while the instance references it, so its delete must wait")

		for _, name := range []string{DesireNameHostedClusterNamespace, DesireNameControlPlaneNamespace} {
			namespace, err := fixture.clusterCRUD.Get(ctx, name)
			require.NoError(t, err, "%s should not be touched yet", name)
			assert.Equal(t, kubeapplierapi.ApplyDesireTypeServerSideApply, namespace.Spec.Type,
				"%s must not be deleted while a namespaced SWIFT finalizer is still running", name)
		}
	})

	t.Run("deletes the PodNetwork once its instance is gone", func(t *testing.T) {
		t.Parallel()
		ctx := utils.ContextWithLogger(context.Background(), testr.New(t))

		desires := seedDesires()
		markDeleted(desires, DesireNameHostedCluster, DesireNamePodNetworkInstance)

		fixture := newDestructFixture(t, ctx, testNodePoolName, desires...)

		err := fixture.controller.deleteAllOwnedApplyDesires(ctx, testKey(), testManagementClusterResourceID)
		require.NoError(t, err, "deleteAllOwnedApplyDesires should succeed")

		_, err = fixture.clusterCRUD.Get(ctx, DesireNamePodNetworkInstance)
		assert.Error(t, err, "PodNetworkInstance should be purged once its delete succeeded")

		podNetwork, err := fixture.clusterCRUD.Get(ctx, DesireNamePodNetwork)
		require.NoError(t, err, "PodNetwork should still exist while its delete is pending")
		assert.Equal(t, kubeapplierapi.ApplyDesireTypeDelete, podNetwork.Spec.Type,
			"cluster-scoped PodNetwork must be deleted explicitly: no namespace cascade can reclaim it")
		assert.Nil(t, podNetwork.Spec.ServerSideApply, "PodNetwork ServerSideApply should be cleared")
	})

	t.Run("deletes the namespaces once every waited-on step has drained", func(t *testing.T) {
		t.Parallel()
		ctx := utils.ContextWithLogger(context.Background(), testr.New(t))

		desires := seedDesires()
		markDeleted(desires,
			DesireNameHostedCluster, DesireNamePodNetworkInstance, DesireNamePodNetwork)

		fixture := newDestructFixture(t, ctx, testNodePoolName, desires...)

		err := fixture.controller.deleteAllOwnedApplyDesires(ctx, testKey(), testManagementClusterResourceID)
		require.NoError(t, err, "deleteAllOwnedApplyDesires should succeed")

		for _, name := range []string{DesireNameHostedCluster, DesireNamePodNetworkInstance, DesireNamePodNetwork} {
			_, err := fixture.clusterCRUD.Get(ctx, name)
			assert.Error(t, err, "%s should be purged once its delete succeeded", name)
		}
		_, err = fixture.nodePoolCRUD.Get(ctx, DesireNameNodePool)
		assert.Error(t, err, "nodepool-scoped NodePool ApplyDesire should be dropped without waiting for deletion status")

		// Cascade-covered desires are dropped outright, never flipped to Delete,
		// so the kube-applier issues no delete of its own and the namespace
		// cascade reclaims the objects.
		_, err = fixture.clusterCRUD.Get(ctx, DesireNameOCPPullSecret)
		assert.Error(t, err, "OCPPullSecret document should be dropped, leaving its object to the namespace cascade")

		// Namespaces are deleted deliberately and waited on, so they are still
		// present, now marked for deletion.
		for _, name := range []string{DesireNameHostedClusterNamespace, DesireNameControlPlaneNamespace} {
			desire, err := fixture.clusterCRUD.Get(ctx, name)
			require.NoError(t, err, "%s should still exist while its delete is pending", name)
			assert.Equal(t, kubeapplierapi.ApplyDesireTypeDelete, desire.Spec.Type,
				"%s should be flipped to Delete", name)
			assert.Nil(t, desire.Spec.ServerSideApply, "%s ServerSideApply should be cleared", name)
		}
	})

	t.Run("leaves desires owned by other controllers alone", func(t *testing.T) {
		t.Parallel()
		ctx := utils.ContextWithLogger(context.Background(), testr.New(t))

		foreign := newOwnedClusterDesire(DesireNameOCPPullSecret)
		foreign.Tags = nil

		fixture := newDestructFixture(t, ctx, testNodePoolName, foreign)

		err := fixture.controller.deleteAllOwnedApplyDesires(ctx, testKey(), testManagementClusterResourceID)
		require.NoError(t, err, "deleteAllOwnedApplyDesires should succeed")

		desire, err := fixture.clusterCRUD.Get(ctx, DesireNameOCPPullSecret)
		require.NoError(t, err, "untagged desire should still exist")
		assert.Equal(t, kubeapplierapi.ApplyDesireTypeServerSideApply, desire.Spec.Type,
			"untagged desire should be untouched")
	})

	t.Run("deletes paired ReadDesires and retains NodePool observations until namespaces are gone", func(t *testing.T) {
		t.Parallel()
		ctx := utils.ContextWithLogger(context.Background(), testr.New(t))

		desires := seedDesires()
		markDeleted(desires, DesireNameHostedCluster,
			DesireNamePodNetworkInstance, DesireNamePodNetwork)

		fixture := newDestructFixtureWithReadDesires(t, ctx, testNodePoolName, desires,
			DesireNameNodePool, DesireNamePodNetworkInstance, DesireNamePodNetwork)

		err := fixture.controller.deleteAllOwnedApplyDesires(ctx, testKey(), testManagementClusterResourceID)
		require.NoError(t, err, "deleteAllOwnedApplyDesires should succeed")

		// Verify ReadDesires were deleted along with their ApplyDesires
		readDesireCRUD, err := fixture.mockKubeApplierClient.ReadDesiresForCluster(testSubscriptionID, testResourceGroupName, testClusterName)
		require.NoError(t, err, "get ReadDesire CRUD")

		_, err = readDesireCRUD.Get(ctx, DesireNamePodNetworkInstance)
		assert.Error(t, err, "PodNetworkInstance ReadDesire should be deleted with its ApplyDesire")

		_, err = readDesireCRUD.Get(ctx, DesireNamePodNetwork)
		assert.Error(t, err, "PodNetwork ReadDesire should be deleted with its ApplyDesire")

		nodePoolReadDesireCRUD, err := fixture.mockKubeApplierClient.ReadDesiresForNodePool(testSubscriptionID, testResourceGroupName, testClusterName, testNodePoolName)
		require.NoError(t, err, "get NodePool ReadDesire CRUD")

		_, err = nodePoolReadDesireCRUD.Get(ctx, DesireNameNodePool)
		require.NoError(t, err, "NodePool ReadDesire must remain while namespace deletion is pending")

		// Simulate kube-applier completing namespace deletion, then reconcile
		// the same fixture again to exercise the orphan sweep.
		clusterCRUD, err := fixture.mockKubeApplierClient.ApplyDesiresForCluster(testSubscriptionID, testResourceGroupName, testClusterName)
		require.NoError(t, err, "get writable cluster ApplyDesire CRUD")
		for _, name := range []string{DesireNameHostedClusterNamespace, DesireNameControlPlaneNamespace} {
			desire, err := clusterCRUD.Get(ctx, name)
			require.NoError(t, err, "namespace desire %s must exist until deletion succeeds", name)
			markDeleted([]*kubeapplierapi.ApplyDesire{desire}, name)
			_, err = clusterCRUD.Replace(ctx, desire, nil)
			require.NoError(t, err, "report namespace %s deleted", name)
		}
		err = fixture.controller.deleteAllOwnedApplyDesires(ctx, testKey(), testManagementClusterResourceID)
		require.NoError(t, err, "cleanup should finish after namespace deletion")
		_, err = nodePoolReadDesireCRUD.Get(ctx, DesireNameNodePool)
		assert.True(t, cosmosstorageutils.IsNotFoundError(err), "orphaned NodePool ReadDesire should be swept, got %v", err)
	})

	t.Run("sweeps orphaned ReadDesires after ApplyDesires are gone", func(t *testing.T) {
		t.Parallel()
		ctx := utils.ContextWithLogger(context.Background(), testr.New(t))

		// Seed only namespaces as ApplyDesires (fully drained chain),
		// but add orphaned ReadDesires for resources whose ApplyDesires are already gone
		desires := []*kubeapplierapi.ApplyDesire{
			newOwnedClusterDesire(DesireNameHostedClusterNamespace),
			newOwnedClusterDesire(DesireNameControlPlaneNamespace),
		}
		// Mark namespace desires as deleted so the chain completes and sweep runs
		markDeleted(desires, DesireNameHostedClusterNamespace, DesireNameControlPlaneNamespace)

		fixture := newDestructFixtureWithReadDesires(t, ctx, testNodePoolName, desires,
			DesireNamePodNetworkInstance, DesireNamePodNetwork)

		// Verify orphaned ReadDesires exist before cleanup
		readDesireCRUD, err := fixture.mockKubeApplierClient.ReadDesiresForCluster(testSubscriptionID, testResourceGroupName, testClusterName)
		require.NoError(t, err, "get ReadDesire CRUD")

		_, err = readDesireCRUD.Get(ctx, DesireNamePodNetworkInstance)
		require.NoError(t, err, "orphaned PodNetworkInstance ReadDesire should exist before sweep")

		_, err = readDesireCRUD.Get(ctx, DesireNamePodNetwork)
		require.NoError(t, err, "orphaned PodNetwork ReadDesire should exist before sweep")

		// Run deletion - should sweep orphans
		err = fixture.controller.deleteAllOwnedApplyDesires(ctx, testKey(), testManagementClusterResourceID)
		require.NoError(t, err, "deleteAllOwnedApplyDesires should succeed")

		// Verify orphaned ReadDesires were swept
		_, err = readDesireCRUD.Get(ctx, DesireNamePodNetworkInstance)
		assert.Error(t, err, "orphaned PodNetworkInstance ReadDesire should be swept")

		_, err = readDesireCRUD.Get(ctx, DesireNamePodNetwork)
		assert.Error(t, err, "orphaned PodNetwork ReadDesire should be swept")
	})
}
