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

package verifiers

import (
	"strings"
	"testing"

	configv1 "github.com/openshift/api/config/v1"
)

func TestClusterVersionRolloutCompleted(t *testing.T) {
	const version = "5.1.0-0.nightly-multi-2026-10-22-071948"
	failing := configv1.ClusterOperatorStatusCondition{
		Type:    clusterVersionFailing,
		Status:  configv1.ConditionTrue,
		Message: "Cluster operators console, insights, monitoring are not available",
	}
	progressing := configv1.ClusterOperatorStatusCondition{
		Type:    configv1.OperatorProgressing,
		Status:  configv1.ConditionTrue,
		Message: "Unable to apply " + version + ": some cluster operators are not available",
	}

	tests := []struct {
		name           string
		history        []configv1.UpdateHistory
		conditions     []configv1.ClusterOperatorStatusCondition
		wantErr        bool
		wantSubstrings []string
	}{
		{
			name:           "no history yet",
			wantErr:        true,
			wantSubstrings: []string{"no status.history entries yet"},
		},
		{
			name:       "install still partial reports the operators being waited on",
			history:    []configv1.UpdateHistory{{Version: version, State: configv1.PartialUpdate}},
			conditions: []configv1.ClusterOperatorStatusCondition{failing, progressing},
			wantErr:    true,
			wantSubstrings: []string{
				version + `="Partial"`,
				"Failing=True: Cluster operators console, insights, monitoring are not available",
				"Progressing=True: Unable to apply " + version,
			},
		},
		{
			name:           "entry without a state is not completed",
			history:        []configv1.UpdateHistory{{Version: version}},
			wantErr:        true,
			wantSubstrings: []string{version + `=""`},
		},
		{
			name:    "install completed",
			history: []configv1.UpdateHistory{{Version: version, State: configv1.CompletedUpdate}},
		},
		{
			name: "every entry must be completed",
			history: []configv1.UpdateHistory{
				{Version: version, State: configv1.CompletedUpdate},
				{Version: "5.1.0-0.nightly-multi-2026-10-21-105434", State: configv1.PartialUpdate},
			},
			wantErr:        true,
			wantSubstrings: []string{`5.1.0-0.nightly-multi-2026-10-21-105434="Partial"`},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clusterVersion := &configv1.ClusterVersion{
				Status: configv1.ClusterVersionStatus{
					History:    tt.history,
					Conditions: tt.conditions,
				},
			}
			err := clusterVersionRolloutCompleted(clusterVersion)
			if !tt.wantErr {
				if err != nil {
					t.Fatalf("expected no error, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected an error, got nil")
			}
			for _, want := range tt.wantSubstrings {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not contain %q", err.Error(), want)
				}
			}
		})
	}
}
