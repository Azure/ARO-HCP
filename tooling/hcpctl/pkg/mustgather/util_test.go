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
