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

package prow

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeForDedupUUIDs(t *testing.T) {
	msg := "resource 550e8400-e29b-41d4-a716-446655440000 not found"
	result := NormalizeForDedup(msg)
	assert.Contains(t, result, "<uuid>")
	assert.NotContains(t, result, "550e8400")
}

func TestNormalizeForDedupResourceGroups(t *testing.T) {
	msg := "failed in rg-hcp-dev-westus2-abc123"
	result := NormalizeForDedup(msg)
	assert.Contains(t, result, "<rg>")
	assert.NotContains(t, result, "rg-hcp-dev")
}

func TestNormalizeForDedupTimestamps(t *testing.T) {
	msg := "failed at 2025-04-01T12:30:00Z with error"
	result := NormalizeForDedup(msg)
	assert.Contains(t, result, "<timestamp>")
	assert.NotContains(t, result, "2025-04-01")
}

func TestNormalizeForDedupGoFileLine(t *testing.T) {
	msg := "error at handler.go:174"
	result := NormalizeForDedup(msg)
	assert.Contains(t, result, ".go:<line>")
}

func TestNormalizeForDedupLargeNumbers(t *testing.T) {
	msg := "build 1900000000000000000 failed"
	result := NormalizeForDedup(msg)
	assert.Contains(t, result, "<num>")
}

func TestDedupMessagesBasic(t *testing.T) {
	messages := []string{
		"error in rg-hcp-dev-abc123: timeout",
		"error in rg-hcp-dev-xyz789: timeout",
		"different error entirely",
	}
	result := DedupMessages(messages)
	require.Len(t, result, 2)
	// First entry should be the one with count 2
	assert.Equal(t, 2, result[0].Count)
	assert.Equal(t, 1, result[1].Count)
	// Verbatim should be the first occurrence
	assert.Equal(t, "error in rg-hcp-dev-abc123: timeout", result[0].Msg)
}

func TestDedupMessagesEmpty(t *testing.T) {
	assert.Nil(t, DedupMessages(nil))
	assert.Nil(t, DedupMessages([]string{}))
}

func TestDedupMessagesSingleMessage(t *testing.T) {
	result := DedupMessages([]string{"one error"})
	require.Len(t, result, 1)
	assert.Equal(t, "one error", result[0].Msg)
	assert.Equal(t, 1, result[0].Count)
}

func TestDedupMessagesUUIDs(t *testing.T) {
	messages := []string{
		"cluster 550e8400-e29b-41d4-a716-446655440000 failed",
		"cluster aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee failed",
	}
	result := DedupMessages(messages)
	require.Len(t, result, 1)
	assert.Equal(t, 2, result[0].Count)
}
