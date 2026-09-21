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

package backups

import (
	"testing"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
)

func TestBackupConfig_SchedulePaused(t *testing.T) {
	for _, tt := range []struct {
		name         string
		globalState  coreapi.BackupScheduleState
		clusterState coreapi.BackupScheduleState
		override     coreapi.BackupScheduleState
		want         bool
	}{
		{
			name:         "everything enabled, no override",
			globalState:  coreapi.BackupScheduleStateEnabled,
			clusterState: coreapi.BackupScheduleStateEnabled,
			want:         false,
		},
		{
			name:         "deployment disabled, no override",
			globalState:  coreapi.BackupScheduleStateDisabled,
			clusterState: coreapi.BackupScheduleStateEnabled,
			want:         true,
		},
		{
			name:         "admin API pause, no override",
			globalState:  coreapi.BackupScheduleStateEnabled,
			clusterState: coreapi.BackupScheduleStateDisabled,
			want:         true,
		},
		{
			name:         "both disabled, no override",
			globalState:  coreapi.BackupScheduleStateDisabled,
			clusterState: coreapi.BackupScheduleStateDisabled,
			want:         true,
		},
		{
			name:         "override is a no-op where the deployment already runs schedules",
			globalState:  coreapi.BackupScheduleStateEnabled,
			clusterState: coreapi.BackupScheduleStateEnabled,
			override:     coreapi.BackupScheduleStateEnabled,
			want:         false,
		},
		{
			name:         "override lifts the deployment-wide pause",
			globalState:  coreapi.BackupScheduleStateDisabled,
			clusterState: coreapi.BackupScheduleStateEnabled,
			override:     coreapi.BackupScheduleStateEnabled,
			want:         false,
		},
		{
			name:         "admin API pause outranks the override",
			globalState:  coreapi.BackupScheduleStateEnabled,
			clusterState: coreapi.BackupScheduleStateDisabled,
			override:     coreapi.BackupScheduleStateEnabled,
			want:         true,
		},
		{
			name:         "admin API pause outranks the override with the deployment disabled too",
			globalState:  coreapi.BackupScheduleStateDisabled,
			clusterState: coreapi.BackupScheduleStateDisabled,
			override:     coreapi.BackupScheduleStateEnabled,
			want:         true,
		},
		{
			name: "all zero values",
			want: false,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := &BackupConfig{BackupScheduleState: tt.globalState}
			if got := c.SchedulePaused(tt.clusterState, tt.override); got != tt.want {
				t.Errorf("SchedulePaused(%q, %q) with global %q = %v, want %v", tt.clusterState, tt.override, tt.globalState, got, tt.want)
			}
		})
	}
}
