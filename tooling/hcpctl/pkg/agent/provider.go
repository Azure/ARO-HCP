// Copyright 2025 Microsoft Corporation
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
	"context"
	"encoding/json"
	"time"

	"github.com/go-logr/logr"
)

// LLMProvider creates and manages LLM sessions for a specific backend.
// Implementations handle provider-specific authentication, client lifecycle,
// and session creation. The two built-in implementations are CopilotClient
// (GitHub Copilot SDK) and ClaudeProvider (Anthropic API).
type LLMProvider interface {
	// CreateProviderSession creates a new LLM session configured for
	// analysis. The provider translates the provider-neutral
	// ProviderSessionConfig into its native format (e.g. copilot
	// SessionConfig sections, Anthropic MessageNewParams).
	CreateProviderSession(ctx context.Context, logger logr.Logger, cfg ProviderSessionConfig) (LLMSession, error)

	// Stop shuts down the provider and releases all resources.
	Stop() error
}

// LLMSession is a single conversation with an LLM. Implementations handle
// provider-specific message protocols, tool-use loops, and conversation
// state management. The Analyze function and ValidateDraftLoop accept this
// interface rather than a concrete session type.
type LLMSession interface {
	// SendAndWait sends a user prompt and blocks until the model finishes
	// responding, including any tool-use rounds. Returns the final
	// assistant text content.
	SendAndWait(ctx context.Context, prompt string) (string, error)

	// Usage returns the aggregate usage observed by this session.
	// Implementations include every completed LLM request made while handling
	// SendAndWait calls, including requests made during tool-use loops.
	Usage() UsageReport

	// SaveConversation writes the conversation history to a JSON file at
	// the given path. This is best-effort: implementations should log
	// errors rather than return them.
	SaveConversation(path string)

	// SessionID returns a unique identifier for this session.
	SessionID() string

	// Disconnect releases in-memory session resources while preserving
	// on-disk state for potential later resumption.
	Disconnect() error

	// Delete permanently removes all session data.
	Delete(ctx context.Context) error
}

// ProviderSessionConfig holds provider-neutral configuration for creating
// an LLM session. Each LLMProvider translates these fields into its native
// format during CreateProviderSession.
//
// The prompt is split into three parts so the caller can assemble the full
// prompt centrally while each provider applies them in its native format:
//
//   - IdentityPrompt: who the model is (role, specialization).
//   - TonePrompt: how the model should respond (style, evidence rules).
//   - SystemPrompt: domain-specific content (system.md, references,
//     exemplars) built by BuildDomainPrompt.
//
// For example, the Copilot provider maps IdentityPrompt and TonePrompt to
// SDK section overrides, while the Claude provider concatenates all three
// into a single system message.
type ProviderSessionConfig struct {
	// IdentityPrompt carries the identity/role instructions that tell
	// the model who it is (e.g. "You are a senior SRE …").
	IdentityPrompt string

	// TonePrompt carries the tone/style instructions that tell the
	// model how to respond (e.g. "Be precise, evidence-driven …").
	TonePrompt string

	// SystemPrompt carries domain-specific content (system.md, references,
	// exemplars) built by BuildDomainPrompt.
	SystemPrompt string

	// Tools are provider-neutral tool definitions. Each provider converts
	// them to its native tool format (e.g. copilot.Tool, Anthropic tool
	// params).
	Tools []ToolDefinition

	// WorkingDirectory is the workspace root. Providers that support
	// file-access tools scope operations to this directory.
	WorkingDirectory string

	// Model overrides the provider's default model for this session.
	// When empty, the provider uses its configured default.
	Model string
}

// UsageReport contains the billable usage dimensions for an LLM session.
// Totals make the report convenient for operational telemetry, while Breakdown
// preserves the provider, model, backend, service tier, and other dimensions
// needed to apply an external price book accurately. StartedAt and EndedAt
// bound the reporting interval so consumers can select time-dependent rates.
type UsageReport struct {
	StartedAt             time.Time          `json:"startedAt"`
	EndedAt               time.Time          `json:"endedAt"`
	Requests              int64              `json:"requests"`
	Tokens                TokenUsage         `json:"tokens"`
	AdditionalUnits       map[string]float64 `json:"additionalUnits,omitempty"`
	ProviderReportedCosts map[string]float64 `json:"providerReportedCosts,omitempty"`
	Breakdown             []UsageBreakdown   `json:"breakdown,omitempty"`
}

