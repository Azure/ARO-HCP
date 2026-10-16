// Copyright 2025 Microsoft Corporation
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

package nodehealth

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/record"
	"k8s.io/component-base/metrics/legacyregistry"

	"github.com/Azure/ARO-HCP/mgmt-agent/pkg/controller/nodehealth/detectors"
)

// testNow is a fixed clock for labeler tests.
var testNow = time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)

func newTestLabeler(objs ...*corev1.Node) (*labeler, *fake.Clientset) {
	rt := make([]runtime.Object, 0, len(objs))
	for _, o := range objs {
		rt = append(rt, o)
	}
	client := fake.NewSimpleClientset(rt...)
	l := newLabeler(client, record.NewFakeRecorder(16), func() time.Time { return testNow })
	return l, client
}

func getNode(t *testing.T, client *fake.Clientset, name string) *corev1.Node {
	t.Helper()
	n, err := client.CoreV1().Nodes().Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get node: %v", err)
	}
	return n
}

func snap() detectors.Snapshot {
	return detectors.Snapshot{
		DetectorName:     "swift-vf-teardown",
		Window:           10 * time.Minute,
		Pods:             &detectors.PodEvidence{FailureCount: 30},
		MatchedSignature: `no such network interface`,
	}
}

// completeRecord is the annotation set a fully written detection record carries.
// The values are irrelevant to the steady-state check, only their presence is.
func completeRecord() map[string]string {
	return map[string]string{
		annotationDetector:   "swift-vf-teardown",
		annotationReason:     "recorded earlier in this episode",
		annotationSignature:  "no such network interface",
		annotationObservedAt: "t",
	}
}

func TestLabelAppliesLabelAndAnnotations(t *testing.T) {
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "n1"}}
	l, client := newTestLabeler(node)

	changed, err := l.label(context.Background(), node, "swift-vf-teardown", snap())
	if err != nil {
		t.Fatalf("label: %v", err)
	}
	if !changed {
		t.Fatal("expected changed=true")
	}

	got := getNode(t, client, "n1")
	if got.Labels[labelKey] != labelValue {
		t.Errorf("label = %q, want %q", got.Labels[labelKey], labelValue)
	}
	if got.Annotations[annotationDetector] != "swift-vf-teardown" {
		t.Errorf("detector annotation = %q", got.Annotations[annotationDetector])
	}
	if got.Annotations[annotationReason] == "" {
		t.Error("reason annotation should be set")
	}
	if got.Annotations[annotationSignature] != `no such network interface` {
		t.Errorf("signature annotation = %q, want %q", got.Annotations[annotationSignature], `no such network interface`)
	}
	if got.Annotations[annotationObservedAt] == "" {
		t.Error("observed-at annotation should be set")
	}
}

func TestLabelWithoutSignatureClearsStaleSignatureAnnotation(t *testing.T) {
	// A detection that classified no failure mode must not leave the previous
	// episode's signature sitting next to a fresh detection record.
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{
		Name:        "n1",
		Annotations: map[string]string{annotationSignature: "stale"},
	}}
	l, client := newTestLabeler(node)

	s := snap()
	s.MatchedSignature = ""
	if _, err := l.label(context.Background(), node, "swift-vf-teardown", s); err != nil {
		t.Fatalf("label: %v", err)
	}

	got := getNode(t, client, "n1")
	if v, ok := got.Annotations[annotationSignature]; ok {
		t.Errorf("signature annotation = %q, want it removed", v)
	}
}

func TestLabelIsIdempotent(t *testing.T) {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "n1",
			Labels:      map[string]string{labelKey: labelValue},
			Annotations: completeRecord(),
		},
	}
	l, _ := newTestLabeler(node)

	changed, err := l.label(context.Background(), node, "swift-vf-teardown", snap())
	if err != nil {
		t.Fatalf("label: %v", err)
	}
	if changed {
		t.Error("re-labeling an already-labeled node should be a no-op")
	}
}

func TestLabelDoesNotChurnOnChangedEvidence(t *testing.T) {
	// The reason and signature values move with the evidence. A complete record
	// must stay untouched when they change, or every wedged node is rewritten on
	// every sweep.
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "n1",
			Labels: map[string]string{labelKey: labelValue},
			Annotations: map[string]string{
				annotationDetector:   "swift-vf-teardown",
				annotationReason:     "2 pods stuck past dwell (2 stuck total), no recent success in 10m0s",
				annotationSignature:  "mtpnc is not ready",
				annotationObservedAt: "t",
			},
		},
	}
	l, _ := newTestLabeler(node)

	// snap() reports different counts and a different signature.
	changed, err := l.label(context.Background(), node, "swift-vf-teardown", snap())
	if err != nil {
		t.Fatalf("label: %v", err)
	}
	if changed {
		t.Error("changed evidence must not rewrite a complete record")
	}
}

