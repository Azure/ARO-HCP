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

package orchestration

import (
	"strings"
	"testing"

	"github.com/Azure/ARO-HCP/tooling/depctl/internal/model"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/policy"
)

func TestPlanPinnedBlockersTakePrecedenceOverNoChange(t *testing.T) {
	engine := New(Deps{})
	blocker := "inventory is incomplete; version states are not authoritative"
	partial := model.Inventory{
		Repository: "Azure/ARO-HCP", BaseSHA: strings.Repeat("a", 40), Profile: "tools", Complete: false,
	}
	plan, err := engine.PlanPinned(partial, nil, policy.Selection{Blockers: []string{blocker}})
	if err != nil {
		t.Fatalf("PlanPinned() error = %v", err)
	}
	if plan.Outcome != model.OutcomeBlocked {
		t.Fatalf("outcome = %q, want %q for an incomplete inventory", plan.Outcome, model.OutcomeBlocked)
	}
	if len(plan.Blockers) != 1 || plan.Blockers[0] != blocker {
		t.Fatalf("blockers = %v", plan.Blockers)
	}
	complete := partial
	complete.Complete = true
	plan, err = engine.PlanPinned(complete, nil, policy.Selection{})
	if err != nil {
		t.Fatalf("PlanPinned() error = %v", err)
	}
	if plan.Outcome != model.OutcomeNoChange {
		t.Fatalf("outcome = %q, want %q without blockers or families", plan.Outcome, model.OutcomeNoChange)
	}
}
