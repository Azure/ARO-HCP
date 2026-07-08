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

package ocm

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUpdateDispatchConfigCanonicalJSONAndHash(t *testing.T) {
	type fieldOrderProbe struct {
		Zebra string `json:"zebra"`
		Alpha string `json:"alpha"`
	}

	type nestedInner struct {
		Z string `json:"z"`
		A string `json:"a"`
	}

	type nestedProbe struct {
		Zebra nestedInner `json:"zebra"`
		Alpha nestedInner `json:"alpha"`
	}

	type omitProbe struct {
		Zebra string `json:"zebra,omitempty"`
		Alpha string `json:"alpha"`
	}

	tests := []struct {
		name  string
		input func(t *testing.T) any
		want  string
	}{
		{
			name: "sorts top-level struct keys",
			input: func(t *testing.T) any {
				return fieldOrderProbe{Zebra: "z", Alpha: "a"}
			},
			want: `{"alpha":"a","zebra":"z"}`,
		},
		{
			name: "sorts nested object keys too",
			input: func(t *testing.T) any {
				return nestedProbe{
					Zebra: nestedInner{Z: "z", A: "a"},
					Alpha: nestedInner{A: "1", Z: "2"},
				}
			},
			want: `{"alpha":{"a":"1","z":"2"},"zebra":{"a":"a","z":"z"}}`,
		},
		{
			name: "honors omitempty before sorting",
			input: func(t *testing.T) any {
				return omitProbe{Alpha: "a"}
			},
			want: `{"alpha":"a"}`,
		},
		{
			name: "JSON fixture loaded into struct",
			input: func(t *testing.T) any {
				var probe fieldOrderProbe
				require.NoError(t, json.Unmarshal([]byte(`{"zebra":"from-json","alpha":"fixture"}`), &probe))
				return probe
			},
			want: `{"alpha":"fixture","zebra":"from-json"}`,
		},
		{
			name: "map input round-trips with sorted keys",
			input: func(t *testing.T) any {
				return map[string]any{
					"zebra": "from-map",
					"alpha": "input",
				}
			},
			want: `{"alpha":"input","zebra":"from-map"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := tt.input(t)

			got, err := canonicalJSONForUpdateDispatchConfig(input)
			require.NoError(t, err)
			assert.Equal(t, tt.want, string(got))

			hash, err := hashUpdateDispatchConfig(input)
			require.NoError(t, err)
			require.NotEmpty(t, hash)

			hashAgain, err := hashUpdateDispatchConfig(input)
			require.NoError(t, err)
			assert.Equal(t, hash, hashAgain)
		})
	}

	t.Run("different values produce different hash", func(t *testing.T) {
		hashA, err := hashUpdateDispatchConfig(fieldOrderProbe{Zebra: "z", Alpha: "a"})
		require.NoError(t, err)
		hashB, err := hashUpdateDispatchConfig(fieldOrderProbe{Zebra: "z", Alpha: "b"})
		require.NoError(t, err)
		assert.NotEqual(t, hashA, hashB)
	})
}
