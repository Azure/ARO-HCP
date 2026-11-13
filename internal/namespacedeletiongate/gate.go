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

package namespacedeletiongate

import (
	"context"
	"fmt"
	"strings"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
)

// Verdict is the outcome of the ownership gate.
//
// It is deliberately tri-state. "No record found" and "could not find out" are
// different answers with opposite safety implications, and collapsing them is
// the single most dangerous mistake this gate could make: a failed lookup is
// never evidence of absence.
type Verdict string

const (
	// VerdictUnowned means every query completed and nothing claims the target.
	// This is the only verdict that permits a mutation.
	VerdictUnowned Verdict = "Unowned"

	// VerdictOwned means a live Cosmos record or a foreign ApplyDesire claims
	// the target. The cluster still exists; use the normal deletion flow.
	VerdictOwned Verdict = "Owned"

	// VerdictIndeterminate means records exist but their identity or placement
	// is contradictory or unrecognized. Blocks, and asks for investigation.
	VerdictIndeterminate Verdict = "Indeterminate"

	// VerdictUnavailable means a query failed, timed out, or could not be read
	// to completion. Blocks, and is retryable once the database is healthy.
	VerdictUnavailable Verdict = "Unavailable"
)

// Result carries the verdict plus the evidence behind it, so that an operator
// message can name the conflicting observation rather than say "validation
// failed".
type Result struct {
	Verdict Verdict

	// OwnerResourceID is the ARM resource ID of the record that claims the
	// target, when Verdict is Owned because of a cluster record.
	OwnerResourceID string

	// OwnerDeleting reports whether that record is already being deleted, when
	// the caller can determine it. An owner in Deleting state still blocks: the
	// normal flow is mid-flight and this API must not race it, so this is only
	// ever used to make the operator message more specific, never to decide.
	OwnerDeleting bool

	// ConflictingDesire names the foreign ApplyDesire that still maintains the
	// target, when Verdict is Owned because of a desire.
	ConflictingDesire string

	// Detail explains an Indeterminate or Unavailable verdict.
	Detail string
}

// Blocks reports whether this result forbids a mutation. Everything except a
// clean Unowned blocks.
func (r Result) Blocks() bool { return r.Verdict != VerdictUnowned }

// Target identifies what the gate is being asked about.
type Target struct {
	// ManagementClusterResourceID scopes the match. Identity is only meaningful
	// within one management cluster: the same namespace name on a different
	// management cluster is a different object.
	ManagementClusterResourceID string

	// HostedClusterNamespace and ControlPlaneNamespace are both checked, not
	// just the one the operator named. A record that owns either namespace owns
	// the cluster, and so blocks a request aimed at the other.
	HostedClusterNamespace string
	ControlPlaneNamespace  string

	// HostedClusterName is matched against desired identity, which is populated
	// before the namespace exists on the management cluster and therefore
	// covers the creation window that observed identity does not.
	HostedClusterName string
}

// ServiceProviderClusterLister is the subset of the resources global listers
// this gate needs.
type ServiceProviderClusterLister interface {
	List(ctx context.Context, options *cosmosstorageutils.DBClientListResourceDocsOptions) (cosmosstorageutils.DBClientIterator[coreapi.ServiceProviderCluster], error)
}

// ApplyDesireLister is the subset of the kube-applier listers this gate needs.
type ApplyDesireLister interface {
	List(ctx context.Context, options *cosmosstorageutils.DBClientListResourceDocsOptions) (cosmosstorageutils.DBClientIterator[kubeapplierapi.ApplyDesire], error)
}

// Evaluate runs the ownership gate against live Cosmos data.
//
// It is deliberately not backed by an informer cache. A cache hit is a fine
// fast blocker, but a cache miss proves nothing and must never authorize a
// mutation, so the transition that matters always takes the live read.
//
// ownedDesirePrefix names the desires belonging to the requesting object, which
// are excluded from the foreign-desire check — a request must not block on
// itself. Observation-only ReadDesires are not consulted at all: reading an
// object is not a claim to own it.
func Evaluate(
	ctx context.Context,
	clusters ServiceProviderClusterLister,
	desires ApplyDesireLister,
	target Target,
	ownedDesirePrefix string,
) Result {
	if result := evaluateClusterRecords(ctx, clusters, target); result.Blocks() {
		return result
	}
	if desires == nil {
		// The desire half of the gate is what covers the creation race, where a
		// cluster has a live ServerSideApply desire for its namespace before its
		// ServiceProviderCluster status is populated. Skipping it silently would
		// leave that window open, so say so rather than return a clean pass.
		return Result{
			Verdict: VerdictIndeterminate,
			Detail:  "no ApplyDesire lister was available, so the creation-race half of the ownership gate could not run",
		}
	}
	return evaluateForeignDesires(ctx, desires, target, ownedDesirePrefix)
}

func evaluateClusterRecords(ctx context.Context, clusters ServiceProviderClusterLister, target Target) Result {
	if clusters == nil {
		return Result{Verdict: VerdictUnavailable, Detail: "no ServiceProviderCluster lister was available"}
	}

	iterator, err := clusters.List(ctx, nil)
	if err != nil {
		return Result{Verdict: VerdictUnavailable, Detail: fmt.Sprintf("listing ServiceProviderClusters failed: %v", err)}
	}

	for _, spc := range iterator.Items(ctx) {
		if spc == nil {
			continue
		}
		if !sameManagementCluster(spc, target.ManagementClusterResourceID) {
			continue
		}
		if matched, detail := matchesTarget(spc, target); matched {
			resourceID := ""
			if spc.GetResourceID() != nil {
				resourceID = spc.GetResourceID().String()
			}
			return Result{
				Verdict:         VerdictOwned,
				OwnerResourceID: resourceID,
				Detail:          detail,
			}
		}
	}

	// Consume every page before concluding absence. A query that stopped early
	// has not shown that nothing matches; it has shown nothing at all.
	if err := iterator.GetError(); err != nil {
		return Result{Verdict: VerdictUnavailable, Detail: fmt.Sprintf("reading ServiceProviderCluster pages failed: %v", err)}
	}

	return Result{Verdict: VerdictUnowned}
}

