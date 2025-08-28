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

var _ io.Writer = &ReplacementWriter{}
