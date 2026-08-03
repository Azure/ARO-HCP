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
	"testing"

	copilot "github.com/github/copilot-sdk/go"
)

func TestTokenUsageAdd(t *testing.T) {
	var usage TokenUsage

	usage.Add(100, 25)
	usage.Add(10, 5)

	if usage.InputTokens != 110 {
		t.Errorf("InputTokens = %d, want 110", usage.InputTokens)
	}
	if usage.OutputTokens != 30 {
		t.Errorf("OutputTokens = %d, want 30", usage.OutputTokens)
	}
	if usage.TotalTokens != 140 {
		t.Errorf("TotalTokens = %d, want 140", usage.TotalTokens)
	}
	if usage.Requests != 2 {
		t.Errorf("Requests = %d, want 2", usage.Requests)
	}
}

func TestTokenUsageFromCopilotEvents(t *testing.T) {
	input1, output1 := int64(100), int64(25)
	input2, output2 := int64(10), int64(5)

	usage := tokenUsageFromCopilotEvents([]copilot.SessionEvent{
		{Data: &copilot.AssistantUsageData{InputTokens: &input1, OutputTokens: &output1}},
		{Data: &copilot.AssistantUsageData{InputTokens: &input2, OutputTokens: &output2}},
		{Data: &copilot.SessionInfoData{}},
	})

	if usage.InputTokens != 110 || usage.OutputTokens != 30 || usage.TotalTokens != 140 || usage.Requests != 2 {
		t.Errorf("usage = %+v, want input=110 output=30 total=140 requests=2", usage)
	}
}
