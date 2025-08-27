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
