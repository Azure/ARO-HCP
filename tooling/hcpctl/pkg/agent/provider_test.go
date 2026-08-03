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
	"sync"
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
	compactionInput, compactionOutput := int64(50), int64(4)
	compactionCacheRead, compactionCacheWrite := int64(40), int64(3)
	cost1, cost2 := 1.5, 0.5
	endpoint := copilot.AssistantUsageAPIEndpointResponses
	apiCallID := "api-call-1"
	compactionServiceRequestID := "compaction-service-request"
	model := "gpt-test"
	firstUsage := &copilot.AssistantUsageData{
		Model:            model,
		APICallID:        &apiCallID,
		APIEndpoint:      &endpoint,
		InputTokens:      &input1,
		OutputTokens:     &output1,
		CacheReadTokens:  &cacheRead1,
		CacheWriteTokens: &cacheWrite1,
		ReasoningTokens:  &reasoning1,
		Cost:             &cost1,
		CopilotUsage:     &rpc.AssistantUsageCopilotUsage{TotalNanoAiu: 7},
	}
	compaction := &copilot.SessionCompactionCompleteData{
		Success:          true,
		ServiceRequestID: &compactionServiceRequestID,
		// Model is intentionally omitted to verify that compaction inherits the
		// most recently observed model when the optional SDK field is absent.
		CompactionTokensUsed: &copilot.CompactionCompleteCompactionTokensUsed{
			InputTokens:      &compactionInput,
			OutputTokens:     &compactionOutput,
			CacheReadTokens:  &compactionCacheRead,
			CacheWriteTokens: &compactionCacheWrite,
			CopilotUsage:     &rpc.CompactionCompleteCompactionTokensUsedCopilotUsage{TotalNanoAiu: 3},
		},
	}

	usage := usageReportFromCopilotEvents([]copilot.SessionEvent{
		{ID: "usage-event-1", Data: firstUsage},
		// The same API call may be replayed with a different event ID. It must
		// still be counted only once.
		{ID: "usage-event-1-replayed", Data: firstUsage},
		{ID: "usage-event-2", Data: &copilot.AssistantUsageData{Model: model, APIEndpoint: &endpoint, InputTokens: &input2, OutputTokens: &output2, Cost: &cost2}},
		{ID: "compaction-event", Data: compaction},
		// Compaction events use their request IDs for cross-delivery deduplication.
		{ID: "compaction-event-replayed", Data: compaction},
		{Data: &copilot.SessionInfoData{}},
	}, "github-copilot", map[string]string{"backend": "github"})

	if usage.Requests != 3 {
		t.Errorf("Requests = %d, want 3", usage.Requests)
	}
	if usage.Tokens.UncachedInputTokens != 100 || usage.Tokens.CacheReadInputTokens != 60 || usage.Tokens.CacheWriteInputTokens != 13 || usage.Tokens.OutputTokens != 34 {
		t.Errorf("Tokens = %+v, want input=100 cacheRead=60 cacheWrite=13 output=34", usage.Tokens)
	}
	if usage.Tokens.TotalTokens != 207 {
		t.Errorf("TotalTokens = %d, want 207", usage.Tokens.TotalTokens)
	}
	if usage.Tokens.OutputTokenDetails["reasoning"] != 5 {
		t.Errorf("OutputTokenDetails = %+v, want reasoning=5", usage.Tokens.OutputTokenDetails)
	}
	if usage.ProviderReportedCosts["modelMultiplier"] != 2 || usage.ProviderReportedCosts["nanoAIU"] != 10 {
		t.Errorf("ProviderReportedCosts = %+v, want modelMultiplier=2 nanoAIU=10", usage.ProviderReportedCosts)
	}
	if len(usage.Breakdown) != 2 || usage.Breakdown[0].Dimensions["apiEndpoint"] != "/responses" || usage.Breakdown[1].Requests != 1 || usage.Breakdown[1].Model != model {
		t.Errorf("Breakdown = %+v, want normal and compaction entries", usage.Breakdown)
	}
}

func TestCopilotCompactionUsageRequiresReportedTokens(t *testing.T) {
	input := int64(12)
	model := "gpt-test"
	usage := usageReportFromCopilotEvents([]copilot.SessionEvent{
		{ID: "failed-compaction-with-usage", Data: &copilot.SessionCompactionCompleteData{
			Success: false,
			CompactionTokensUsed: &copilot.CompactionCompleteCompactionTokensUsed{
				Model:       &model,
				InputTokens: &input,
			},
		}},
		{ID: "failed-compaction-without-usage", Data: &copilot.SessionCompactionCompleteData{Success: false}},
	}, "github-copilot", map[string]string{"backend": "github"})

	if usage.Requests != 1 || usage.Tokens.TotalTokens != input {
		t.Errorf("Usage() = requests:%d totalTokens:%d, want requests:1 totalTokens:%d", usage.Requests, usage.Tokens.TotalTokens, input)
	}
}

func TestRecordCopilotUsageEventConcurrent(t *testing.T) {
	session := &Session{
		usageProvider:   "github-copilot",
		usageDimensions: map[string]string{"backend": "github"},
		seenUsageKeys:   make(map[string]struct{}),
	}

	const requests = 50
	var wg sync.WaitGroup
	for i := 0; i < requests; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			input, output := int64(1), int64(1)
			apiCallID := fmt.Sprintf("api-call-%d", i)
			session.recordUsageEvent(copilot.SessionEvent{
				ID: fmt.Sprintf("usage-event-%d", i),
				Data: &copilot.AssistantUsageData{
					Model:        "gpt-test",
					APICallID:    &apiCallID,
					InputTokens:  &input,
					OutputTokens: &output,
				},
			})
			_ = session.Usage()
		}(i)
	}
	wg.Wait()

	usage := session.Usage()
	if usage.Requests != requests || usage.Tokens.TotalTokens != requests*2 {
		t.Errorf("Usage() = requests:%d totalTokens:%d, want requests:%d totalTokens:%d", usage.Requests, usage.Tokens.TotalTokens, requests, requests*2)
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
