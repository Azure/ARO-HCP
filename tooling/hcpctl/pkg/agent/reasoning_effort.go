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

package agent

import (
	"fmt"
	"slices"
	"strings"
)

// Reasoning effort levels. An empty effort leaves the backend/model default
// in place. Whether a level has an effect depends on the selected model.
const (
	ReasoningEffortNone    = "none"
	ReasoningEffortMinimal = "minimal"
	ReasoningEffortLow     = "low"
	ReasoningEffortMedium  = "medium"
	ReasoningEffortHigh    = "high"
	ReasoningEffortXHigh   = "xhigh"
	ReasoningEffortMax     = "max"
)

// CopilotReasoningEfforts are the levels accepted by the Copilot runtime.
// The runtime stores unknown values verbatim instead of rejecting them, so
// they are validated client-side.
var CopilotReasoningEfforts = []string{
	ReasoningEffortNone,
	ReasoningEffortMinimal,
	ReasoningEffortLow,
	ReasoningEffortMedium,
	ReasoningEffortHigh,
	ReasoningEffortXHigh,
	ReasoningEffortMax,
}

// ClaudeReasoningEfforts are the levels accepted by both the Anthropic
// Messages API (output_config.effort) and the Claude Code CLI (--effort).
var ClaudeReasoningEfforts = []string{
	ReasoningEffortLow,
	ReasoningEffortMedium,
	ReasoningEffortHigh,
	ReasoningEffortXHigh,
	ReasoningEffortMax,
}

// ValidateCopilotReasoningEffort returns an error if effort is non-empty and
// not supported by the Copilot runtime.
func ValidateCopilotReasoningEffort(effort string) error {
	return validateReasoningEffort("copilot", effort, CopilotReasoningEfforts)
}

// ValidateClaudeReasoningEffort returns an error if effort is non-empty and
// not supported by the Claude providers.
func ValidateClaudeReasoningEffort(effort string) error {
	return validateReasoningEffort("claude", effort, ClaudeReasoningEfforts)
}

func validateReasoningEffort(provider, effort string, allowed []string) error {
	if effort == "" || slices.Contains(allowed, effort) {
		return nil
	}
	return fmt.Errorf("unsupported %s reasoning effort %q, must be one of: %s", provider, effort, strings.Join(allowed, ", "))
}
