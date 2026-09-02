package config_test

import (
	"fmt"
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
	fmt.Println(val)
	if val.(string) != "override_value" {
		t.Errorf("Expected 'override_value', got '%v'", val)
	}

	val2, err := config.ServiceConfig.GetByPath("test.cloud")
	if err != nil {
		t.Fatalf("Failed to get test.cloud: %v", err)
	}
	if val2.(string) != "public" {
		t.Errorf("Expected 'public', got '%v'", val2)
	}

	val3, err := config.ServiceConfig.GetByPath("test.region_prop")
	if err != nil {
		t.Fatalf("Failed to get test.region_prop: %v", err)
	}
	if val3.(string) != "region_override" {
		t.Errorf("Expected 'region_override', got '%v'", val3)
	}
}
