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
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"

	api "github.com/Azure/ARO-HCP/mgmt-agent/pkg/apis/capacityreport/v1alpha1"
	"github.com/Azure/ARO-HCP/mgmt-agent/pkg/controller/nodehealth/detectors"
)

const (
	PhaseCordon   = "Cordon"
	PhaseRescue   = "Rescue"
	PhaseDrain    = "Drain"
	PhaseDelete   = "DeleteNode"
	PhaseObserve  = "Observe"
	PhaseComplete = "Complete"
)

type Input struct {
	Node      *corev1.Node
	Detection detectors.Detection
	Episode   *api.MitigationEpisode
	Policy    Config
	Now       time.Time
}

type Decision struct {
	Phase string
	Hold  string
}

type Mitigator interface {
	Name() string
	DetectorNames() []string
	Plan(Input) (Decision, error)
}

type swiftMitigator struct{}

func (swiftMitigator) Name() string { return "swift" }
func (swiftMitigator) DetectorNames() []string {
	return []string{"swift-vf-teardown", detectors.SwiftPodSandboxStalled}
}
func (swiftMitigator) Plan(input Input) (Decision, error) {
	if input.Episode == nil {
		return Decision{Phase: PhaseCordon}, nil
	}
	switch phase := input.Episode.Status.Phase; phase {
	case "", PhaseCordon, PhaseRescue, PhaseDrain, PhaseDelete, PhaseObserve, PhaseComplete:
		if phase == "" {
			phase = PhaseCordon
		}
		return Decision{Phase: phase}, nil
	default:
		return Decision{}, fmt.Errorf("unknown SWIFT episode phase %q", phase)
	}
}

type neverReadyMitigator struct{}

func (neverReadyMitigator) Name() string            { return "never-ready" }
func (neverReadyMitigator) DetectorNames() []string { return []string{"never-ready"} }
func (neverReadyMitigator) Plan(input Input) (Decision, error) {
	if input.Episode != nil {
		switch input.Episode.Status.Phase {
		case PhaseObserve, PhaseComplete:
			return Decision{Phase: input.Episode.Status.Phase}, nil
		case "", PhaseDelete:
		default:
			return Decision{}, fmt.Errorf("unknown never-ready episode phase %q", input.Episode.Status.Phase)
		}
	}
	if input.Detection.Detector != "never-ready" {
		return Decision{Hold: "never-ready evidence no longer valid"}, nil
	}
	return Decision{Phase: PhaseDelete}, nil
}

func registry(mitigators ...Mitigator) (map[string]Mitigator, error) {
	routes := map[string]Mitigator{}
	names := map[string]bool{}
	for _, mitigator := range mitigators {
		if mitigator == nil || mitigator.Name() == "" || names[mitigator.Name()] {
			return nil, fmt.Errorf("invalid or duplicate mitigator")
		}
		names[mitigator.Name()] = true
		for _, detector := range mitigator.DetectorNames() {
			if detector == "" || routes[detector] != nil {
				return nil, fmt.Errorf("invalid or conflicting detector route %q", detector)
			}
			routes[detector] = mitigator
		}
	}
	return routes, nil
}
