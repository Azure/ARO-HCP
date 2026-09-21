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
	"time"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
)

type BackupCadenceProfile string

const (
	BackupCadenceProduction BackupCadenceProfile = "production"
	BackupCadenceTesting    BackupCadenceProfile = "testing"
)

type BackupConfig struct {
	BackupScheduleState  coreapi.BackupScheduleState
	BackupCadenceProfile BackupCadenceProfile
}

// SchedulePaused computes spec.paused for a cluster's Velero Schedules from the
// three levers that govern backup scheduling:
//
//  1. clusterState, the per-cluster admin API pause
//     (ServiceProviderCluster.Spec.BackupScheduleState). Highest precedence: an SRE
//     pause is never defeated by anything below it.
//  2. c.BackupScheduleState, the deployment-wide --backup-schedule-state.
//  3. override, the experimental ARM tag projected onto
//     HCPOpenShiftCluster.ServiceProviderProperties.ExperimentalFeatures.BackupScheduleOverride
//     by admission. Overrides (2) only, so an individual test can opt its cluster
//     into active backups where the deployment keeps schedules off.
//
// paused = cluster == Disabled || (global == Disabled && override != Enabled)
func (c *BackupConfig) SchedulePaused(clusterState, override coreapi.BackupScheduleState) bool {
	if clusterState == coreapi.BackupScheduleStateDisabled {
		return true
	}
	return c.BackupScheduleState == coreapi.BackupScheduleStateDisabled &&
		override != coreapi.BackupScheduleStateEnabled
}

func (c *BackupConfig) Schedules() []BackupScheduleConfig {
	switch c.BackupCadenceProfile {
	case BackupCadenceTesting:
		return []BackupScheduleConfig{
			{Name: "10min", Schedule: "*/10 * * * *", TTL: 1 * time.Hour},
		}
	default:
		return []BackupScheduleConfig{
			{Name: "hourly", Schedule: "0 */1 * * *", TTL: 24 * 2 * time.Hour},
			{Name: "daily", Schedule: "0 2 * * *", TTL: 24 * 30 * time.Hour},
			{Name: "weekly", Schedule: "0 3 * * 0", TTL: 24 * 90 * time.Hour},
		}
	}
}

type BackupScheduleConfig struct {
	Name     string
	Schedule string
	TTL      time.Duration
}
