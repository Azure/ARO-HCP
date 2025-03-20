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
