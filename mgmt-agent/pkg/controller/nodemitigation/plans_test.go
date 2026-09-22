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
	routes, err := registry(swiftMitigator{})
	if err != nil || len(routes) != 1 || routes["unknown"] != nil || routes["swift-vf-teardown"] != nil || routes["never-ready"] != nil {
		t.Fatalf("unexpected registered routes: %v, %v", routes, err)
	}
}

func TestPlansRequireCurrentEvidence(t *testing.T) {
	decision, err := (swiftMitigator{}).Plan(Input{})
	if err != nil || decision.Hold == "" || decision.Action != "" {
		t.Fatal("SWIFT acted without current evidence")
	}
	decision, err = (swiftMitigator{}).Plan(Input{Detection: detectors.Detection{Detector: detectors.SwiftPodSandboxStalled, PodUIDs: []types.UID{"pod"}}})
	if err != nil || decision.Action != ActionEvict || decision.Hold != "" {
		t.Fatalf("SWIFT action: %+v %v", decision, err)
	}
}
