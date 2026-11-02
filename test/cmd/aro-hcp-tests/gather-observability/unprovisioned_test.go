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

package gatherobservability

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
)

// TestUnprovisionedJUnitKeepsUnresolvedWorkspaces pins the three-case JUnit
// contract on the skip path. Complete stores a workspace whose configured name
// is missing or invalid in workspaceErrors instead of Workspaces, so deriving
// the cases from Workspaces alone would drop that case exactly when collection
// is also skipped.
func TestUnprovisionedJUnitKeepsUnresolvedWorkspaces(t *testing.T) {
	t.Parallel()
	reason := errors.New("region resource group /subscriptions/sub/resourceGroups/hcp-underlay-ci01-dm does not exist")

	resolvedSvc := *mustParseResourceID("sub", "hcp-underlay-ci01-dm", "svc")
	resolvedHcp := *mustParseResourceID("sub", "hcp-underlay-ci01-dm", "hcp")

	for _, tc := range []struct {
		name          string
		workspaces    map[string]azcorearm.ResourceID
		workspaceErrs map[string]error
	}{
		{
			name:       "every workspace resolved",
			workspaces: map[string]azcorearm.ResourceID{workspaceSvc: resolvedSvc, workspaceHcp: resolvedHcp},
		},
		{
			name:          "one workspace name unresolvable",
			workspaces:    map[string]azcorearm.ResourceID{workspaceSvc: resolvedSvc},
			workspaceErrs: map[string]error{workspaceHcp: errors.New("failed to get hcp workspace name from config: secret-error")},
		},
		{
			name: "every workspace name unresolvable",
			workspaceErrs: map[string]error{
				workspaceSvc: errors.New("failed to get svc workspace name from config: secret-error"),
				workspaceHcp: errors.New("failed to get hcp workspace name from config: secret-error"),
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			o := Options{completedOptions: &completedOptions{
				Workspaces:      tc.workspaces,
				workspaceErrors: tc.workspaceErrs,
			}}

			suites := o.unprovisionedJUnit(reason)
			if len(suites.Suites) != 1 {
				t.Fatalf("expected a single suite, got %d", len(suites.Suites))
			}
			suite := suites.Suites[0]
			if suite.NumTests != 3 || suite.NumSkipped != 3 || suite.NumFailed != 0 {
				t.Fatalf("expected three skipped cases and no failures, got %+v", suite)
			}
			var names []string
			for _, testCase := range suite.TestCases {
				names = append(names, testCase.Name)
				if testCase.SkipMessage == nil || !strings.Contains(testCase.SkipMessage.Message, "not evaluated: "+reason.Error()) {
					t.Errorf("case %q is not an explicit skip with the collection reason: %+v", testCase.Name, testCase)
				}
				// Workspace setup errors can carry credential diagnostics, so the
				// skip must not embed them.
				if testCase.SkipMessage != nil && strings.Contains(testCase.SkipMessage.Message, "secret-error") {
					t.Errorf("case %q leaks the workspace setup error: %+v", testCase.Name, testCase)
				}
			}
			slices.Sort(names)
			want := []string{
				fmt.Sprintf("[aro-hcp-observability] [%s] alert collection is complete", workspaceHcp),
				fmt.Sprintf("[aro-hcp-observability] [%s] alert collection is complete", workspaceInfra),
				fmt.Sprintf("[aro-hcp-observability] [%s] alert collection is complete", workspaceSvc),
			}
			if !slices.Equal(names, want) {
				t.Errorf("wrong skipped cases: got %v, want %v", names, want)
			}
		})
	}
}
