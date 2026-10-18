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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/go-logr/logr"
	claudeagent "github.com/roasbeef/claude-agent-sdk-go"
)

// DefaultClaudeCodeModel is explicit because SDK v1.1.0 rejects an empty model.
const DefaultClaudeCodeModel = "claude-sonnet-4-5-20250929"

const claudeCodeMCPServerName = "hcpctl"

const claudeCodeMaxPromptArgumentBytes = 100 * 1024

const claudeCodeStdinInstructions = "The first user message starts with the analysis instructions and reference material. " +
	"Apply those instructions throughout this conversation."

var _ LLMProvider = (*ClaudeCodeProvider)(nil)
var _ LLMSession = (*ClaudeCodeSession)(nil)

type ClaudeCodeConfig struct {
	Model         string
	APIKeyFile    string
	Backend       string
	VertexProject string
	VertexRegion  string
}

type claudeCodeClient interface {
	Connect(context.Context) error
	Query(context.Context, string) iter.Seq[claudeagent.Message]
	Close() error
}

type ClaudeCodeProvider struct {
	cfg       ClaudeCodeConfig
	env       map[string]string
	newClient func(...claudeagent.Option) (claudeCodeClient, error)
	mu        sync.Mutex
	sessions  []*ClaudeCodeSession
	stopped   bool
}

func NewClaudeCodeProvider(cfg *ClaudeCodeConfig) (*ClaudeCodeProvider, error) {
	provider := &ClaudeCodeProvider{
		newClient: func(opts ...claudeagent.Option) (claudeCodeClient, error) {
			return claudeagent.NewClient(opts...)
		},
	}
	if cfg != nil {
		provider.cfg = *cfg
	}
	switch provider.cfg.Backend {
	case ClaudeBackendAPI, "":
		provider.env = map[string]string{
			"CLAUDE_CODE_USE_VERTEX":  "0",
			"CLAUDE_CODE_USE_BEDROCK": "0",
		}
		if provider.cfg.APIKeyFile != "" {
			data, err := os.ReadFile(provider.cfg.APIKeyFile)
			if err != nil {
				return nil, fmt.Errorf("reading Anthropic API key: %w", err)
			}
			key := strings.TrimSpace(string(data))
			if key == "" {
				return nil, fmt.Errorf("anthropic API key file is empty")
			}
			provider.env["ANTHROPIC_API_KEY"] = key
		}
	case ClaudeBackendVertex:
		if provider.cfg.VertexProject == "" {
			return nil, fmt.Errorf("vertex project is required when Claude backend is %q", ClaudeBackendVertex)
		}
		if provider.cfg.VertexRegion == "" {
			return nil, fmt.Errorf("vertex region is required when Claude backend is %q", ClaudeBackendVertex)
		}
		provider.env = map[string]string{
			"CLAUDE_CODE_USE_VERTEX":      "1",
			"CLAUDE_CODE_USE_BEDROCK":     "0",
			"ANTHROPIC_VERTEX_PROJECT_ID": provider.cfg.VertexProject,
			"CLOUD_ML_REGION":             provider.cfg.VertexRegion,
		}
	default:
		return nil, fmt.Errorf("unknown Claude backend %q, must be one of: api, vertex", provider.cfg.Backend)
	}
	return provider, nil
}

func (p *ClaudeCodeProvider) CreateProviderSession(ctx context.Context, logger logr.Logger, cfg ProviderSessionConfig) (LLMSession, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stopped {
		return nil, fmt.Errorf("claude-code provider is stopped")
	}
	if cfg.Model == "" {
		cfg.Model = p.cfg.Model
	}
	if cfg.Model == "" {
		cfg.Model = DefaultClaudeCodeModel
	}
	opts, initialInstructions, err := claudeCodeSessionOptions(cfg)
	if err != nil {
		return nil, err
	}
	if p.env != nil {
		opts = append(opts, claudeagent.WithEnv(p.env))
	}
	client, err := p.newClient(opts...)
	if err != nil {
		return nil, fmt.Errorf("creating Claude Code client: %w", err)
	}
	if err := client.Connect(ctx); err != nil {
		if closeErr := client.Close(); closeErr != nil {
			logger.Error(closeErr, "Failed to close Claude Code client after connection failure.")
		}
		return nil, fmt.Errorf("connecting Claude Code client: %w", err)
	}
	session := &ClaudeCodeSession{
		client:              client,
		logger:              logger,
		model:               cfg.Model,
		id:                  fmt.Sprintf("claude-code-%d", time.Now().UnixNano()),
		initialInstructions: initialInstructions,
	}
	p.sessions = append(p.sessions, session)
	logger.Info("Created Claude Code session.", "sessionID", session.id, "model", cfg.Model)
	return session, nil
}

