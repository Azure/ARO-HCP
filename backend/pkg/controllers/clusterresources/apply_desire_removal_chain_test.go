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
		"hosted-cluster":           hostedClusterDesireNames,
		"swift-podnetworkinstance": swiftPodNetworkInstanceDesireNames,
		"swift-podnetwork":         swiftPodNetworkDesireNames,
		"cascade-covered":          cascadeCoveredDesireNames,
		"namespaces":               namespaceDesireNames,
	}

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
// being slow; the last one keeps the chain from stalling Cluster Service's own
// teardown.
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

	hostedCluster := indexOf(hostedClusterRemovalStep{})
	podNetworkInstance := indexOf(swiftPodNetworkInstanceRemovalStep{})
	podNetwork := indexOf(swiftPodNetworkRemovalStep{})
	cascadeCovered := indexOf(cascadeCoveredRemovalStep{})
	namespaces := indexOf(namespacesRemovalStep{})

	require.NotEqual(t, -1, hostedCluster, "hosted-cluster step must be in the chain")
	require.NotEqual(t, -1, podNetworkInstance, "swift-podnetworkinstance step must be in the chain")
	require.NotEqual(t, -1, podNetwork, "swift-podnetwork step must be in the chain")
	require.NotEqual(t, -1, cascadeCovered, "cascade-covered step must be in the chain")
	require.NotEqual(t, -1, namespaces, "namespaces step must be in the chain")

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
	assert.Less(t, cascadeCovered, hostedCluster,
		"cascade-covered waits for nothing, so it must not sit behind a waited-on step: while the "+
			"NodePool desire is live the kube-applier keeps re-applying the CR that Cluster Service "+
			"is deleting through its ManifestWork, and foreground propagation stalls that deletion")
}

// TestPodNetworkIsNeverLeftToTheCascade guards the specific mistake that the
// cascade-covered set invites. PodNetwork is cluster-scoped, so no namespace
// delete can ever reclaim it; dropping its document rather than deleting the
// object would strand the PodNetwork and its Azure subnet delegation for good.
func TestPodNetworkIsNeverLeftToTheCascade(t *testing.T) {
	t.Parallel()

	for _, desireName := range []string{desireNamePodNetwork, desireNamePodNetworkInstance} {
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
	resourceIDStr := kubeapplierapi.ToClusterScopedApplyDesireResourceIDString(
		testSubscriptionID, testResourceGroupName, testClusterName, name,
	)
	return newOwnedDesire(resourceIDStr, name)
}

func newOwnedNodePoolDesire(nodePoolName, name string) *kubeapplierapi.ApplyDesire {
	resourceIDStr := kubeapplierapi.ToNodePoolScopedApplyDesireResourceIDString(
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
	controller   *clusterResourcesController
	clusterCRUD  applyDesireCRUD
	nodePoolCRUD applyDesireCRUD
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
		},
		clusterCRUD:  clusterCRUD,
		nodePoolCRUD: nodePoolCRUD,
	}
}

