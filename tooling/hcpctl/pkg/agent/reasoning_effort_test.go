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
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/go-logr/logr"
	claudeagent "github.com/roasbeef/claude-agent-sdk-go"
)

func TestValidateReasoningEffort(t *testing.T) {
	for _, test := range []struct {
		effort              string
		copilotOK, claudeOK bool
	}{
		{effort: "", copilotOK: true, claudeOK: true},
		{effort: "none", copilotOK: true},
		{effort: "minimal", copilotOK: true},
		{effort: "low", copilotOK: true, claudeOK: true},
		{effort: "medium", copilotOK: true, claudeOK: true},
		{effort: "high", copilotOK: true, claudeOK: true},
		{effort: "xhigh", copilotOK: true, claudeOK: true},
		{effort: "max", copilotOK: true, claudeOK: true},
		{effort: "HIGH"},
		{effort: "bogus"},
	} {
		t.Run(test.effort, func(t *testing.T) {
			if err := ValidateCopilotReasoningEffort(test.effort); (err == nil) != test.copilotOK {
				t.Errorf("copilot: err = %v, want ok=%v", err, test.copilotOK)
			}
			if err := ValidateClaudeReasoningEffort(test.effort); (err == nil) != test.claudeOK {
				t.Errorf("claude: err = %v, want ok=%v", err, test.claudeOK)
			}
		})
	}
}

func TestCopilotReasoningEffort(t *testing.T) {
	if _, err := NewCopilotClient(&AgentConfig{ReasoningEffort: "bogus"}); err == nil || !strings.Contains(err.Error(), "bogus") {
		t.Fatalf("invalid effort not rejected: %v", err)
	}
	for _, effort := range []string{"", ReasoningEffortHigh} {
		t.Run(effort, func(t *testing.T) {
			client, err := NewCopilotClient(&AgentConfig{Model: "gpt-6-luna", ReasoningEffort: effort})
			if err != nil {
				t.Fatal(err)
			}
			cfg, err := client.buildSessionConfig(t.Context(), SessionConfig{Model: "session-model"})
			if err != nil {
				t.Fatal(err)
			}
			if cfg.ReasoningEffort != effort {
				t.Errorf("ReasoningEffort = %q, want %q", cfg.ReasoningEffort, effort)
			}
		})
	}
}

func TestClaudeCodeReasoningEffort(t *testing.T) {
	if _, err := NewClaudeCodeProvider(&ClaudeCodeConfig{ReasoningEffort: "minimal"}); err == nil || !strings.Contains(err.Error(), "minimal") {
		t.Fatalf("invalid effort not rejected: %v", err)
	}
	for _, test := range []struct {
		effort   string
		wantArgs []string
	}{
		{effort: ""},
		{effort: ReasoningEffortHigh, wantArgs: []string{"--effort", "high"}},
	} {
		t.Run(test.effort, func(t *testing.T) {
			// A fake CLI records the argv the SDK transport builds and exits.
			dir := t.TempDir()
			argsFile := filepath.Join(dir, "args")
			cli := filepath.Join(dir, "claude")
			script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > '" + argsFile + "'\nexit 1\n"
			if err := os.WriteFile(cli, []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
			provider, err := NewClaudeCodeProvider(&ClaudeCodeConfig{ReasoningEffort: test.effort})
			if err != nil {
				t.Fatal(err)
			}
			provider.newClient = func(opts ...claudeagent.Option) (claudeCodeClient, error) {
				return claudeagent.NewClient(append(opts, claudeagent.WithCLIPath(cli))...)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			// Connection fails because the fake CLI exits; only argv matters.
			_, _ = provider.CreateProviderSession(ctx, logr.Discard(), ProviderSessionConfig{WorkingDirectory: dir})
			data, err := os.ReadFile(argsFile)
			if err != nil {
				t.Fatalf("fake CLI was not invoked: %v", err)
			}
			args := strings.Split(strings.TrimSpace(string(data)), "\n")
			i := slices.Index(args, "--effort")
			if test.wantArgs == nil {
				if i >= 0 {
					t.Fatalf("unexpected --effort in argv: %v", args)
				}
				return
			}
			if i < 0 || i+1 >= len(args) || !slices.Equal(args[i:i+2], test.wantArgs) {
				t.Fatalf("argv %v does not contain %v", args, test.wantArgs)
			}
		})
	}
}

func TestClaudeReasoningEffort(t *testing.T) {
	if _, err := NewClaudeProvider(t.Context(), &ClaudeConfig{ReasoningEffort: "none"}); err == nil || !strings.Contains(err.Error(), "none") {
		t.Fatalf("invalid effort not rejected: %v", err)
	}
	for _, effort := range []string{"", ReasoningEffortHigh} {
		t.Run(effort, func(t *testing.T) {
			var body map[string]any
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				data, err := io.ReadAll(r.Body)
				if err == nil {
					err = json.Unmarshal(data, &body)
				}
				if err != nil {
					t.Error(err)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"id":"msg_1","type":"message","role":"assistant","model":"m","stop_reason":"end_turn",` +
					`"content":[{"type":"text","text":"done"}],"usage":{"input_tokens":1,"output_tokens":1}}`))
			}))
			defer server.Close()

			provider, err := NewClaudeProvider(t.Context(), &ClaudeConfig{Model: "m", ReasoningEffort: effort})
			if err != nil {
				t.Fatal(err)
			}
			provider.client = anthropic.NewClient(option.WithBaseURL(server.URL), option.WithAPIKey("test"), option.WithMaxRetries(0))
			session, err := provider.CreateProviderSession(t.Context(), logr.Discard(), ProviderSessionConfig{})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := session.SendAndWait(t.Context(), "hi"); err != nil {
				t.Fatal(err)
			}
			outputConfig, present := body["output_config"].(map[string]any)
			if effort == "" {
				if present {
					t.Fatalf("output_config sent without configured effort: %v", outputConfig)
				}
				return
			}
			if outputConfig["effort"] != effort {
				t.Fatalf("output_config = %v, want effort %q", body["output_config"], effort)
			}
		})
	}
}
