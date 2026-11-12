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
	"iter"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr"
	claudeagent "github.com/roasbeef/claude-agent-sdk-go"
)

type fakeClaudeCodeClient struct {
	connectErr error
	closeErr   error
	connected  bool
	closes     int
	queries    [][]claudeagent.Message
	prompts    []string
}

func (client *fakeClaudeCodeClient) Connect(context.Context) error {
	client.connected = true
	return client.connectErr
}

func (client *fakeClaudeCodeClient) Close() error {
	client.closes++
	return client.closeErr
}

func (client *fakeClaudeCodeClient) Query(ctx context.Context, prompt string) iter.Seq[claudeagent.Message] {
	return func(yield func(claudeagent.Message) bool) {
		client.prompts = append(client.prompts, prompt)
		if ctx.Err() != nil || len(client.queries) == 0 {
			return
		}
		messages := client.queries[0]
		client.queries = client.queries[1:]
		for _, message := range messages {
			if !yield(message) {
				return
			}
		}
	}
}

func newTestClaudeCodeSession(t *testing.T, client *fakeClaudeCodeClient) (*ClaudeCodeProvider, LLMSession) {
	t.Helper()
	provider, err := NewClaudeCodeProvider(nil)
	if err != nil {
		t.Fatal(err)
	}
	provider.newClient = func(...claudeagent.Option) (claudeCodeClient, error) { return client, nil }
	session, err := provider.CreateProviderSession(context.Background(), logr.Discard(), ProviderSessionConfig{WorkingDirectory: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := provider.Stop(); err != nil {
			t.Error(err)
		}
	})
	if !client.connected {
		t.Fatal("session must explicitly connect before returning")
	}
	return provider, session
}

func TestClaudeCodeOptions(t *testing.T) {
	for _, test := range []struct {
		name, configured, override, want string
	}{
		{name: "default", want: DefaultClaudeCodeModel},
		{name: "configured", configured: "configured-model", want: "configured-model"},
		{name: "override", configured: "configured-model", override: "session-model", want: "session-model"},
	} {
		t.Run(test.name, func(t *testing.T) {
			provider, err := NewClaudeCodeProvider(&ClaudeCodeConfig{Model: test.configured})
			if err != nil {
				t.Fatal(err)
			}
			var options claudeagent.Options
			provider.newClient = func(opts ...claudeagent.Option) (claudeCodeClient, error) {
				options = claudeagent.DefaultOptions()
				for _, opt := range opts {
					opt(&options)
				}
				sdkClient, err := claudeagent.NewClient(opts...)
				if err != nil {
					return nil, err
				}
				if err := sdkClient.Close(); err != nil {
					return nil, err
				}
				return &fakeClaudeCodeClient{}, nil
			}
			workspace := t.TempDir()
			_, err = provider.CreateProviderSession(context.Background(), logr.Discard(), ProviderSessionConfig{
				Model: test.override, WorkingDirectory: workspace,
				IdentityPrompt: "identity", TonePrompt: "tone", SystemPrompt: "domain",
				Tools: []ToolDefinition{NewKustoToolDefinition(&noopKustoClient{})},
			})
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := provider.Stop(); err != nil {
					t.Error(err)
				}
			}()
			if options.Model != test.want || options.Cwd != workspace {
				t.Fatalf("model/cwd = %q/%q", options.Model, options.Cwd)
			}
			if !strings.HasPrefix(options.SystemPrompt, "identity\n\ntone\n\ndomain") || !strings.Contains(options.SystemPrompt, "mcp__hcpctl__kusto_query") {
				t.Fatalf("missing prompt composition or MCP name mapping: %s", options.SystemPrompt)
			}
			if !options.StrictMCPConfig || !options.AllowDangerouslySkipPermissions || options.PermissionMode != claudeagent.PermissionModeBypassAll {
				t.Fatal("missing MCP isolation or unattended permissions")
			}
			if options.SkillsConfig.EnableSkills || options.Settings == nil || !reflect.DeepEqual(options.Settings.ClaudeMDExcludes, []string{"**"}) {
				t.Fatal("missing memory/skills isolation")
			}
			if sources := options.ExtraArgs["setting-sources"]; sources == nil || *sources != "" {
				t.Fatal("must explicitly pass an empty setting-sources argument")
			}
			if !options.NoSessionPersistence {
				t.Fatal("CLI history should not persist analysis data")
			}
			server := options.SDKMcpServers[claudeCodeMCPServerName]
			if server == nil || !reflect.DeepEqual(server.ToolNames(), []string{"kusto_query"}) {
				t.Fatal("kusto_query not registered in-process")
			}
			result, err := server.CallTool(context.Background(), "kusto_query", json.RawMessage(`{"kql":""}`))
			if err != nil || !result.IsError || result.Content[0].Text != "kql must not be empty" {
				t.Fatalf("existing Kusto handler validation not invoked: %+v, %v", result, err)
			}
			runner := claudeagent.NewMockSubprocessRunner()
			transport := claudeagent.NewSubprocessTransportWithRunner(runner, &options)
			if err := transport.Connect(context.Background()); err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := transport.Close(); err != nil {
					t.Error(err)
				}
			}()
			for _, flag := range []string{"--strict-mcp-config", "--dangerously-skip-permissions", "--no-session-persistence"} {
				if !slices.Contains(runner.StartArgs, flag) {
					t.Errorf("SDK transport did not emit %s", flag)
				}
			}
			index := slices.Index(runner.StartArgs, "--setting-sources")
			if index < 0 || index+1 >= len(runner.StartArgs) || runner.StartArgs[index+1] != "" || runner.StartCwd != workspace {
				t.Fatal("SDK transport lost empty settings sources or working directory")
			}
		})
	}
}