// TestDeleteAllOwnedApplyDesires walks the chain and pins the behaviours that
// distinguish it from the old "delete every document" cleanup: each waited-on
// step blocks everything behind it until its finalizer completes, and only the
// genuinely inert desires have their documents dropped.
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
			newOwnedClusterDesire(desireNameHostedCluster),
			newOwnedClusterDesire(desireNamePodNetworkInstance),
			newOwnedClusterDesire(desireNamePodNetwork),
			newOwnedClusterDesire(desireNameOCPPullSecret),
			newOwnedClusterDesire(desireNameHostedClusterNamespace),
			newOwnedClusterDesire(desireNameControlPlaneNamespace),
			newOwnedNodePoolDesire(testNodePoolName, desireNameNodePool),
		}
	}

	downstreamOfHostedCluster := []string{
		desireNamePodNetworkInstance, desireNamePodNetwork,
		desireNameHostedClusterNamespace, desireNameControlPlaneNamespace,
	}

	t.Run("drops the cascade-covered desires on the first pass, then blocks on the HostedCluster", func(t *testing.T) {
		t.Parallel()
		ctx := utils.ContextWithLogger(context.Background(), testr.New(t))

		fixture := newDestructFixture(t, ctx, testNodePoolName, seedDesires()...)

		err := fixture.controller.deleteAllOwnedApplyDesires(ctx, testKey(), testManagementClusterResourceID)
		require.NoError(t, err, "deleteAllOwnedApplyDesires should succeed")

		// cascade-covered waits for nothing, so its documents are gone before the
		// HostedCluster delete is even issued. That is what stops the kube-applier
		// re-applying the NodePool CR that Cluster Service is concurrently
		// deleting through its ManifestWork.
		_, err = fixture.nodePoolCRUD.Get(ctx, desireNameNodePool)
		assert.Error(t, err,
			"NodePool document should be dropped on the first pass, not queued behind the HostedCluster")
		_, err = fixture.clusterCRUD.Get(ctx, desireNameOCPPullSecret)
		assert.Error(t, err, "OCPPullSecret document should be dropped on the first pass")

		hostedCluster, err := fixture.clusterCRUD.Get(ctx, desireNameHostedCluster)
		require.NoError(t, err, "HostedCluster desire should still exist while its delete is pending")
		assert.Equal(t, kubeapplierapi.ApplyDesireTypeDelete, hostedCluster.Spec.Type,
			"HostedCluster desire should be flipped to Delete")

		// The chain stopped on the HostedCluster, so nothing downstream moved.
		for _, name := range downstreamOfHostedCluster {
			desire, err := fixture.clusterCRUD.Get(ctx, name)
			require.NoError(t, err, "%s should be untouched while the HostedCluster is still deleting", name)
			assert.Equal(t, kubeapplierapi.ApplyDesireTypeServerSideApply, desire.Spec.Type,
				"%s should not be marked for deletion yet", name)
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
		markDeleted(desires, desireNameHostedCluster)

		fixture := newDestructFixture(t, ctx, testNodePoolName, desires...)

		err := fixture.controller.deleteAllOwnedApplyDesires(ctx, testKey(), testManagementClusterResourceID)
		require.NoError(t, err, "deleteAllOwnedApplyDesires should succeed")

		podNetworkInstance, err := fixture.clusterCRUD.Get(ctx, desireNamePodNetworkInstance)
		require.NoError(t, err, "PodNetworkInstance should still exist while its finalizer runs")
		assert.Equal(t, kubeapplierapi.ApplyDesireTypeDelete, podNetworkInstance.Spec.Type,
			"PodNetworkInstance should be deleted through the kube-applier, not dropped")

		podNetwork, err := fixture.clusterCRUD.Get(ctx, desireNamePodNetwork)
		require.NoError(t, err, "PodNetwork should not be touched yet")
		assert.Equal(t, kubeapplierapi.ApplyDesireTypeServerSideApply, podNetwork.Spec.Type,
			"PodNetwork stays InUse while the instance references it, so its delete must wait")

		for _, name := range []string{desireNameHostedClusterNamespace, desireNameControlPlaneNamespace} {
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
		markDeleted(desires, desireNameHostedCluster, desireNamePodNetworkInstance)

		fixture := newDestructFixture(t, ctx, testNodePoolName, desires...)

		err := fixture.controller.deleteAllOwnedApplyDesires(ctx, testKey(), testManagementClusterResourceID)
		require.NoError(t, err, "deleteAllOwnedApplyDesires should succeed")

		_, err = fixture.clusterCRUD.Get(ctx, desireNamePodNetworkInstance)
		assert.Error(t, err, "PodNetworkInstance should be purged once its delete succeeded")

		podNetwork, err := fixture.clusterCRUD.Get(ctx, desireNamePodNetwork)
		require.NoError(t, err, "PodNetwork should still exist while its delete is pending")
		assert.Equal(t, kubeapplierapi.ApplyDesireTypeDelete, podNetwork.Spec.Type,
			"cluster-scoped PodNetwork must be deleted explicitly: no namespace cascade can reclaim it")
		assert.Nil(t, podNetwork.Spec.ServerSideApply, "PodNetwork ServerSideApply should be cleared")
	})

	t.Run("deletes the namespaces once every waited-on step has drained", func(t *testing.T) {
		t.Parallel()
		ctx := utils.ContextWithLogger(context.Background(), testr.New(t))

		desires := seedDesires()
		markDeleted(desires, desireNameHostedCluster, desireNamePodNetworkInstance, desireNamePodNetwork)

		fixture := newDestructFixture(t, ctx, testNodePoolName, desires...)

		err := fixture.controller.deleteAllOwnedApplyDesires(ctx, testKey(), testManagementClusterResourceID)
		require.NoError(t, err, "deleteAllOwnedApplyDesires should succeed")

		for _, name := range []string{desireNameHostedCluster, desireNamePodNetworkInstance, desireNamePodNetwork} {
			_, err := fixture.clusterCRUD.Get(ctx, name)
			assert.Error(t, err, "%s should be purged once its delete succeeded", name)
		}

		// Cascade-covered desires are dropped outright, never flipped to Delete,
		// so the kube-applier issues no delete of its own and the namespace
		// cascade reclaims the objects.
		_, err = fixture.clusterCRUD.Get(ctx, desireNameOCPPullSecret)
		assert.Error(t, err, "OCPPullSecret document should be dropped, leaving its object to the namespace cascade")
		_, err = fixture.nodePoolCRUD.Get(ctx, desireNameNodePool)
		assert.Error(t, err, "nodepool-scoped NodePool document should be dropped")

		// Namespaces are deleted deliberately and waited on, so they are still
		// present, now marked for deletion.
		for _, name := range []string{desireNameHostedClusterNamespace, desireNameControlPlaneNamespace} {
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

		foreign := newOwnedClusterDesire(desireNameOCPPullSecret)
		foreign.Tags = nil

		fixture := newDestructFixture(t, ctx, testNodePoolName, foreign)

		err := fixture.controller.deleteAllOwnedApplyDesires(ctx, testKey(), testManagementClusterResourceID)
		require.NoError(t, err, "deleteAllOwnedApplyDesires should succeed")

		desire, err := fixture.clusterCRUD.Get(ctx, desireNameOCPPullSecret)
		require.NoError(t, err, "untagged desire should still exist")
		assert.Equal(t, kubeapplierapi.ApplyDesireTypeServerSideApply, desire.Spec.Type,
			"untagged desire should be untouched")
	})
}
