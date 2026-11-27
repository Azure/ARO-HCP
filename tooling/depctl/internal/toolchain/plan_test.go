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

package toolchain

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadWorkspaceVersion(t *testing.T) {
	for _, test := range []struct {
		name, content, workspace, toolchain string
		wantError                           bool
	}{
		{"patch and toolchain", "go 1.26.7\n\ntoolchain go1.26.10\n", "1.26.7", "go1.26.10", false},
		{"minor only", "go 1.26\n", "1.26", "", false},
		{"malformed", "go not-a-version\n", "", "", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "go.work")
			if err := os.WriteFile(path, []byte(test.content), 0600); err != nil {
				t.Fatal(err)
			}
			checkout, err := os.OpenRoot(root)
			if err != nil {
				t.Fatal(err)
			}
			defer checkout.Close()
			got, err := readWorkspaceVersion(checkout)
			if (err != nil) != test.wantError || got.Go != test.workspace || got.Toolchain != test.toolchain {
				t.Fatalf("readWorkspaceVersion = %+v, %v", got, err)
			}
			after, err := os.ReadFile(path)
			if err != nil || string(after) != test.content {
				t.Fatalf("workspace changed during read: %q, %v", after, err)
			}
		})
	}
}
