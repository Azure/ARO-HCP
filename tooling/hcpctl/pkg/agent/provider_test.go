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

	"github.com/anthropics/anthropic-sdk-go"
	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"
)

func TestUsageReportAdd(t *testing.T) {
	var report UsageReport
	report.Add(UsageBreakdown{
		Provider:   "anthropic",
		Model:      "claude-test",
		Dimensions: map[string]string{"backend": "api", "serviceTier": "standard"},
		Requests:   1,
		Tokens: TokenUsage{
			UncachedInputTokens:        100,
			CacheReadInputTokens:       20,
			CacheWriteInputTokens:      30,
			CacheWriteInputTokensByTTL: map[string]int64{"5m": 30},
			OutputTokens:               25,
			OutputTokenDetails:         map[string]int64{"reasoning": 5},
		},
		AdditionalUnits:       map[string]float64{"webSearchRequests": 1},
		ProviderReportedCosts: map[string]float64{"credits": 1.5},
	})
	report.Add(UsageBreakdown{
		Provider:   "anthropic",
		Model:      "claude-test",
		Dimensions: map[string]string{"backend": "api", "serviceTier": "standard"},
		Requests:   1,
		Tokens: TokenUsage{
			UncachedInputTokens:        10,
			CacheReadInputTokens:       2,
			CacheWriteInputTokens:      4,
			CacheWriteInputTokensByTTL: map[string]int64{"1h": 4},
			OutputTokens:               5,
			OutputTokenDetails:         map[string]int64{"reasoning": 1},
		},
		AdditionalUnits:       map[string]float64{"webSearchRequests": 2},
		ProviderReportedCosts: map[string]float64{"credits": 0.5},
	})
	report.Add(UsageBreakdown{
		Provider:   "anthropic",
		Model:      "claude-test",
		Dimensions: map[string]string{"backend": "api", "serviceTier": "priority"},
		Requests:   1,
	})

	if report.Requests != 3 {
		t.Errorf("Requests = %d, want 3", report.Requests)
	}
	if report.Tokens.UncachedInputTokens != 110 || report.Tokens.CacheReadInputTokens != 22 || report.Tokens.CacheWriteInputTokens != 34 || report.Tokens.OutputTokens != 30 {
		t.Errorf("Tokens = %+v, want input=110 cacheRead=22 cacheWrite=34 output=30", report.Tokens)
	}
	if report.Tokens.TotalTokens != 196 {
		t.Errorf("TotalTokens = %d, want 196", report.Tokens.TotalTokens)
	}
	if report.Tokens.CacheWriteInputTokensByTTL["5m"] != 30 || report.Tokens.CacheWriteInputTokensByTTL["1h"] != 4 {
		t.Errorf("CacheWriteInputTokensByTTL = %+v, want 5m=30 1h=4", report.Tokens.CacheWriteInputTokensByTTL)
	}
	if report.Tokens.OutputTokenDetails["reasoning"] != 6 {
		t.Errorf("OutputTokenDetails = %+v, want reasoning=6", report.Tokens.OutputTokenDetails)
	}
	if report.AdditionalUnits["webSearchRequests"] != 3 {
		t.Errorf("AdditionalUnits = %+v, want webSearchRequests=3", report.AdditionalUnits)
	}
	if report.ProviderReportedCosts["credits"] != 2 {
		t.Errorf("ProviderReportedCosts = %+v, want credits=2", report.ProviderReportedCosts)
	}
	if len(report.Breakdown) != 2 {
		t.Fatalf("len(Breakdown) = %d, want 2", len(report.Breakdown))
	}
	if report.Breakdown[0].Requests != 2 {
		t.Errorf("Breakdown[0].Requests = %d, want 2", report.Breakdown[0].Requests)
	}
}