func TestLabelSelfHealsIncompleteRecord(t *testing.T) {
	// A record can be incomplete because it was stripped by hand, or because it
	// was written by an earlier build that did not record every annotation. Both
	// look the same here: the detector annotation still matches, so only a
	// completeness check catches them.
	for _, tc := range []struct {
		name    string
		missing string
	}{
		{"stripped reason", annotationReason},
		{"stripped observed-at", annotationObservedAt},
		{"record predates the signature annotation", annotationSignature},
	} {
		t.Run(tc.name, func(t *testing.T) {
			anns := map[string]string{
				annotationDetector:   "swift-vf-teardown",
				annotationReason:     "x",
				annotationSignature:  "mtpnc is not ready",
				annotationObservedAt: "t",
			}
			delete(anns, tc.missing)
			node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{
				Name:        "n1",
				Labels:      map[string]string{labelKey: labelValue},
				Annotations: anns,
			}}
			l, client := newTestLabeler(node)

			changed, err := l.label(context.Background(), node, "swift-vf-teardown", snap())
			if err != nil {
				t.Fatalf("label: %v", err)
			}
			if !changed {
				t.Fatalf("an incomplete record missing %q should be restored", tc.missing)
			}
			got := getNode(t, client, "n1")
			if _, ok := got.Annotations[tc.missing]; !ok {
				t.Errorf("annotation %q should have been restored", tc.missing)
			}
		})
	}
}

func TestUnlabelRemovesLabelPreservingOthers(t *testing.T) {
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{
		Name: "n1",
		Labels: map[string]string{
			labelKey:                  labelValue,
			"kubernetes.io/os":        "linux",
			detectors.SwiftV2LabelKey: detectors.SwiftV2LabelValue,
		},
		Annotations: map[string]string{
			annotationDetector:   "swift-vf-teardown",
			annotationReason:     "x",
			annotationSignature:  "no such network interface",
			annotationObservedAt: "t",
			"unrelated":          "keep",
		},
	}}
	l, client := newTestLabeler(node)

	changed, err := l.unlabel(context.Background(), node)
	if err != nil {
		t.Fatalf("unlabel: %v", err)
	}
	if !changed {
		t.Fatal("expected changed=true")
	}

	got := getNode(t, client, "n1")
	if _, ok := got.Labels[labelKey]; ok {
		t.Error("wedged label should be removed")
	}
	if got.Labels["kubernetes.io/os"] != "linux" {
		t.Error("unrelated label should be preserved")
	}
	if got.Labels[detectors.SwiftV2LabelKey] != detectors.SwiftV2LabelValue {
		t.Error("SWIFT label should be preserved")
	}
	if _, ok := got.Annotations[annotationDetector]; ok {
		t.Error("detector annotation should be removed")
	}
	if _, ok := got.Annotations[annotationSignature]; ok {
		t.Error("signature annotation should be removed")
	}
	if got.Annotations["unrelated"] != "keep" {
		t.Error("unrelated annotation should be preserved")
	}
}

func TestUnlabelNoopWhenAbsent(t *testing.T) {
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "n1"}}
	l, _ := newTestLabeler(node)

	changed, err := l.unlabel(context.Background(), node)
	if err != nil {
		t.Fatalf("unlabel: %v", err)
	}
	if changed {
		t.Error("unlabeling a node without the label should be a no-op")
	}
}

func TestUnlabelNoopOnForeignValue(t *testing.T) {
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{
		Name:   "n1",
		Labels: map[string]string{labelKey: "somethingelse"},
	}}
	l, client := newTestLabeler(node)

	changed, err := l.unlabel(context.Background(), node)
	if err != nil {
		t.Fatalf("unlabel: %v", err)
	}
	if changed {
		t.Error("a same-key label with a foreign value must not be clobbered")
	}
	if getNode(t, client, "n1").Labels[labelKey] != "somethingelse" {
		t.Error("foreign label value should be untouched")
	}
}

