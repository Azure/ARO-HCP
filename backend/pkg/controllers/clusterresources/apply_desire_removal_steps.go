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
	"strings"

	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/kubeappliercosmosstorage"
)

// The concrete steps of applyDesireRemovalChain. The chain contract and the
// order they run in live in apply_desire_destruct_chain.go.

var (
	_ applyDesireRemovalStep = cascadeCoveredRemovalStep{}
	_ applyDesireRemovalStep = hostedClusterRemovalStep{}
	_ applyDesireRemovalStep = swiftPodNetworkInstanceRemovalStep{}
	_ applyDesireRemovalStep = swiftPodNetworkRemovalStep{}
	_ applyDesireRemovalStep = namespacesRemovalStep{}
)

// The desire names classifyClusterResource produces. It returns an error for
// any resource it does not recognize, so this enumeration is closed: a new
// resource type cannot reach Cosmos without being added there first, and adding
// it there without assigning it to one of the sets below trips the
// unclaimed-desire warning in deleteAllOwnedApplyDesires.
const (
	desireNameHostedCluster = "HostedCluster"
	desireNameNodePool      = "NodePool"

	desireNameHostedClusterNamespace = "HostedClusterNamespace"
	desireNameControlPlaneNamespace  = "ControlPlaneNamespace"

	desireNameDefaultIngressConfigMap = "DefaultIngressConfigMap"
	desireNameOCPPullSecret           = "OCPPullSecret"
	desireNamePodNetwork              = "PodNetwork"
	desireNamePodNetworkInstance      = "PodNetworkInstance"

	desireNameBoundServiceAccountSigningKeySecretSync = "BoundServiceAccountSigningKeySecretSync"
	desireNameDefaultIngressWildcardCertSecretSync    = "DefaultIngressWildcardCertSecretSync"
	desireNameKubeAPIServerServingCertSecretSync      = "KubeAPIServerServingCertSecretSync"

	desireNameBoundServiceAccountSigningKeySecretProviderClass = "BoundServiceAccountSigningKeySecretProviderClass"
	desireNameDefaultIngressWildcardCertSecretProviderClass    = "DefaultIngressWildcardCertSecretProviderClass"
	desireNameKubeAPIServerServingCertSecretProviderClass      = "KubeAPIServerServingCertSecretProviderClass"
)

// desireNameSet is a step's claim: the exact desire names it is responsible for.
type desireNameSet map[string]struct{}

// newDesireNameSet folds names to lower case, because that is the only form a
// step will ever see: ToClusterScopedApplyDesireResourceIDString lowercases the
// resource ID it builds, so a desire created as "HostedCluster" reads back with
// ResourceID.Name == "hostedcluster". The constants keep their original casing
// to mirror classifyClusterResource.
func newDesireNameSet(names ...string) desireNameSet {
	set := make(desireNameSet, len(names))
	for _, name := range names {
		set[strings.ToLower(name)] = struct{}{}
	}
	return set
}

// has reports whether the set claims desireName, ignoring case.
func (s desireNameSet) has(desireName string) bool {
	_, ok := s[strings.ToLower(desireName)]
	return ok
}

// selects matches on the desire name rather than spec.targetItem. The names are
// assigned by classifyClusterResource and are stable and intent-bearing, so a
// step says what it tears down instead of inferring it from a GVR.
func (s desireNameSet) selects(desire *kubeapplierapi.ApplyDesire) bool {
	return s.has(desire.ResourceID.Name)
}

// hostedClusterDesireNames is the only thing worth an ordered, waited-on delete:
// the HostedCluster finalizer deprovisions Azure infrastructure and tears down
// the control plane, and it needs the rest of the namespace intact while it runs.
var hostedClusterDesireNames = newDesireNameSet(desireNameHostedCluster)

// The SWIFT networking resources are destroyed in reverse order of creation and
// cannot be left to the namespace cascade.
//
// PodNetworkInstance is namespaced, but its finalizer releases Azure networking
// state that namespace garbage collection knows nothing about, and PodNetwork
// reports InUse while any instance still references it — so the instance has to
// drain before the network can go.
//
// PodNetwork is cluster-scoped (restmapper.go registers it RESTScopeRoot), so no
// namespace delete will ever reclaim it. Dropping its document instead of
// deleting the object would strand the PodNetwork and its Azure subnet
// delegation for good.
var (
	swiftPodNetworkInstanceDesireNames = newDesireNameSet(desireNamePodNetworkInstance)
	swiftPodNetworkDesireNames         = newDesireNameSet(desireNamePodNetwork)
)

// cascadeCoveredDesireNames are the desires whose Kubernetes objects the
// namespace delete will garbage collect for us. They are inert — namespaced, and
// with no finalizer reaching outside their own namespace — so deleting them
// individually would be slower and would pull configuration out from under the
// HostedCluster teardown that is still running. Dropping the document leaves the
// object in place for that teardown to read; only the reconcile stops.
//
// NodePool is here because Cluster Service still dispatches node pool deletion
// itself; see the comment in SyncOnce's node pool branch. Its Azure state is
// deprovisioned by the HostedCluster finalizer, which hostedClusterRemovalStep
// waits out, so nothing is stranded by letting the namespace reclaim the CR.
var cascadeCoveredDesireNames = newDesireNameSet(
	desireNameNodePool,
	desireNameDefaultIngressConfigMap,
	desireNameOCPPullSecret,
	desireNameBoundServiceAccountSigningKeySecretSync,
	desireNameDefaultIngressWildcardCertSecretSync,
	desireNameKubeAPIServerServingCertSecretSync,
	desireNameBoundServiceAccountSigningKeySecretProviderClass,
	desireNameDefaultIngressWildcardCertSecretProviderClass,
	desireNameKubeAPIServerServingCertSecretProviderClass,
)