func TestClaudeCodeToolConversion(t *testing.T) {
	ctx := context.Background()
	var received json.RawMessage
	tools := []ToolDefinition{
		{Name: "query", Description: "Execute query", ParamSchema: json.RawMessage(`{"type":"object","properties":{"kql":{"type":"string"}},"required":["kql"]}`),
			Handler: func(actual context.Context, params json.RawMessage) (string, error) {
				if actual != ctx {
					t.Error("handler context not preserved")
				}
				received = params
				return "query result", nil
			}},
		{Name: "failure", Handler: func(context.Context, json.RawMessage) (string, error) { return "", errors.New("tool failed") }},
	}
	server, err := buildClaudeCodeMCPServer(tools)
	if err != nil {
		t.Fatal(err)
	}
	for _, def := range server.ToolDefs() {
		if def.Name == "query" {
			var want map[string]any
			if err := json.Unmarshal(tools[0].ParamSchema, &want); err != nil {
				t.Fatal(err)
			}
			if def.Description != tools[0].Description || !reflect.DeepEqual(def.InputSchema, want) {
				t.Fatalf("tool metadata changed: %+v", def)
			}
		}
	}
	params := json.RawMessage(`{"kql":"traces | take 1"}`)
	result, err := server.CallTool(ctx, "query", params)
	if err != nil || result.IsError || result.Content[0].Text != "query result" || string(received) != string(params) {
		t.Fatalf("handler dispatch failed: %+v, %v", result, err)
	}
	result, err = server.CallTool(ctx, "failure", nil)
	if err != nil || !result.IsError || result.Content[0].Text != "tool failed" {
		t.Fatalf("handler error not returned as MCP error: %+v, %v", result, err)
	}
	for _, schema := range []string{"{invalid", "null", "[]"} {
		invalid := tools[0]
		invalid.ParamSchema = json.RawMessage(schema)
		if _, err := buildClaudeCodeMCPServer([]ToolDefinition{invalid}); err == nil {
			t.Errorf("accepted invalid schema %s", schema)
		}
	}
	for _, invalid := range [][]ToolDefinition{{{}}, {{Name: "no-handler"}}, {tools[0], tools[0]}} {
		if _, err := buildClaudeCodeMCPServer(invalid); err == nil {
			t.Error("accepted missing name/handler or duplicate tool")
		}
	}
}

