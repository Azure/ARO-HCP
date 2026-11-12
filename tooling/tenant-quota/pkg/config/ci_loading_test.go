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

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadCIJobOutcomesOnly(t *testing.T) {
	cfg, err := LoadCIJobOutcomesFromFile("../../ci-outcomes.example.yaml")
	require.NoError(t, err)
	require.Empty(t, cfg.Tenants)
	require.True(t, cfg.CIJobOutcomes.Enabled, "explicit command validates even a disabled collector")
	require.Equal(t, DefaultCIJobOutcomesCacheTTL, cfg.CIJobOutcomes.GetCacheTTL())
	_, err = LoadFromFile("../../ci-outcomes.example.yaml")
	require.ErrorContains(t, err, "at least one tenant")
	data, err := os.ReadFile("../../ci-outcomes.example.yaml")
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "runtime.yaml")
	for _, replacement := range []string{"", "workers: 0", "window: invalid"} {
		content := string(data)
		if replacement == "" {
			content = strings.ReplaceAll(content, "database: ServiceLogs", "database: ''")
		} else {
			content += "  " + replacement + "\n"
		}
		require.NoError(t, os.WriteFile(path, []byte(content), 0600))
		_, err = LoadCIJobOutcomesFromFile(path)
		require.Error(t, err)
	}
	// Other collectors and incomplete tenant entries must not be validated.
	require.NoError(t, os.WriteFile(path, append(data, []byte("tenants:\n- tenantId: unused\nprow:\n  enabled: true\n")...), 0600))
	_, err = LoadCIJobOutcomesFromFile(path)
	require.NoError(t, err)
}
