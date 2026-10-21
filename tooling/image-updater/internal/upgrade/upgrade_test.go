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

package upgrade

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-logr/logr"

	"github.com/Azure/ARO-HCP/tooling/image-updater/internal/config"
)

func testLogger() logr.Logger {
	return logr.FromSlogHandler(slog.NewTextHandler(&strings.Builder{}, &slog.HandlerOptions{
		Level: slog.LevelError,
	}))
}

func testContext() context.Context {
	return logr.NewContext(context.Background(), testLogger())
}

func TestExtractVersion(t *testing.T) {
	tests := []struct {
		name       string
		repository string
		prefix     string
		want       string
		wantErr    bool
	}{
		{
			name:       "ACM 2.16",
			repository: "redhat-user-workloads/crt-redhat-acm-tenant/acm-operator-bundle-acm-216",
			prefix:     "acm-operator-bundle-acm-",
			want:       "2.16",
		},
		{
			name:       "ACM 2.17",
			repository: "redhat-user-workloads/crt-redhat-acm-tenant/acm-operator-bundle-acm-217",
			prefix:     "acm-operator-bundle-acm-",
			want:       "2.17",
		},
		{
			name:       "MCE 2.11",
			repository: "redhat-user-workloads/crt-redhat-acm-tenant/mce-operator-bundle-mce-211",
			prefix:     "mce-operator-bundle-mce-",
			want:       "2.11",
		},
		{
			name:       "MCE 2.12",
			repository: "redhat-user-workloads/crt-redhat-acm-tenant/mce-operator-bundle-mce-212",
			prefix:     "mce-operator-bundle-mce-",
			want:       "2.12",
		},
		{
			name:       "single digit minor",
			repository: "redhat-user-workloads/crt-redhat-acm-tenant/acm-operator-bundle-acm-29",
			prefix:     "acm-operator-bundle-acm-",
			want:       "2.9",
		},
		{
			name:       "prefix not found",
			repository: "redhat-user-workloads/crt-redhat-acm-tenant/something-else-123",
			prefix:     "acm-operator-bundle-acm-",
			wantErr:    true,
		},
		{
			name:       "no version suffix",
			repository: "redhat-user-workloads/crt-redhat-acm-tenant/acm-operator-bundle-acm-",
			prefix:     "acm-operator-bundle-acm-",
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := extractVersion(tt.repository, tt.prefix)
			if (err != nil) != tt.wantErr {
				t.Errorf("extractVersion() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("extractVersion() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIncrementMinorVersion(t *testing.T) {
	tests := []struct {
		name    string
		version string
		want    string
		wantErr bool
	}{
		{
			name:    "2.16 to 2.17",
			version: "2.16",
			want:    "2.17",
		},
		{
			name:    "2.11 to 2.12",
			version: "2.11",
			want:    "2.12",
		},
		{
			name:    "2.9 to 2.10",
			version: "2.9",
			want:    "2.10",
		},
		{
			name:    "3.0 to 3.1",
			version: "3.0",
			want:    "3.1",
		},
		{
			name:    "invalid format",
			version: "invalid",
			wantErr: true,
		},
		{
			name:    "empty string",
			version: "",
			wantErr: true,
		},
		{
			name:    "non-numeric major",
			version: "x.1",
			wantErr: true,
		},
		{
			name:    "non-numeric minor",
			version: "2.x",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := incrementMinorVersion(tt.version)
			if (err != nil) != tt.wantErr {
				t.Errorf("incrementMinorVersion() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("incrementMinorVersion() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBuildNextRepo(t *testing.T) {
	tests := []struct {
		name        string
		currentRepo string
		prefix      string
		nextVersion string
		want        string
	}{
		{
			name:        "ACM 2.16 to 2.17",
			currentRepo: "redhat-user-workloads/crt-redhat-acm-tenant/acm-operator-bundle-acm-216",
			prefix:      "acm-operator-bundle-acm-",
			nextVersion: "2.17",
			want:        "redhat-user-workloads/crt-redhat-acm-tenant/acm-operator-bundle-acm-217",
		},
		{
			name:        "MCE 2.11 to 2.12",
			currentRepo: "redhat-user-workloads/crt-redhat-acm-tenant/mce-operator-bundle-mce-211",
			prefix:      "mce-operator-bundle-mce-",
			nextVersion: "2.12",
			want:        "redhat-user-workloads/crt-redhat-acm-tenant/mce-operator-bundle-mce-212",
		},
		{
			name:        "ACM 2.9 to 2.10",
			currentRepo: "redhat-user-workloads/crt-redhat-acm-tenant/acm-operator-bundle-acm-29",
			prefix:      "acm-operator-bundle-acm-",
			nextVersion: "2.10",
			want:        "redhat-user-workloads/crt-redhat-acm-tenant/acm-operator-bundle-acm-210",
		},
		{
			name:        "prefix not found",
			currentRepo: "some-other-repo",
			prefix:      "acm-operator-bundle-acm-",
			nextVersion: "2.17",
			want:        "some-other-repo",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildNextRepo(tt.currentRepo, tt.prefix, tt.nextVersion)
			if got != tt.want {
				t.Errorf("buildNextRepo() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCheckerCheckAll(t *testing.T) {
	// Set up a mock Quay server
	mux := http.NewServeMux()

	// ACM 2.17 repo exists with tags
	mux.HandleFunc("/api/v1/repository/redhat-user-workloads/crt-redhat-acm-tenant/acm-operator-bundle-acm-217", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(quayRepoResponse{Name: "acm-operator-bundle-acm-217"})
	})
	mux.HandleFunc("/api/v1/repository/redhat-user-workloads/crt-redhat-acm-tenant/acm-operator-bundle-acm-217/tag/", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(quayTagsResponse{
			Tags: []quayTag{
				{Name: "v2.17.0-155", LastModified: "Wed, 15 Apr 2026 04:02:24 -0000"},
				{Name: "v2.17.0-154", LastModified: "Tue, 14 Apr 2026 21:24:54 -0000"},
			},
		})
	})

	// MCE 2.12 repo does not exist
	mux.HandleFunc("/api/v1/repository/redhat-user-workloads/crt-redhat-acm-tenant/mce-operator-bundle-mce-212", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Not Found", http.StatusNotFound)
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	cfg := &config.Config{
		Images: map[string]config.ImageConfig{
			"acm-operator": {
				Group: "hypershift-stack",
				Source: config.Source{
					Image:      "quay.io/redhat-user-workloads/crt-redhat-acm-tenant/acm-operator-bundle-acm-216",
					TagPattern: `^v\d+\.\d+\.\d+-\d+$`,
					RepoVersionUpgrade: &config.RepoVersionUpgrade{
						RepoPrefix: "acm-operator-bundle-acm-",
					},
				},
				Targets: []config.Target{
					{JsonPath: "defaults.acm.operator.bundle.digest", FilePath: "../../config/config.yaml"},
				},
			},
			"acm-mce": {
				Group: "hypershift-stack",
				Source: config.Source{
					Image:      "quay.io/redhat-user-workloads/crt-redhat-acm-tenant/mce-operator-bundle-mce-211",
					TagPattern: `^v\d+\.\d+\.\d+-\d+$`,
					RepoVersionUpgrade: &config.RepoVersionUpgrade{
						RepoPrefix: "mce-operator-bundle-mce-",
					},
				},
				Targets: []config.Target{
					{JsonPath: "defaults.acm.mce.bundle.digest", FilePath: "../../config/config.yaml"},
				},
			},
		},
	}

	checker := NewChecker(cfg)
	checker.baseURL = server.URL + "/api/v1"

	ctx := testContext()
	results, err := checker.CheckAll(ctx)
	if err != nil {
		t.Fatalf("CheckAll() error = %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("CheckAll() returned %d results, want 2", len(results))
	}

	// Sort results by component name for deterministic assertions
	var acmResult, mceResult *Result
	for i := range results {
		switch results[i].ComponentName {
		case "acm-operator":
			acmResult = &results[i]
		case "acm-mce":
			mceResult = &results[i]
		}
	}

	if acmResult == nil || mceResult == nil {
		t.Fatal("CheckAll() missing expected component results")
	}

	// ACM 2.17 should be detected
	if !acmResult.UpgradeAvailable {
		t.Error("acm-operator: expected UpgradeAvailable=true")
	}
	if !acmResult.NextRepoExists {
		t.Error("acm-operator: expected NextRepoExists=true")
	}
	if acmResult.NextVersion != "2.17" {
		t.Errorf("acm-operator: NextVersion = %q, want %q", acmResult.NextVersion, "2.17")
	}
	if acmResult.LatestTag != "v2.17.0-155" {
		t.Errorf("acm-operator: LatestTag = %q, want %q", acmResult.LatestTag, "v2.17.0-155")
	}

	// MCE 2.12 should not exist
	if mceResult.UpgradeAvailable {
		t.Error("acm-mce: expected UpgradeAvailable=false")
	}
	if mceResult.NextRepoExists {
		t.Error("acm-mce: expected NextRepoExists=false")
	}
	if mceResult.NextVersion != "2.12" {
		t.Errorf("acm-mce: NextVersion = %q, want %q", mceResult.NextVersion, "2.12")
	}
}

func TestCheckerCheckAllNoACMComponents(t *testing.T) {
	cfg := &config.Config{
		Images: map[string]config.ImageConfig{
			"maestro": {
				Group: "hypershift-stack",
				Source: config.Source{
					Image: "quay.io/redhat-user-workloads/maestro-rhtap-tenant/maestro/maestro",
				},
			},
		},
	}

	checker := NewChecker(cfg)
	ctx := testContext()

	results, err := checker.CheckAll(ctx)
	if err != nil {
		t.Fatalf("CheckAll() error = %v", err)
	}

	if len(results) != 0 {
		t.Errorf("CheckAll() returned %d results, want 0 (no components with repoVersionUpgrade)", len(results))
	}
}

func TestHasUpgrades(t *testing.T) {
	tests := []struct {
		name    string
		results []Result
		want    bool
	}{
		{
			name:    "empty results",
			results: []Result{},
			want:    false,
		},
		{
			name: "no upgrades",
			results: []Result{
				{ComponentName: "acm-operator", UpgradeAvailable: false},
				{ComponentName: "acm-mce", UpgradeAvailable: false},
			},
			want: false,
		},
		{
			name: "one upgrade",
			results: []Result{
				{ComponentName: "acm-operator", UpgradeAvailable: true},
				{ComponentName: "acm-mce", UpgradeAvailable: false},
			},
			want: true,
		},
		{
			name: "all upgrades",
			results: []Result{
				{ComponentName: "acm-operator", UpgradeAvailable: true},
				{ComponentName: "acm-mce", UpgradeAvailable: true},
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := HasUpgrades(tt.results); got != tt.want {
				t.Errorf("HasUpgrades() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFormatResults(t *testing.T) {
	t.Run("upgrade available", func(t *testing.T) {
		results := []Result{
			{
				ComponentName:    "acm-operator",
				CurrentRepo:      "redhat-user-workloads/crt-redhat-acm-tenant/acm-operator-bundle-acm-216",
				CurrentVersion:   "2.16",
				NextRepo:         "redhat-user-workloads/crt-redhat-acm-tenant/acm-operator-bundle-acm-217",
				NextVersion:      "2.17",
				NextRepoExists:   true,
				LatestTag:        "v2.17.0-155",
				LatestTagDate:    "Wed, 15 Apr 2026 04:02:24 -0000",
				UpgradeAvailable: true,
			},
		}
		output, err := FormatResults(results, "table")
		if err != nil {
			t.Fatalf("FormatResults() error: %v", err)
		}
		if !strings.Contains(output, "acm-operator") {
			t.Error("expected output to contain component name")
		}
		if !strings.Contains(output, "Available") {
			t.Error("expected output to contain Available status")
		}
	})

	t.Run("no upgrade", func(t *testing.T) {
		results := []Result{
			{
				ComponentName:  "acm-mce",
				CurrentVersion: "2.11",
				NextVersion:    "2.12",
				NextRepoExists: false,
			},
		}
		output, err := FormatResults(results, "table")
		if err != nil {
			t.Fatalf("FormatResults() error: %v", err)
		}
		if !strings.Contains(output, "Not available") {
			t.Error("expected output to indicate repo not available")
		}
	})

	t.Run("empty results", func(t *testing.T) {
		output, err := FormatResults(nil, "table")
		if err != nil {
			t.Fatalf("FormatResults() error: %v", err)
		}
		if !strings.Contains(output, "No components with repoVersionUpgrade") {
			t.Error("expected output to indicate no components found")
		}
	})
}

func TestApplyUpgrades(t *testing.T) {
	t.Run("updates both config files", func(t *testing.T) {
		tmpDir := t.TempDir()

		// Create a mock image-updater config
		updaterConfig := filepath.Join(tmpDir, "image-updater-config.yaml")
		updaterContent := `images:
  acm-operator:
    group: hypershift-stack
    source:
      image: quay.io/redhat-user-workloads/crt-redhat-acm-tenant/acm-operator-bundle-acm-216
      tagPattern: "^v\\d+\\.\\d+\\.\\d+-\\d+$"
    targets:
    - jsonPath: defaults.acm.operator.bundle.digest
      filePath: ../../config/config.yaml
  acm-mce:
    group: hypershift-stack
    source:
      image: quay.io/redhat-user-workloads/crt-redhat-acm-tenant/mce-operator-bundle-mce-211
      tagPattern: "^v\\d+\\.\\d+\\.\\d+-\\d+$"
    targets:
    - jsonPath: defaults.acm.mce.bundle.digest
      filePath: ../../config/config.yaml
`
		if err := os.WriteFile(updaterConfig, []byte(updaterContent), 0644); err != nil {
			t.Fatal(err)
		}

		// Create a mock target config
		targetConfig := filepath.Join(tmpDir, "config.yaml")
		targetContent := `defaults:
  acm:
    operator:
      bundle:
        registry: quay.io
        repository: redhat-user-workloads/crt-redhat-acm-tenant/acm-operator-bundle-acm-216
        digest: sha256:abc123
    mce:
      bundle:
        registry: quay.io
        repository: redhat-user-workloads/crt-redhat-acm-tenant/mce-operator-bundle-mce-211
        digest: sha256:def456
`
		if err := os.WriteFile(targetConfig, []byte(targetContent), 0644); err != nil {
			t.Fatal(err)
		}

		results := []Result{
			{
				ComponentName:    "acm-operator",
				CurrentRepo:      "redhat-user-workloads/crt-redhat-acm-tenant/acm-operator-bundle-acm-216",
				NextRepo:         "redhat-user-workloads/crt-redhat-acm-tenant/acm-operator-bundle-acm-217",
				UpgradeAvailable: true,
			},
			{
				ComponentName:    "acm-mce",
				CurrentRepo:      "redhat-user-workloads/crt-redhat-acm-tenant/mce-operator-bundle-mce-211",
				NextRepo:         "redhat-user-workloads/crt-redhat-acm-tenant/mce-operator-bundle-mce-212",
				UpgradeAvailable: false, // MCE not available yet
			},
		}

		cfg := &config.Config{
			Images: map[string]config.ImageConfig{
				"acm-operator": {
					Source: config.Source{
						RepoVersionUpgrade: &config.RepoVersionUpgrade{
							RepoPrefix: "acm-operator-bundle-acm-",
						},
					},
					Targets: []config.Target{
						{JsonPath: "defaults.acm.operator.bundle.digest", FilePath: targetConfig},
					},
				},
				"acm-mce": {
					Source: config.Source{
						RepoVersionUpgrade: &config.RepoVersionUpgrade{
							RepoPrefix: "mce-operator-bundle-mce-",
						},
					},
					Targets: []config.Target{
						{JsonPath: "defaults.acm.mce.bundle.digest", FilePath: targetConfig},
					},
				},
			},
		}

		err := ApplyUpgrades(results, updaterConfig, cfg)
		if err != nil {
			t.Fatalf("ApplyUpgrades() error = %v", err)
		}

		// Verify updater config was updated for ACM but not MCE
		updaterData, _ := os.ReadFile(updaterConfig)
		updaterStr := string(updaterData)
		if !strings.Contains(updaterStr, "acm-operator-bundle-acm-217") {
			t.Error("expected updater config to contain acm-217")
		}
		if strings.Contains(updaterStr, "acm-operator-bundle-acm-216") {
			t.Error("expected updater config to no longer contain acm-216")
		}
		if !strings.Contains(updaterStr, "mce-operator-bundle-mce-211") {
			t.Error("expected updater config to still contain mce-211 (no upgrade)")
		}

		// Verify target config was updated for ACM but not MCE
		targetData, _ := os.ReadFile(targetConfig)
		targetStr := string(targetData)
		if !strings.Contains(targetStr, "acm-operator-bundle-acm-217") {
			t.Error("expected target config to contain acm-217")
		}
		if strings.Contains(targetStr, "acm-operator-bundle-acm-216") {
			t.Error("expected target config to no longer contain acm-216")
		}
		if !strings.Contains(targetStr, "mce-operator-bundle-mce-211") {
			t.Error("expected target config to still contain mce-211 (no upgrade)")
		}
	})

	t.Run("no upgrades skips file writes", func(t *testing.T) {
		tmpDir := t.TempDir()

		updaterConfig := filepath.Join(tmpDir, "updater.yaml")
		content := "some content"
		_ = os.WriteFile(updaterConfig, []byte(content), 0644)

		results := []Result{
			{UpgradeAvailable: false},
		}

		cfg := &config.Config{
			Images: map[string]config.ImageConfig{},
		}

		err := ApplyUpgrades(results, updaterConfig, cfg)
		if err != nil {
			t.Fatalf("ApplyUpgrades() error = %v", err)
		}

		// Files should be unchanged
		data, _ := os.ReadFile(updaterConfig)
		if string(data) != content {
			t.Error("expected file to be unchanged when no upgrades")
		}
	})
}