// sameManagementCluster scopes a candidate record to the management cluster
// under consideration. Spec placement is checked as well as Status because a
// record written but not yet reconciled has only the former.
func sameManagementCluster(spc *coreapi.ServiceProviderCluster, managementClusterResourceID string) bool {
	if len(managementClusterResourceID) == 0 {
		return true
	}
	for _, candidate := range []*azcorearm.ResourceID{
		spc.Spec.ManagementClusterResourceID,
		spc.Status.ManagementClusterResourceID,
	} {
		if candidate != nil && strings.EqualFold(candidate.String(), managementClusterResourceID) {
			return true
		}
	}
	return false
}

// matchesTarget reports whether this record claims either namespace or the
// HostedCluster identity.
//
// A matching namespace blocks even when the operator supplied a different
// HostedCluster name: the namespace is the thing being destroyed, so ownership
// of it is what matters. Name alone is never matched across namespaces.
func matchesTarget(spc *coreapi.ServiceProviderCluster, target Target) (bool, string) {
	if ns := spc.Status.HostedClusterNamespace; len(ns) > 0 {
		if strings.EqualFold(ns, target.HostedClusterNamespace) || strings.EqualFold(ns, target.ControlPlaneNamespace) {
			return true, fmt.Sprintf("observed hostedClusterNamespace %q", ns)
		}
	}
	if ns := spc.Status.ControlPlaneNamespace; len(ns) > 0 {
		if strings.EqualFold(ns, target.HostedClusterNamespace) || strings.EqualFold(ns, target.ControlPlaneNamespace) {
			return true, fmt.Sprintf("observed controlPlaneNamespace %q", ns)
		}
	}

	// Desired identity is populated before the namespace exists on the
	// management cluster, so it covers the window where observed identity is
	// still empty.
	if hc := spc.Spec.DesiredHostedCluster; hc != nil {
		desiredNamespace, desiredName := hc.GetNamespace(), hc.GetName()
		if len(desiredNamespace) > 0 &&
			(strings.EqualFold(desiredNamespace, target.HostedClusterNamespace) ||
				strings.EqualFold(desiredNamespace, target.ControlPlaneNamespace)) {
			return true, fmt.Sprintf("desired hostedCluster namespace %q", desiredNamespace)
		}
		if len(desiredNamespace) > 0 && len(desiredName) > 0 &&
			strings.EqualFold(desiredNamespace, target.HostedClusterNamespace) &&
			strings.EqualFold(desiredName, target.HostedClusterName) {
			return true, fmt.Sprintf("desired hostedCluster %q/%q", desiredNamespace, desiredName)
		}
	}

	return false, ""
}

// evaluateForeignDesires blocks on a surviving ApplyDesire that actively
// maintains either namespace or the HostedCluster.
//
// These are Cosmos desired ownership, so they belong in the same safety gate:
// another controller asserting that an object should exist is a live claim on
// it. This gate never deletes another controller's intent — it refuses and
// names the owner.
func evaluateForeignDesires(ctx context.Context, desires ApplyDesireLister, target Target, ownedDesirePrefix string) Result {
	iterator, err := desires.List(ctx, nil)
	if err != nil {
		return Result{Verdict: VerdictUnavailable, Detail: fmt.Sprintf("listing ApplyDesires failed: %v", err)}
	}

	for _, desire := range iterator.Items(ctx) {
		if desire == nil {
			continue
		}
		// Only ServerSideApply asserts that something should exist. A Delete or
		// FinalizeNamespace desire is tearing the same thing down, which is not
		// a competing claim.
		if desire.Spec.Type != kubeapplierapi.ApplyDesireTypeServerSideApply {
			continue
		}
		name := ""
		if desire.GetResourceID() != nil {
			name = desire.GetResourceID().String()
		}
		if len(ownedDesirePrefix) > 0 && strings.Contains(strings.ToLower(name), strings.ToLower(ownedDesirePrefix)) {
			continue
		}
		if !desireTargetsNamespace(desire, target) {
			continue
		}
		return Result{Verdict: VerdictOwned, ConflictingDesire: name}
	}

	if err := iterator.GetError(); err != nil {
		return Result{Verdict: VerdictUnavailable, Detail: fmt.Sprintf("reading ApplyDesire pages failed: %v", err)}
	}

	return Result{Verdict: VerdictUnowned}
}

func desireTargetsNamespace(desire *kubeapplierapi.ApplyDesire, target Target) bool {
	item := desire.Spec.TargetItem
	for _, ns := range []string{target.HostedClusterNamespace, target.ControlPlaneNamespace} {
		if len(ns) == 0 {
			continue
		}
		// An object inside the namespace.
		if strings.EqualFold(item.Namespace, ns) {
			return true
		}
		// The namespace object itself.
		if len(item.Namespace) == 0 && item.Resource == "namespaces" && strings.EqualFold(item.Name, ns) {
			return true
		}
	}
	return false
}
