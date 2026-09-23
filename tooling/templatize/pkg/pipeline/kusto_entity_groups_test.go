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

package pipeline

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Azure/ARO-Tools/pipelines/graph"
	"github.com/Azure/ARO-Tools/pipelines/types"
	"github.com/Azure/ARO-Tools/tools/kustoctl/cmd/entitygroups"
)

func TestKustoEntityGroupsOptions(t *testing.T) {
	defaultTimeout := entitygroups.DefaultSyncOptions().Timeout
	tests := []struct {
		name        string
		timeout     string
		environment string
		wantTimeout time.Duration
		wantError   string
	}{
		{
			name:        "valid timeout",
			timeout:     "10m",
			wantTimeout: 10 * time.Minute,
		},
		{
			name:        "valid timeout seconds",
			timeout:     "30s",
			wantTimeout: 30 * time.Second,
		},
		{
			name:        "empty timeout uses default",
			wantTimeout: defaultTimeout,
		},
		{
			name:        "environment is propagated",
			timeout:     "10m",
			environment: "stg",
			wantTimeout: 10 * time.Minute,
		},
		{
			name:      "invalid timeout",
			timeout:   "notaduration",
			wantError: "failed to parse timeout",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			step := &types.KustoEntityGroupsStep{
				EntityGroups: []string{"TestEG:TestDB"},
				Timeout:      tt.timeout,
				Environment:  tt.environment,
			}

			opts, err := kustoEntityGroupsOptions(step)
			if tt.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantError) {
					t.Fatalf("kustoEntityGroupsOptions() error = %v, want error containing %q", err, tt.wantError)
				}
				return
			}
			if err != nil {
				t.Fatalf("kustoEntityGroupsOptions() error = %v", err)
			}
			if opts.Timeout != tt.wantTimeout {
				t.Errorf("kustoEntityGroupsOptions() timeout = %v, want %v", opts.Timeout, tt.wantTimeout)
			}
			if opts.Environment != tt.environment {
				t.Errorf("kustoEntityGroupsOptions() environment = %q, want %q", opts.Environment, tt.environment)
			}
			if len(opts.EntityGroups) != 1 || opts.EntityGroups[0] != "TestEG:TestDB" {
				t.Errorf("kustoEntityGroupsOptions() entity groups = %v, want [TestEG:TestDB]", opts.EntityGroups)
			}
		})
	}
}

func TestRunStepDispatchesKustoEntityGroupsStep(t *testing.T) {
	step := &types.KustoEntityGroupsStep{
		Timeout: "notaduration",
	}

	_, _, err := RunStep(graph.Identifier{}, step, context.Background(), nil, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "error running Kusto Entity Groups Step: failed to parse timeout") {
		t.Fatalf("RunStep() error = %v, want KustoEntityGroupsStep timeout parse error", err)
	}
}
