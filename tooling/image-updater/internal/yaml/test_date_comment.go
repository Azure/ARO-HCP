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

package yaml

import (
	"os"
	"testing"
)

func TestCommentWithDate(t *testing.T) {
	content := `defaults:
  pko:
    imagePackage:
      digest: sha256:olddigest123
`
	tmpfile, _ := os.CreateTemp("", "test-*.yaml")
	if _, err := tmpfile.WriteString(content); err != nil {
		t.Fatalf("WriteString() failed: %v", err)
	}
	tmpfile.Close()
	defer os.Remove(tmpfile.Name())

	editor, _ := NewEditor(tmpfile.Name())

	updates := []Update{
		{
			Line:      4,
			OldDigest: "sha256:olddigest123",
			NewDigest: "sha256:newdigest456",
			Tag:       "v1.18.4",
			Date:      "2025-11-24 14:30",
		},
	}

	if err := editor.ApplyUpdates(updates); err != nil {
		t.Fatalf("ApplyUpdates() failed: %v", err)
	}

	result, _ := os.ReadFile(tmpfile.Name())
	expected := `defaults:
  pko:
    imagePackage:
      digest: sha256:newdigest456 # v1.18.4 (2025-11-24 14:30)
`
	if string(result) != expected {
		t.Errorf("Expected:\n%s\nGot:\n%s", expected, string(result))
	}
}
