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
  "version": 3, "start": "2026-09-01T00:00:00Z", "end": "2026-09-02T00:00:00Z",
  "headroom": 1, "changeThreshold": 0.1, "cpuWindow": "2m", "warnings": [], "recommendations": [{
    "cluster": "svc", "namespace": "aro-hcp", "kind": "Deployment", "workload": "backend",
    "container": "aro-hcp-backend", "resource": "cpu", "initContainer": false,
    "replicas": 1, "measuredReplicas": 1, "peak": 0.239, "burstPeak": 0.4,
    "requestMin": 0.1, "requestMax": 0.1, "suggested": 0.24, "suggestedQuantity": "240m",
    "delta": -0.14, "direction": "under", "eligible": true, "actionable": false, "alertRisk": false, "warnings": []
  }]
}`

// Keep report requests and CLI inputs independent of changes to chart sizing.
const cliSizingDocument = `apiVersion: scheduling.hypershift.openshift.io/v1alpha1
kind: ClusterSizingConfiguration
metadata:
  name: cluster
spec:
  sizes:
  - name: e2e_minimal
    effects:
      resourceRequests:
      - containerName: etcd
        deploymentName: etcd
        memory: 100Mi
        cpu: 100m
`

const cliSizingTemplate = "{{ if .Values.limitClusterSizes }}\n" + cliSizingDocument + "{{ else }}\n" + cliSizingDocument + "{{ end }}\n"

func TestExperimentalHCPCPUFlags(t *testing.T) {
	for _, args := range [][]string{
		{"--input=not-read", "--allow-decrease"},
		{"--input=not-read", "--sizing-template=not-read", "--namespace-prefix=ocm-arohcpci01-"},
		{"--input=not-read", "--allow-decrease", "--sizing-template=not-read", "--additional-hcp-config=not-read", "--namespace-prefix=ocm-arohcpci01-"},
		{"--grafana-url=https://example.com", "--allow-decrease", "--sizing-template=not-read", "--namespace-prefix=ocm-arohcpci01-"},
		{"--input=not-read", "--allow-decrease", "--additional-hcp-config=not-read", "--namespace-prefix=ocm-arohcpprod-"},
	} {
		cmd := NewRootCommand()
		cmd.SetArgs(append(args, "--experimental-hcp-cpu-reductions"))
		if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "--experimental-hcp-cpu-reductions requires") {
			t.Fatalf("expected experimental guard before reads for %v: %v", args, err)
		}
	}
	for _, prefix := range []string{"ocm-arohcpci00-", "ocm-arohcpci01-"} {
		t.Setenv("AZURE_TOKEN_CREDENTIALS", "not-a-credential")
		dir := t.TempDir()
		input, config := filepath.Join(dir, "input.json"), filepath.Join(dir, "config.yaml")
		data := strings.NewReplacer(`"cluster": "svc"`, `"cluster": "ci01-mgmt-1"`, `"namespace": "aro-hcp"`, `"namespace": "`+prefix+`hcp"`, `"workload": "backend"`, `"workload": "kube-scheduler"`, `"container": "aro-hcp-backend"`, `"container": "kube-scheduler"`, `"requestMin": 0.1`, `"requestMin": 0.3`, `"requestMax": 0.1`, `"requestMax": 0.3`).Replace(cliInput)
		const before = "defaults:\n  hypershift:\n    additionalMinimalResourceRequests: {}\n"
		for path, content := range map[string]string{input: data, config: before} {
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		cmd := NewRootCommand()
		cmd.SetArgs([]string{"--input=" + input, "--additional-hcp-config=" + config, "--namespace-prefix=" + prefix, "--allow-decrease", "--experimental-hcp-cpu-reductions", "--dry-run"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("experimental dry run failed: %v", err)
		}
		got, err := os.ReadFile(config)
		if err != nil || string(got) != before {
			t.Fatalf("dry run changed config: %v", err)
		}
	}
}

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
	for _, args := range [][]string{nil, {"--grafana-url="}} {
		cmd := NewRootCommand()
		cmd.SetArgs(args)
		if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "exactly one") {
			t.Fatalf("expected source selection error for %v, got %v", args, err)
		}
	}
}

func TestInputCLIEmptyPaths(t *testing.T) {
	t.Setenv("AZURE_TOKEN_CREDENTIALS", "not-a-credential")
	for _, args := range [][]string{
		{"--input="},
		{"--input=", "--input=not-read.json"},
		{"--input=not-read.json", "--input="},
		{"--input=not-read.json", "--input=", "--input=also-not-read.json"},
		{"--input=not-read.json", "--input=", "--sizing-template=not-read.yaml", "--namespace-prefix=ocm-arohcpci01-"},
	} {
		cmd := NewRootCommand()
		cmd.SetArgs(args)
		if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "--input path") || !strings.Contains(err.Error(), "must be nonempty") {
			t.Fatalf("expected empty input rejection before auth/read for %v, got %v", args, err)
		}
	}
}

func TestRepeatedInputCLI(t *testing.T) {
	t.Setenv("AZURE_TOKEN_CREDENTIALS", "not-a-credential")
	t.Setenv("PATH", t.TempDir())
	for _, sizing := range []bool{false, true} {
		for _, scenario := range []string{"forward", "reverse", "bad second", "dry run"} {
			name := "config/" + scenario
			if sizing {
				name = "sizing/" + scenario
			}
			t.Run(name, func(t *testing.T) {
				dir := t.TempDir()
				quiet, busy := filepath.Join(dir, "quiet,report.json"), filepath.Join(dir, "busy.json")
				target := filepath.Join(dir, "target.yaml")
				data := strings.NewReplacer(`"requestMin": 0.1`, `"requestMin": 0.3`, `"requestMax": 0.1`, `"requestMax": 0.3`).Replace(cliInput)
				before := "defaults:\n  backend:\n    k8s:\n      resources:\n        requests:\n          cpu: 300m\n"
				args := []string{"--config", target}
				if sizing {
					data = strings.NewReplacer(`"namespace": "aro-hcp"`, `"namespace": "ocm-arohcpci01-cluster"`, `"workload": "backend"`, `"workload": "etcd"`, `"container": "aro-hcp-backend"`, `"container": "etcd"`, `"kind": "Deployment"`, `"kind": "StatefulSet"`).Replace(data)
					before = strings.Replace(cliSizingTemplate, "cpu: 100m", "cpu: 300m", 1)
					args = []string{"--sizing-template", target, "--namespace-prefix=ocm-arohcpci01-"}
				}
				quietData := strings.NewReplacer(`"peak": 0.239`, `"peak": 0.119`, `"suggested": 0.24`, `"suggested": 0.12`, `"240m"`, `"120m"`).Replace(data)
				busyData := strings.NewReplacer(`"peak": 0.239`, `"peak": 0.479`, `"suggested": 0.24`, `"suggested": 0.48`, `"240m"`, `"480m"`).Replace(data)
				if scenario == "bad second" {
					busyData = strings.Replace(busyData, `"version": 3`, `"version": 1`, 1)
				}
				for path, content := range map[string]string{quiet: quietData, busy: busyData, target: before} {
					if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				inputs := []string{"--input", quiet, "--input", busy}
				if scenario == "reverse" {
					inputs = []string{"--input", busy, "--input", quiet}
				}
				args = append(args, inputs...)
				args = append(args, "--allow-decrease", "--change-threshold=0")
				if scenario == "dry run" {
					args = append(args, "--dry-run")
				}
				cmd := NewRootCommand()
				cmd.SetArgs(args)
				err := cmd.Execute()
				if scenario == "bad second" {
					if err == nil || !strings.Contains(err.Error(), "version") {
						t.Fatalf("expected second report validation error, got %v", err)
					}
				} else if err != nil {
					t.Fatalf("auth-free repeated input failed: %v", err)
				}
				got, err := os.ReadFile(target)
				if err != nil {
					t.Fatal(err)
				}
				if scenario == "bad second" || scenario == "dry run" {
					if string(got) != before {
						t.Fatalf("validation failure or dry run wrote target:\n%s", got)
					}
				} else if sizing {
					want := strings.Replace(before, "cpu: 300m", "cpu: 480m", 1)
					if string(got) != want || want == before {
						t.Fatalf("expected only etcd CPU to become 480m:\n%s", got)
					}
				} else if !strings.HasPrefix(string(got), before) || !strings.Contains(string(got), "cpu: 480m") || strings.Contains(string(got), "cpu: 120m") {
					t.Fatalf("expected maximum 480m dev override, not quiet reduction:\n%s", got)
				}
			})
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

func TestSizingInputCLI(t *testing.T) {
	t.Setenv("AZURE_TOKEN_CREDENTIALS", "not-a-credential")
	for _, dry := range []bool{true, false} {
		dir := t.TempDir()
		input, templatePath := filepath.Join(dir, "input.json"), filepath.Join(dir, "sizing.yaml")
		data := strings.NewReplacer(`"namespace": "aro-hcp"`, `"namespace": "ocm-arohcpci01-cluster"`, `"workload": "backend"`, `"workload": "etcd"`, `"container": "aro-hcp-backend"`, `"container": "etcd"`, `"kind": "Deployment"`, `"kind": "StatefulSet"`).Replace(cliInput)
		if err := os.WriteFile(input, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(templatePath, []byte(cliSizingTemplate), 0600); err != nil {
			t.Fatal(err)
		}
		cmd := NewRootCommand()
		args := []string{"--input", input, "--sizing-template", templatePath, "--namespace-prefix", "ocm-arohcpci01-"}
		if dry {
			args = append(args, "--dry-run")
		}
		cmd.SetArgs(args)
		if err := cmd.Execute(); err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(templatePath)
		if err != nil {
			t.Fatal(err)
		}
		want := cliSizingTemplate
		if !dry {
			want = strings.Replace(want, "cpu: 100m", "cpu: 240m", 1)
			if want == cliSizingTemplate {
				t.Fatal("expected etcd CPU to become 240m")
			}
		}
		if string(got) != want {
			t.Fatal("sizing CLI changed unexpected template content")
		}
	}
}

func TestSizingInputFlags(t *testing.T) {
	t.Setenv("AZURE_TOKEN_CREDENTIALS", "not-a-credential")
	for _, extra := range []string{"--config=x", "--source-prefix=defaults", "--write-config=x", "--write-prefix=clouds.dev.defaults", "--commit", "--render-cmd=x", "--namespace-prefix=", "--sizing-template="} {
		cmd := NewRootCommand()
		cmd.SetArgs([]string{"--input=x", "--sizing-template=x", "--namespace-prefix=ocm-arohcpci01-", extra})
		if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "--") || strings.Contains(err.Error(), "credentials") {
			t.Fatalf("expected flag rejection for %s, got %v", extra, err)
		}
	}
	for _, args := range [][]string{
		{"--input=x", "--sizing-template=x"},
		{"--input=x", "--namespace-prefix=ocm-arohcpci01-"},
		{"--grafana-url=https://example.com", "--sizing-template=x", "--namespace-prefix=ocm-arohcpci01-"},
	} {
		cmd := NewRootCommand()
		cmd.SetArgs(args)
		if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "--sizing-template") {
			t.Fatalf("expected paired input flags, got %v", err)
		}
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
				`"peak": 0.239`, `"peak": 0.109`,
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

func TestAdditionalSizingCLI(t *testing.T) {
	t.Setenv("AZURE_TOKEN_CREDENTIALS", "not-a-credential")
	t.Setenv("PATH", t.TempDir())
	for _, scenario := range []string{"apply", "dry run", "bad second"} {
		t.Run(scenario, func(t *testing.T) {
			dir := t.TempDir()
			for _, sub := range []string{"config", "hypershiftoperator/deploy"} {
				if err := os.MkdirAll(filepath.Join(dir, sub), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			config := filepath.Join(dir, "config/config.yaml")
			quiet, busy := filepath.Join(dir, "quiet,input.json"), filepath.Join(dir, "busy.json")
			const before = "defaults:\n  hypershift:\n    additionalMinimalResourceRequests:\n      scheduler:\n        deploymentName: kube-scheduler\n        containerName: kube-scheduler\n        cpu: 100m\n"
			data := strings.NewReplacer(`"namespace": "aro-hcp"`, `"namespace": "ocm-arohcpci01-cluster"`, `"workload": "backend"`, `"workload": "kube-scheduler"`, `"container": "aro-hcp-backend"`, `"container": "kube-scheduler"`).Replace(cliInput)
			busyData := strings.NewReplacer(`"peak": 0.239`, `"peak": 0.479`, `"suggested": 0.24`, `"suggested": 0.48`, `"240m"`, `"480m"`).Replace(data)
			if scenario == "bad second" {
				busyData = strings.Replace(busyData, `"version": 3`, `"version": 1`, 1)
			}
			for path, content := range map[string]string{
				config: before, quiet: data, busy: busyData,
				filepath.Join(dir, "hypershiftoperator/deploy/regular-resource-targets.yaml"): "kube-scheduler: [kube-scheduler]\n",
			} {
				if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			cmd := NewRootCommand()
			args := []string{"--input", quiet, "--input", busy, "--additional-hcp-config", config, "--namespace-prefix=ocm-arohcpci01-"}
			if scenario == "dry run" {
				args = append(args, "--dry-run")
			}
			cmd.SetArgs(args)
			err := cmd.Execute()
			if scenario == "bad second" {
				if err == nil || !strings.Contains(err.Error(), "version") {
					t.Fatalf("expected second report validation failure: %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(config)
			if err != nil {
				t.Fatal(err)
			}
			if scenario != "apply" {
				if string(got) != before {
					t.Fatal("dry run/invalid report changed config")
				}
			} else if !strings.HasPrefix(string(got), before) || !strings.Contains(string(got), "cpu: 480m") || strings.Contains(string(got), "resources:") {
				t.Fatalf("expected flat dev object using max across reports: %s", got)
			}
		})
	}
}

func TestAdditionalSizingCLIFlags(t *testing.T) {
	t.Setenv("AZURE_TOKEN_CREDENTIALS", "not-a-credential")
	for _, flag := range []string{
		"--config=x", "--source-prefix=defaults", "--write-config=x", "--write-prefix=clouds.dev.defaults",
		"--sizing-template=x", "--sizing-template=", "--additional-hcp-config=", "--namespace-prefix=",
		"--commit=false", "--render-cmd=", "--grafana-url=", "--margin=1.2",
	} {
		cmd := NewRootCommand()
		cmd.SetArgs([]string{"--input=not-read.json", "--additional-hcp-config=not-read.yaml", "--namespace-prefix=ocm-arohcpci01-", flag})
		if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "--") || strings.Contains(err.Error(), "credentials") {
			t.Fatalf("expected flag rejection before reads/auth for %s: %v", flag, err)
		}
	}
	for _, args := range [][]string{
		{"--input=x", "--additional-hcp-config=x"},
		{"--grafana-url=https://example.com", "--additional-hcp-config=x", "--namespace-prefix=ocm-arohcpci01-"},
	} {
		cmd := NewRootCommand()
		cmd.SetArgs(args)
		if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "--additional-hcp-config") {
			t.Fatalf("expected paired input flags: %v", err)
		}
	}
}
