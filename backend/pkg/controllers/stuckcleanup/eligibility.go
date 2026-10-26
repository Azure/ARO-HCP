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

package stuckcleanup

import (
	"fmt"
	"strings"
	"time"

	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
)

// Eligibility is a separate question from ownership.
//
// The ownership preflight asks "does anything still own this namespace?" —
// a live ARM Cosmos document, a live CS record, a surviving publisher. Those
// checks answer whether something would be broken by retiring it.
//
// Eligibility asks the prior question: "is this the kind of object this action
// is permitted to touch at all?" An operator who mistypes a namespace name may
// land on a perfectly healthy namespace that happens to have no cluster record,
// and every ownership check would pass. Ownership alone therefore does not
// protect against a mistyped or mistaken request, which is what these checks
// exist for.
//
// Both sets must pass. Eligibility is evaluated first: it is cheaper, and its
// failures are definitive rather than provisional.

const (
	// namespacePhaseTerminating is the only namespace phase this action accepts.
	namespacePhaseTerminating = "Terminating"

	// minimumTerminatingDwell is how long a namespace must have been deleting
	// before it is considered stuck rather than merely in progress.
	//
	// A normal HostedCluster teardown takes minutes: HyperShift's finalizer
	// deprovisions Azure infrastructure before the namespace cascade completes.
	// Accepting a namespace that has only just begun deleting would race that
	// teardown, which is the very mechanism that strands Azure resources and
	// wedges namespaces in the first place.
	minimumTerminatingDwell = time.Hour

	// hostedClusterGroup and hostedClusterResource identify a HostedCluster CR
	// surviving inside the namespace.
	hostedClusterGroup    = "hypershift.openshift.io"
	hostedClusterResource = "hostedclusters"
)

// protectedNamespaceExact are namespaces this action must never target,
// whatever their state.
var protectedNamespaceExact = map[string]bool{
	"default":           true,
	"kube-system":       true,
	"kube-public":       true,
	"kube-node-lease":   true,
	"hypershift":        true,
	"cert-manager":      true,
	"gatekeeper-system": true,
}

// protectedNamespacePrefixes guard the platform's own namespaces.
//
// This is a denylist and not an attempt to positively identify an HCP
// namespace. A prefix is never accepted as proof that a namespace belongs to a
// cluster — that remains the ownership preflight's job. A prefix is perfectly
// good evidence that a namespace is *not* a candidate, which is all this is
// used for.
var protectedNamespacePrefixes = []string{
	"kube-",
	"openshift-",
	"azure-",
	"open-cluster-management",
	"calico-",
	"tigera-",
	"maestro",
}

// eligibilityVerdict is the outcome of the eligibility checks. An ineligible
// namespace produces a machine-readable reason and an operator-readable message.
type eligibilityVerdict struct {
	eligible bool
	reason   string
	message  string
}

func eligible() eligibilityVerdict {
	return eligibilityVerdict{eligible: true}
}

func ineligible(reason, format string, args ...any) eligibilityVerdict {
	return eligibilityVerdict{
		eligible: false,
		reason:   reason,
		message:  fmt.Sprintf(format, args...),
	}
}

// evaluateEligibility decides whether the observed namespace is a legitimate
// target for this action.
func evaluateEligibility(namespaceName string, observed *fleetapi.ObservedTerminatingNamespace, now time.Time) eligibilityVerdict {
	if isProtectedNamespace(namespaceName) {
		return ineligible("ProtectedNamespace",
			"namespace %q is a platform namespace and is never a valid cleanup target", namespaceName)
	}

	if observed == nil {
		return ineligible("NamespaceNotObserved",
			"namespace %q has not been observed, so its eligibility cannot be established", namespaceName)
	}

	// The decisive check against a mistyped namespace name. A healthy namespace
	// is not deleting, and nothing about this action applies to it.
	if observed.Phase != namespacePhaseTerminating {
		return ineligible("NamespaceNotTerminating",
			"namespace %q is in phase %q, not %q; this action only retires namespaces that are already being deleted",
			namespaceName, observed.Phase, namespacePhaseTerminating)
	}

	if observed.DeletionTimestamp == nil {
		return ineligible("NoDeletionTimestamp",
			"namespace %q reports phase %q but carries no deletionTimestamp, which is contradictory; refusing on ambiguous data",
			namespaceName, observed.Phase)
	}

	if dwell := now.Sub(observed.DeletionTimestamp.Time); dwell < minimumTerminatingDwell {
		return ineligible("DeletionInProgress",
			"namespace %q has only been deleting for %s; normal teardown takes minutes, so it is not yet established as stuck (minimum %s)",
			namespaceName, dwell.Round(time.Second), minimumTerminatingDwell)
	}

	// A surviving HostedCluster CR means HyperShift's finalizer may still be
	// deprovisioning Azure infrastructure. Retiring the namespace underneath it
	// would garbage-collect the CR before that finalizer completes, which is
	// precisely how these orphans are created. This is the stalled-deletion
	// case and is out of scope for the first release.
	if hasSurvivingHostedCluster(observed.RemainingContent) {
		return ineligible("HostedClusterPresent",
			"namespace %q still contains a HostedCluster; its deletion is stalled rather than orphaned, which needs a separate case definition",
			namespaceName)
	}

	return eligible()
}

func isProtectedNamespace(namespaceName string) bool {
	normalized := strings.ToLower(strings.TrimSpace(namespaceName))
	if len(normalized) == 0 {
		return true
	}
	if protectedNamespaceExact[normalized] {
		return true
	}
	for _, prefix := range protectedNamespacePrefixes {
		if strings.HasPrefix(normalized, prefix) {
			return true
		}
	}
	return false
}

func hasSurvivingHostedCluster(remaining []fleetapi.RemainingGroupResource) bool {
	for _, groupResource := range remaining {
		if groupResource.Group == hostedClusterGroup && groupResource.Resource == hostedClusterResource {
			return true
		}
	}
	return false
}
