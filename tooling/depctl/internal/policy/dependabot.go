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

import "strings"

// Security remediation constants, preserved from the current automation.
const (
	// SecurityLabel marks an owned security remediation proposal.
	SecurityLabel = "agentic-dependabot"
	// SecurityTitlePrefix starts every owned security proposal title.
	SecurityTitlePrefix = "fix(deps): "
	// SecurityWorkflowMarker identifies the remediation workflow in a body.
	SecurityWorkflowMarker = "<!-- gh-aw-workflow-id: dependabot-remediation -->"
)

// IsSecurityProposal reports whether a proposal carries the security markers,
// including the pre-label publication window.
func IsSecurityProposal(pull PullRequest) bool {
	for _, label := range pull.Labels {
		if label == SecurityLabel {
			return true
		}
	}
	if !strings.HasPrefix(pull.Title, SecurityTitlePrefix) {
		return false
	}
	body := strings.NewReplacer("\r\n", "\n", "\r", "\n").Replace(pull.Body)
	for _, line := range strings.Split(body, "\n") {
		if line == SecurityWorkflowMarker {
			return true
		}
	}
	return false
}