func TestClaudeCodeLargePromptUsesStdin(t *testing.T) {
	const maxArgumentBytes = 100 * 1024
	const toolMapping = "\nUse mcp__hcpctl__kusto_query whenever these instructions refer to the kusto_query tool."
	for _, test := range []struct {
		name          string
		mode          Mode
		largePreamble bool
		unicodeDomain bool
	}{
		{name: "test analysis", mode: ModeTest},
		{name: "intent analysis", mode: ModeIntent},
		{name: "oversized identity and tone", largePreamble: true},
		{name: "UTF-8 byte limit", unicodeDomain: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			domain, err := BuildDomainPromptFor(test.mode)
			if err != nil {
				t.Fatal(err)
			}
			cfg := ProviderSessionConfig{
				WorkingDirectory: t.TempDir(), IdentityPrompt: IdentityPromptFor(test.mode),
				TonePrompt: TonePrompt, SystemPrompt: domain,
				Tools: []ToolDefinition{NewKustoToolDefinition(&noopKustoClient{})},
			}
			if test.largePreamble {
				cfg.IdentityPrompt = strings.Repeat("identity instruction\n", 6000)
				cfg.TonePrompt = strings.Repeat("tone instruction\n", 6000)
			}
			if test.unicodeDomain {
				cfg.SystemPrompt = strings.Repeat("界", 40000)
			}
			provider, err := NewClaudeCodeProvider(nil)
			if err != nil {
				t.Fatal(err)
			}
			client := &fakeClaudeCodeClient{queries: [][]claudeagent.Message{
				{claudeagent.ResultMessage{Subtype: "success"}},
				{claudeagent.ResultMessage{Subtype: "success"}},
			}}
			var options claudeagent.Options
			provider.newClient = func(opts ...claudeagent.Option) (claudeCodeClient, error) {
				options = claudeagent.DefaultOptions()
				for _, opt := range opts {
					opt(&options)
				}
				return client, nil
			}
			session, err := provider.CreateProviderSession(context.Background(), logr.Discard(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := provider.Stop(); err != nil {
					t.Error(err)
				}
			})
			runner := claudeagent.NewMockSubprocessRunner()
			transport := claudeagent.NewSubprocessTransportWithRunner(runner, &options)
			if err := transport.Connect(context.Background()); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := transport.Close(); err != nil {
					t.Error(err)
				}
			})
			maxActual := 0
			for _, argument := range runner.StartArgs {
				maxActual = max(maxActual, len(argument))
				if len(argument) > maxArgumentBytes {
					t.Fatalf("launch argument is %d bytes, exceeding %d-byte safe bound", len(argument), maxArgumentBytes)
				}
				if strings.Contains(argument, cfg.SystemPrompt) {
					t.Fatal("bulk domain instructions still present in argv")
				}
			}
			if _, err := session.SendAndWait(context.Background(), "first analysis request"); err != nil {
				t.Fatal(err)
			}
			wantBulk := cfg.SystemPrompt + toolMapping
			if test.largePreamble {
				wantBulk = cfg.IdentityPrompt + "\n\n" + cfg.TonePrompt + "\n\n" + wantBulk
			} else if !strings.HasPrefix(options.SystemPrompt, cfg.IdentityPrompt+"\n\n"+cfg.TonePrompt) {
				t.Fatal("small identity/tone must remain system instructions")
			}
			wantFirst := wantBulk + "\n\n" + "first analysis request"
			if len(client.prompts) != 1 || client.prompts[0] != wantFirst {
				t.Fatal("first stdin query did not preserve all bulk instructions verbatim")
			}
			message := claudeagent.UserMessage{Type: "user", Message: claudeagent.APIUserMessage{
				Role: "user", Content: []claudeagent.UserContentBlock{{Type: "text", Text: client.prompts[0]}},
			}}
			if err := transport.Write(context.Background(), message); err != nil {
				t.Fatal(err)
			}
			var received claudeagent.UserMessage
			if err := json.NewDecoder(runner.StdinPipe).Decode(&received); err != nil {
				t.Fatal(err)
			}
			if len(received.Message.Content) != 1 || received.Message.Content[0].Text != wantFirst {
				t.Fatal("SDK stream-json transport changed or truncated the instructions")
			}
			session.ResetHistory()
			if _, err := session.SendAndWait(context.Background(), "second analysis request"); err != nil {
				t.Fatal(err)
			}
			if len(client.prompts) != 2 || client.prompts[1] != "second analysis request" {
				t.Fatal("bulk instructions should be sent only once per subprocess session")
			}
			transcriptPath := filepath.Join(t.TempDir(), "conversation.json")
			session.SaveConversation(transcriptPath)
			data, err := os.ReadFile(transcriptPath)
			if err != nil {
				t.Fatal(err)
			}
			var transcript []map[string]any
			if err := json.Unmarshal(data, &transcript); err != nil {
				t.Fatal(err)
			}
			if len(transcript) == 0 || transcript[0]["content"] != wantFirst {
				t.Fatal("transcript must preserve the actual first query with its instructions")
			}
			t.Logf("domain=%d bytes; maximum argv=%d bytes; first stdin query=%d bytes", len(cfg.SystemPrompt), maxActual, len(wantFirst))
		})
	}
}

