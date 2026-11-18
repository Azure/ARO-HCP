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

package dependabot

import (
	"errors"
	"testing"

	"github.com/Azure/ARO-HCP/tooling/depctl/internal/forge"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/policy"
)

const head = "39af3a51dca8b0507907cd9f00c2d33ad659e227"

func securityPull() forge.PullRequest {
	return forge.PullRequest{
		Number: 7892, Author: policy.Robot, State: "open",
		Title:   "fix(deps): sprintf-js to 1.1.3",
		Body:    "body\n" + policy.SecurityWorkflowMarker + "\n",
		HeadRef: "dependabot/go_modules/api/github.com/example", HeadSHA: head,
		HeadRepo: "Azure/ARO-HCP", BaseRef: "main", BaseRepo: "Azure/ARO-HCP",
	}
}

func TestOwnedSecurityProposalsUsesMarkerOrLabel(t *testing.T) {
	labelled := securityPull()
	labelled.Number, labelled.Title, labelled.Body = 1, "unrelated title", ""
	labelled.Labels = []string{policy.SecurityLabel}
	unrelated := securityPull()
	unrelated.Number, unrelated.Title, unrelated.Body, unrelated.Labels = 2, "chore(deps): x", "", nil
	human := securityPull()
	human.Number, human.Author = 3, "raelga"
	otherBase := securityPull()
	otherBase.Number, otherBase.BaseRef = 4, "release"
	records, err := OwnedSecurityProposals(
		[]forge.PullRequest{securityPull(), labelled, unrelated, human, otherBase}, "Azure/ARO-HCP")
	if err != nil {
		t.Fatalf("OwnedSecurityProposals() error = %v", err)
	}
	if len(records) != 2 || records[0].Number != 7892 || records[1].Number != 1 {
		t.Fatalf("records = %+v", records)
	}
	if records[0].SHA != head || records[0].Body == "" || records[0].HeadRepo != "Azure/ARO-HCP" {
		t.Fatalf("record = %+v", records[0])
	}
}

func TestOwnedSecurityProposalsFailsClosed(t *testing.T) {
	foreign := securityPull()
	foreign.HeadRepo = "someone/ARO-HCP"
	shortSHA := securityPull()
	shortSHA.HeadSHA = "abc"
	noRef := securityPull()
	noRef.HeadRef = ""
	foreignBase := securityPull()
	foreignBase.BaseRepo = "someone/ARO-HCP"
	for name, pull := range map[string]forge.PullRequest{
		"foreign head": foreign, "short sha": shortSHA, "missing head ref": noRef, "foreign base": foreignBase,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := OwnedSecurityProposals([]forge.PullRequest{pull}, "Azure/ARO-HCP"); !errors.Is(err, ErrInventory) {
				t.Fatalf("error = %v, want ErrInventory", err)
			}
		})
	}
	if _, err := OwnedSecurityProposals([]forge.PullRequest{securityPull(), securityPull()}, "Azure/ARO-HCP"); !errors.Is(err, ErrInventory) {
		t.Fatalf("duplicate error = %v, want ErrInventory", err)
	}
}
