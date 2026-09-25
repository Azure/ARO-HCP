// Copyright 2026 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

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
