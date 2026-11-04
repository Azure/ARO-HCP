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

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Azure/ARO-Tools/tools/yamlwrap"
)

func TestProcessDirectory(t *testing.T) {
	original := []byte("value: {{ .Value }}\n")
	wrapped, err := yamlwrap.WrapYAML(original, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, wrap := range []bool{true, false} {
		name := "unwrap"
		input, want := wrapped, original
		if wrap {
			name, input, want = "wrap", original, wrapped
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			paths := []string{"owned.yaml", "nested/owned.yml", "vendor/dep/config.yaml", "nested/vendor/dep/config.yml", ".git/config.yaml", ".bingo/config.yml", "ignored.txt"}
			for _, path := range paths {
				full := filepath.Join(dir, path)
				if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(full, input, 0644); err != nil {
					t.Fatal(err)
				}
			}
			// A symlink must not provide a route into vendored sources.
			if err := os.Symlink(filepath.Join(dir, "vendor/dep/config.yaml"), filepath.Join(dir, "linked.yaml")); err != nil {
				t.Fatal(err)
			}
			if err := processDirectory(dir, wrap, true); err != nil {
				t.Fatal(err)
			}
			for i, path := range paths {
				expected := input
				if i < 2 {
					expected = want
				}
				got, err := os.ReadFile(filepath.Join(dir, path))
				if err != nil {
					t.Fatal(err)
				}
				if string(got) != string(expected) {
					t.Errorf("%s: got %q, want %q", path, got, expected)
				}
			}
		})
	}
}
