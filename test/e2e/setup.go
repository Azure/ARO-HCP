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

package e2e

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"

	"github.com/Azure/ARO-HCP/test/util/config"
	"github.com/Azure/ARO-HCP/test/util/integration"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/log"
)

var (
	e2eSetup integration.SetupModel
)

func findRepoRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.work")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func envOrDefault(envVar, defaultVal string) string {
	if v := os.Getenv(envVar); v != "" {
		return v
	}
	return defaultVal
}

func setup(ctx context.Context) error {
	labelFilter := GinkgoLabelFilter()

	configFile := os.Getenv("ARO_HCP_CONFIG_FILE")
	if configFile == "" {
		if root := findRepoRoot(); root != "" {
			candidate := filepath.Join(root, "config", "config.yaml")
			if _, err := os.Stat(candidate); err == nil {
				configFile = candidate
			}
		}
	}

	// Load templated configuration
	opts := config.ConfigOptions{
		ConfigFile:         configFile,
		ConfigFileOverride: os.Getenv("ARO_HCP_CONFIG_FILE_OVERRIDE"),
		Cloud:              envOrDefault("CLOUD", "dev"),
		DeployEnv:          os.Getenv("DEPLOY_ENV"),
		Region:             envOrDefault("REGION", "centralus"),
	}

	if opts.ConfigFileOverride != "" && opts.ConfigFile == "" {
		return fmt.Errorf("ARO_HCP_CONFIG_FILE_OVERRIDE is set but ARO_HCP_CONFIG_FILE is not set")
	}

	requiresConfig := strings.Contains(labelFilter, labels.RequiresConfig[0])
	if requiresConfig && opts.ConfigFile == "" {
		return fmt.Errorf("test requires config but ARO_HCP_CONFIG_FILE is not set")
	}
	if requiresConfig && (opts.Cloud == "" || opts.DeployEnv == "" || opts.Region == "") {
		return fmt.Errorf("test requires config but CLOUD/DEPLOY_ENV/REGION are not all set (CLOUD=%q, DEPLOY_ENV=%q, REGION=%q)", opts.Cloud, opts.DeployEnv, opts.Region)
	}

	// Only fail if a config file is explicitly supplied but fails to render
	if opts.ConfigFile != "" {
		if err := config.LoadConfig(opts); err != nil {
			return fmt.Errorf("failed to load service config: %w", err)
		}
	}

	// Use GinkgoLabelFilter to determine if the test should load the e2e setup file
	if strings.Contains(labelFilter, labels.RequireNothing[0]) ||
		strings.Contains(labelFilter, labels.UpgradeInPlace[0]) {
		// Skip loading the e2esetup file
		e2eSetup = integration.SetupModel{} // zero value
	} else {
		var err error
		e2eSetup, err = integration.LoadE2ESetupFile(os.Getenv("SETUP_FILEPATH"))
		if err != nil {
			if bicepName, found := os.LookupEnv("FALLBACK_TO_BICEP"); found {
				// Fallback: create a complete HCP cluster using bicep
				log.Logger.Warnf("Failed to load e2e setup file: %v. Falling back to bicep deployment.", err)
				e2eSetup, err = integration.FallbackCreateClusterWithBicep(ctx, bicepName)
				if err != nil {
					return fmt.Errorf("failed to create cluster with bicep fallback: %w", err)
				}
			} else {
				return fmt.Errorf("failed to load e2e setup file and FALLBACK_TO_BICEP is not set: %w", err)
			}
		}
	}

	return nil
}
