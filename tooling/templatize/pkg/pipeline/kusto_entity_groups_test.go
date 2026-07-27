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
	"testing"

	"github.com/Azure/ARO-Tools/pipelines/graph"
	"github.com/Azure/ARO-Tools/pipelines/types"
)

func TestRunKustoEntityGroupsStep_TimeoutParse(t *testing.T) {
	tests := []struct {
		name      string
		timeout   string
		wantError bool
		errorMsg  string
	}{
		{
			name:      "valid timeout",
			timeout:   "10m",
			wantError: false,
		},
		{
			name:      "valid timeout seconds",
			timeout:   "30s",
			wantError: false,
		},
		{
			name:      "empty timeout uses default",
			timeout:   "",
			wantError: false,
		},
		{
			name:      "invalid timeout",
			timeout:   "notaduration",
			wantError: true,
			errorMsg:  "failed to parse timeout",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			step := &types.KustoEntityGroupsStep{
				EntityGroups: []string{"TestEG:TestDB"},
				Timeout:      tt.timeout,
			}

			// runKustoEntityGroupsStep will fail at opts.Run (no Azure creds)
			// but we're testing the timeout parse path before that
			err := runKustoEntityGroupsStep(graph.Identifier{}, step, context.Background())

			if tt.wantError {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.errorMsg)
				}
				if tt.errorMsg != "" && !containsStr(err.Error(), tt.errorMsg) {
					t.Fatalf("expected error containing %q, got %q", tt.errorMsg, err.Error())
				}
			}
			// For valid timeouts, the function will fail later (no Azure creds),
			// which is fine - we only care that it didn't fail on timeout parse
			if !tt.wantError && err != nil && containsStr(err.Error(), "failed to parse timeout") {
				t.Fatalf("unexpected timeout parse error: %v", err)
			}
		})
	}
}

func containsStr(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && findSubstr(s, substr))
}

func findSubstr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
