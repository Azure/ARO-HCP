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
	"os"
	"regexp"
	"strconv"
)

const (
	DefaultAzureOpenAIAPIVersion       = "2025-04-01-preview"
	DefaultModel                       = "azure/gpt-5.2"
	DefaultTimeoutSeconds              = 600
	DefaultMaxConcurrentInvestigations = 20

	kvSecretAzureOpenAIAPIBase = "holmes-azure-api-base"

	envHolmesImage                 = "HOLMES_IMAGE"
	envAzureOpenAIAPIBase          = "HOLMES_AZURE_OPENAI_API_BASE"
	envAzureOpenAIAPIVersion       = "HOLMES_AZURE_OPENAI_API_VERSION"
	envModel                       = "HOLMES_MODEL"
	envDefaultTimeout              = "HOLMES_DEFAULT_TIMEOUT"
	envMaxConcurrentInvestigations = "HOLMES_MAX_CONCURRENT_INVESTIGATIONS"
	envServiceClusterEndpoint      = "HOLMES_SERVICE_CLUSTER_ENDPOINT"

	defaultServiceClusterEndpoint = "http://holmesgpt-svc.aro-holmesgpt.svc.cluster.local:80"

	HolmesNamespace      = "aro-holmesgpt"
	HolmesServiceAccount = "holmesgpt"
)

var modelRegex = regexp.MustCompile(`^[a-zA-Z0-9/.:_-]+$`)

type KeyVaultSecretResponse struct {
	Value *string
}

type KeyVaultSecretClient interface {
	GetSecret(ctx context.Context, name string, version string) (KeyVaultSecretResponse, error)
}

type HolmesConfig struct {
	Image                       string
	AzureOpenAIAPIBase          string
	AzureOpenAIAPIVersion       string
	Model                       string
	DefaultTimeout              int
	MaxConcurrentInvestigations int
	ServiceClusterEndpoint      string
}

func HolmesImage(acrDomain string) string {
	return acrDomain + "/holmesgpt:latest"
}

func NewHolmesConfig(ctx context.Context, acrDomain string, kvClient KeyVaultSecretClient) (*HolmesConfig, error) {
	apiBaseResp, err := kvClient.GetSecret(ctx, kvSecretAzureOpenAIAPIBase, "")
	if err != nil {
		return nil, fmt.Errorf("failed to get %s from Key Vault: %w", kvSecretAzureOpenAIAPIBase, err)
	}
	if apiBaseResp.Value == nil {
		return nil, fmt.Errorf("Key Vault secret %s has nil value", kvSecretAzureOpenAIAPIBase)
	}

	cfg := newHolmesConfigBase(acrDomain)
	cfg.AzureOpenAIAPIBase = *apiBaseResp.Value

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid holmes configuration: %w", err)
	}

	return cfg, nil
}

func NewHolmesConfigFromEnv(acrDomain string) (*HolmesConfig, error) {
	cfg := newHolmesConfigBase(acrDomain)
	cfg.AzureOpenAIAPIBase = os.Getenv(envAzureOpenAIAPIBase)

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid holmes configuration: %w", err)
	}

	return cfg, nil
}

func newHolmesConfigBase(acrDomain string) *HolmesConfig {
	return &HolmesConfig{
		Image:                       envWithDefault(envHolmesImage, HolmesImage(acrDomain)),
		AzureOpenAIAPIVersion:       envWithDefault(envAzureOpenAIAPIVersion, DefaultAzureOpenAIAPIVersion),
		Model:                       envWithDefault(envModel, DefaultModel),
		DefaultTimeout:              envIntWithDefault(envDefaultTimeout, DefaultTimeoutSeconds),
		MaxConcurrentInvestigations: envIntWithDefault(envMaxConcurrentInvestigations, DefaultMaxConcurrentInvestigations),
		ServiceClusterEndpoint:      envWithDefault(envServiceClusterEndpoint, defaultServiceClusterEndpoint),
	}
}

func (c *HolmesConfig) Validate() error {
	if c.AzureOpenAIAPIBase == "" {
		return fmt.Errorf("AzureOpenAIAPIBase is required")
	}
	if c.Image == "" || c.Image[0] == '/' {
		return fmt.Errorf("Image %q is invalid (set HOLMES_IMAGE or provide acrDomain)", c.Image)
	}
	if !modelRegex.MatchString(c.Model) {
		return fmt.Errorf("Model %q does not match required pattern %s", c.Model, modelRegex.String())
	}
	if c.DefaultTimeout <= 0 {
		return fmt.Errorf("DefaultTimeout must be > 0, got %d", c.DefaultTimeout)
	}
	if c.MaxConcurrentInvestigations <= 0 {
		return fmt.Errorf("MaxConcurrentInvestigations must be > 0, got %d", c.MaxConcurrentInvestigations)
	}
	return nil
}

func envWithDefault(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}

func envIntWithDefault(key string, defaultVal int) int {
	v := os.Getenv(key)
	if v == "" {
		return defaultVal
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return defaultVal
	}
	return n
}
