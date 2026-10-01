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

package detection

import (
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type registryDetector struct {
	name   string
	scope  Scope
	reason string
	window time.Duration
}

func (d registryDetector) Name() string            { return d.name }
func (d registryDetector) Scope() Scope            { return d.scope }
func (d registryDetector) Reason() string          { return d.reason }
func (d registryDetector) Window() time.Duration   { return d.window }
func (registryDetector) Applies(*corev1.Node) bool { return true }
func (d registryDetector) EvaluateNode(*corev1.Node, time.Time) (Decision, Snapshot) {
	return DecisionWedged, Snapshot{DetectorName: d.name}
}

type ambiguousDetector struct{ registryDetector }

func (ambiguousDetector) EvaluatePod(*corev1.Pod, []*corev1.Event, time.Time) (time.Time, time.Time, bool) {
	return time.Time{}, time.Time{}, false
}

func TestRegistryValidation(t *testing.T) {
	valid := registryDetector{name: "fault", scope: NodeScope, reason: "fault", window: time.Minute}
	var typedNil *registryDetector
	for _, test := range []struct {
		name      string
		detectors []Detector
	}{
		{name: "nil", detectors: []Detector{nil}},
		{name: "typed nil", detectors: []Detector{typedNil}},
		{name: "empty name", detectors: []Detector{registryDetector{scope: NodeScope, reason: "fault", window: time.Minute}}},
		{name: "duplicate", detectors: []Detector{valid, valid}},
		{name: "missing reason", detectors: []Detector{registryDetector{name: "fault", scope: NodeScope, window: time.Minute}}},
		{name: "invalid window", detectors: []Detector{registryDetector{name: "fault", scope: NodeScope, reason: "fault"}}},
		{name: "wrong scope", detectors: []Detector{registryDetector{name: "fault", scope: PodScope, reason: "fault", window: time.Minute}}},
		{name: "unknown scope", detectors: []Detector{registryDetector{name: "fault", scope: "unknown", reason: "fault", window: time.Minute}}},
		{name: "ambiguous evaluator", detectors: []Detector{ambiguousDetector{valid}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewRegistry(test.detectors...); err == nil {
				t.Fatal("invalid registry accepted")
			}
		})
	}
}

func TestRegistryPreservesOrderAndOwnsRegistration(t *testing.T) {
	first := registryDetector{name: "first", scope: NodeScope, reason: "first", window: time.Minute}
	second := registryDetector{name: "second", scope: NodeScope, reason: "second", window: time.Minute}
	list := []Detector{first, second}
	registry, err := NewRegistry(list...)
	if err != nil {
		t.Fatal(err)
	}
	list[0] = second
	other, err := NewRegistry(second)
	if err != nil {
		t.Fatal(err)
	}
	empty, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{UID: "node"}}
	for _, test := range []struct {
		registry *Registry
		name     string
		decision Decision
	}{
		{registry: registry, name: "first", decision: DecisionWedged},
		{registry: other, name: "second", decision: DecisionWedged},
		{registry: empty, decision: DecisionNotApplicable},
	} {
		decision, snapshot := test.registry.Decide(node, nil, nil, time.Now())
		if decision != test.decision || snapshot.DetectorName != test.name {
			t.Fatalf("unexpected verdict %v, %+v", decision, snapshot)
		}
		faults := test.registry.CollectDetections(node, nil, nil, time.Now())
		if test.name == "" {
			if len(faults) != 0 {
				t.Fatalf("empty registry produced faults: %+v", faults)
			}
		} else if len(faults) != 1 || faults[0].Detector != test.name {
			t.Fatalf("collection did not use its registry: %+v", faults)
		}
	}
}
