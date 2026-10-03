// Copyright 2026 Microsoft Corporation
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

package coreapihelpers

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTagsFromBody(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		want      map[string]string
		wantError bool
	}{
		{
			name: "tags omitted",
			body: `{"properties":{"version":{"id":"4.20"}}}`,
		},
		{
			name: "tags populated",
			body: `{"tags":{"apple":"adam","banana":"bob"}}`,
			want: map[string]string{
				"apple":  "adam",
				"banana": "bob",
			},
		},
		{
			name: "tags empty",
			body: `{"tags":{}}`,
			want: map[string]string{},
		},
		{
			name: "tags null",
			body: `{"tags":null}`,
		},
		{
			name: "null tag value is dropped",
			body: `{"tags":{"apple":"adam","banana":null}}`,
			want: map[string]string{
				"apple": "adam",
			},
		},
		{
			name: "all tag values null",
			body: `{"tags":{"banana":null}}`,
			want: map[string]string{},
		},
		{
			name:      "invalid tags",
			body:      `{"tags":"not-an-object"}`,
			wantError: true,
		},
		{
			name:      "invalid body",
			body:      `{"tags":`,
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := TagsFromBody([]byte(tt.body))
			if tt.wantError {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}
