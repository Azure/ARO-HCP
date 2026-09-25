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

import "github.com/Azure/ARO-HCP/mgmt-agent/pkg/controller/nodehealth/detectors"

type swiftMitigator struct{}

func (swiftMitigator) Name() string { return "swift" }
func (swiftMitigator) DetectorNames() []string {
	return []string{detectors.SwiftPodSandboxStalled}
}
func (swiftMitigator) Plan(input Input) (Decision, error) {
	if input.Detection.Detector != detectors.SwiftPodSandboxStalled ||
		input.Detection.Scope != detectors.PodScope || len(input.Detection.PodUIDs) == 0 {
		return Decision{Hold: "pod-scoped SWIFT evidence missing"}, nil
	}
	return Decision{Action: ActionEvict}, nil
}
