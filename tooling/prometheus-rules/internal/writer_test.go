// Copyright 2025 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package internal

import (
	"bytes"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestReplacementWriter(t *testing.T) {
	writer := NewReplacementWriter(os.Stdout, []Replacements{
		{From: "fooo", To: "bar"},
	})

	bytesWritten, err := writer.Write([]byte("fooo"))
	assert.NoError(t, err)
	assert.Equal(t, 3, bytesWritten)
}

func TestReplacementWriter_MultipleReplacements(t *testing.T) {
	writer := NewReplacementWriter(os.Stdout, []Replacements{
		{From: "fooo", To: "bar"},
		{From: "bar", To: "baz"},
	})

	bytesWritten, err := writer.Write([]byte("fooo"))
	assert.NoError(t, err)
	assert.Equal(t, 3, bytesWritten)
}

func TestReplacementWriter_CheckContent(t *testing.T) {
	buf := bytes.NewBuffer(nil)

	writer := NewReplacementWriter(buf, []Replacements{
		{From: "fooo", To: "bar"},
	})

	_, err := writer.Write([]byte("fooo"))
	assert.NoError(t, err)

	content := buf.String()
	assert.Equal(t, "bar", content)
}
