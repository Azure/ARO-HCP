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
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	copilot "github.com/github/copilot-sdk/go"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

// Exercise runtime startup: a client can construct successfully even when the
// SDK defaults to an embedded runtime that hcpctl does not bundle.
func TestCopilotStartsInstalledCLI(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake runtime uses a POSIX shell")
	}
	for _, useOverride := range []bool{false, true} {
		name := "PATH"
		if useOverride {
			name = "COPILOT_CLI_PATH"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			marker := filepath.Join(dir, "started")
			cliName := "copilot"
			if useOverride {
				cliName = "custom-copilot"
			}
			cliPath := filepath.Join(dir, cliName)
			if err := os.WriteFile(cliPath, []byte("#!/bin/sh\nprintf started > \"$COPILOT_RUNTIME_TEST_MARKER\"\nexit 1\n"), 0o700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", dir)
			t.Setenv("COPILOT_RUNTIME_TEST_MARKER", marker)
			t.Setenv("COPILOT_CLI_PATH", "")
			t.Setenv("COPILOT_SDK_DEFAULT_CONNECTION", "")
			if useOverride {
				t.Setenv("COPILOT_CLI_PATH", cliPath)
			}
			client, err := NewCopilotClient(&AgentConfig{})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = client.Stop() })
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			if err := client.inner.Start(ctx); err == nil {
				t.Fatal("fake CLI should exit before completing the handshake")
			}
			if _, err := os.Stat(marker); err != nil {
				t.Fatalf("installed CLI was not started: %v", err)
			}
		})
	}
}

type tokenCredentialFunc func(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error)

func (f tokenCredentialFunc) GetToken(ctx context.Context, opts policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return f(ctx, opts)
}

func TestBuildSessionConfigBYOK(t *testing.T) {
	for _, tc := range []struct {
		name         string
		clientModel  string
		sessionModel string
		promptTokens int
		wantModel    string
	}{
		{name: "deployment default", wantModel: "analysis"},
		{name: "client override with budget", clientModel: "client-model", promptTokens: 128000, wantModel: "client-model"},
		{name: "session override with budget", clientModel: "client-model", sessionModel: "session-model", promptTokens: 200000, wantModel: "session-model"},
		{name: "deployment with budget", promptTokens: 900000, wantModel: "analysis"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tokenRequested := false
			client, err := NewCopilotClient(&AgentConfig{
				AuthMode:        CopilotAuthModeBYOK,
				ModelEndpoint:   "https://example.invalid",
				ModelDeployment: "analysis",
				Model:           tc.clientModel,
				MaxPromptTokens: tc.promptTokens,
				AzureCredential: tokenCredentialFunc(func(_ context.Context, opts policy.TokenRequestOptions) (azcore.AccessToken, error) {
					tokenRequested = true
					if want := []string{"https://cognitiveservices.azure.com/.default"}; !reflect.DeepEqual(opts.Scopes, want) {
						t.Errorf("Scopes = %v, want %v", opts.Scopes, want)
					}
					return azcore.AccessToken{Token: "test-token"}, nil
				}),
			})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := client.Stop(); err != nil {
					t.Error(err)
				}
			})

			cfg, err := client.buildSessionConfig(t.Context(), SessionConfig{Model: tc.sessionModel})
			if err != nil {
				t.Fatal(err)
			}
			if !tokenRequested {
				t.Fatal("Azure token was not requested")
			}
			if cfg.Provider == nil {
				t.Fatal("Provider = nil, want Azure BYOK provider")
			}
			if cfg.Model != tc.wantModel {
				t.Errorf("Model = %q, want %q", cfg.Model, tc.wantModel)
			}
			// Check the complete SDK provider contract: setting a budget must
			// not introduce model-identity, routing, or authentication overrides.
			wantProvider := &copilot.ProviderConfig{
				Type: "azure", WireAPI: "responses", BaseURL: client.cfg.ModelEndpoint,
				BearerToken: "test-token", MaxPromptTokens: tc.promptTokens,
			}
			if !reflect.DeepEqual(cfg.Provider, wantProvider) {
				t.Errorf("Provider = %+v, want %+v", cfg.Provider, wantProvider)
			}
		})
	}
}

func TestCopilotRejectsInvalidPromptBudget(t *testing.T) {
	for _, tc := range []struct {
		name    string
		config  AgentConfig
		wantErr string
	}{
		{name: "negative budget", config: AgentConfig{AuthMode: CopilotAuthModeBYOK, MaxPromptTokens: -1}, wantErr: "must not be negative"},
		{name: "implicit GitHub auth", config: AgentConfig{MaxPromptTokens: 128000}, wantErr: "requires copilot auth mode"},
		{name: "logged-in auth", config: AgentConfig{AuthMode: CopilotAuthModeLoggedIn, MaxPromptTokens: 128000}, wantErr: "requires copilot auth mode"},
		{name: "token auth", config: AgentConfig{AuthMode: CopilotAuthModeToken, MaxPromptTokens: 128000}, wantErr: "requires copilot auth mode"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, err := NewCopilotClient(&tc.config)
			if client != nil {
				t.Cleanup(func() {
					if err := client.Stop(); err != nil {
						t.Error(err)
					}
				})
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("NewCopilotClient() error = %v, want %q", err, tc.wantErr)
			}
		})
	}
}

func TestBuildSessionConfigGitHubAuth(t *testing.T) {
	for _, authMode := range []string{"", CopilotAuthModeLoggedIn, CopilotAuthModeToken} {
		t.Run(authMode, func(t *testing.T) {
			client := &CopilotClient{cfg: &AgentConfig{AuthMode: authMode, Model: "client-model"}}
			cfg, err := client.buildSessionConfig(t.Context(), SessionConfig{})
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Provider != nil {
				t.Fatal("Provider must remain unset for GitHub-authenticated sessions")
			}
			if cfg.Model != client.cfg.Model {
				t.Errorf("Model = %q, want %q", cfg.Model, client.cfg.Model)
			}
		})
	}
}

func TestBuildSessionConfigBYOKTokenError(t *testing.T) {
	tokenErr := errors.New("token unavailable")
	client := &CopilotClient{cfg: &AgentConfig{
		AuthMode: CopilotAuthModeBYOK,
		AzureCredential: tokenCredentialFunc(func(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
			return azcore.AccessToken{}, tokenErr
		}),
	}}
	cfg, err := client.buildSessionConfig(t.Context(), SessionConfig{})
	if !errors.Is(err, tokenErr) {
		t.Fatalf("error = %v, want wrapped token error", err)
	}
	if cfg != nil {
		t.Fatal("configuration must not be returned after token acquisition fails")
	}
}
