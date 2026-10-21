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

// Package testutil holds shared helpers for swift-recorder tests.
package testutil

import (
	"path/filepath"
	"testing"
)

// ResolvedTempDir returns a t.TempDir() with its symlinks resolved, matching the
// dir the recorder stores after filepath.EvalSymlinks. On macOS TMPDIR lives
// under /var (a symlink to /private/var), so the raw and resolved dirs differ;
// on linux /tmp is not symlinked and EvalSymlinks is the identity. Deriving
// expected paths from the resolved dir keeps test assertions portable across
// both platforms.
func ResolvedTempDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}
