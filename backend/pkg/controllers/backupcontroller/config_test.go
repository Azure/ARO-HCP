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
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadBackupConfig(t *testing.T) {
	tests := []struct {
		name        string
		content     string
		expectError bool
		errContains string
		validate    func(t *testing.T, cfg *BackupConfig)
	}{
		{
			name: "valid config with three schedules",
			content: `
schedules:
- name: hourly
  schedule: "0 */1 * * *"
  ttl: "48h"
- name: daily
  schedule: "0 2 * * *"
  ttl: "336h"
- name: weekly
  schedule: "0 3 * * 0"
  ttl: "2160h"
`,
			validate: func(t *testing.T, cfg *BackupConfig) {
				require.Len(t, cfg.Schedules, 3)
				assert.Equal(t, "hourly", cfg.Schedules[0].Name)
				assert.Equal(t, "0 */1 * * *", cfg.Schedules[0].Schedule)
				assert.Equal(t, "48h", cfg.Schedules[0].TTL)
				assert.Equal(t, "daily", cfg.Schedules[1].Name)
				assert.Equal(t, "weekly", cfg.Schedules[2].Name)
			},
		},
		{
			name:        "empty schedules",
			content:     `schedules: []`,
			expectError: true,
			errContains: "at least one schedule is required",
		},
		{
			name: "missing schedule name",
			content: `
schedules:
- schedule: "0 */1 * * *"
  ttl: "48h"
`,
			expectError: true,
			errContains: "schedule name is required",
		},
		{
			name: "duplicate schedule names",
			content: `
schedules:
- name: hourly
  schedule: "0 */1 * * *"
  ttl: "48h"
- name: hourly
  schedule: "0 2 * * *"
  ttl: "336h"
`,
			expectError: true,
			errContains: "duplicate schedule name",
		},
		{
			name: "missing cron schedule",
			content: `
schedules:
- name: hourly
  ttl: "48h"
`,
			expectError: true,
			errContains: "cron schedule is required",
		},
		{
			name: "missing TTL",
			content: `
schedules:
- name: hourly
  schedule: "0 */1 * * *"
`,
			expectError: true,
			errContains: "TTL is required",
		},
		{
			name: "invalid TTL format",
			content: `
schedules:
- name: hourly
  schedule: "0 */1 * * *"
  ttl: "not-a-duration"
`,
			expectError: true,
			errContains: "invalid TTL",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			configPath := filepath.Join(tmpDir, "config.yaml")
			require.NoError(t, os.WriteFile(configPath, []byte(tt.content), 0644))

			cfg, err := LoadBackupConfig(configPath)
			if tt.expectError {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errContains)
			} else {
				require.NoError(t, err)
				if tt.validate != nil {
					tt.validate(t, cfg)
				}
			}
		})
	}
}

func TestLoadBackupConfig_EmptyPath(t *testing.T) {
	_, err := LoadBackupConfig("")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "path is required")
}

func TestLoadBackupConfig_NonexistentFile(t *testing.T) {
	_, err := LoadBackupConfig("/nonexistent/path/config.yaml")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "error reading file")
}