func claudeCodeSessionOptions(cfg ProviderSessionConfig) ([]claudeagent.Option, string, error) {
	if cfg.WorkingDirectory == "" {
		return nil, "", fmt.Errorf("claude-code working directory is required")
	}
	info, err := os.Stat(cfg.WorkingDirectory)
	if err != nil {
		return nil, "", fmt.Errorf("checking Claude Code working directory: %w", err)
	}
	if !info.IsDir() {
		return nil, "", fmt.Errorf("claude-code working directory is not a directory")
	}
	server, err := buildClaudeCodeMCPServer(cfg.Tools)
	if err != nil {
		return nil, "", err
	}
	preamble := cfg.IdentityPrompt + "\n\n" + cfg.TonePrompt
	domain := cfg.SystemPrompt
	for _, tool := range cfg.Tools {
		domain += fmt.Sprintf("\nUse mcp__%s__%s whenever these instructions refer to the %s tool.", claudeCodeMCPServerName, tool.Name, tool.Name)
	}
	prompt := preamble + "\n\n" + domain
	var initialInstructions string
	// SDK v1.1.0 puts WithSystemPrompt in a single argv element, subject to
	// Linux's ~128 KiB per-argument execve limit. Keep small identity/tone as
	// system instructions and send the complete bulk content once via Query's
	// stdin stream-json user message instead. No instruction bytes are truncated;
	// an oversized identity/tone also moves to stdin under a small system directive.
	if len(prompt) > claudeCodeMaxPromptArgumentBytes {
		initialInstructions = domain
		prompt = preamble + "\n\n" + claudeCodeStdinInstructions
		if len(prompt) > claudeCodeMaxPromptArgumentBytes {
			initialInstructions = preamble + "\n\n" + domain
			prompt = claudeCodeStdinInstructions
		}
	}
	// SDK v1.1.0 ignores Options.SettingSources in its transport, so pass the
	// empty CLI flag explicitly and disable SDK skill discovery. Memory exclusion
	// is best-effort: managed instructions and explicit Read of CLAUDE.md/AGENTS.md
	// are not prevented. This is configuration isolation, not a filesystem sandbox.
	emptySources := ""
	return []claudeagent.Option{
		claudeagent.WithModel(cfg.Model),
		claudeagent.WithSystemPrompt(prompt),
		claudeagent.WithCwd(cfg.WorkingDirectory),
		claudeagent.WithMcpServer(claudeCodeMCPServerName, server),
		claudeagent.WithStrictMCPConfig(true),
		claudeagent.WithPermissionMode(claudeagent.PermissionModeBypassAll),
		claudeagent.WithAllowDangerouslySkipPermissions(true),
		claudeagent.WithSkillsDisabled(),
		claudeagent.WithSettings(claudeagent.Settings{ClaudeMDExcludes: []string{"**"}}),
		claudeagent.WithExtraArgs(map[string]*string{"setting-sources": &emptySources}),
		// Keep the transcript in hcpctl's conversation.json, not in CLI history.
		claudeagent.WithNoSessionPersistence(),
	}, initialInstructions, nil
}

func buildClaudeCodeMCPServer(tools []ToolDefinition) (*claudeagent.McpServer, error) {
	server := claudeagent.CreateMcpServer(claudeagent.McpServerOptions{Name: claudeCodeMCPServerName})
	seen := make(map[string]bool, len(tools))
	for _, tool := range tools {
		if tool.Name == "" || tool.Handler == nil {
			return nil, fmt.Errorf("claude-code tool requires a name and handler")
		}
		if seen[tool.Name] {
			return nil, fmt.Errorf("duplicate Claude Code tool %q", tool.Name)
		}
		seen[tool.Name] = true
		schema := map[string]any{"type": "object", "properties": map[string]any{}}
		if len(tool.ParamSchema) > 0 {
			if err := json.Unmarshal(tool.ParamSchema, &schema); err != nil {
				return nil, fmt.Errorf("parsing parameter schema for %q: %w", tool.Name, err)
			}
			if schema == nil {
				return nil, fmt.Errorf("parameter schema for %q must be an object", tool.Name)
			}
		}
		claudeagent.AddToolUntyped(server, claudeagent.ToolDef{
			Name: tool.Name, Description: tool.Description, InputSchema: schema,
		}, func(ctx context.Context, params json.RawMessage) (claudeagent.ToolResult, error) {
			text, err := tool.Handler(ctx, params)
			if err != nil {
				return claudeagent.ErrorResult(err.Error()), nil
			}
			return claudeagent.TextResult(text), nil
		})
	}
	return server, nil
}

