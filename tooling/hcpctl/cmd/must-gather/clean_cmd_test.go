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
	"context"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
)

func createTempData(t *testing.T) string {
	tempDir, err := os.MkdirTemp("", "must-gather-clean-test-*")
	if err != nil {
		t.Fatalf("failed to create temp directory: %v", err)
	}
	return tempDir
}

func TestWalkAndMatchRegexPatterns(t *testing.T) {
	tempDir := createTempData(t)
	defer os.RemoveAll(tempDir)

	err := os.WriteFile(filepath.Join(tempDir, "test.txt"), []byte("123e4567-e89b-12d3-a456-426614174000"), 0644)
	assert.NoError(t, err)

	patterns := []*replacement{
		{
			Regex:              regexp.MustCompile("([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})"),
			ReplacementPattern: "x-uid-%010d",
		},
	}
	allMatches, err := walkAndMatchRegexPatterns(context.Background(), tempDir, patterns)
	assert.NoError(t, err)
	assert.Equal(t, 1, len(allMatches))
}

func TestGenerateMustGatherCleanConfig(t *testing.T) {
	tempDir := createTempData(t)
	defer os.RemoveAll(tempDir)

	opts := &CleanOptions{
		WorkingDir: tempDir,
	}
	opts.ValidatedCleanOptions = &ValidatedCleanOptions{
		RawCleanOptions: &RawCleanOptions{
			ServiceConfigPath: tempDir,
		},
	}
	generatedConfigPath, err := generateMustGatherCleanConfig(context.Background(), opts)
	assert.NoError(t, err)
	assert.NotEmpty(t, generatedConfigPath)

	content, err := os.ReadFile(generatedConfigPath)
	assert.NoError(t, err)
	assert.Equal(t, string(content), defaultConfig)
}
