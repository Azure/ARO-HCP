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
	"context"
	"testing"

	"github.com/Azure/ARO-HCP/tooling/depctl/internal/forge"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/policy"
)

func inventorySnapshot(number int, head string) (forge.PullRequest, forge.Snapshot) {
	pull := forge.PullRequest{
		Number: number, Author: policy.Robot, State: "open", Title: "chore(tools): bump example to 1.2.0",
		Body: "<!-- agentic-tool-family:example.com/tool -->", Labels: []string{policy.ToolLabel},
		HeadRef: "agentic-tool-upgrade-tool-1.2.0", HeadSHA: head,
		HeadRepo: "Azure/ARO-HCP", BaseRef: "main", BaseRepo: "Azure/ARO-HCP",
	}
	return pull, forge.Snapshot{
		Pull:    pull,
		Commits: []string{head},
		Statuses: []forge.Status{
			{ID: 1, Context: "ci/pipeline-gate", State: "success", CreatedAt: "2026-11-05T15:00:00Z"},
			{ID: 2, Context: "ci/prow/e2e-parallel", State: "success", CreatedAt: "2026-11-05T15:00:00Z"},
		},
	}
}

func TestMaintenanceInventorySkipsUnrelatedRobotProposals(t *testing.T) {
	fake := forge.NewFake()
	pull, _ := inventorySnapshot(7895, "39af3a51dca8b0507907cd9f00c2d33ad659e227")
	pull.Title, pull.Body, pull.Labels = "Automated - Update component image digests", "", []string{"image-updater"}
	fake.Seed(pull, forge.Snapshot{Pull: pull})
	// A snapshot failure proves the unrelated proposal is never read.
	fake.FailOn("snapshot")
	engine := New(Deps{Forge: fake})
	decisions, err := engine.MaintenanceInventory(context.Background(), "Azure/ARO-HCP")
	if err != nil {
		t.Fatalf("MaintenanceInventory() error = %v", err)
	}
	if len(decisions) != 0 {
		t.Fatalf("decisions = %+v, want none", decisions)
	}
}

func TestMaintenanceInventoryAssessesOwnedOpenProposals(t *testing.T) {
	fake := forge.NewFake()
	pull, snapshot := inventorySnapshot(7892, "39af3a51dca8b0507907cd9f00c2d33ad659e227")
	fake.Seed(pull, snapshot)
	closed := pull
	closed.Number, closed.State = 7893, "closed"
	fake.Seed(closed, forge.Snapshot{Pull: closed})
	human := pull
	human.Number, human.Author = 7894, "raelga"
	fake.Seed(human, forge.Snapshot{Pull: human})
	unrelated := pull
	unrelated.Number, unrelated.Title, unrelated.Labels = 7895, "Automated - Update component image digests", []string{"image-updater"}
	fake.Seed(unrelated, forge.Snapshot{Pull: unrelated})
	engine := New(Deps{Forge: fake})
	decisions, err := engine.MaintenanceInventory(context.Background(), "Azure/ARO-HCP")
	if err != nil {
		t.Fatalf("MaintenanceInventory() error = %v", err)
	}
	if len(decisions) != 1 || decisions[0].PullRequest != 7892 {
		t.Fatalf("decisions = %+v", decisions)
	}
	if decisions[0].Kind != policy.KindTool || decisions[0].Repository != "Azure/ARO-HCP" {
		t.Fatalf("decision = %+v", decisions[0])
	}
}