func TestClaudeCodeErrorDiagnostics(t *testing.T) {
	for _, test := range []struct {
		name   string
		errors []string
		result string
		want   string
	}{
		{name: "errors take precedence", errors: []string{"first error", "second error"}, result: "result diagnostic", want: "first error; second error"},
		{name: "result fallback", result: "authentication failed", want: "authentication failed"},
		{name: "empty error detail", errors: []string{""}, result: "authentication failed", want: "authentication failed"},
		{name: "last assistant fallback", want: "last assistant diagnostic"},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			runner := claudeagent.NewMockSubprocessRunner()
			options := claudeagent.DefaultOptions()
			transport := claudeagent.NewSubprocessTransportWithRunner(runner, &options)
			if err := transport.Connect(ctx); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := transport.Close(); err != nil {
					t.Error(err)
				}
			})
			encoder := json.NewEncoder(runner.StdoutPipe)
			for _, text := range []string{"earlier assistant text", "last assistant diagnostic"} {
				assistant := claudeagent.AssistantMessage{Type: "assistant"}
				assistant.Message.Content = []claudeagent.ContentBlock{{Type: "text", Text: text}}
				if err := encoder.Encode(assistant); err != nil {
					t.Fatal(err)
				}
			}
			if err := encoder.Encode(claudeagent.ResultMessage{
				Type: "result", Subtype: "success", IsError: true,
				Errors: test.errors, Result: test.result,
			}); err != nil {
				t.Fatal(err)
			}
			var messages []claudeagent.Message
			for message, err := range transport.ReadMessages(ctx) {
				if err != nil {
					t.Fatal(err)
				}
				messages = append(messages, message)
				if _, ok := message.(claudeagent.ResultMessage); ok {
					break
				}
			}
			client := &fakeClaudeCodeClient{queries: [][]claudeagent.Message{messages}}
			_, session := newTestClaudeCodeSession(t, client)
			got, err := session.SendAndWait(ctx, "investigate")
			wantErr := "claude-code query failed (success): " + test.want
			if err == nil || err.Error() != wantErr || got != "" {
				t.Fatalf("got %q, %v; want empty response and error %q", got, err, wantErr)
			}
		})
	}
}

func TestClaudeCodeSendAndWait(t *testing.T) {
	assistant := claudeagent.AssistantMessage{}
	assistant.Message.Content = []claudeagent.ContentBlock{{Type: "text", Text: "assistant text"}}
	for _, test := range []struct {
		name     string
		messages []claudeagent.Message
		want     string
		wantErr  string
	}{
		{name: "result", messages: []claudeagent.Message{assistant, claudeagent.ResultMessage{Subtype: "success", Result: "final text"}}, want: "final text"},
		{name: "fallback", messages: []claudeagent.Message{assistant, claudeagent.ResultMessage{Subtype: "success"}}, want: "assistant text"},
		{name: "legacy status", messages: []claudeagent.Message{claudeagent.ResultMessage{Status: "success", Result: "legacy"}}, want: "legacy"},
		{name: "silent end", wantErr: "without a terminal result"},
		{name: "partial end", messages: []claudeagent.Message{assistant}, wantErr: "without a terminal result"},
		{name: "error result", messages: []claudeagent.Message{claudeagent.ResultMessage{Subtype: "error_max_turns", Status: "success", Errors: []string{"budget exceeded"}}}, wantErr: "budget exceeded"},
		{name: "error flag", messages: []claudeagent.Message{claudeagent.ResultMessage{Subtype: "success", IsError: true}}, wantErr: "query failed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := &fakeClaudeCodeClient{queries: [][]claudeagent.Message{test.messages}}
			_, session := newTestClaudeCodeSession(t, client)
			got, err := session.SendAndWait(context.Background(), "investigate")
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("got error %v, want %q", err, test.wantErr)
				}
			} else if err != nil || got != test.want {
				t.Fatalf("got %q, %v; want %q", got, err, test.want)
			}
			if !reflect.DeepEqual(client.prompts, []string{"investigate"}) {
				t.Fatal("prompt not sent")
			}
		})
	}
}

