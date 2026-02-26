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

package app

import (
	"context"
	"fmt"
	"os"

	"go.opentelemetry.io/otel/trace"

	k8soperation "k8s.io/apimachinery/pkg/api/operation"

	"sigs.k8s.io/yaml"

	apisconfigv1 "github.com/Azure/ARO-HCP/backend/pkg/apis/config/v1"
	azureconfig "github.com/Azure/ARO-HCP/backend/pkg/azure/config"
)

func loadAzureRuntimeConfig(ctx context.Context, path string) (*apisconfigv1.AzureRuntimeConfig, error) {
	if len(path) == 0 {
		return nil, fmt.Errorf("configuration path is required")
	}

	rawBytes, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("error reading file %s: %w", path, err)
	}

	var config apisconfigv1.AzureRuntimeConfig
	err = yaml.Unmarshal(rawBytes, &config)
	if err != nil {
		return nil, fmt.Errorf("error unmarshaling file %s: %w", path, err)
	}

	validationErrors := config.Validate(ctx, k8soperation.Operation{Type: k8soperation.Create})
	if len(validationErrors) > 0 {
		return nil,
			fmt.Errorf("error validating file %s: %w", path, validationErrors.ToAggregate())
	}

	return &config, nil
}

func buildAzureConfig(azureRuntimeConfig *apisconfigv1.AzureRuntimeConfig, tracerProvider trace.TracerProvider) (*azureconfig.AzureConfig, error) {
	cloudEnvironment, err := azureconfig.NewAzureCloudEnvironment(azureRuntimeConfig.CloudEnvironmentName, tracerProvider)
	if err != nil {
		return nil, fmt.Errorf("error building azure cloud environment configuration: %w", err)
	}

	out := &azureconfig.AzureConfig{
		CloudEnvironment:   cloudEnvironment,
		AzureRuntimeConfig: azureRuntimeConfig,
	}

	return out, err
}

func NewAzureConfig(ctx context.Context, azureRuntimeConfigPath string, tracerProvider trace.TracerProvider) (*azureconfig.AzureConfig, error) {
	if len(azureRuntimeConfigPath) == 0 {
		return nil, nil
	}

	azureRuntimeConfig, err := loadAzureRuntimeConfig(ctx, azureRuntimeConfigPath)
	if err != nil {
		return nil, fmt.Errorf("error loading azure runtime config: %w", err)
	}

	azureConfig, err := buildAzureConfig(azureRuntimeConfig, tracerProvider)
	if err != nil {
		return nil, fmt.Errorf("error building azure configuration: %w", err)
	}

	return azureConfig, nil
}
