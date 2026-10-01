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

package mitigators

import (
	"testing"

	"k8s.io/apimachinery/pkg/types"

	"github.com/Azure/ARO-HCP/mgmt-agent/pkg/detection"
	"github.com/Azure/ARO-HCP/mgmt-agent/pkg/detection/detectors"
	"github.com/Azure/ARO-HCP/mgmt-agent/pkg/mitigation"
)

type testMitigator struct {
	name     string
	detector string
}

func (m testMitigator) Name() string            { return m.name }
func (m testMitigator) DetectorNames() []string { return []string{m.detector} }
func (m testMitigator) Plan(_ mitigation.Input) (mitigation.Decision, error) {
	return mitigation.Decision{}, nil
}

func TestRegistry(t *testing.T) {
	for _, test := range []struct {
		name       string
		mitigators []mitigation.Mitigator
	}{
		{"nil", []mitigation.Mitigator{nil}},
		{"empty name", []mitigation.Mitigator{testMitigator{detector: "fault"}}},
		{"empty detector", []mitigation.Mitigator{testMitigator{name: "test"}}},
		{"duplicate name", []mitigation.Mitigator{swiftMitigator{}, swiftMitigator{}}},
		{"conflicting detector", []mitigation.Mitigator{swiftMitigator{}, testMitigator{name: "other", detector: detectors.SwiftPodSandboxStalled}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := mitigation.NewRegistry(test.mitigators...); err == nil {
				t.Fatal("invalid registry was accepted")
			}
		})
	}
	routes, err := mitigation.NewRegistry(NewSwift())
	if err != nil || routes.ForDetector("unknown") != nil || routes.ForDetector("swift-vf-teardown") != nil || routes.ForDetector("never-ready") != nil {
		t.Fatalf("unexpected registered routes: %v, %v", routes, err)
	}
	if routes.ForDetector(detectors.SwiftPodSandboxStalled) == nil ||
		routes.ForDetector(detectors.SwiftPodSandboxStalled).Name() != "swift" {
		t.Fatalf("SWIFT detector must route to the named swift mitigator: %v", routes)
	}
}

func TestPlansRequireCurrentEvidence(t *testing.T) {
	for _, test := range []struct {
		name      string
		detection detection.Detection
		wantEvict bool
	}{
		{name: "missing evidence"},
		{name: "missing scope", detection: detection.Detection{Detector: detectors.SwiftPodSandboxStalled, PodUIDs: []types.UID{"pod"}}},
		{name: "node scope", detection: detection.Detection{Detector: detectors.SwiftPodSandboxStalled, Scope: detection.NodeScope, PodUIDs: []types.UID{"pod"}}},
		{name: "unknown scope", detection: detection.Detection{Detector: detectors.SwiftPodSandboxStalled, Scope: detection.Scope("unknown"), PodUIDs: []types.UID{"pod"}}},
		{name: "wrong detector", detection: detection.Detection{Detector: "swift-vf-teardown", Scope: detection.PodScope, PodUIDs: []types.UID{"pod"}}},
		{name: "missing pod", detection: detection.Detection{Detector: detectors.SwiftPodSandboxStalled, Scope: detection.PodScope}},
		{name: "pod scope", detection: detection.Detection{Detector: detectors.SwiftPodSandboxStalled, Scope: detection.PodScope, PodUIDs: []types.UID{"pod"}}, wantEvict: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			decision, err := (swiftMitigator{}).Plan(mitigation.Input{Detection: test.detection})
			if err != nil {
				t.Fatal(err)
			}
			if test.wantEvict {
				if decision.Action != mitigation.ActionEvict || decision.Hold != "" {
					t.Fatalf("SWIFT did not admit Pod evidence: %+v", decision)
				}
			} else if decision.Action != "" || decision.Hold == "" {
				t.Fatalf("SWIFT acted without Pod evidence: %+v", decision)
			}
		})
	}
}