// namespaceDesireNames are the two namespaces, whose
// deletion cascades to everything in cascadeCoveredDesireNames.
var namespaceDesireNames = newDesireNameSet(
	desireNameHostedClusterNamespace,
	desireNameControlPlaneNamespace,
)

// hostedClusterRemovalStep deletes the HostedCluster CR and waits for HyperShift
// to finish with it. Nothing else in the chain moves until it is gone.
type hostedClusterRemovalStep struct{}

func (hostedClusterRemovalStep) name() string { return "hosted-cluster" }

func (s hostedClusterRemovalStep) remove(
	ctx context.Context,
	kubeApplierDBClient kubeappliercosmosstorage.KubeApplierDBClient,
	owned []*kubeapplierapi.ApplyDesire,
) (bool, error) {
	return ensureMatchingApplyDesiresRemoved(ctx, kubeApplierDBClient, s.name(), owned, hostedClusterDesireNames.selects)
}

// swiftPodNetworkInstanceRemovalStep deletes the PodNetworkInstance and waits
// for its finalizer, so the Azure networking state it holds is released before
// the PodNetwork it references is touched.
type swiftPodNetworkInstanceRemovalStep struct{}

func (swiftPodNetworkInstanceRemovalStep) name() string { return "swift-podnetworkinstance" }

func (s swiftPodNetworkInstanceRemovalStep) remove(
	ctx context.Context,
	kubeApplierDBClient kubeappliercosmosstorage.KubeApplierDBClient,
	owned []*kubeapplierapi.ApplyDesire,
) (bool, error) {
	return ensureMatchingApplyDesiresRemoved(ctx, kubeApplierDBClient, s.name(), owned, swiftPodNetworkInstanceDesireNames.selects)
}

// swiftPodNetworkRemovalStep deletes the cluster-scoped PodNetwork. It runs
// after swiftPodNetworkInstanceRemovalStep because PodNetwork stays InUse while
// an instance references it.
type swiftPodNetworkRemovalStep struct{}

func (swiftPodNetworkRemovalStep) name() string { return "swift-podnetwork" }

func (s swiftPodNetworkRemovalStep) remove(
	ctx context.Context,
	kubeApplierDBClient kubeappliercosmosstorage.KubeApplierDBClient,
	owned []*kubeapplierapi.ApplyDesire,
) (bool, error) {
	return ensureMatchingApplyDesiresRemoved(ctx, kubeApplierDBClient, s.name(), owned, swiftPodNetworkDesireNames.selects)
}

// cascadeCoveredRemovalStep stops reconciling the desires the namespace delete
// will clean up, by removing their Cosmos documents without deleting the
// Kubernetes objects. It runs first: it waits on nothing, and every pass it
// spends queued behind a waited-on step is a pass the kube-applier spends
// re-applying objects — the NodePool CR above all — that Cluster Service is
// concurrently trying to delete. See applyDesireRemovalChain.
//
// It must in any case run before namespacesRemovalStep, since a desire left live
// while its namespace is Terminating makes the kube-applier retry an apply that
// can never succeed.
type cascadeCoveredRemovalStep struct{}

func (cascadeCoveredRemovalStep) name() string { return "cascade-covered" }

func (s cascadeCoveredRemovalStep) remove(
	ctx context.Context,
	kubeApplierDBClient kubeappliercosmosstorage.KubeApplierDBClient,
	owned []*kubeapplierapi.ApplyDesire,
) (bool, error) {
	return deleteMatchingApplyDesiresDocuments(ctx, kubeApplierDBClient, s.name(), owned, cascadeCoveredDesireNames.selects)
}

// namespacesRemovalStep tears down the cluster's namespaces. It only runs once
// previous steps have drained, so by the time a Namespace delete is issued the
// HostedCluster has already been deleted and finalized on its own terms rather
// than being garbage collected along with its namespace.
type namespacesRemovalStep struct{}

func (namespacesRemovalStep) name() string { return "namespaces" }

func (s namespacesRemovalStep) remove(
	ctx context.Context,
	kubeApplierDBClient kubeappliercosmosstorage.KubeApplierDBClient,
	owned []*kubeapplierapi.ApplyDesire,
) (bool, error) {
	return ensureMatchingApplyDesiresRemoved(ctx, kubeApplierDBClient, s.name(), owned, namespaceDesireNames.selects)
}

// claimedDesireNames is every name some step in applyDesireRemovalChain claims.
// Used only to warn about desires no step would tear down.
func claimedDesireNames() desireNameSet {
	claimed := make(desireNameSet)
	for _, set := range []desireNameSet{
		cascadeCoveredDesireNames,
		hostedClusterDesireNames,
		swiftPodNetworkInstanceDesireNames,
		swiftPodNetworkDesireNames,
		namespaceDesireNames,
	} {
		for name := range set {
			claimed[name] = struct{}{}
		}
	}
	return claimed
}
