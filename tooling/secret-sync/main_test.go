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

package main

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"testing"

	"gotest.tools/v3/assert"
)

func TestReadAndChunkData(t *testing.T) {
	testMsg := "test"
	data, err := readAndChunkData(strings.NewReader(testMsg))
	assert.NilError(t, err)

	assert.Equal(t, string(data[0]), testMsg)

	testBytes := make([]byte, 800)
	for i := range testBytes {
		testBytes[i] = byte('a')
	}
	data, err = readAndChunkData(bytes.NewReader(testBytes))
	assert.NilError(t, err)

	assert.Equal(t, string(bytes.Join(data, []byte{})), string(testBytes))
	assert.Equal(t, len(data), 2)
}

func TestPersistEncryptedChunk(t *testing.T) {
	tempdir := t.TempDir()
	outputFile := fmt.Sprintf("%s/output", tempdir)
	os.Setenv(outputFileEnvKey, outputFile)

	testData := make([][]byte, 1)
	testData[0] = []byte{'a'}
	err := persistEncryptedChunks(testData)
	assert.NilError(t, err)

	data, err := os.ReadFile(outputFile)
	assert.NilError(t, err)

	assert.Equal(t, string(data), fmt.Sprintf("YQ==%s", chunkDelemiter))
}
