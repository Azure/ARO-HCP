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

package holmes

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockKVClient struct {
	secrets map[string]*string
	err     error
}

func (m *mockKVClient) GetSecret(_ context.Context, name string, _ string) (KeyVaultSecretResponse, error) {
	if m.err != nil {
		return KeyVaultSecretResponse{}, m.err
	}
	v, ok := m.secrets[name]
	if !ok {
		return KeyVaultSecretResponse{}, fmt.Errorf("secret %q not found", name)
	}
	return KeyVaultSecretResponse{Value: v}, nil
}

func strPtr(s string) *string { return &s }

func TestHolmesImage(t *testing.T) {
	assert.Equal(t, "myacr.azurecr.io/holmesgpt:latest", HolmesImage("myacr.azurecr.io"))
}

func TestNewHolmesConfigFromEnv(t *testing.T) {
	tests := []struct {
		name    string
		envVars map[string]string
		wantErr bool
		check   func(t *testing.T, cfg *HolmesConfig)
	}{
		{
			name: "happy path",
			envVars: map[string]string{
				"HOLMES_AZURE_OPENAI_API_BASE":    "https://myoai.openai.azure.com",
				"HOLMES_AZURE_OPENAI_API_VERSION": "2025-01-01",
				"HOLMES_MODEL":                    "azure/gpt-4o",
				"HOLMES_DEFAULT_TIMEOUT":          "300",
				"HOLMES_IMAGE":                    "custom.io/holmesgpt:v2",
			},
			check: func(t *testing.T, cfg *HolmesConfig) {
				assert.Equal(t, "https://myoai.openai.azure.com", cfg.AzureOpenAIAPIBase)
				assert.Equal(t, "2025-01-01", cfg.AzureOpenAIAPIVersion)
				assert.Equal(t, "azure/gpt-4o", cfg.Model)
				assert.Equal(t, 300, cfg.DefaultTimeout)
				assert.Equal(t, "custom.io/holmesgpt:v2", cfg.Image)
			},
		},
		{
			name: "defaults applied",
			envVars: map[string]string{
				"HOLMES_AZURE_OPENAI_API_BASE": "https://myoai.openai.azure.com",
			},
			check: func(t *testing.T, cfg *HolmesConfig) {
				assert.Equal(t, DefaultAzureOpenAIAPIVersion, cfg.AzureOpenAIAPIVersion)
				assert.Equal(t, DefaultModel, cfg.Model)
				assert.Equal(t, DefaultTimeoutSeconds, cfg.DefaultTimeout)
				assert.Equal(t, DefaultMaxConcurrentInvestigations, cfg.MaxConcurrentInvestigations)
				assert.Equal(t, "myacr.azurecr.io/holmesgpt:latest", cfg.Image)
			},
		},
		{
			name:    "missing API base",
			envVars: map[string]string{},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, key := range []string{
				"HOLMES_IMAGE", "HOLMES_AZURE_OPENAI_API_BASE",
				"HOLMES_AZURE_OPENAI_API_VERSION", "HOLMES_MODEL",
				"HOLMES_DEFAULT_TIMEOUT", "HOLMES_MAX_CONCURRENT_INVESTIGATIONS",
			} {
				t.Setenv(key, "")
			}
			for k, v := range tt.envVars {
				t.Setenv(k, v)
			}

			cfg, err := NewHolmesConfigFromEnv("myacr.azurecr.io")
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			tt.check(t, cfg)
		})
	}
}

func TestNewHolmesConfig(t *testing.T) {
	tests := []struct {
		name     string
		kvClient *mockKVClient
		wantErr  bool
		errMsg   string
	}{
		{
			name: "happy path from Key Vault",
			kvClient: &mockKVClient{
				secrets: map[string]*string{
					"holmes-azure-api-base": strPtr("https://kv-oai.openai.azure.com"),
				},
			},
		},
		{
			name: "Key Vault client error",
			kvClient: &mockKVClient{
				err: fmt.Errorf("connection refused"),
			},
			wantErr: true,
			errMsg:  "failed to get holmes-azure-api-base from Key Vault",
		},
		{
			name: "nil secret value",
			kvClient: &mockKVClient{
				secrets: map[string]*string{
					"holmes-azure-api-base": nil,
				},
			},
			wantErr: true,
			errMsg:  "nil value",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, key := range []string{
				"HOLMES_IMAGE", "HOLMES_AZURE_OPENAI_API_VERSION",
				"HOLMES_MODEL", "HOLMES_DEFAULT_TIMEOUT",
				"HOLMES_MAX_CONCURRENT_INVESTIGATIONS",
			} {
				t.Setenv(key, "")
			}

			cfg, err := NewHolmesConfig(context.Background(), "myacr.azurecr.io", tt.kvClient)
			if tt.wantErr {
				require.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
				return
			}
			require.NoError(t, err)
			assert.Equal(t, "https://kv-oai.openai.azure.com", cfg.AzureOpenAIAPIBase)
			assert.NotEmpty(t, cfg.Image)
			assert.NotEmpty(t, cfg.Model)
		})
	}
}

func TestHolmesConfigValidate(t *testing.T) {
	validConfig := func() HolmesConfig {
		return HolmesConfig{
			Image:                       "myacr.azurecr.io/holmesgpt:latest",
			AzureOpenAIAPIBase:          "https://myoai.openai.azure.com",
			AzureOpenAIAPIVersion:       DefaultAzureOpenAIAPIVersion,
			Model:                       DefaultModel,
			DefaultTimeout:              DefaultTimeoutSeconds,
			MaxConcurrentInvestigations: DefaultMaxConcurrentInvestigations,
		}
	}

	tests := []struct {
		name    string
		modify  func(c *HolmesConfig)
		wantErr bool
		errMsg  string
	}{
		{name: "valid config", modify: func(_ *HolmesConfig) {}},
		{name: "empty API base", modify: func(c *HolmesConfig) { c.AzureOpenAIAPIBase = "" }, wantErr: true, errMsg: "AzureOpenAIAPIBase is required"},
		{name: "empty image", modify: func(c *HolmesConfig) { c.Image = "" }, wantErr: true, errMsg: "Image"},
		{name: "image with leading slash", modify: func(c *HolmesConfig) { c.Image = "/holmesgpt:latest" }, wantErr: true, errMsg: "Image"},
		{name: "invalid model", modify: func(c *HolmesConfig) { c.Model = "invalid model" }, wantErr: true, errMsg: "does not match"},
		{name: "zero timeout", modify: func(c *HolmesConfig) { c.DefaultTimeout = 0 }, wantErr: true, errMsg: "DefaultTimeout must be > 0"},
		{name: "zero max concurrent", modify: func(c *HolmesConfig) { c.MaxConcurrentInvestigations = 0 }, wantErr: true, errMsg: "MaxConcurrentInvestigations must be > 0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfig()
			tt.modify(&cfg)
			err := cfg.Validate()
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				require.NoError(t, err)
			}
		})
	}
}
