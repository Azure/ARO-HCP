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
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Azure/ARO-HCP/tooling/component-updater/internal/config"
	"github.com/Azure/ARO-HCP/tooling/component-updater/internal/updater"
)

func TestCommandPreservesFailureAndReport(t *testing.T) {
	var output bytes.Buffer
	command := newCommand("check", func(context.Context, *config.Config, string, updater.ProviderFactory, []string) ([]updater.ComponentResult, error) {
		if !strings.Contains(output.String(), "Discovering available versions and validating update eligibility (timeout: 2m0s)...") {
			t.Fatalf("discovery must be announced before it runs: %s", &output)
		}
		err := errors.New("discovery failed")
		return []updater.ComponentResult{{Name: "istio", Error: err}}, err
	})
	command.SetOut(&output)
	command.SetErr(&output)
	command.SetArgs([]string{"--config", "../config.yaml", "--subscription-id", "fake-sub"})
	if err := command.Execute(); err == nil || !strings.Contains(output.String(), "discovery failed") {
		t.Fatalf("expected nonzero command result with diagnostic report: %v, %s", err, output.String())
	}
}

func TestCommandDefersAzureUntilProviderFactory(t *testing.T) {
	t.Setenv("AZURE_TOKEN_CREDENTIALS", "")
	var output bytes.Buffer
	command := newCommand("update", func(_ context.Context, _ *config.Config, _ string, _ updater.ProviderFactory, _ []string) ([]updater.ComponentResult, error) {
		return []updater.ComponentResult{{Name: "istio-mesh-revision", Paused: true, Detail: "paused"}}, nil
	})
	command.SetOut(&output)
	command.SetErr(&output)
	command.SetArgs([]string{"--config", "../config.yaml", "--subscription-id", "fake-sub", "--skip-materialize"})
	if err := command.Execute(); err != nil {
		t.Fatalf("paused-only update must not require Azure credentials: %v\n%s", err, output.String())
	}
}

func TestCommandRequiresTokenCredentialsWhenDiscovering(t *testing.T) {
	t.Setenv("AZURE_TOKEN_CREDENTIALS", "")
	var output bytes.Buffer
	command := newCommand("check", func(_ context.Context, _ *config.Config, _ string, factory updater.ProviderFactory, _ []string) ([]updater.ComponentResult, error) {
		_, err := factory("azure-istio")
		return nil, err
	})
	command.SetOut(&output)
	command.SetErr(&output)
	command.SetArgs([]string{"--config", "../config.yaml", "--subscription-id", "fake-sub"})
	if err := command.Execute(); err == nil {
		t.Fatal("discovery must fail closed without AZURE_TOKEN_CREDENTIALS")
	}
}

func TestCommandMaterializesSuccessfulWritesOnPartialFailure(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "updater.yaml")
	targetPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(targetPath, []byte("istio: asm-1-29\naks: '1.35'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte(`
components:
  istio:
    provider: azure-istio
    locations: [all]
    kubernetesVersionPath: aks
    targets:
    - jsonPath: istio
      filePath: config.yaml
`), 0600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	partial := errors.New("other component failed")
	command := newCommand("update", func(context.Context, *config.Config, string, updater.ProviderFactory, []string) ([]updater.ComponentResult, error) {
		return []updater.ComponentResult{
			{Name: "istio", Updated: true, Next: "asm-1-30"},
			{Name: "other", Error: partial},
		}, partial
	})
	command.SetOut(&output)
	command.SetErr(&output)
	command.SetArgs([]string{"--config", configPath, "--subscription-id", "fake-sub", "--skip-materialize"})
	err := command.Execute()
	if err == nil || !strings.Contains(err.Error(), "other component failed") {
		t.Fatalf("partial failure must still be returned: %v", err)
	}
	if !strings.Contains(output.String(), "Run: make") {
		t.Fatalf("successful writes must print materialization recovery instructions: %s", output.String())
	}
}

func TestCommandValidatesInputs(t *testing.T) {
	for _, args := range [][]string{
		{"--config", "../config.yaml", "--component", "typo"},
		{"--timeout", "0s"},
		{"unexpected-positional-argument"},
		{"--materialize"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			command := NewCheckCommand()
			var output bytes.Buffer
			command.SetOut(&output)
			command.SetErr(&output)
			command.SetArgs(args)
			if err := command.Execute(); err == nil {
				t.Fatal("invalid command must fail")
			}
		})
	}
}
