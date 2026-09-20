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

	api "github.com/Azure/ARO-HCP/mgmt-agent/pkg/apis/capacityreport/v1alpha1"
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
	routes, err := registry(swiftMitigator{}, neverReadyMitigator{})
	if err != nil || len(routes) != 3 || routes["unknown"] != nil {
		t.Fatalf("unexpected registered routes: %v, %v", routes, err)
	}
}

func TestPlansKeepAcceptedCleanup(t *testing.T) {
	for _, phase := range []string{PhaseCordon, PhaseRescue, PhaseDrain, PhaseDelete, PhaseObserve, PhaseComplete} {
		episode := &api.MitigationEpisode{Status: api.MitigationEpisodeStatus{Phase: phase}}
		decision, err := (swiftMitigator{}).Plan(Input{Episode: episode})
		if err != nil || decision.Phase != phase || decision.Hold != "" {
			t.Fatalf("fault recovery changed accepted SWIFT phase %q: %+v, %v", phase, decision, err)
		}
	}
	decision, err := (neverReadyMitigator{}).Plan(Input{})
	if err != nil || decision.Hold == "" || decision.Phase != "" {
		t.Fatal("never-ready planned deletion without current evidence")
	}
	for _, mitigator := range []Mitigator{swiftMitigator{}, neverReadyMitigator{}} {
		_, err := mitigator.Plan(Input{Episode: &api.MitigationEpisode{Status: api.MitigationEpisodeStatus{Phase: "unknown"}}})
		if err == nil {
			t.Fatalf("%s accepted an unknown phase", mitigator.Name())
		}
	}
}
