// Copyright 2025 Microsoft Corporation
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

package grafana

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-logr/logr"

	"github.com/Azure/ARO-Tools/tools/grafanactl/config"
)

// ValidateAllDashboards reads all configured dashboards from disk and validates them.
// It returns validation errors and warnings. Warnings are logged but do not cause failure.
func ValidateAllDashboards(ctx context.Context, cfg *config.ObservabilityConfig, configDir string) (allErrors []ValidationIssue, allWarnings []ValidationIssue, err error) {
	logger := logr.FromContextOrDiscard(ctx)

	for _, folder := range cfg.GrafanaDashboards.DashboardFolders {
		fullPath := filepath.Join(configDir, folder.Path)
		logger.Info("Validating dashboards", "folder", folder.Name, "path", fullPath)

		entries, err := os.ReadDir(fullPath)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to read directory %q: %w", fullPath, err)
		}

		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}

			filePath := filepath.Join(fullPath, entry.Name())
			dashboard, err := readDashboardFile(filePath)
			if err != nil {
				logger.Error(err, "Failed to read dashboard file", "file", filePath)
				continue
			}

			errors, warnings := validateDashboard(dashboard.meta, folder.Path)
			allErrors = append(allErrors, errors...)
			allWarnings = append(allWarnings, warnings...)
		}
	}

	reportValidationIssues(ctx, allErrors, allWarnings)

	return allErrors, allWarnings, nil
}
