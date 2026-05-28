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
	"fmt"
	"os"
	"time"

	"sigs.k8s.io/yaml"
)

type BackupConfig struct {
	Schedules []BackupScheduleConfig `json:"schedules"`
}

type BackupScheduleConfig struct {
	Name     string `json:"name"`
	Schedule string `json:"schedule"`
	TTL      string `json:"ttl"`
	Paused   bool   `json:"paused"`
}

func (s *BackupScheduleConfig) TTLDuration() time.Duration {
	d, _ := time.ParseDuration(s.TTL)
	return d
}

func LoadBackupConfig(path string) (*BackupConfig, error) {
	if len(path) == 0 {
		return nil, fmt.Errorf("backup configuration path is required")
	}

	rawBytes, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("error reading file %s: %w", path, err)
	}

	var config BackupConfig
	err = yaml.Unmarshal(rawBytes, &config)
	if err != nil {
		return nil, fmt.Errorf("error unmarshaling file %s: %w", path, err)
	}

	if err := config.validate(); err != nil {
		return nil, fmt.Errorf("error validating backup config %s: %w", path, err)
	}

	return &config, nil
}

func (c *BackupConfig) validate() error {
	if len(c.Schedules) == 0 {
		return fmt.Errorf("at least one schedule is required")
	}

	seen := make(map[string]bool)
	for _, s := range c.Schedules {
		if len(s.Name) == 0 {
			return fmt.Errorf("schedule name is required")
		}
		if seen[s.Name] {
			return fmt.Errorf("duplicate schedule name %q", s.Name)
		}
		seen[s.Name] = true

		if len(s.Schedule) == 0 {
			return fmt.Errorf("schedule %q: cron schedule is required", s.Name)
		}

		if len(s.TTL) == 0 {
			return fmt.Errorf("schedule %q: TTL is required", s.Name)
		}
		if _, err := time.ParseDuration(s.TTL); err != nil {
			return fmt.Errorf("schedule %q: invalid TTL %q: %w", s.Name, s.TTL, err)
		}
	}

	return nil
}
