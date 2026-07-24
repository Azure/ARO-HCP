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

package backupcontroller

import (
	"time"
)

type BackupCadence string

const (
	BackupCadenceProduction BackupCadence = "production"
	BackupCadenceTesting    BackupCadence = "testing"
)

type BackupConfig struct {
	Paused  bool          `json:"paused"`
	Cadence BackupCadence `json:"cadence"`
}

func (c *BackupConfig) Schedules() []BackupScheduleConfig {
	switch c.Cadence {
	case BackupCadenceTesting:
		return []BackupScheduleConfig{
			{Name: "5min", Schedule: "*/5 * * * *", TTL: 1 * time.Hour},
		}
	default:
		return []BackupScheduleConfig{
			{Name: "hourly", Schedule: "0 */1 * * *", TTL: 48 * time.Hour},
			{Name: "daily", Schedule: "0 2 * * *", TTL: 336 * time.Hour},
			{Name: "weekly", Schedule: "0 3 * * 0", TTL: 2160 * time.Hour},
		}
	}
}

type BackupScheduleConfig struct {
	Name     string
	Schedule string
	TTL      time.Duration
}
