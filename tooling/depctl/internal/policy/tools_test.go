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

package policy

import (
	"strings"
	"testing"

	"github.com/Azure/ARO-HCP/tooling/depctl/internal/model"
)

func ownedToolPull() PullRequest {
	return PullRequest{
		Number: 8123, Author: ToolOwner, State: "open", Base: "main",
		Title: "chore(tools): bump github.com/example/tool to 1.2.0", Body: "<!-- agentic-tool-family:github.com/example/tool -->",
		Labels: []string{ToolLabel}, HeadRepo: "Azure/ARO-HCP", BaseRepo: "Azure/ARO-HCP",
	}
}

func TestIsToolProposalRecognizesMarkersAndLabel(t *testing.T) {
	cases := []struct {
		name string
		pull PullRequest
		want bool
	}{
		{name: "label", pull: ownedToolPull(), want: true},
		{name: "pre-label window", pull: PullRequest{
			Title: "chore(tools): bump something", Body: "text <!-- agentic-tool-family:x -->",
		}, want: true},
		{name: "human title only", pull: PullRequest{Title: "chore(tools): bump something"}, want: false},
		{name: "marker in unrelated title", pull: PullRequest{
			Title: "fix: something", Body: "<!-- agentic-tool-family:x -->",
		}, want: false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := IsToolProposal(testCase.pull); got != testCase.want {
				t.Fatalf("IsToolProposal() = %t, want %t", got, testCase.want)
			}
		})
	}
}

func TestOpenOwnedToolProposalIgnoresUnrelatedAuthorsAndBases(t *testing.T) {
	human := PullRequest{Number: 1, Author: "someone", State: "open", Base: "main", Title: "chore(tools): bump x to 1.0.0"}
	otherBase := ownedToolPull()
	otherBase.Number = 2
	otherBase.Base = "release-4.20"
	otherLabel := ownedToolPull()
	otherLabel.Number = 3
	otherLabel.Labels = []string{"agentic-dependabot"}
	otherLabel.Body = ""
	otherLabel.Title = "chore(tools): bump x to 1.0.0"

	if number, found := OpenOwnedToolProposal([]PullRequest{human, otherBase, otherLabel}); found {
		t.Fatalf("unexpected single-flight blocker #%d", number)
	}
	if number, found := OpenOwnedToolProposal([]PullRequest{human, otherBase, ownedToolPull()}); !found || number != 8123 {
		t.Fatalf("expected blocker #8123, got #%d found=%t", number, found)
	}
}

func TestToolBranchAndTitle(t *testing.T) {
	if got := ToolBranch("github.com/spf13/cobra", "v1.10.3"); got != "agentic-tool-upgrade-github.com-spf13-cobra-1.10.3" {
		t.Fatalf("unexpected branch %q", got)
	}
	if got := ToolTitle("Azure/bicep", "0.49.0"); got != "chore(tools): bump Azure/bicep to 0.49.0" {
		t.Fatalf("unexpected title %q", got)
	}
	markers := ToolMarkers("Azure/bicep")
	if len(markers) != 1 || !strings.Contains(markers[0], ToolBodyMarker) {
		t.Fatalf("unexpected markers %v", markers)
	}
}

func TestSelectFamiliesRequiresExplicitTargets(t *testing.T) {
	inventory := model.Inventory{
		Complete: true,
		Families: []model.Family{{
			Family: "github.com/example/tool", Kind: model.FamilyGoModule, Current: "1.0.0",
			Status: model.VersionOutdated,
			Pins:   []model.Pin{{ToolName: "tool", Source: ".bingo/tool.mod", Current: "1.0.0"}},
		}},
	}
	selection, err := SelectFamilies(inventory, nil)
	if err != nil {
		t.Fatalf("SelectFamilies() error = %v", err)
	}
	if len(selection.Families) != 0 || len(selection.Skipped) != 1 {
		t.Fatalf("expected a skipped family, got %+v", selection)
	}
	if selection.Skipped[0].Reason != "no target requested" {
		t.Fatalf("unexpected skip reason %q", selection.Skipped[0].Reason)
	}
}