// UsageBreakdown aggregates requests with the same pricing identity.
// Dimensions is deliberately open-ended so providers can expose pricing
// attributes without changing the common schema.
type UsageBreakdown struct {
	Provider              string             `json:"provider"`
	Model                 string             `json:"model"`
	Dimensions            map[string]string  `json:"dimensions,omitempty"`
	Requests              int64              `json:"requests"`
	Tokens                TokenUsage         `json:"tokens"`
	AdditionalUnits       map[string]float64 `json:"additionalUnits,omitempty"`
	ProviderReportedCosts map[string]float64 `json:"providerReportedCosts,omitempty"`
}

// TokenUsage separates the token categories that may have different prices.
// The input categories are normalized to be mutually exclusive, even when a
// provider reports cache reads as part of its input token count.
type TokenUsage struct {
	UncachedInputTokens        int64            `json:"uncachedInputTokens"`
	CacheReadInputTokens       int64            `json:"cacheReadInputTokens"`
	CacheWriteInputTokens      int64            `json:"cacheWriteInputTokens"`
	CacheWriteInputTokensByTTL map[string]int64 `json:"cacheWriteInputTokensByTTL,omitempty"`
	OutputTokens               int64            `json:"outputTokens"`
	OutputTokenDetails         map[string]int64 `json:"outputTokenDetails,omitempty"`
	TotalTokens                int64            `json:"totalTokens"`
}

// Add merges token usage and recomputes TotalTokens from the mutually
// exclusive top-level token categories. Token details are subsets and are not
// added to the total.
func (u *TokenUsage) Add(other TokenUsage) {
	u.UncachedInputTokens += other.UncachedInputTokens
	u.CacheReadInputTokens += other.CacheReadInputTokens
	u.CacheWriteInputTokens += other.CacheWriteInputTokens
	u.OutputTokens += other.OutputTokens
	addInt64Values(&u.CacheWriteInputTokensByTTL, other.CacheWriteInputTokensByTTL)
	addInt64Values(&u.OutputTokenDetails, other.OutputTokenDetails)
	u.TotalTokens = u.UncachedInputTokens + u.CacheReadInputTokens + u.CacheWriteInputTokens + u.OutputTokens
}

// Add merges one pricing-homogeneous usage entry into the report.
func (u *UsageReport) Add(entry UsageBreakdown) {
	u.Requests += entry.Requests
	u.Tokens.Add(entry.Tokens)
	addFloat64Values(&u.AdditionalUnits, entry.AdditionalUnits)
	addFloat64Values(&u.ProviderReportedCosts, entry.ProviderReportedCosts)

	for i := range u.Breakdown {
		if u.Breakdown[i].Provider == entry.Provider &&
			u.Breakdown[i].Model == entry.Model &&
			stringMapsEqual(u.Breakdown[i].Dimensions, entry.Dimensions) {
			u.Breakdown[i].Requests += entry.Requests
			u.Breakdown[i].Tokens.Add(entry.Tokens)
			addFloat64Values(&u.Breakdown[i].AdditionalUnits, entry.AdditionalUnits)
			addFloat64Values(&u.Breakdown[i].ProviderReportedCosts, entry.ProviderReportedCosts)
			return
		}
	}

	entry.Tokens.TotalTokens = entry.Tokens.UncachedInputTokens + entry.Tokens.CacheReadInputTokens + entry.Tokens.CacheWriteInputTokens + entry.Tokens.OutputTokens
	entry.Dimensions = cloneStringMap(entry.Dimensions)
	entry.Tokens.CacheWriteInputTokensByTTL = cloneInt64Map(entry.Tokens.CacheWriteInputTokensByTTL)
	entry.Tokens.OutputTokenDetails = cloneInt64Map(entry.Tokens.OutputTokenDetails)
	entry.AdditionalUnits = cloneFloat64Map(entry.AdditionalUnits)
	entry.ProviderReportedCosts = cloneFloat64Map(entry.ProviderReportedCosts)
	u.Breakdown = append(u.Breakdown, entry)
}

