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
