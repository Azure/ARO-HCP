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

package types

import (
	"fmt"
	"slices"
)

const StepActionIstioUpgrade = "IstioUpgrade"

type IstioUpgradeStep struct {
	StepMeta     `json:",inline"`
	AKSCluster   Value  `json:"aksCluster"`
	Timeout      string `json:"timeout,omitempty"`
	IdentityFrom Input  `json:"identityFrom,omitempty"`
}

func (s *IstioUpgradeStep) Description() string {
	return fmt.Sprintf("Step %s\n  Kind: %s\n  AKSCluster: %s\n", s.Name, s.Action, s.AKSCluster.String())
}

func (s *IstioUpgradeStep) RequiredInputs() []StepDependency {
	var deps []StepDependency
	if s.AKSCluster.Input != nil {
		deps = append(deps, s.AKSCluster.Input.StepDependency)
	}
	if s.IdentityFrom.StepDependency != (StepDependency{}) {
		deps = append(deps, s.IdentityFrom.StepDependency)
	}
	slices.SortFunc(deps, SortDependencies)
	deps = slices.Compact(deps)
	return deps
}

func (s *IstioUpgradeStep) IsWellFormedOverInputs() bool {
	return true
}
