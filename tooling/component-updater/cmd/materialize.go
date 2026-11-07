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

package cmd

import (
	"context"
	"fmt"
	"io"
	"path/filepath"

	"github.com/Azure/ARO-HCP/tooling/component-updater/internal/config"
	"github.com/Azure/ARO-HCP/tooling/component-updater/internal/updater"
)

func materializeUpdates(ctx context.Context, cfg *config.Config, configDir string, results []updater.ComponentResult, enabled bool, run func(context.Context, string) error, output io.Writer) error {
	seen := map[string]bool{}
	for _, result := range results {
		if !result.Updated || result.Error != nil {
			continue
		}
		file := cfg.Components[result.Name].Targets[0].FilePath
		if !filepath.IsAbs(file) {
			file = filepath.Join(configDir, file)
		}
		directory := filepath.Dir(file)
		if seen[directory] {
			continue
		}
		seen[directory] = true
		if !enabled {
			if _, err := fmt.Fprintf(output, "Source configuration updated. Run: make -C %q materialize\n", directory); err != nil {
				return err
			}
			continue
		}
		if err := run(ctx, directory); err != nil {
			return fmt.Errorf("source configuration was updated, but materialization failed; rerun make -C %q materialize: %w", directory, err)
		}
		if _, err := fmt.Fprintln(output, "Materialization completed."); err != nil {
			return err
		}
	}
	if enabled && len(seen) == 0 {
		_, err := fmt.Fprintln(output, "Materialization skipped: no source configuration changes.")
		return err
	}
	return nil
}
