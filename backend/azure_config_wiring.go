package main

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
			fmt.Errorf("error validating file: %s: %w", path, validationErrors.ToAggregate())
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

func getAzureConfig(ctx context.Context, azureRuntimeConfigPath string, tracerProvider trace.TracerProvider) (*azureconfig.AzureConfig, error) {
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