func TestSelectFamiliesRefusesDowngrade(t *testing.T) {
	inventory := model.Inventory{
		Complete: true,
		Families: []model.Family{{
			Family: "github.com/example/tool", Kind: model.FamilyGoModule, Current: "2.0.0",
			Status: model.VersionOutdated,
			Pins:   []model.Pin{{ToolName: "tool", Source: ".bingo/tool.mod", Current: "2.0.0"}},
		}},
	}
	if _, err := SelectFamilies(inventory, map[string]string{"github.com/example/tool": "1.9.0"}); err == nil {
		t.Fatal("expected a downgrade refusal")
	}
}

func TestSelectFamiliesBlocksIncompleteInventory(t *testing.T) {
	inventory := model.Inventory{
		Complete: false, Errors: []string{"Bingo pin discovery: boom"},
		Families: []model.Family{{
			Family: "github.com/example/tool", Kind: model.FamilyGoModule, Current: "1.0.0",
			Status: model.VersionBlocked,
			Pins:   []model.Pin{{ToolName: "tool", Source: ".bingo/tool.mod", Current: "1.0.0"}},
		}},
	}
	selection, err := SelectFamilies(inventory, map[string]string{"github.com/example/tool": "1.1.0"})
	if err != nil {
		t.Fatalf("SelectFamilies() error = %v", err)
	}
	if len(selection.Blockers) == 0 {
		t.Fatal("expected an incomplete-inventory blocker")
	}
}

func TestRequiredConsumersForToolsProfiles(t *testing.T) {
	planned := []model.PlannedFamily{
		{Family: "github.com/example/tool", Kind: model.FamilyGoModule, Targets: []model.PinTarget{{ToolName: "tool"}}},
	}
	consumers := RequiredConsumers(planned)
	ids := map[string]bool{}
	for _, consumer := range consumers {
		ids[consumer.ID] = true
	}
	for _, want := range []string{"bingo-regenerate", "ci-versions-verify"} {
		if !ids[want] {
			t.Fatalf("expected consumer %q in %v", want, consumers)
		}
	}
}

func TestSelectFamiliesAccountsForEveryRequestedFamily(t *testing.T) {
	inventory := model.Inventory{Complete: true, Families: []model.Family{
		{Family: "Azure/bicep", Kind: model.FamilyGitHubRelease, Current: "0.48.1", Status: model.VersionOutdated,
			Pins: []model.Pin{{ToolName: "bicep", Source: CIVersionsFile, Variable: "BICEP_VERSION", Current: "0.48.1"}}},
		{Family: "helm/helm", Kind: model.FamilyGitHubRelease, Current: "3.16.0", Status: model.VersionOutdated,
			Pins: []model.Pin{{ToolName: "helm", Source: CIVersionsFile, Variable: "HELM_VERSION", Current: "3.16.0"}}},
		{Family: "prometheus/prometheus", Kind: model.FamilyGitHubRelease, Current: "3.15.1", Status: model.VersionOutdated,
			Pins: []model.Pin{{ToolName: "promtool", Source: CIVersionsFile, Variable: "PROMTOOL_VERSION", Current: "3.15.1"}}},
	}}
	selection, err := SelectFamilies(inventory, map[string]string{
		"Azure/bicep":           "0.49.0",
		"helm/helm":             "3.17.0",
		"prometheus/prometheus": "3.16.0",
	})
	if err != nil {
		t.Fatalf("SelectFamilies() error = %v", err)
	}
	if len(selection.Families) != 1 || selection.Families[0].Family != "Azure/bicep" {
		t.Fatalf("prepared families = %+v, want only Azure/bicep", selection.Families)
	}
	reasons := map[string]string{}
	for _, skipped := range selection.Skipped {
		reasons[skipped.Family] = skipped.Reason
	}
	if !strings.Contains(reasons["helm/helm"], "one family per preparation") {
		t.Fatalf("helm/helm reason = %q", reasons["helm/helm"])
	}
	if !strings.Contains(reasons["prometheus/prometheus"], "attempt budget") {
		t.Fatalf("prometheus/prometheus reason = %q", reasons["prometheus/prometheus"])
	}
	accounted := map[string]bool{"Azure/bicep": true}
	for family := range reasons {
		accounted[family] = true
	}
	for _, requested := range []string{"Azure/bicep", "helm/helm", "prometheus/prometheus"} {
		if !accounted[requested] {
			t.Fatalf("requested family %s disappeared from the selection", requested)
		}
	}
}
