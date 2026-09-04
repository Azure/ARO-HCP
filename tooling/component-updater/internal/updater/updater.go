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

package updater

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"

	"github.com/Azure/ARO-HCP/tooling/component-updater/internal/config"
	"github.com/Azure/ARO-HCP/tooling/component-updater/internal/providers"
)

type ComponentResult struct {
	Name      string
	Current   string
	Available []string
	Next      string
	Error     error
}

func ProviderForName(name string, subscriptionID string, azureClient *http.Client) (providers.VersionProvider, error) {
	switch name {
	case "azure-grafana":
		return providers.NewGrafanaProvider(subscriptionID, nil), nil
	case "azure-aks":
		return providers.NewAKSProvider(subscriptionID, azureClient), nil
	case "azure-istio":
		return providers.NewIstioProvider(subscriptionID, azureClient), nil
	default:
		return nil, fmt.Errorf("unknown provider %q", name)
	}
}

func Check(ctx context.Context, cfg *config.Config, configDir, subscriptionID string, cred azcore.TokenCredential, components []string) ([]ComponentResult, error) {
	var azureClient *http.Client
	if cred != nil {
		azureClient = providers.NewAzureHTTPClient(cred)
	}

	var results []ComponentResult

	for name, comp := range cfg.Components {
		if len(components) > 0 && !containsString(components, name) {
			continue
		}

		result := checkComponent(ctx, name, comp, configDir, subscriptionID, azureClient)
		results = append(results, result)
	}

	return results, nil
}

func checkComponent(ctx context.Context, name string, comp config.ComponentConfig, configDir, subscriptionID string, azureClient *http.Client) ComponentResult {
	result := ComponentResult{Name: name}

	provider, err := ProviderForName(comp.Provider, subscriptionID, azureClient)
	if err != nil {
		result.Error = err
		return result
	}

	if len(comp.Targets) > 0 {
		target := comp.Targets[0]
		filePath := resolveFilePath(configDir, target.FilePath)
		current, err := config.ReadCurrentVersion(filePath, target.JsonPath)
		if err != nil {
			result.Error = fmt.Errorf("reading current version: %w", err)
			return result
		}
		result.Current = current
	}

	available, err := provider.AvailableVersions(ctx, comp.Locations)
	if err != nil {
		result.Error = fmt.Errorf("fetching available versions: %w", err)
		return result
	}
	result.Available = available

	if result.Current != "" {
		result.Next = provider.NextVersion(result.Current, available)
	}

	return result
}

func Update(ctx context.Context, cfg *config.Config, configDir, subscriptionID string, cred azcore.TokenCredential, components []string) ([]ComponentResult, error) {
	results, err := Check(ctx, cfg, configDir, subscriptionID, cred, components)
	if err != nil {
		return nil, err
	}

	for i, r := range results {
		if r.Error != nil || r.Next == "" {
			continue
		}

		comp := cfg.Components[r.Name]
		for _, target := range comp.Targets {
			filePath := resolveFilePath(configDir, target.FilePath)
			if err := config.WriteVersion(filePath, target.JsonPath, r.Next); err != nil {
				results[i].Error = fmt.Errorf("updating %s in %s: %w", target.JsonPath, target.FilePath, err)
				break
			}
		}
	}

	return results, nil
}

func resolveFilePath(configDir, filePath string) string {
	if filepath.IsAbs(filePath) {
		return filePath
	}
	return filepath.Join(configDir, filePath)
}

func containsString(ss []string, s string) bool {
	for _, v := range ss {
		if strings.EqualFold(v, s) {
			return true
		}
	}
	return false
}
