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

package snapshot

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// queriesRoot is the root of the query library inside the embedded FS.
const queriesRoot = "queries"

// WriteQueryLibrary writes the complete embedded query library to dir,
// preserving its layout: the overview README.md, params.yaml, guides/,
// partials/, and every <component>/<queryName>/ directory with its README.md,
// query.kql, and query.yaml. Investigators read and search these files;
// queries are rendered and executed through a LibrarySession.
func WriteQueryLibrary(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("failed to create query library directory: %w", err)
	}
	err := fs.WalkDir(queriesFS, queriesRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := strings.TrimPrefix(strings.TrimPrefix(p, queriesRoot), "/")
		target := filepath.Join(dir, filepath.FromSlash(rel))
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := queriesFS.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
	if err != nil {
		return fmt.Errorf("failed to write query library: %w", err)
	}
	return nil
}

// LibraryOverview returns the handwritten overview of the query library.
func LibraryOverview() (string, error) {
	data, err := queriesFS.ReadFile(queriesRoot + "/" + LibraryOverviewFile)
	if err != nil {
		return "", fmt.Errorf("failed to read query library overview: %w", err)
	}
	return string(data), nil
}
