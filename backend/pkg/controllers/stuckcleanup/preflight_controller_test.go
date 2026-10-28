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

	"github.com/google/go-cmp/cmp"

	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
)

func TestNamespaceTargetForIsClusterScoped(t *testing.T) {
	target := namespaceTargetFor("ocm-production-abc123-mycluster")

	if target.Resource != "namespaces" || target.Version != "v1" || target.Group != "" {
		t.Errorf("unexpected GVR: %+v", target)
	}
	// A namespace is cluster-scoped; setting TargetItem.Namespace would make the
	// kube-applier build a namespaced URL and the read would never resolve.
	if target.Namespace != "" {
		t.Errorf("TargetItem.Namespace = %q, want empty for a cluster-scoped namespace read", target.Namespace)
	}
	if target.Name != "ocm-production-abc123-mycluster" {
		t.Errorf("TargetItem.Name = %q", target.Name)
	}
}

// parseRemainingContent is the only path by which the inspection learns what is
// left inside a wedged namespace: a ReadDesire reads one object by exact name
// and there is no list primitive, so these condition messages are the whole
// inventory. Both real message shapes are covered.
func TestParseRemainingContent(t *testing.T) {
	for _, tt := range []struct {
		name    string
		message string
		want    []remainingSummary
	}{
		{
			name:    "content remaining spells resource.group",
			message: "Some resources are remaining: poddisruptionbudgets.policy has 2 resource instances",
			want: []remainingSummary{
				{Group: "policy", Resource: "poddisruptionbudgets", Permitted: false},
			},
		},
		{
			name:    "finalizers remaining spells group/name",
			message: "Some content in the namespace has finalizers remaining: cluster.x-k8s.io/machine in 1 resource instances",
			want: []remainingSummary{
				{Group: "cluster.x-k8s.io", Resource: "machine", Permitted: false},
			},
		},
		{
			name:    "multiple comma separated entries",
			message: "Some resources are remaining: poddisruptionbudgets.policy has 2 resource instances, secrets. has 4 resource instances",
			want: []remainingSummary{
				{Group: "policy", Resource: "poddisruptionbudgets", Permitted: false},
				{Group: "", Resource: "secrets", Permitted: true},
			},
		},
		{
			name:    "core resource with no group separator",
			message: "Some resources are remaining: configmaps has 1 resource instances",
			want: []remainingSummary{
				{Group: "", Resource: "configmaps", Permitted: true},
			},
		},
		{
			name:    "message with no colon yields nothing",
			message: "namespace is terminating",
			want:    nil,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := summarize(parseRemainingContent(tt.message))
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("parseRemainingContent() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestDistillNamespaceReportsBlockersAndUID(t *testing.T) {
	raw := []byte(`{
		"apiVersion": "v1",
		"kind": "Namespace",
		"metadata": {
			"name": "ocm-production-abc123-mycluster",
			"uid": "11111111-2222-3333-4444-555555555555",
			"deletionTimestamp": "2026-10-19T10:00:00Z"
		},
		"spec": {"finalizers": ["kubernetes"]},
		"status": {
			"phase": "Terminating",
			"conditions": [
				{
					"type": "NamespaceContentRemaining",
					"status": "True",
					"reason": "SomeResourcesRemain",
					"message": "Some resources are remaining: poddisruptionbudgets.policy has 2 resource instances"
				}
			]
		}
	}`)

	observed, err := distillNamespace(raw)
	if err != nil {
		t.Fatalf("distillNamespace() error = %v", err)
	}
	if observed.UID != "11111111-2222-3333-4444-555555555555" {
		t.Errorf("UID = %q", observed.UID)
	}
	if observed.Phase != "Terminating" {
		t.Errorf("Phase = %q", observed.Phase)
	}
	if observed.DeletionTimestamp == nil {
		t.Error("DeletionTimestamp was not captured; namespace age is the strongest wedged-vs-slow signal")
	}
	if diff := cmp.Diff([]string{"kubernetes"}, observed.SpecFinalizers); diff != "" {
		t.Errorf("SpecFinalizers mismatch (-want +got):\n%s", diff)
	}
	if len(observed.NamespaceConditions) != 1 {
		t.Fatalf("expected the namespace conditions to be carried verbatim, got %d", len(observed.NamespaceConditions))
	}

	want := []remainingSummary{{Group: "policy", Resource: "poddisruptionbudgets", Permitted: false}}
	if diff := cmp.Diff(want, summarize(observed.RemainingContent)); diff != "" {
		t.Errorf("RemainingContent mismatch (-want +got):\n%s", diff)
	}
}

func TestDistillNamespaceOnEmptyContent(t *testing.T) {
	observed, err := distillNamespace(nil)
	if err != nil {
		t.Fatalf("distillNamespace(nil) error = %v", err)
	}
	if observed != nil {
		t.Errorf("distillNamespace(nil) = %+v, want nil to mean not-yet-observed", observed)
	}
}

// Both condition types can name the same group/resource; the findings should
// list it once.
func TestDistillNamespaceDedupesAcrossConditions(t *testing.T) {
	raw := []byte(`{
		"apiVersion": "v1",
		"kind": "Namespace",
		"metadata": {"name": "ns", "uid": "abc"},
		"status": {
			"phase": "Terminating",
			"conditions": [
				{"type": "NamespaceContentRemaining", "status": "True",
				 "message": "Some resources are remaining: machines.cluster.x-k8s.io has 1 resource instances"},
				{"type": "NamespaceFinalizersRemaining", "status": "True",
				 "message": "Some content in the namespace has finalizers remaining: cluster.x-k8s.io/machines in 1 resource instances"}
			]
		}
	}`)

	observed, err := distillNamespace(raw)
	if err != nil {
		t.Fatalf("distillNamespace() error = %v", err)
	}
	if len(observed.RemainingContent) != 1 {
		t.Errorf("RemainingContent = %+v, want a single deduplicated entry", summarize(observed.RemainingContent))
	}
}

func TestReadDesireNameIsStableAndNamespaced(t *testing.T) {
	first := ReadDesireNameFor("ns-deadbeef")
	if first != ReadDesireNameFor("ns-deadbeef") {
		t.Error("ReadDesireNameFor must be stable so the desire is recoverable across restarts")
	}
	if first == "ns-deadbeef" {
		t.Error("ReadDesireNameFor must namespace the desire so it cannot collide with another controller's")
	}
}

// remainingSummary flattens the pointer-valued permission flag so table
// expectations stay readable.
type remainingSummary struct {
	Group     string
	Resource  string
	Permitted bool
}

func summarize(in []fleetapi.RemainingGroupResource) []remainingSummary {
	if len(in) == 0 {
		return nil
	}
	out := make([]remainingSummary, 0, len(in))
	for _, entry := range in {
		summary := remainingSummary{Group: entry.Group, Resource: entry.Resource}
		if entry.KubeApplierPermitted != nil {
			summary.Permitted = *entry.KubeApplierPermitted
		}
		out = append(out, summary)
	}
	return out
}

func TestPhaseIsTerminal(t *testing.T) {
	for phase, want := range map[fleetapi.StuckClusterCleanupPhase]bool{
		fleetapi.StuckClusterCleanupPhaseInspected: true,
		fleetapi.StuckClusterCleanupPhaseRefused:   true,
		fleetapi.StuckClusterCleanupPhasePending:   false,
		fleetapi.StuckClusterCleanupPhaseObserving: false,
		// Failed is an out-of-band error and is expected to be transient, so it
		// must stay retryable rather than settle and release its watch.
		fleetapi.StuckClusterCleanupPhaseFailed: false,
	} {
		if got := phase.IsTerminal(); got != want {
			t.Errorf("phase %q IsTerminal() = %v, want %v", phase, got, want)
		}
	}
}

func TestSetPhaseStartsRetentionClockOnceOnly(t *testing.T) {
	cleanup := &fleetapi.StuckClusterCleanup{}

	setPhase(cleanup, fleetapi.StuckClusterCleanupPhaseObserving, "waiting")
	if cleanup.Status.CompletedAt != nil {
		t.Fatal("a non-terminal phase must not start the retention clock")
	}

	setPhase(cleanup, fleetapi.StuckClusterCleanupPhaseInspected, "done")
	if cleanup.Status.CompletedAt == nil {
		t.Fatal("reaching a terminal phase must start the retention clock")
	}
	first := *cleanup.Status.CompletedAt

	// Re-stamping on every later pass would keep pushing the expiry out and the
	// document would never be retired.
	setPhase(cleanup, fleetapi.StuckClusterCleanupPhaseRefused, "changed mind")
	if !cleanup.Status.CompletedAt.Equal(&first) {
		t.Errorf("CompletedAt was re-stamped: %v -> %v", first, *cleanup.Status.CompletedAt)
	}
}

func TestTerminalRetentionPeriodIsSane(t *testing.T) {
	// Short enough that a fleet sweep does not accumulate indefinitely, long
	// enough that findings survive a weekend plus a working week.
	if terminalRetentionPeriod < 7*24*time.Hour {
		t.Errorf("retention period %v is too short for an operator to read the findings", terminalRetentionPeriod)
	}
	if terminalRetentionPeriod > 30*24*time.Hour {
		t.Errorf("retention period %v lets settled requests accumulate", terminalRetentionPeriod)
	}
}