func TestClaudeCodeLifecycleAndUsage(t *testing.T) {
	first := claudeagent.ResultMessage{Subtype: "success", SessionID: "session-123", TotalCostUSD: 0.25,
		Usage: &claudeagent.NonNullableUsage{InputTokens: 10, OutputTokens: 20, CacheReadInputTokens: 30, CacheCreationInputTokens: 40}}
	second := first
	second.TotalCostUSD = 0.5
	second.ModelUsage = map[string]claudeagent.ModelUsage{
		"model-one": {InputTokens: 20, OutputTokens: 40, CacheReadInputTokens: 60, CacheCreationInputTokens: 80, CostUSD: 0.3},
		"model-two": {InputTokens: 1, OutputTokens: 2, CostUSD: 0.2, WebSearchRequests: 1},
	}
	client := &fakeClaudeCodeClient{queries: [][]claudeagent.Message{{first}, {second}}}
	provider, session := newTestClaudeCodeSession(t, client)
	if _, err := session.SendAndWait(context.Background(), "first"); err != nil {
		t.Fatal(err)
	}
	if usage := session.Usage(); usage.Tokens.TotalTokens != 100 || usage.ProviderReportedCosts["usd"] != 0.25 || session.SessionID() != "session-123" {
		t.Fatalf("aggregate usage or session ID lost: %+v", usage)
	}
	session.ResetHistory()
	if _, err := session.SendAndWait(context.Background(), "second"); err != nil {
		t.Fatal(err)
	}
	usage := session.Usage()
	if usage.Tokens.TotalTokens != 203 || usage.ProviderReportedCosts["usd"] != 0.5 || len(usage.Breakdown) != 2 || usage.AdditionalUnits["webSearchRequests"] != 1 {
		t.Fatalf("cumulative/per-model accounting double-counted or lost data: %+v", usage)
	}
	usage.ProviderReportedCosts["usd"] = 999
	if session.Usage().ProviderReportedCosts["usd"] != 0.5 {
		t.Fatal("Usage did not return a deep copy")
	}
	path := filepath.Join(t.TempDir(), "output", "conversation.json")
	session.SaveConversation(path)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var transcript []json.RawMessage
	if err := json.Unmarshal(data, &transcript); err != nil || len(transcript) != 4 {
		t.Fatalf("archived transcript missing turns: %s, %v", data, err)
	}
	if err := session.Delete(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := provider.Stop(); err != nil || client.closes != 1 {
		t.Fatalf("cleanup not idempotent: %v, closes=%d", err, client.closes)
	}
	if _, err := session.SendAndWait(context.Background(), "after close"); err == nil {
		t.Fatal("closed session accepted another query")
	}
	if _, err := provider.CreateProviderSession(context.Background(), logr.Discard(), ProviderSessionConfig{}); err == nil {
		t.Fatal("stopped provider accepted a session")
	}
}

func TestClaudeCodeFailures(t *testing.T) {
	provider, err := NewClaudeCodeProvider(nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, directory := range []string{"", filepath.Join(t.TempDir(), "missing")} {
		if _, err := provider.CreateProviderSession(context.Background(), logr.Discard(), ProviderSessionConfig{WorkingDirectory: directory}); err == nil {
			t.Error("accepted invalid working directory")
		}
	}
	file := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.CreateProviderSession(context.Background(), logr.Discard(), ProviderSessionConfig{WorkingDirectory: file}); err == nil {
		t.Fatal("accepted a file as working directory")
	}
	provider.newClient = func(...claudeagent.Option) (claudeCodeClient, error) { return nil, errors.New("invalid options") }
	if _, err := provider.CreateProviderSession(context.Background(), logr.Discard(), ProviderSessionConfig{WorkingDirectory: t.TempDir()}); err == nil || !strings.Contains(err.Error(), "invalid options") {
		t.Fatalf("client construction failure not reported: %v", err)
	}
	client := &fakeClaudeCodeClient{connectErr: errors.New("connect failed")}
	provider.newClient = func(...claudeagent.Option) (claudeCodeClient, error) { return client, nil }
	if _, err := provider.CreateProviderSession(context.Background(), logr.Discard(), ProviderSessionConfig{WorkingDirectory: t.TempDir()}); err == nil || !strings.Contains(err.Error(), "connect failed") || client.closes != 1 {
		t.Fatalf("connection error/cleanup lost: %v", err)
	}
	_, session := newTestClaudeCodeSession(t, &fakeClaudeCodeClient{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := session.SendAndWait(ctx, "cancelled"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
}

func TestClaudeCodeAPIKeyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "key")
	if _, err := NewClaudeCodeProvider(&ClaudeCodeConfig{APIKeyFile: path}); err == nil {
		t.Fatal("accepted missing key file")
	}
	if err := os.WriteFile(path, []byte(" \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewClaudeCodeProvider(&ClaudeCodeConfig{APIKeyFile: path}); err == nil {
		t.Fatal("accepted empty key file")
	}
	if err := os.WriteFile(path, []byte(" test-key \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	provider, err := NewClaudeCodeProvider(&ClaudeCodeConfig{Backend: ClaudeBackendAPI, APIKeyFile: path})
	if err != nil {
		t.Fatal(err)
	}
	if provider.env["ANTHROPIC_API_KEY"] != "test-key" {
		t.Fatal("API key was not trimmed and configured")
	}
	provider.newClient = func(opts ...claudeagent.Option) (claudeCodeClient, error) {
		options := claudeagent.DefaultOptions()
		for _, opt := range opts {
			opt(&options)
		}
		if options.Env["ANTHROPIC_API_KEY"] != "test-key" {
			t.Error("API key not passed to SDK environment")
		}
		return &fakeClaudeCodeClient{}, nil
	}
	if _, err := provider.CreateProviderSession(context.Background(), logr.Discard(), ProviderSessionConfig{WorkingDirectory: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	if err := provider.Stop(); err != nil {
		t.Fatal(err)
	}
}

func TestClaudeCodeBackendEnvironment(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "ambient-test-key")
	t.Setenv("CLAUDE_CODE_USE_VERTEX", "1")
	t.Setenv("CLAUDE_CODE_USE_BEDROCK", "1")
	keyFile := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(keyFile, []byte(" file-test-key \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name    string
		config  ClaudeCodeConfig
		wantEnv map[string]string
		wantErr string
	}{
		{
			name:    "default API without key file",
			wantEnv: map[string]string{"CLAUDE_CODE_USE_VERTEX": "0", "CLAUDE_CODE_USE_BEDROCK": "0"},
		},
		{
			name: "explicit API without key file", config: ClaudeCodeConfig{Backend: ClaudeBackendAPI},
			wantEnv: map[string]string{"CLAUDE_CODE_USE_VERTEX": "0", "CLAUDE_CODE_USE_BEDROCK": "0"},
		},
		{
			name: "default API with key file", config: ClaudeCodeConfig{APIKeyFile: keyFile},
			wantEnv: map[string]string{"CLAUDE_CODE_USE_VERTEX": "0", "CLAUDE_CODE_USE_BEDROCK": "0", "ANTHROPIC_API_KEY": "file-test-key"},
		},
		{
			name: "explicit API with key file", config: ClaudeCodeConfig{Backend: ClaudeBackendAPI, APIKeyFile: keyFile},
			wantEnv: map[string]string{"CLAUDE_CODE_USE_VERTEX": "0", "CLAUDE_CODE_USE_BEDROCK": "0", "ANTHROPIC_API_KEY": "file-test-key"},
		},
		{
			name: "vertex",
			config: ClaudeCodeConfig{
				Backend: ClaudeBackendVertex, VertexProject: "test-project", VertexRegion: "us-east5",
				APIKeyFile: filepath.Join(t.TempDir(), "unused-key"),
			},
			wantEnv: map[string]string{
				"CLAUDE_CODE_USE_VERTEX": "1", "CLAUDE_CODE_USE_BEDROCK": "0", "ANTHROPIC_VERTEX_PROJECT_ID": "test-project", "CLOUD_ML_REGION": "us-east5",
			},
		},
		{
			name: "vertex missing project", config: ClaudeCodeConfig{Backend: ClaudeBackendVertex, VertexRegion: "us-east5"},
			wantErr: "vertex project is required",
		},
		{
			name: "vertex missing region", config: ClaudeCodeConfig{Backend: ClaudeBackendVertex, VertexProject: "test-project"},
			wantErr: "vertex region is required",
		},
		{
			name: "unknown backend", config: ClaudeCodeConfig{Backend: "unsupported"},
			wantErr: "unknown Claude backend",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			provider, err := NewClaudeCodeProvider(&test.config)
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("expected error containing %q, got %v", test.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(provider.env, test.wantEnv) {
				t.Fatalf("provider environment = %v, want %v", provider.env, test.wantEnv)
			}
			provider.newClient = func(opts ...claudeagent.Option) (claudeCodeClient, error) {
				options := claudeagent.DefaultOptions()
				for _, opt := range opts {
					opt(&options)
				}
				if len(options.Env) != len(test.wantEnv) {
					t.Errorf("SDK environment has %d entries, want %d", len(options.Env), len(test.wantEnv))
				}
				for key, want := range test.wantEnv {
					if options.Env[key] != want {
						t.Errorf("SDK environment %s = %q, want %q", key, options.Env[key], want)
					}
				}
				runner := claudeagent.NewMockSubprocessRunner()
				transport := claudeagent.NewSubprocessTransportWithRunner(runner, &options)
				if err := transport.Connect(context.Background()); err != nil {
					t.Fatal(err)
				}
				defer func() {
					if err := transport.Close(); err != nil {
						t.Error(err)
					}
				}()
				effectiveEnv := make(map[string]string)
				for _, entry := range runner.StartEnv {
					key, value, found := strings.Cut(entry, "=")
					if found {
						effectiveEnv[key] = value
					}
				}
				for key, want := range test.wantEnv {
					if effectiveEnv[key] != want {
						t.Errorf("subprocess environment %s = %q, want %q", key, effectiveEnv[key], want)
					}
				}
				wantKey := test.wantEnv["ANTHROPIC_API_KEY"]
				if wantKey == "" {
					wantKey = "ambient-test-key"
				}
				if effectiveEnv["ANTHROPIC_API_KEY"] != wantKey {
					t.Error("subprocess API key did not honor explicit-file or ambient fallback")
				}
				return &fakeClaudeCodeClient{}, nil
			}
			if _, err := provider.CreateProviderSession(context.Background(), logr.Discard(), ProviderSessionConfig{WorkingDirectory: t.TempDir()}); err != nil {
				t.Fatal(err)
			}
			if err := provider.Stop(); err != nil {
				t.Fatal(err)
			}
			if os.Getenv("ANTHROPIC_API_KEY") != "ambient-test-key" {
				t.Fatal("ambient API key changed")
			}
			if os.Getenv("CLAUDE_CODE_USE_VERTEX") != "1" || os.Getenv("CLAUDE_CODE_USE_BEDROCK") != "1" {
				t.Fatal("parent backend switches changed")
			}
		})
	}
}

func TestClaudeCodeErrorUsageAndCleanup(t *testing.T) {
	client := &fakeClaudeCodeClient{queries: [][]claudeagent.Message{{claudeagent.ResultMessage{
		Subtype: "error_max_turns", TotalCostUSD: 0.25,
	}}}}
	_, session := newTestClaudeCodeSession(t, client)
	if _, err := session.SendAndWait(context.Background(), "query"); err == nil {
		t.Fatal("expected terminal error")
	}
	if session.Usage().ProviderReportedCosts["usd"] != 0.25 {
		t.Fatal("failed query cost not recorded")
	}
	closeFailure := errors.New("close failed")
	failedSession := &ClaudeCodeSession{client: &fakeClaudeCodeClient{closeErr: closeFailure}}
	if err := failedSession.Disconnect(); !errors.Is(err, closeFailure) {
		t.Fatalf("cleanup failure lost: %v", err)
	}
}
