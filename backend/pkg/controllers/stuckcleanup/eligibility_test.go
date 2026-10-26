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
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
)

var evaluationTime = time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)

// wedgedNamespace is the shape the action exists to retire: Terminating well
// past the dwell, with leftover content and no HostedCluster.
func wedgedNamespace() *fleetapi.ObservedTerminatingNamespace {
	deletionTimestamp := metav1.NewTime(evaluationTime.Add(-72 * time.Hour))
	return &fleetapi.ObservedTerminatingNamespace{
		UID:               "11111111-2222-3333-4444-555555555555",
		Phase:             namespacePhaseTerminating,
		DeletionTimestamp: &deletionTimestamp,
		SpecFinalizers:    []string{"kubernetes"},
		RemainingContent: []fleetapi.RemainingGroupResource{
			{Group: "policy", Resource: "poddisruptionbudgets"},
		},
	}
}

func TestEvaluateEligibility(t *testing.T) {
	for _, tt := range []struct {
		name          string
		namespaceName string
		observed      func() *fleetapi.ObservedTerminatingNamespace
		wantEligible  bool
		wantReason    string
	}{
		{
			name:          "a genuinely wedged namespace is eligible",
			namespaceName: "ocm-production-abc123-mycluster",
			observed:      wedgedNamespace,
			wantEligible:  true,
		},
		{
			// The case this gate exists for. Every ownership check would pass
			// on a healthy namespace that no cluster record refers to.
			name:          "an Active namespace is refused",
			namespaceName: "ocm-production-abc123-mycluster",
			observed: func() *fleetapi.ObservedTerminatingNamespace {
				observed := wedgedNamespace()
				observed.Phase = "Active"
				observed.DeletionTimestamp = nil
				return observed
			},
			wantEligible: false,
			wantReason:   "NamespaceNotTerminating",
		},
		{
			name:          "a namespace deleting normally is refused as not yet stuck",
			namespaceName: "ocm-production-abc123-mycluster",
			observed: func() *fleetapi.ObservedTerminatingNamespace {
				observed := wedgedNamespace()
				recent := metav1.NewTime(evaluationTime.Add(-30 * time.Second))
				observed.DeletionTimestamp = &recent
				return observed
			},
			wantEligible: false,
			wantReason:   "DeletionInProgress",
		},
		{
			name:          "a Terminating namespace with no deletionTimestamp is refused as contradictory",
			namespaceName: "ocm-production-abc123-mycluster",
			observed: func() *fleetapi.ObservedTerminatingNamespace {
				observed := wedgedNamespace()
				observed.DeletionTimestamp = nil
				return observed
			},
			wantEligible: false,
			wantReason:   "NoDeletionTimestamp",
		},
		{
			// Deleting the namespace underneath a live HostedCluster is the
			// mechanism that creates these orphans in the first place.
			name:          "a surviving HostedCluster is refused as out of scope",
			namespaceName: "ocm-production-abc123-mycluster",
			observed: func() *fleetapi.ObservedTerminatingNamespace {
				observed := wedgedNamespace()
				observed.RemainingContent = append(observed.RemainingContent,
					fleetapi.RemainingGroupResource{Group: hostedClusterGroup, Resource: hostedClusterResource})
				return observed
			},
			wantEligible: false,
			wantReason:   "HostedClusterPresent",
		},
		{
			name:          "an unobserved namespace is refused",
			namespaceName: "ocm-production-abc123-mycluster",
			observed:      func() *fleetapi.ObservedTerminatingNamespace { return nil },
			wantEligible:  false,
			wantReason:    "NamespaceNotObserved",
		},
		{
			name:          "a platform namespace is refused even when wedged",
			namespaceName: "kube-system",
			observed:      wedgedNamespace,
			wantEligible:  false,
			wantReason:    "ProtectedNamespace",
		},
		{
			name:          "the hypershift operator namespace is refused",
			namespaceName: "hypershift",
			observed:      wedgedNamespace,
			wantEligible:  false,
			wantReason:    "ProtectedNamespace",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			verdict := evaluateEligibility(tt.namespaceName, tt.observed(), evaluationTime)

			if verdict.eligible != tt.wantEligible {
				t.Fatalf("eligible = %v, want %v (reason %q, message %q)",
					verdict.eligible, tt.wantEligible, verdict.reason, verdict.message)
			}
			if verdict.reason != tt.wantReason {
				t.Errorf("reason = %q, want %q", verdict.reason, tt.wantReason)
			}
			if !verdict.eligible && len(verdict.message) == 0 {
				t.Error("a refusal must carry an operator-readable message")
			}
		})
	}
}

// The dwell boundary decides whether a normal teardown is mistaken for a wedge,
// so pin both sides of it.
func TestEligibilityDwellBoundary(t *testing.T) {
	justUnder := metav1.NewTime(evaluationTime.Add(-minimumTerminatingDwell + time.Minute))
	observed := wedgedNamespace()
	observed.DeletionTimestamp = &justUnder
	if verdict := evaluateEligibility("ocm-prod-x", observed, evaluationTime); verdict.eligible {
		t.Error("a namespace just inside the dwell window must not be eligible")
	}

	justOver := metav1.NewTime(evaluationTime.Add(-minimumTerminatingDwell - time.Minute))
	observed = wedgedNamespace()
	observed.DeletionTimestamp = &justOver
	if verdict := evaluateEligibility("ocm-prod-x", observed, evaluationTime); !verdict.eligible {
		t.Errorf("a namespace past the dwell window must be eligible, got %q", verdict.reason)
	}
}

func TestIsProtectedNamespace(t *testing.T) {
	for _, namespaceName := range []string{
		"default", "kube-system", "kube-public", "kube-node-lease",
		"hypershift", "cert-manager", "gatekeeper-system",
		"openshift-monitoring", "azure-arc", "open-cluster-management-agent",
		"calico-system", "tigera-operator", "maestro",
		"KUBE-SYSTEM", "  kube-system  ", "",
	} {
		if !isProtectedNamespace(namespaceName) {
			t.Errorf("isProtectedNamespace(%q) = false, want true", namespaceName)
		}
	}

	for _, namespaceName := range []string{
		"ocm-production-abc123-mycluster",
		"ocm-stage-xyz789-othercluster",
	} {
		if isProtectedNamespace(namespaceName) {
			t.Errorf("isProtectedNamespace(%q) = true, want false; real HCP namespaces must remain targetable", namespaceName)
		}
	}
}
