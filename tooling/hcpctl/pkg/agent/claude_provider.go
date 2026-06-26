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
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-logr/logr"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

const (
	// DefaultClaudeModel is the default Anthropic model used when no
	// model override is specified.
	DefaultClaudeModel = "claude-sonnet-4-20250514"

	// claudeMaxTokens is the maximum number of output tokens per API call.
	claudeMaxTokens int64 = 16384
)

// Compile-time check: *ClaudeProvider implements LLMProvider.
var _ LLMProvider = (*ClaudeProvider)(nil)

// Compile-time check: *ClaudeSession implements LLMSession.
var _ LLMSession = (*ClaudeSession)(nil)

// ClaudeConfig holds configuration for the Anthropic Claude provider.
type ClaudeConfig struct {
	// APIKeyFile is the path to a file containing the Anthropic API key.
	// If empty, the ANTHROPIC_API_KEY environment variable is used.
	APIKeyFile string

	// Model is the Anthropic model to use (e.g. "claude-sonnet-4-20250514").
	// Defaults to DefaultClaudeModel.
	Model string

	// Verbosity is the log verbosity level from the CLI.
	Verbosity int
}

// ClaudeProvider implements LLMProvider using the Anthropic Messages API.
// It manages a single anthropic.Client and creates ClaudeSession instances
// for individual analysis runs.
type ClaudeProvider struct {
	client anthropic.Client
	cfg    *ClaudeConfig
}

// NewClaudeProvider creates a ClaudeProvider configured with the given API key
// and model settings.
func NewClaudeProvider(cfg *ClaudeConfig) (*ClaudeProvider, error) {
	var opts []option.RequestOption

	if cfg.APIKeyFile != "" {
		keyData, err := os.ReadFile(cfg.APIKeyFile)
		if err != nil {
			return nil, fmt.Errorf("reading Anthropic API key from %s: %w", cfg.APIKeyFile, err)
		}
		opts = append(opts, option.WithAPIKey(strings.TrimSpace(string(keyData))))
	}
	// If no key file, the SDK reads ANTHROPIC_API_KEY from the environment.

	return &ClaudeProvider{
		client: anthropic.NewClient(opts...),
		cfg:    cfg,
	}, nil
}

// CreateProviderSession creates a new ClaudeSession for an analysis run.
func (p *ClaudeProvider) CreateProviderSession(ctx context.Context, logger logr.Logger, cfg ProviderSessionConfig) (LLMSession, error) {
	model := cfg.Model
	if model == "" {
		model = p.cfg.Model
	}
	if model == "" {
		model = DefaultClaudeModel
	}

	// Convert provider-neutral tool definitions to Anthropic tool params.
	tools := make([]anthropic.ToolUnionParam, 0, len(cfg.Tools))
	for _, td := range cfg.Tools {
		at, err := toolDefinitionToAnthropicTool(td)
		if err != nil {
			return nil, fmt.Errorf("converting tool %q: %w", td.Name, err)
		}
		tools = append(tools, at)
	}

	sessionID := fmt.Sprintf("claude-%d", time.Now().UnixNano())
	logger = logger.WithValues("sessionID", sessionID)
	logger.Info("Created Claude session.", "model", model)

	return &ClaudeSession{
		client:       p.client,
		model:        model,
		systemPrompt: cfg.SystemPrompt,
		tools:        tools,
		toolHandlers: buildToolHandlerMap(cfg.Tools),
		messages:     nil,
		sessionID:    sessionID,
		logger:       logger,
		verbosity:    p.cfg.Verbosity,
	}, nil
}

// Stop is a no-op for the Claude provider — the HTTP client has no
// long-running subprocess to shut down.
func (p *ClaudeProvider) Stop() error {
	return nil
}

// ClaudeSession implements LLMSession using the Anthropic Messages API.
// It maintains the conversation history and handles the tool-use loop
// internally within SendAndWait.
type ClaudeSession struct {
	client       anthropic.Client
	model        string
	systemPrompt string
	tools        []anthropic.ToolUnionParam
	toolHandlers map[string]func(ctx context.Context, params json.RawMessage) (string, error)
	messages     []anthropic.MessageParam
	sessionID    string
	logger       logr.Logger
	verbosity    int
}

// SessionID returns the unique identifier for this session.
func (s *ClaudeSession) SessionID() string {
	return s.sessionID
}

// SendAndWait sends a user prompt and blocks until Claude finishes responding,
// including any tool-use rounds. It implements the tool-use loop: when Claude
// returns tool_use content blocks, the session calls the corresponding handler,
// sends the tool_result back, and waits for the next response.
func (s *ClaudeSession) SendAndWait(ctx context.Context, prompt string) (string, error) {
	s.logger.V(1).Info("Sending message to Claude session.", "promptLength", len(prompt))

	// Add user message to conversation history.
	s.messages = append(s.messages, anthropic.NewUserMessage(
		anthropic.NewTextBlock(prompt),
	))

	// Tool-use loop.
	for {
		params := anthropic.MessageNewParams{
			Model:     s.model,
			MaxTokens: claudeMaxTokens,
			System: []anthropic.TextBlockParam{
				{Text: s.systemPrompt},
			},
			Messages: s.messages,
		}
		if len(s.tools) > 0 {
			params.Tools = s.tools
		}

		s.logger.V(3).Info("Calling Anthropic Messages API.", "messageCount", len(s.messages))

		resp, err := s.client.Messages.New(ctx, params)
		if err != nil {
			return "", fmt.Errorf("claude API call failed: %w", err)
		}

		s.logger.V(3).Info("Claude API responded.",
			"stopReason", resp.StopReason,
			"inputTokens", resp.Usage.InputTokens,
			"outputTokens", resp.Usage.OutputTokens,
		)

		// Add assistant response to conversation history.
		s.messages = append(s.messages, resp.ToParam())

		// If stop reason is not tool_use, extract text and return.
		if resp.StopReason != anthropic.StopReasonToolUse {
			return extractTextFromResponse(resp), nil
		}

		// Process tool use blocks.
		toolResults, err := s.processToolUseBlocks(ctx, resp)
		if err != nil {
			return "", err
		}

		// Add tool results as user message and continue the loop.
		s.messages = append(s.messages, anthropic.NewUserMessage(toolResults...))
	}
}

