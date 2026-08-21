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

func (c *BackupConfig) Schedules() []BackupScheduleConfig {
	switch c.BackupCadenceProfile {
	case BackupCadenceTesting:
		return []BackupScheduleConfig{
			{Name: "10min", Schedule: "*/10 * * * *", TTL: 1 * time.Hour},
		}
	default:
		return []BackupScheduleConfig{
			{Name: "hourly", Schedule: "0 */1 * * *", TTL: 24 * 7 * time.Hour},
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
