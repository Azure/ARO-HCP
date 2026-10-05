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

package config

import (
	"path/filepath"
	"testing"
)

// realRepoConfigFile points at the actual repo config/config.yaml so
// applyDevEnvironmentRegionShort can find the real tooling/templatize/settings.yaml
// alongside it, exactly as it does in production (ARO_HCP_CONFIG_FILE points at
// <repo-root>/config/config.yaml).
const realRepoConfigFile = "../../../config/config.yaml"

func TestApplyDevEnvironmentRegionShort_Override(t *testing.T) {
	t.Setenv("BUILD_ID", "1234567890")

	// Cloud is "public" here, not "dev": that's the real ARO_HCP_CLOUD value
	// e2e tests run with in CI even though settings.yaml declares ci01 under
	// cloud "dev" — the lookup below must not key off opts.Cloud.
	opts := ConfigOptions{
		ConfigFile: filepath.FromSlash(realRepoConfigFile),
		Cloud:      "public",
		DeployEnv:  "ci01",
	}

	got := applyDevEnvironmentRegionShort(opts, "yt")
	want := "j4567890"
	if got != want {
		t.Errorf("applyDevEnvironmentRegionShort() = %q, want %q", got, want)
	}
}

func TestApplyDevEnvironmentRegionShort_Suffix(t *testing.T) {
	t.Setenv("USER", "alice")

	opts := ConfigOptions{
		ConfigFile: filepath.FromSlash(realRepoConfigFile),
		Cloud:      "dev",
		DeployEnv:  "pers",
	}

	got := applyDevEnvironmentRegionShort(opts, "yt")
	want := "ytalic"
	if got != want {
		t.Errorf("applyDevEnvironmentRegionShort() = %q, want %q", got, want)
	}
}

func TestApplyDevEnvironmentRegionShort_NoOverrideConfigured(t *testing.T) {
	opts := ConfigOptions{
		ConfigFile: filepath.FromSlash(realRepoConfigFile),
		Cloud:      "dev",
		DeployEnv:  "cspr",
	}

	got := applyDevEnvironmentRegionShort(opts, "yt")
	if got != "yt" {
		t.Errorf("applyDevEnvironmentRegionShort() = %q, want unchanged %q", got, "yt")
	}
}

func TestApplyDevEnvironmentRegionShort_UnknownEnvironment(t *testing.T) {
	opts := ConfigOptions{
		ConfigFile: filepath.FromSlash(realRepoConfigFile),
		Cloud:      "public",
		DeployEnv:  "prod",
	}

	got := applyDevEnvironmentRegionShort(opts, "yt")
	if got != "yt" {
		t.Errorf("applyDevEnvironmentRegionShort() = %q, want unchanged %q", got, "yt")
	}
}

func TestApplyDevEnvironmentRegionShort_MissingSettingsFile(t *testing.T) {
	opts := ConfigOptions{
		ConfigFile: filepath.FromSlash("nonexistent/config/config.yaml"),
		Cloud:      "dev",
		DeployEnv:  "ci01",
	}

	got := applyDevEnvironmentRegionShort(opts, "yt")
	if got != "yt" {
		t.Errorf("applyDevEnvironmentRegionShort() = %q, want unchanged %q", got, "yt")
	}
}