// processToolUseBlocks iterates over the response content blocks, identifies
// tool_use blocks, calls the corresponding handler, and returns tool_result
// content blocks.
func (s *ClaudeSession) processToolUseBlocks(ctx context.Context, resp *anthropic.Message) ([]anthropic.ContentBlockParamUnion, error) {
	var results []anthropic.ContentBlockParamUnion

	for _, block := range resp.Content {
		variant := block.AsAny()
		toolUse, ok := variant.(anthropic.ToolUseBlock)
		if !ok {
			continue
		}

		s.logger.V(3).Info("Claude requested tool call.",
			"toolName", toolUse.Name,
			"toolCallID", toolUse.ID,
		)

		handler, exists := s.toolHandlers[toolUse.Name]
		if !exists {
			errMsg := fmt.Sprintf("unknown tool %q", toolUse.Name)
			s.logger.Error(fmt.Errorf("unknown tool: %s", toolUse.Name), "Tool not found.")
			results = append(results, anthropic.NewToolResultBlock(
				toolUse.ID, errMsg, true,
			))
			continue
		}

		// Extract raw JSON input from the tool use block.
		rawInput := toolUse.Input

		result, err := handler(ctx, rawInput)
		if err != nil {
			s.logger.V(3).Info("Tool call returned error.",
				"toolName", toolUse.Name,
				"error", err.Error(),
			)
			results = append(results, anthropic.NewToolResultBlock(
				toolUse.ID, err.Error(), true,
			))
			continue
		}

		s.logger.V(3).Info("Tool call succeeded.",
			"toolName", toolUse.Name,
			"resultLength", len(result),
		)
		results = append(results, anthropic.NewToolResultBlock(
			toolUse.ID, result, false,
		))
	}

	return results, nil
}

// SaveConversation writes the conversation history to a JSON file.
func (s *ClaudeSession) SaveConversation(path string) {
	if len(s.messages) == 0 {
		s.logger.Info("No conversation messages to save.")
		return
	}

	data, err := json.MarshalIndent(s.messages, "", "  ")
	if err != nil {
		s.logger.Error(err, "Failed to marshal conversation for saving.")
		return
	}

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		s.logger.Error(err, "Failed to create directory for conversation dump.", "path", path)
		return
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		s.logger.Error(err, "Failed to write conversation to disk.", "path", path)
		return
	}

	s.logger.Info("Wrote conversation to disk.", "path", path, "size", len(data))
}

// Disconnect is a no-op for the Claude provider — there is no persistent
// session state to disconnect from.
func (s *ClaudeSession) Disconnect() error {
	return nil
}

// Delete is a no-op for the Claude provider — there is no server-side
// session state to delete.
func (s *ClaudeSession) Delete(_ context.Context) error {
	return nil
}

// toolDefinitionToAnthropicTool converts a provider-neutral ToolDefinition
// to an Anthropic ToolUnionParam.
func toolDefinitionToAnthropicTool(td ToolDefinition) (anthropic.ToolUnionParam, error) {
	// Parse the JSON schema to extract properties and required fields.
	var schema struct {
		Properties map[string]any `json:"properties"`
		Required   []string       `json:"required"`
	}
	if len(td.ParamSchema) > 0 {
		if err := json.Unmarshal(td.ParamSchema, &schema); err != nil {
			return anthropic.ToolUnionParam{}, fmt.Errorf("parsing parameter schema: %w", err)
		}
	}

	return anthropic.ToolUnionParam{
		OfTool: &anthropic.ToolParam{
			Name:        td.Name,
			Description: anthropic.String(td.Description),
			InputSchema: anthropic.ToolInputSchemaParam{
				Properties: schema.Properties,
				Required:   schema.Required,
			},
		},
	}, nil
}

// buildToolHandlerMap creates a name→handler lookup map from tool definitions.
func buildToolHandlerMap(tools []ToolDefinition) map[string]func(ctx context.Context, params json.RawMessage) (string, error) {
	m := make(map[string]func(ctx context.Context, params json.RawMessage) (string, error), len(tools))
	for _, td := range tools {
		m[td.Name] = td.Handler
	}
	return m
}

// extractTextFromResponse concatenates all text content blocks from an
// Anthropic response into a single string.
func extractTextFromResponse(resp *anthropic.Message) string {
	var parts []string
	for _, block := range resp.Content {
		variant := block.AsAny()
		if textBlock, ok := variant.(anthropic.TextBlock); ok {
			parts = append(parts, textBlock.Text)
		}
	}
	return strings.Join(parts, "")
}
