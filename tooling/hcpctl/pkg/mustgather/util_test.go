package mustgather

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestJsonEncoderWriter_WriteFile(t *testing.T) {
	t.Run("successful write", func(t *testing.T) {
		writer := &JsonEncoderWriter{}

		// Create a temporary directory for testing
		tmpDir := t.TempDir()

		testData := map[string]interface{}{
			"test":   "data",
			"number": 123,
		}

		err := writer.WriteFile(tmpDir, "test.json", testData)

		assert.NoError(t, err)

		// Verify file exists and contains expected content
		// We could add file reading verification here if needed
	})

	t.Run("invalid output path", func(t *testing.T) {
		writer := &JsonEncoderWriter{}

		// Use an invalid path that should fail
		invalidPath := "/nonexistent/path/that/should/not/exist"

		testData := map[string]interface{}{
			"test": "data",
		}

		err := writer.WriteFile(invalidPath, "test.json", testData)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to create output file")
	})
}
