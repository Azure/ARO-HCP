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

package istio

import (
	"testing"
)

func TestDecide(t *testing.T) {
	tests := []struct {
		name       string
		state      ClusterState
		target     string
		wantAction Action
	}{
		{
			name: "already at target",
			state: ClusterState{
				Name:              "svc-cluster-01",
				Revisions:         []string{"asm-1-28"},
				AvailableUpgrades: []string{"asm-1-29"},
				ProvisioningState: "Succeeded",
			},
			target:     "asm-1-28",
			wantAction: ActionSkip,
		},
		{
			name: "upgrade available",
			state: ClusterState{
				Name:              "svc-cluster-01",
				Revisions:         []string{"asm-1-28"},
				AvailableUpgrades: []string{"asm-1-29"},
				ProvisioningState: "Succeeded",
			},
			target:     "asm-1-29",
			wantAction: ActionUpgrade,
		},
		{
			name: "mid-upgrade resume",
			state: ClusterState{
				Name:              "svc-cluster-01",
				Revisions:         []string{"asm-1-28", "asm-1-29"},
				ProvisioningState: "Succeeded",
			},
			target:     "asm-1-29",
			wantAction: ActionResume,
		},
		{
			name: "target not available in region",
			state: ClusterState{
				Name:              "svc-cluster-01",
				Revisions:         []string{"asm-1-27"},
				AvailableUpgrades: []string{"asm-1-28"},
				KubernetesVersion: "1.35",
				ProvisioningState: "Succeeded",
			},
			target:     "asm-1-29",
			wantAction: ActionSkip,
		},
		{
			name: "downgrade detected",
			state: ClusterState{
				Name:              "svc-cluster-01",
				Revisions:         []string{"asm-1-29"},
				ProvisioningState: "Succeeded",
			},
			target:     "asm-1-28",
			wantAction: ActionSkip,
		},
		{
			name: "downgrade detected with multiple revisions",
			state: ClusterState{
				Name:              "svc-cluster-01",
				Revisions:         []string{"asm-1-27", "asm-1-30"},
				ProvisioningState: "Succeeded",
			},
			target:     "asm-1-29",
			wantAction: ActionSkip,
		},
		{
			name: "cluster in Failed state",
			state: ClusterState{
				Name:              "svc-cluster-01",
				Revisions:         []string{"asm-1-28"},
				AvailableUpgrades: []string{"asm-1-29"},
				ProvisioningState: "Failed",
			},
			target:     "asm-1-29",
			wantAction: ActionSkip,
		},
		{
			name: "new cluster install",
			state: ClusterState{
				Name:              "svc-cluster-01",
				Revisions:         []string{},
				ProvisioningState: "Succeeded",
			},
			target:     "asm-1-28",
			wantAction: ActionInstall,
		},
		{
			name: "new cluster nil revisions",
			state: ClusterState{
				Name:              "svc-cluster-01",
				Revisions:         nil,
				ProvisioningState: "Succeeded",
			},
			target:     "asm-1-28",
			wantAction: ActionInstall,
		},
		{
			name: "new cluster in Failed state skips install",
			state: ClusterState{
				Name:              "svc-cluster-01",
				Revisions:         nil,
				ProvisioningState: "Failed",
			},
			target:     "asm-1-28",
			wantAction: ActionSkip,
		},
		{
			name: "ARM default matches config — no action needed",
			state: ClusterState{
				Name:              "svc-cluster-01",
				Revisions:         []string{"asm-1-28"},
				AvailableUpgrades: []string{"asm-1-29"},
				ProvisioningState: "Succeeded",
			},
			target:     "asm-1-28",
			wantAction: ActionSkip,
		},
		{
			name: "ARM default behind config — upgrade to reach target",
			state: ClusterState{
				Name:              "svc-cluster-01",
				Revisions:         []string{"asm-1-28"},
				AvailableUpgrades: []string{"asm-1-29"},
				ProvisioningState: "Succeeded",
			},
			target:     "asm-1-29",
			wantAction: ActionUpgrade,
		},
		{
			name: "ARM default ahead of config — downgrade skip",
			state: ClusterState{
				Name:              "svc-cluster-01",
				Revisions:         []string{"asm-1-29"},
				AvailableUpgrades: []string{},
				ProvisioningState: "Succeeded",
			},
			target:     "asm-1-28",
			wantAction: ActionSkip,
		},
		{
			name: "upgrade in progress from 409",
			state: ClusterState{
				Name:              "svc-cluster-01",
				Revisions:         []string{"asm-1-28"},
				ProvisioningState: "Succeeded",
				UpgradeInProgress: true,
			},
			target:     "asm-1-29",
			wantAction: ActionResume,
		},
		{
			name: "stale canary triggers cleanup and upgrade",
			state: ClusterState{
				Name:              "svc-cluster-01",
				Revisions:         []string{"asm-1-28", "asm-1-29"},
				ProvisioningState: "Succeeded",
			},
			target:     "asm-1-30",
			wantAction: ActionCleanupAndUpgrade,
		},
		{
			name: "three or more revisions skips with manual intervention",
			state: ClusterState{
				Name:              "svc-cluster-01",
				Revisions:         []string{"asm-1-27", "asm-1-28", "asm-1-29"},
				ProvisioningState: "Succeeded",
			},
			target:     "asm-1-30",
			wantAction: ActionSkip,
		},
		{
			name: "single digit minor not treated as newer",
			state: ClusterState{
				Name:              "svc-cluster-01",
				Revisions:         []string{"asm-1-9"},
				AvailableUpgrades: []string{"asm-1-28"},
				ProvisioningState: "Succeeded",
			},
			target:     "asm-1-28",
			wantAction: ActionUpgrade,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := Decide(tt.state, tt.target)
			if d.Action != tt.wantAction {
				t.Errorf("Decide() action = %s, want %s (reason: %s)", d.Action, tt.wantAction, d.Reason)
			}
		})
	}
}

func TestCompareRevisions(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"asm-1-28", "asm-1-28", 0},
		{"asm-1-29", "asm-1-28", 1},
		{"asm-1-28", "asm-1-29", -1},
		{"asm-1-9", "asm-1-28", -1},
		{"asm-1-28", "asm-1-9", 1},
		{"asm-2-1", "asm-1-99", 1},
	}
	for _, tt := range tests {
		got := compareRevisions(tt.a, tt.b)
		if (tt.want < 0 && got >= 0) || (tt.want > 0 && got <= 0) || (tt.want == 0 && got != 0) {
			t.Errorf("compareRevisions(%q, %q) = %d, want sign %d", tt.a, tt.b, got, tt.want)
		}
	}
}
