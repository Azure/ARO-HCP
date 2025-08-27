package internal

import (
	"io"
	"strings"
)

type Replacements struct {
	From string `json:"from"`
	To   string `json:"to"`
}

type ReplacementWriter struct {
	writer       io.Writer
	replacements []Replacements
}

func NewReplacementWriter(w io.Writer, replacements []Replacements) *ReplacementWriter {
	return &ReplacementWriter{
		writer:       w,
		replacements: replacements,
	}
}

func (rw *ReplacementWriter) Write(p []byte) (n int, err error) {
	content := string(p)

	for _, replacement := range rw.replacements {
		content = strings.ReplaceAll(content, replacement.From, replacement.To)
	}

	bytesWritten, err := rw.writer.Write([]byte(content))
	if err != nil {
		return 0, err
	}

	return bytesWritten, nil
}