// Clone returns a deep copy suitable for returning while a provider may still
// be recording usage in another goroutine.
func (u UsageReport) Clone() UsageReport {
	clone := UsageReport{
		StartedAt:             u.StartedAt,
		EndedAt:               u.EndedAt,
		Requests:              u.Requests,
		Tokens:                u.Tokens.clone(),
		AdditionalUnits:       cloneFloat64Map(u.AdditionalUnits),
		ProviderReportedCosts: cloneFloat64Map(u.ProviderReportedCosts),
		Breakdown:             make([]UsageBreakdown, len(u.Breakdown)),
	}
	for i := range u.Breakdown {
		clone.Breakdown[i] = UsageBreakdown{
			Provider:              u.Breakdown[i].Provider,
			Model:                 u.Breakdown[i].Model,
			Dimensions:            cloneStringMap(u.Breakdown[i].Dimensions),
			Requests:              u.Breakdown[i].Requests,
			Tokens:                u.Breakdown[i].Tokens.clone(),
			AdditionalUnits:       cloneFloat64Map(u.Breakdown[i].AdditionalUnits),
			ProviderReportedCosts: cloneFloat64Map(u.Breakdown[i].ProviderReportedCosts),
		}
	}
	return clone
}

func (u TokenUsage) clone() TokenUsage {
	u.CacheWriteInputTokensByTTL = cloneInt64Map(u.CacheWriteInputTokensByTTL)
	u.OutputTokenDetails = cloneInt64Map(u.OutputTokenDetails)
	return u
}

func addInt64Values(destination *map[string]int64, source map[string]int64) {
	if len(source) == 0 {
		return
	}
	if *destination == nil {
		*destination = make(map[string]int64, len(source))
	}
	for key, value := range source {
		(*destination)[key] += value
	}
}

func addFloat64Values(destination *map[string]float64, source map[string]float64) {
	if len(source) == 0 {
		return
	}
	if *destination == nil {
		*destination = make(map[string]float64, len(source))
	}
	for key, value := range source {
		(*destination)[key] += value
	}
}

func stringMapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for key, value := range a {
		if b[key] != value {
			return false
		}
	}
	return true
}

func cloneStringMap(source map[string]string) map[string]string {
	if source == nil {
		return nil
	}
	clone := make(map[string]string, len(source))
	for key, value := range source {
		clone[key] = value
	}
	return clone
}

func cloneInt64Map(source map[string]int64) map[string]int64 {
	if source == nil {
		return nil
	}
	clone := make(map[string]int64, len(source))
	for key, value := range source {
		clone[key] = value
	}
	return clone
}

func cloneFloat64Map(source map[string]float64) map[string]float64 {
	if source == nil {
		return nil
	}
	clone := make(map[string]float64, len(source))
	for key, value := range source {
		clone[key] = value
	}
	return clone
}

// ToolDefinition is a provider-neutral description of a tool that can be
// called by the LLM during a conversation. Each LLMProvider converts this
// to its native tool format.
//
// The ParamSchema field holds a standard JSON Schema object describing the
// tool's input parameters. The Handler function is called when the model
// invokes the tool, receiving the raw JSON arguments and returning a text
// result for the model to consume.
type ToolDefinition struct {
	// Name is the tool's unique identifier (e.g. "kusto_query").
	Name string

	// Description explains what the tool does. This is shown to the model
	// to help it decide when and how to use the tool.
	Description string

	// ParamSchema is the JSON Schema for the tool's input parameters,
	// serialized as a JSON object. Example:
	//
	//   {"type":"object","properties":{"kql":{"type":"string",
	//     "description":"The KQL query to execute."}},"required":["kql"]}
	ParamSchema json.RawMessage

	// Handler executes the tool with the given JSON-encoded parameters
	// and returns the text result. The context carries cancellation and
	// tracing information from the provider's session.
	Handler func(ctx context.Context, params json.RawMessage) (string, error)
}
