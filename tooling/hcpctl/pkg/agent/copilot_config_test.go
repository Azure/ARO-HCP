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
	"reflect"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

type tokenCredentialFunc func(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error)

func (f tokenCredentialFunc) GetToken(ctx context.Context, opts policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return f(ctx, opts)
}

func TestBuildSessionConfigBYOK(t *testing.T) {
	for _, tc := range []struct {
		name         string
		clientModel  string
		sessionModel string
		wantModel    string
	}{
		{name: "deployment default", wantModel: "analysis"},
		{name: "client override", clientModel: "client-model", wantModel: "client-model"},
		{name: "session override", clientModel: "client-model", sessionModel: "session-model", wantModel: "session-model"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tokenRequested := false
			client := &CopilotClient{cfg: &AgentConfig{
				AuthMode:        CopilotAuthModeBYOK,
				ModelEndpoint:   "https://example.invalid",
				ModelDeployment: "analysis",
				Model:           tc.clientModel,
				AzureCredential: tokenCredentialFunc(func(_ context.Context, opts policy.TokenRequestOptions) (azcore.AccessToken, error) {
					tokenRequested = true
					if want := []string{"https://cognitiveservices.azure.com/.default"}; !reflect.DeepEqual(opts.Scopes, want) {
						t.Errorf("Scopes = %v, want %v", opts.Scopes, want)
					}
					return azcore.AccessToken{Token: "test-token"}, nil
				}),
			}}

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
			if cfg.Provider.Type != "azure" || cfg.Provider.WireAPI != "responses" {
				t.Errorf("provider protocol = %q/%q, want azure/responses", cfg.Provider.Type, cfg.Provider.WireAPI)
			}
			if cfg.Provider.BaseURL != client.cfg.ModelEndpoint || cfg.Provider.BearerToken != "test-token" {
				t.Error("provider endpoint or bearer token was not preserved")
			}
			if cfg.Model != tc.wantModel {
				t.Errorf("Model = %q, want %q", cfg.Model, tc.wantModel)
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
