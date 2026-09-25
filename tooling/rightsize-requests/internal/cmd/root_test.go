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

package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const cliInput = `{
  "version": 1, "start": "2026-09-01T00:00:00Z", "end": "2026-09-02T00:00:00Z",
  "headroom": 1.2, "changeThreshold": 0.1, "cpuWindow": "2m", "warnings": [], "recommendations": [{
    "cluster": "svc", "namespace": "aro-hcp", "kind": "Deployment", "workload": "backend",
    "container": "aro-hcp-backend", "resource": "cpu", "initContainer": false,
    "replicas": 1, "measuredReplicas": 1, "peak": 0.2, "burstPeak": 0.4,
    "requestMin": 0.1, "requestMax": 0.1, "suggested": 0.24, "suggestedQuantity": "240m",
    "delta": 0.14, "direction": "increase", "eligible": true, "actionable": false, "alertRisk": false, "warnings": []
  }]
}`

func TestInputCLIWithoutCredentials(t *testing.T) {
	// This makes NewDefaultAzureCredential fail, even before token acquisition.
	t.Setenv("AZURE_TOKEN_CREDENTIALS", "not-a-credential")
	t.Setenv("PATH", t.TempDir())
	for _, dry := range []bool{true, false} {
		dir := t.TempDir()
		input, config := filepath.Join(dir, "right-sizing.json"), filepath.Join(dir, "config.yaml")
		const before = "defaults:\n  backend:\n    k8s:\n      resources:\n        requests:\n          cpu: 100m\n"
		for path, content := range map[string]string{input: cliInput, config: before} {
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		args := []string{"--input", input, "--config", config, "--source-prefix=defaults", "--write-prefix=clouds.dev.defaults"}
		if dry {
			args = append(args, "--dry-run")
		}
		cmd := NewRootCommand()
		cmd.SetArgs(args)
		if err := cmd.Execute(); err != nil {
			t.Fatalf("auth-free CLI failed: %v", err)
		}
		got, err := os.ReadFile(config)
		if err != nil {
			t.Fatal(err)
		}
		if dry && string(got) != before {
			t.Fatal("dry-run wrote config")
		}
		if !dry && (!strings.HasPrefix(string(got), before) || !strings.Contains(string(got), "cpu: 240m")) {
			t.Fatalf("missing dev upsert or changed defaults:\n%s", got)
		}
	}
}

func TestInputCLIConflictingFlags(t *testing.T) {
	t.Setenv("AZURE_TOKEN_CREDENTIALS", "not-a-credential")
	for _, flag := range []string{
		"--grafana-url=https://example.com", "--grafana-url=", "--window=14d", "--step=5m", "--margin=1.25",
		"--percentile=0.95", "--fleet-percentile=0.95", "--datasource-pattern=^services-", "--limit-multiple=0",
		"--commit", "--commit=false", "--render-cmd=", "--render-cmd=false", "--source-prefix=clouds.dev.defaults",
		"--source-prefix=", "--write-prefix=defaults", "--write-prefix=clouds.public.defaults", "--write-prefix=",
	} {
		t.Run(flag, func(t *testing.T) {
			cmd := NewRootCommand()
			cmd.SetArgs([]string{"--input=not-read.json", flag})
			err := cmd.Execute()
			name := strings.SplitN(flag, "=", 2)[0]
			if err == nil || !strings.Contains(err.Error(), name) || strings.Contains(err.Error(), "credentials") {
				t.Fatalf("expected explicit flag error before auth/read for %s, got %v", flag, err)
			}
		})
	}
	for _, args := range [][]string{nil, {"--input="}, {"--grafana-url="}} {
		cmd := NewRootCommand()
		cmd.SetArgs(args)
		if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "exactly one") {
			t.Fatalf("expected source selection error for %v, got %v", args, err)
		}
	}
}

func TestGrafanaCLIStillUsesCredentials(t *testing.T) {
	t.Setenv("AZURE_TOKEN_CREDENTIALS", "not-a-credential")
	cmd := NewRootCommand()
	cmd.SetArgs([]string{"--grafana-url=https://example.com"})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "failed to obtain Azure credentials") {
		t.Fatalf("Grafana path did not authenticate: %v", err)
	}
}

func TestChangeThresholdCLI(t *testing.T) {
	t.Setenv("AZURE_TOKEN_CREDENTIALS", "not-a-credential")
	for _, value := range []string{"-0.1", "1.1", "NaN", "+Inf", "-Inf"} {
		cmd := NewRootCommand()
		cmd.SetArgs([]string{"--input=not-read.json", "--change-threshold=" + value})
		if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "--change-threshold must be finite and between 0 and 1") {
			t.Fatalf("invalid threshold %s did not fail before auth/read: %v", value, err)
		}
	}
	cmd := NewRootCommand()
	cmd.SetArgs([]string{"--grafana-url=https://example.com", "--change-threshold=0.1"})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "--change-threshold requires --input") {
		t.Fatalf("Grafana accepted input-only flag: %v", err)
	}

	for _, tc := range []struct {
		name, threshold, reportThreshold string
		changed                          bool
	}{
		{name: "default overrides report zero", reportThreshold: "0"},
		{name: "explicit zero overrides report one", threshold: "0", reportThreshold: "1", changed: true},
		{name: "explicit smaller fraction", threshold: "0.05", reportThreshold: "0.1", changed: true},
		{name: "inclusive boundary", threshold: "0.1", reportThreshold: "0.1"},
		{name: "maximum fraction", threshold: "1", reportThreshold: "0.1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			input, config := filepath.Join(dir, "input.json"), filepath.Join(dir, "config.yaml")
			// 100m -> 110m is exactly the CLI's default 10% deadband.
			data := strings.NewReplacer(
				`"changeThreshold": 0.1`, `"changeThreshold": `+tc.reportThreshold,
				`"peak": 0.2`, `"peak": 0.09`,
				`"suggested": 0.24`, `"suggested": 0.11`,
				`"240m"`, `"110m"`,
			).Replace(cliInput)
			const before = "defaults:\n  backend:\n    k8s:\n      resources:\n        requests:\n          cpu: 100m\n"
			for path, content := range map[string]string{input: data, config: before} {
				if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			args := []string{"--input", input, "--config", config}
			if tc.threshold != "" {
				args = append(args, "--change-threshold="+tc.threshold)
			}
			cmd := NewRootCommand()
			cmd.SetArgs(args)
			if err := cmd.Execute(); err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(config)
			if err != nil {
				t.Fatal(err)
			}
			if tc.changed {
				if !strings.HasPrefix(string(got), before) || !strings.Contains(string(got), "cpu: 110m") {
					t.Fatalf("missing dev update: %s", got)
				}
			} else if string(got) != before {
				t.Fatalf("deadband did not prevent writes: %s", got)
			}
		})
	}
}
