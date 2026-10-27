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

package coreapi

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
)

func TestNodePoolPropertiesTaintsSurviveJSONRoundTrip(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		// A nil value here is meaningful: it is the "taints not specified" case.
		taints        []Taint
		wantKeyInJSON bool
		wantJSONValue any
	}{
		{
			name:          "nil taints are omitted so Cluster Service is left alone",
			taints:        nil,
			wantKeyInJSON: false,
		},
		{
			name:          "explicitly empty taints are stored as an empty list",
			taints:        []Taint{},
			wantKeyInJSON: true,
			wantJSONValue: []any{},
		},
		{
			name: "populated taints are stored as a populated list",
			taints: []Taint{{
				Effect: metadataapi.EffectNoSchedule,
				Key:    "key1",
				Value:  "value1",
			}},
			wantKeyInJSON: true,
			wantJSONValue: []any{map[string]any{"effect": "NoSchedule", "key": "key1", "value": "value1"}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			raw, err := json.Marshal(NodePoolProperties{Taints: tc.taints})
			require.NoError(t, err)

			var asMap map[string]any
			require.NoError(t, json.Unmarshal(raw, &asMap))

			gotValue, gotKey := asMap["taints"]
			require.Equalf(t, tc.wantKeyInJSON, gotKey,
				"unexpected presence of the taints key in %s", string(raw))
			if tc.wantKeyInJSON {
				assert.Equal(t, tc.wantJSONValue, gotValue)
			}

			// The round trip is what the dispatch controller actually observes.
			var decoded NodePoolProperties
			require.NoError(t, json.Unmarshal(raw, &decoded))
			assert.Equalf(t, tc.taints == nil, decoded.Taints == nil,
				"round trip changed whether taints are nil, which is what distinguishes "+
					"\"not specified\" from \"remove all taints\"")
			assert.Equal(t, tc.taints, decoded.Taints)
		})
	}
}
