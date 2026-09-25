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

package nodemitigation

import (
	"testing"

	"k8s.io/apimachinery/pkg/types"

	"github.com/Azure/ARO-HCP/mgmt-agent/pkg/controller/nodehealth/detectors"
)

type testMitigator struct {
	name     string
	detector string
}

func (m testMitigator) Name() string                   { return m.name }
func (m testMitigator) DetectorNames() []string        { return []string{m.detector} }
func (m testMitigator) Plan(_ Input) (Decision, error) { return Decision{}, nil }

func TestRegistry(t *testing.T) {
	for _, test := range []struct {
		name       string
		mitigators []Mitigator
	}{
		{"nil", []Mitigator{nil}},
		{"empty name", []Mitigator{testMitigator{detector: "fault"}}},
		{"empty detector", []Mitigator{testMitigator{name: "test"}}},
		{"duplicate name", []Mitigator{swiftMitigator{}, swiftMitigator{}}},
		{"conflicting detector", []Mitigator{swiftMitigator{}, testMitigator{name: "other", detector: detectors.SwiftPodSandboxStalled}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := registry(test.mitigators...); err == nil {
				t.Fatal("invalid registry was accepted")
			}
		})
	}
	routes, err := registry(registeredMitigators...)
	if err != nil || len(routes) != 1 || routes["unknown"] != nil || routes["swift-vf-teardown"] != nil || routes["never-ready"] != nil {
		t.Fatalf("unexpected registered routes: %v, %v", routes, err)
	}
	if len(registeredMitigators) != 1 || routes[detectors.SwiftPodSandboxStalled] == nil ||
		routes[detectors.SwiftPodSandboxStalled].Name() != "swift" {
		t.Fatalf("SWIFT detector must route to the named swift mitigator: %v", routes)
	}
}

func TestPlansRequireCurrentEvidence(t *testing.T) {
	for _, test := range []struct {
		name      string
		detection detectors.Detection
		wantEvict bool
	}{
		{name: "missing evidence"},
		{name: "missing scope", detection: detectors.Detection{Detector: detectors.SwiftPodSandboxStalled, PodUIDs: []types.UID{"pod"}}},
		{name: "node scope", detection: detectors.Detection{Detector: detectors.SwiftPodSandboxStalled, Scope: detectors.NodeScope, PodUIDs: []types.UID{"pod"}}},
		{name: "unknown scope", detection: detectors.Detection{Detector: detectors.SwiftPodSandboxStalled, Scope: detectors.Scope("unknown"), PodUIDs: []types.UID{"pod"}}},
		{name: "wrong detector", detection: detectors.Detection{Detector: "swift-vf-teardown", Scope: detectors.PodScope, PodUIDs: []types.UID{"pod"}}},
		{name: "missing pod", detection: detectors.Detection{Detector: detectors.SwiftPodSandboxStalled, Scope: detectors.PodScope}},
		{name: "pod scope", detection: detectors.Detection{Detector: detectors.SwiftPodSandboxStalled, Scope: detectors.PodScope, PodUIDs: []types.UID{"pod"}}, wantEvict: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			decision, err := (swiftMitigator{}).Plan(Input{Detection: test.detection})
			if err != nil {
				t.Fatal(err)
			}
			if test.wantEvict {
				if decision.Action != ActionEvict || decision.Hold != "" {
					t.Fatalf("SWIFT did not admit Pod evidence: %+v", decision)
				}
			} else if decision.Action != "" || decision.Hold == "" {
				t.Fatalf("SWIFT acted without Pod evidence: %+v", decision)
			}
		})
	}
}
