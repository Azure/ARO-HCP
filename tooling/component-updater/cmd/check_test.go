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
