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
	tmpfile.WriteString(content)
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