func TestLabelSkipsDuplicateOnStaleCache(t *testing.T) {
	// The server already has the node labeled wedged, but the informer cache the
	// caller reconciles off is stale and still shows it unlabeled. The live
	// re-check must catch this so no duplicate counter or Event fires for what is
	// not a real transition.
	served := &corev1.Node{ObjectMeta: metav1.ObjectMeta{
		Name:        "n1",
		Labels:      map[string]string{labelKey: labelValue},
		Annotations: completeRecord(),
	}}
	client := fake.NewSimpleClientset(served)
	rec := record.NewFakeRecorder(16)
	l := newLabeler(client, rec, func() time.Time { return testNow })

	stale := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "n1"}}
	changed, err := l.label(context.Background(), stale, "swift-vf-teardown", snap())
	if err != nil {
		t.Fatalf("label: %v", err)
	}
	if changed {
		t.Error("stale-cache label of an already-labeled node must report changed=false")
	}
	select {
	case ev := <-rec.Events:
		t.Errorf("no Event expected on a duplicate transition, got %q", ev)
	default:
	}
}

func TestUnlabelSkipsDuplicateOnStaleCache(t *testing.T) {
	// The server has already cleared the label, but the informer cache the caller
	// reconciles off is stale and still shows it present. The live re-check must
	// catch this so no duplicate counter or Event fires.
	served := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "n1"}}
	client := fake.NewSimpleClientset(served)
	rec := record.NewFakeRecorder(16)
	l := newLabeler(client, rec, func() time.Time { return testNow })

	stale := &corev1.Node{ObjectMeta: metav1.ObjectMeta{
		Name:   "n1",
		Labels: map[string]string{labelKey: labelValue},
	}}
	changed, err := l.unlabel(context.Background(), stale)
	if err != nil {
		t.Fatalf("unlabel: %v", err)
	}
	if changed {
		t.Error("stale-cache unlabel of an already-cleared node must report changed=false")
	}
	select {
	case ev := <-rec.Events:
		t.Errorf("no Event expected on a duplicate transition, got %q", ev)
	default:
	}
}

func TestLabelRefreshesStrippedDetectionRecord(t *testing.T) {
	// The node carries our label but its detection record is gone (stripped, or
	// written by an older version). The record must self-heal rather than stay
	// blank forever behind the already-labeled short-circuit.
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{
		Name:   "n1",
		Labels: map[string]string{labelKey: labelValue},
	}}
	l, client := newTestLabeler(node)

	changed, err := l.label(context.Background(), node, "swift-vf-teardown", snap())
	if err != nil {
		t.Fatalf("label: %v", err)
	}
	if !changed {
		t.Fatal("a missing detection record should be refreshed")
	}

	got := getNode(t, client, "n1")
	if got.Annotations[annotationDetector] != "swift-vf-teardown" {
		t.Errorf("detector annotation = %q, want it restored", got.Annotations[annotationDetector])
	}
	if got.Annotations[annotationReason] == "" {
		t.Error("reason annotation should be restored")
	}
	if got.Labels[labelKey] != labelValue {
		t.Error("label should still be present")
	}
}

func TestLabelSteadyStateMakesNoAPICall(t *testing.T) {
	// A node already labeled by the same detector is the steady state: it must
	// cost neither a write nor a read, so a wedged node does not generate an
	// apiserver GET on every resync tick.
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{
		Name:        "n1",
		Labels:      map[string]string{labelKey: labelValue},
		Annotations: completeRecord(),
	}}
	l, client := newTestLabeler(node)
	client.ClearActions()

	changed, err := l.label(context.Background(), node, "swift-vf-teardown", snap())
	if err != nil {
		t.Fatalf("label: %v", err)
	}
	if changed {
		t.Error("steady state must report changed=false")
	}
	if acts := client.Actions(); len(acts) != 0 {
		t.Errorf("steady state must not call the apiserver, got %d actions: %v", len(acts), acts)
	}
}

func TestLabelClearsStaleSignatureOnAnAlreadyLabeledNode(t *testing.T) {
	// The steady-state short-circuit must not hide a stale signature. A record
	// that is otherwise complete but still carries a signature from an earlier
	// detection is not complete, because this detection classified no failure
	// mode and the annotation now describes nothing.
	anns := completeRecord()
	anns[annotationSignature] = "mtpnc is not ready"
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{
		Name:        "n1",
		Labels:      map[string]string{labelKey: labelValue},
		Annotations: anns,
	}}
	l, client := newTestLabeler(node)

	s := snap()
	s.MatchedSignature = ""
	changed, err := l.label(context.Background(), node, "swift-vf-teardown", s)
	if err != nil {
		t.Fatalf("label: %v", err)
	}
	if !changed {
		t.Fatal("a record carrying a stale signature must be rewritten")
	}
	if v, ok := getNode(t, client, "n1").Annotations[annotationSignature]; ok {
		t.Errorf("signature annotation = %q, want it removed", v)
	}
}

