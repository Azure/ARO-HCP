package config_test

import (
	"path/filepath"
	"testing"

	"github.com/Azure/ARO-HCP/test/util/config"
)

func TestLoadConfig(t *testing.T) {
	opts := config.ConfigOptions{
		ConfigFile:         filepath.Join("..", "..", "testdata", "config", "config.yaml"),
		ConfigFileOverride: filepath.Join("..", "..", "testdata", "config", "override.yaml"),
		Cloud:              "public",
		DeployEnv:          "dev",
		Region:             "uksouth",
	}

	err := config.LoadConfig(opts)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	val, err := config.ServiceConfig.GetByPath("test.property")
	if err != nil {
		t.Fatalf("Failed to get test.property: %v", err)
	}
	valStr, ok := val.(string)
	if !ok {
		t.Fatalf("Expected string, got %T", val)
	}

	if valStr != "override_value" {
		t.Errorf("Expected 'override_value', got '%v'", valStr)
	}

	val2, err := config.ServiceConfig.GetByPath("test.cloud")
	if err != nil {
		t.Fatalf("Failed to get test.cloud: %v", err)
	}
	val2Str, ok := val2.(string)
	if !ok {
		t.Fatalf("Expected string, got %T", val2)
	}
	if val2Str != "public" {
		t.Errorf("Expected 'public', got '%v'", val2)
	}

	val3, err := config.ServiceConfig.GetByPath("test.region_prop")
	if err != nil {
		t.Fatalf("Failed to get test.region_prop: %v", err)
	}
	val3Str, ok := val3.(string)
	if !ok {
		t.Fatalf("Expected string, got %T", val3)
	}
	if val3Str != "region_override" {
		t.Errorf("Expected 'region_override', got '%v'", val3)
	}
}
