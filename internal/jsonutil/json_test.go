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

package jsonutil

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUnmarshal(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		data     string
		wantName string
		wantErr  string
	}{
		{name: "valid", data: `{"name":"value"}`, wantName: "value"},
		{name: "unknown field ignored", data: `{"extra":true,"name":"value"}`, wantName: "value"},
		{name: "last duplicate wins", data: `{"name":"first","name":"second"}`, wantName: "second"},
		{name: "unknown and duplicate fields", data: `{"extra":true,"name":"first","name":"second"}`, wantName: "second"},
		{name: "case mismatch ignored", data: `{"Name":"value"}`},
		{name: "case mismatch does not overwrite", data: `{"name":"value","Name":"other"}`, wantName: "value"},
		{name: "syntax error", data: `{"name":`, wantErr: "unexpected end of JSON input"},
		{name: "type error", data: `{"name":1}`, wantErr: "cannot unmarshal number"},
		{name: "unknown field does not hide type error", data: `{"extra":true,"name":1}`, wantErr: "cannot unmarshal number"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			var value struct {
				Name string `json:"name"`
			}
			err := Unmarshal([]byte(testCase.data), &value)
			if testCase.wantErr == "" {
				require.NoError(t, err)
				require.Equal(t, testCase.wantName, value.Name)
				return
			}
			require.ErrorContains(t, err, testCase.wantErr)
		})
	}
}

func TestUnmarshalPreservesIntegers(t *testing.T) {
	var value map[string]any
	require.NoError(t, Unmarshal([]byte(`{"count":9007199254740993}`), &value))
	require.Equal(t, int64(9007199254740993), value["count"])
}