func (p *ClaudeCodeProvider) Stop() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.stopped = true
	var errs []error
	for _, session := range p.sessions {
		if err := session.Disconnect(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

type ClaudeCodeSession struct {
	client              claudeCodeClient
	logger              logr.Logger
	model               string
	sendMu              sync.Mutex
	mu                  sync.Mutex
	id                  string
	messages            []any
	usage               UsageReport
	closed              bool
	closeErr            error
	initialInstructions string
}

func (s *ClaudeCodeSession) SendAndWait(ctx context.Context, prompt string) (string, error) {
	s.sendMu.Lock()
	defer s.sendMu.Unlock()
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return "", fmt.Errorf("claude-code session is closed")
	}
	if s.initialInstructions != "" {
		prompt = s.initialInstructions + "\n\n" + prompt
		s.initialInstructions = ""
	}
	s.messages = append(s.messages, map[string]string{"role": "user", "content": prompt})
	s.mu.Unlock()
	var lastAssistantText string
	for message := range s.client.Query(ctx, prompt) {
		s.mu.Lock()
		s.messages = append(s.messages, message)
		s.mu.Unlock()
		switch message := message.(type) {
		case claudeagent.AssistantMessage:
			lastAssistantText = message.ContentText()
		case claudeagent.ResultMessage:
			s.mu.Lock()
			s.usage = usageReportFromClaudeCodeResult(message, s.model)
			if message.SessionID != "" {
				s.id = message.SessionID
			}
			s.mu.Unlock()
			status := message.Subtype
			if status == "" {
				status = message.Status
			}
			if message.IsError || status != "success" {
				detail := strings.Join(message.Errors, "; ")
				if detail == "" {
					detail = message.Result
				}
				if detail == "" {
					detail = lastAssistantText
				}
				return "", fmt.Errorf("claude-code query failed (%s): %s", status, detail)
			}
			if message.Result != "" {
				return message.Result, nil
			}
			return lastAssistantText, nil
		}
	}
	// Query has no error channel and silently ends on transport/send failures.
	// Close an interrupted conversation so a later turn cannot consume stale events.
	if err := s.Disconnect(); err != nil {
		s.logger.Error(err, "Failed to close interrupted Claude Code session.")
	}
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("claude-code query interrupted: %w", err)
	}
	return "", fmt.Errorf("claude-code query ended without a terminal result")
}

// Result snapshots are cumulative and coarser than the in-process provider's
// per-request accounting. Replace rather than sum snapshots; API request counts,
// cache TTLs and service tiers are unavailable. Prefer per-model usage when present.
func usageReportFromClaudeCodeResult(result claudeagent.ResultMessage, fallbackModel string) UsageReport {
	var report UsageReport
	models := make([]string, 0, len(result.ModelUsage))
	for model := range result.ModelUsage {
		models = append(models, model)
	}
	sort.Strings(models)
	for _, model := range models {
		usage := result.ModelUsage[model]
		report.Add(UsageBreakdown{
			Provider: "claude-code", Model: model,
			Dimensions: map[string]string{"backend": "cli"},
			Tokens: TokenUsage{
				UncachedInputTokens: int64(usage.InputTokens), OutputTokens: int64(usage.OutputTokens),
				CacheReadInputTokens: int64(usage.CacheReadInputTokens), CacheWriteInputTokens: int64(usage.CacheCreationInputTokens),
			},
			AdditionalUnits:       map[string]float64{"webSearchRequests": float64(usage.WebSearchRequests)},
			ProviderReportedCosts: map[string]float64{"usd": usage.CostUSD},
		})
	}
	if len(models) == 0 {
		entry := UsageBreakdown{
			Provider: "claude-code", Model: fallbackModel,
			Dimensions:            map[string]string{"backend": "cli"},
			ProviderReportedCosts: map[string]float64{"usd": result.TotalCostUSD},
		}
		if usage := result.Usage; usage != nil {
			entry.Tokens = TokenUsage{
				UncachedInputTokens: int64(usage.InputTokens), OutputTokens: int64(usage.OutputTokens),
				CacheReadInputTokens: int64(usage.CacheReadInputTokens), CacheWriteInputTokens: int64(usage.CacheCreationInputTokens),
			}
		}
		report.Add(entry)
	}
	report.ProviderReportedCosts["usd"] = result.TotalCostUSD
	return report
}

// ResetHistory is a no-op because conversation state belongs to the CLI.
func (s *ClaudeCodeSession) ResetHistory() {}

func (s *ClaudeCodeSession) Usage() UsageReport {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.usage.Clone()
}

func (s *ClaudeCodeSession) SessionID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.id
}

func (s *ClaudeCodeSession) SaveConversation(path string) {
	s.mu.Lock()
	data, err := json.MarshalIndent(s.messages, "", "  ")
	s.mu.Unlock()
	if err == nil {
		err = os.MkdirAll(filepath.Dir(path), 0o755)
	}
	if err == nil {
		err = os.WriteFile(path, data, 0o600)
	}
	if err != nil {
		s.logger.Error(err, "Failed to save Claude Code conversation.", "path", path)
	}
}

func (s *ClaudeCodeSession) Disconnect() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		s.closed = true
		s.closeErr = s.client.Close()
	}
	return s.closeErr
}

func (s *ClaudeCodeSession) Delete(_ context.Context) error {
	err := s.Disconnect()
	s.mu.Lock()
	s.messages = nil
	s.initialInstructions = ""
	s.mu.Unlock()
	return err
}
