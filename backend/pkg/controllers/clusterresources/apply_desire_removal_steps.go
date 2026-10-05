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
// order they run in live in apply_desire_removal_chain.go.

var (
	_ applyDesireRemovalStep = cascadeCoveredRemovalStep{}
	_ applyDesireRemovalStep = managedClusterRemovalStep{}
	_ applyDesireRemovalStep = ingressManifestRemovalStep{}
	_ applyDesireRemovalStep = hostedClusterRemovalStep{}
	_ applyDesireRemovalStep = swiftPodNetworkInstanceRemovalStep{}
	_ applyDesireRemovalStep = swiftPodNetworkRemovalStep{}
	_ applyDesireRemovalStep = namespacesRemovalStep{}
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

// managedClusterNames is the desire representing the ManagedCluster CR.
var managedClusterNames = newDesireNameSet(DesireNameManagedCluster)

// hostedClusterDesireNames is an ordered, waited-on delete: the
// HostedCluster finalizer deprovisions Azure infrastructure and tears down the
// control plane, and it needs the rest of the namespace intact while it runs.
var hostedClusterDesireNames = newDesireNameSet(DesireNameHostedCluster)

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
	swiftPodNetworkInstanceDesireNames = newDesireNameSet(DesireNamePodNetworkInstance)
	swiftPodNetworkDesireNames         = newDesireNameSet(DesireNamePodNetwork)
)

// cascadeCoveredDesireNames are the desires whose Kubernetes objects are removed
// by HostedCluster teardown or namespace deletion. Dropping the documents stops
// reconciliation while leaving configuration available to the teardown.
//
// NodePool is the exception to the otherwise inert configuration here:
// HyperShift deletes matching NodePools during HostedCluster teardown and sets
// their owner references during reconciliation. Stop applying them first so they
// cannot be recreated during that cleanup. If the HostedCluster is already
// absent, namespace deletion must reclaim any remaining NodePools and wait for
// their finalizers; dropping an ApplyDesire alone does not confirm their removal.
var cascadeCoveredDesireNames = newDesireNameSet(
	DesireNameOCPPullSecret,
	DesireNameBoundServiceAccountSigningKeySecretSync,
	DesireNameKubeAPIServerServingCertSecretSync,
	DesireNameBoundServiceAccountSigningKeySecretProviderClass,
	DesireNameKubeAPIServerServingCertSecretProviderClass,
	DesireNameNodePool,
)

// ingressManifestDesireNames live in open-cluster-management-policies, outside
// the cluster's namespaces, so they need explicit deletion.
var ingressManifestDesireNames = newDesireNameSet(
	DesireNameDefaultIngressConfigMap,
	DesireNameDefaultIngressWildcardCertSecretProviderClass,
	DesireNameDefaultIngressWildcardCertSecretSync,
)

// namespaceDesireNames are the two namespaces, whose
// deletion cascades to everything in cascadeCoveredDesireNames.
var namespaceDesireNames = newDesireNameSet(
	DesireNameHostedClusterNamespace,
	DesireNameControlPlaneNamespace,
)

// managedClusterRemovalStep deletes the cluster-scoped ManagedCluster and waits
// for its cleanup before removing ingress manifests and the HostedCluster.
type managedClusterRemovalStep struct{}

func (managedClusterRemovalStep) name() string { return "managed-cluster" }

func (s managedClusterRemovalStep) remove(
	ctx context.Context,
	kubeApplierDBClient kubeappliercosmosstorage.KubeApplierDBClient,
	owned []*kubeapplierapi.ApplyDesire,
) (bool, error) {
	return ensureMatchingApplyDesiresRemoved(ctx, kubeApplierDBClient, s.name(), owned, managedClusterNames.selects)
}

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

// cascadeCoveredRemovalStep stops reconciling the desires HostedCluster teardown
// or namespace deletion will clean up, by removing their Cosmos documents without
// deleting the Kubernetes objects. It runs first: it waits on nothing, and every pass it
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

// ingressManifestRemovalStep deletes ingress configuration in the shared policy
// namespace and waits for removal before HostedCluster teardown proceeds.
type ingressManifestRemovalStep struct{}

func (ingressManifestRemovalStep) name() string { return "ingress-manifests" }

func (s ingressManifestRemovalStep) remove(
	ctx context.Context,
	kubeApplierDBClient kubeappliercosmosstorage.KubeApplierDBClient,
	owned []*kubeapplierapi.ApplyDesire,
) (bool, error) {
	return ensureMatchingApplyDesiresRemoved(ctx, kubeApplierDBClient, s.name(), owned, ingressManifestDesireNames.selects)
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
		managedClusterNames,
		ingressManifestDesireNames,
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
