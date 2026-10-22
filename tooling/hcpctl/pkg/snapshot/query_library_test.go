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
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestWriteQueryLibraryCopiesEveryEmbeddedFile ensures the exported library
// is a byte-for-byte copy of every embedded file.
func TestWriteQueryLibraryCopiesEveryEmbeddedFile(t *testing.T) {
	dir := t.TempDir()
	if err := WriteQueryLibrary(dir); err != nil {
		t.Fatalf("WriteQueryLibrary: %v", err)
	}

	var readmes, templates int
	err := fs.WalkDir(queriesFS, queriesRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		switch {
		case d.Name() == "README.md":
			readmes++
		case strings.HasSuffix(d.Name(), ".kql"):
			templates++
		}
		want, err := queriesFS.ReadFile(p)
		if err != nil {
			return err
		}
		got, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(strings.TrimPrefix(p, queriesRoot+"/"))))
		if err != nil {
			t.Errorf("library missing %s: %v", p, err)
			return nil
		}
		if string(got) != string(want) {
			t.Errorf("library copy of %s differs from the embedded file", p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if readmes == 0 || templates == 0 {
		t.Fatalf("expected embedded READMEs and templates, got %d READMEs and %d templates", readmes, templates)
	}
	for _, f := range []string{LibraryOverviewFile, "params.yaml", "guides/resource-identity.md", "frontend/frontendRequests/query.yaml"} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(f))); err != nil {
			t.Errorf("library missing %s: %v", f, err)
		}
	}
}