func TestUsageReportFromCopilotEvents(t *testing.T) {
	input1, output1 := int64(100), int64(25)
	cacheRead1, cacheWrite1, reasoning1 := int64(20), int64(10), int64(5)
	input2, output2 := int64(10), int64(5)
	cost1, cost2 := 1.5, 0.5
	endpoint := copilot.AssistantUsageAPIEndpointResponses

	usage := usageReportFromCopilotEvents([]copilot.SessionEvent{
		{Data: &copilot.AssistantUsageData{
			Model:            "gpt-test",
			APIEndpoint:      &endpoint,
			InputTokens:      &input1,
			OutputTokens:     &output1,
			CacheReadTokens:  &cacheRead1,
			CacheWriteTokens: &cacheWrite1,
			ReasoningTokens:  &reasoning1,
			Cost:             &cost1,
			CopilotUsage:     &rpc.AssistantUsageCopilotUsage{TotalNanoAiu: 7},
		}},
		{Data: &copilot.AssistantUsageData{Model: "gpt-test", APIEndpoint: &endpoint, InputTokens: &input2, OutputTokens: &output2, Cost: &cost2}},
		{Data: &copilot.SessionInfoData{}},
	}, "github-copilot", map[string]string{"backend": "github"})

	if usage.Requests != 2 {
		t.Errorf("Requests = %d, want 2", usage.Requests)
	}
	if usage.Tokens.UncachedInputTokens != 90 || usage.Tokens.CacheReadInputTokens != 20 || usage.Tokens.CacheWriteInputTokens != 10 || usage.Tokens.OutputTokens != 30 {
		t.Errorf("Tokens = %+v, want input=90 cacheRead=20 cacheWrite=10 output=30", usage.Tokens)
	}
	if usage.Tokens.TotalTokens != 150 {
		t.Errorf("TotalTokens = %d, want 150", usage.Tokens.TotalTokens)
	}
	if usage.Tokens.OutputTokenDetails["reasoning"] != 5 {
		t.Errorf("OutputTokenDetails = %+v, want reasoning=5", usage.Tokens.OutputTokenDetails)
	}
	if usage.ProviderReportedCosts["modelMultiplier"] != 2 || usage.ProviderReportedCosts["nanoAIU"] != 7 {
		t.Errorf("ProviderReportedCosts = %+v, want modelMultiplier=2 nanoAIU=7", usage.ProviderReportedCosts)
	}
	if len(usage.Breakdown) != 1 || usage.Breakdown[0].Dimensions["apiEndpoint"] != "/responses" {
		t.Errorf("Breakdown = %+v, want one /responses entry", usage.Breakdown)
	}
}

func TestUsageBreakdownFromClaudeResponse(t *testing.T) {
	response := &anthropic.Message{
		Model: "claude-test",
		Usage: anthropic.Usage{
			InputTokens:              100,
			CacheReadInputTokens:     200,
			CacheCreationInputTokens: 300,
			CacheCreation: anthropic.CacheCreation{
				Ephemeral5mInputTokens: 250,
				Ephemeral1hInputTokens: 50,
			},
			OutputTokens: 25,
			OutputTokensDetails: anthropic.OutputTokensDetails{
				ThinkingTokens: 5,
			},
			ServerToolUse: anthropic.ServerToolUsage{
				WebSearchRequests: 2,
				WebFetchRequests:  1,
			},
			ServiceTier:  anthropic.UsageServiceTierPriority,
			InferenceGeo: "us",
		},
	}

	entry := usageBreakdownFromClaudeResponse("anthropic", map[string]string{"backend": "api"}, response)
	if entry.Provider != "anthropic" || entry.Model != "claude-test" || entry.Requests != 1 {
		t.Errorf("identity = provider=%q model=%q requests=%d", entry.Provider, entry.Model, entry.Requests)
	}
	if entry.Dimensions["serviceTier"] != "priority" || entry.Dimensions["inferenceGeo"] != "us" {
		t.Errorf("Dimensions = %+v, want serviceTier=priority inferenceGeo=us", entry.Dimensions)
	}
	if entry.Tokens.UncachedInputTokens != 100 || entry.Tokens.CacheReadInputTokens != 200 || entry.Tokens.CacheWriteInputTokens != 300 || entry.Tokens.OutputTokens != 25 {
		t.Errorf("Tokens = %+v, want input=100 cacheRead=200 cacheWrite=300 output=25", entry.Tokens)
	}
	if entry.Tokens.CacheWriteInputTokensByTTL["5m"] != 250 || entry.Tokens.CacheWriteInputTokensByTTL["1h"] != 50 {
		t.Errorf("CacheWriteInputTokensByTTL = %+v, want 5m=250 1h=50", entry.Tokens.CacheWriteInputTokensByTTL)
	}
	if entry.Tokens.OutputTokenDetails["reasoning"] != 5 {
		t.Errorf("OutputTokenDetails = %+v, want reasoning=5", entry.Tokens.OutputTokenDetails)
	}
	if entry.AdditionalUnits["webSearchRequests"] != 2 || entry.AdditionalUnits["webFetchRequests"] != 1 {
		t.Errorf("AdditionalUnits = %+v, want webSearchRequests=2 webFetchRequests=1", entry.AdditionalUnits)
	}
}