func TestLabelPreservesObservedAtWhenRepairingARecord(t *testing.T) {
	// observed-at marks the start of the wedge episode. Repairing an incomplete
	// record is not a new episode, so the original stamp has to survive.
	const episodeStart = "2020-01-01T00:00:00Z"
	anns := completeRecord()
	anns[annotationObservedAt] = episodeStart
	delete(anns, annotationReason)
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{
		Name:        "n1",
		Labels:      map[string]string{labelKey: labelValue},
		Annotations: anns,
	}}
	l, client := newTestLabeler(node)

	changed, err := l.label(context.Background(), node, "swift-vf-teardown", snap())
	if err != nil {
		t.Fatalf("label: %v", err)
	}
	if !changed {
		t.Fatal("an incomplete record should be repaired")
	}
	got := getNode(t, client, "n1")
	if got.Annotations[annotationObservedAt] != episodeStart {
		t.Errorf("observed-at = %q, want the episode start %q preserved",
			got.Annotations[annotationObservedAt], episodeStart)
	}
	if got.Annotations[annotationReason] == "" {
		t.Error("reason should have been restored")
	}
}

func TestLabelStampsObservedAtOnAFreshTransition(t *testing.T) {
	// A node that was not labeled by this detector is a new episode, so
	// observed-at is stamped now even if a previous episode left one behind.
	anns := completeRecord()
	anns[annotationDetector] = "some-other-detector"
	anns[annotationObservedAt] = "2020-01-01T00:00:00Z"
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{
		Name:        "n1",
		Labels:      map[string]string{labelKey: labelValue},
		Annotations: anns,
	}}
	l, client := newTestLabeler(node)

	if _, err := l.label(context.Background(), node, "swift-vf-teardown", snap()); err != nil {
		t.Fatalf("label: %v", err)
	}
	want := testNow.UTC().Format(time.RFC3339)
	if got := getNode(t, client, "n1").Annotations[annotationObservedAt]; got != want {
		t.Errorf("observed-at = %q, want %q stamped for the new episode", got, want)
	}
}

// countLabelActions returns the current value of the label-actions counter for
// one action/result pair, so a test can assert nothing was counted.
func countLabelActions(t *testing.T, action, result string) float64 {
	t.Helper()
	families, err := legacyregistry.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	for _, f := range families {
		if f.GetName() != "nodehealth_label_actions_total" {
			continue
		}
		for _, m := range f.GetMetric() {
			got := map[string]string{}
			for _, l := range m.GetLabel() {
				got[l.GetName()] = l.GetValue()
			}
			if got["action"] == action && got["result"] == result {
				return m.GetCounter().GetValue()
			}
		}
	}
	return 0
}

// A node can be deleted between the live read and the patch. That is not an
// error, but it is not a mutation either, so it must not be counted as a
// successful action or announced with an Event against an object that is gone.
func TestPatchOnDeletedNodeIsNotCountedAsAnAction(t *testing.T) {
	RegisterMetrics()

	for _, tc := range []struct {
		name   string
		action string
		node   *corev1.Node
		run    func(l *labeler, node *corev1.Node) (bool, error)
	}{
		{
			name:   "label",
			action: "label",
			node:   &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "gone"}},
			run: func(l *labeler, node *corev1.Node) (bool, error) {
				return l.label(context.Background(), node, "swift-vf-teardown", snap())
			},
		},
		{
			name:   "unlabel",
			action: "unlabel",
			node: &corev1.Node{ObjectMeta: metav1.ObjectMeta{
				Name:        "gone",
				Labels:      map[string]string{labelKey: labelValue},
				Annotations: completeRecord(),
			}},
			run: func(l *labeler, node *corev1.Node) (bool, error) {
				return l.unlabel(context.Background(), node)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l, client := newTestLabeler(tc.node)
			// The live read still sees the node; the write finds it gone.
			client.PrependReactor("patch", "nodes", func(action k8stesting.Action) (bool, runtime.Object, error) {
				return true, nil, apierrors.NewNotFound(corev1.Resource("nodes"), "gone")
			})
			recorder := record.NewFakeRecorder(4)
			l.recorder = recorder

			before := countLabelActions(t, tc.action, "success")
			changed, err := tc.run(l, tc.node)
			if err != nil {
				t.Fatalf("%s: %v", tc.action, err)
			}
			if changed {
				t.Error("a node that vanished before the patch is not a change")
			}
			if after := countLabelActions(t, tc.action, "success"); after != before {
				t.Errorf("%s success counter moved from %v to %v", tc.action, before, after)
			}
			select {
			case ev := <-recorder.Events:
				t.Errorf("unexpected Event for a deleted node: %s", ev)
			default:
			}
		})
	}
}
