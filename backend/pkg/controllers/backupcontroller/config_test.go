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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBackupConfig_Schedules(t *testing.T) {
	tests := []struct {
		name        string
		cadence     BackupCadence
		expectCount int
		expectNames []string
		expectCrons []string
		expectTTLs  []time.Duration
	}{
		{
			name:        "production cadence returns three schedules",
			cadence:     BackupCadenceProduction,
			expectCount: 3,
			expectNames: []string{"hourly", "daily", "weekly"},
			expectCrons: []string{"0 */1 * * *", "0 2 * * *", "0 3 * * 0"},
			expectTTLs:  []time.Duration{48 * time.Hour, 336 * time.Hour, 2160 * time.Hour},
		},
		{
			name:        "testing cadence returns one schedule",
			cadence:     BackupCadenceTesting,
			expectCount: 1,
			expectNames: []string{"5min"},
			expectCrons: []string{"*/5 * * * *"},
			expectTTLs:  []time.Duration{1 * time.Hour},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &BackupConfig{Cadence: tt.cadence}
			schedules := cfg.Schedules()

			require.Len(t, schedules, tt.expectCount)
			for i, s := range schedules {
				assert.Equal(t, tt.expectNames[i], s.Name)
				assert.Equal(t, tt.expectCrons[i], s.Schedule)
				assert.Equal(t, tt.expectTTLs[i], s.TTL)
			}
		})
	}
}
