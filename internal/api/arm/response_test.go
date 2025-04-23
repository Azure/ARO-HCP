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

package arm

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteJSONResponse(t *testing.T) {
	resourceStruct := &TrackedResource{
		Resource: Resource{
			ID:   "00000000-0000-0000-0000-000000000000",
			Name: "testVM",
			Type: "Microsoft.Compute/virtualMachines",
		},
		Location: "eastus1",
		Tags: map[string]string{
			"nameA": "valueA",
			"nameB": "valueB",
		},
	}

	resourceBytes, err := MarshalJSON(resourceStruct)
	require.NoError(t, err)

	tests := []struct {
		name       string
		statusCode int
		body       any
	}{
		{
			name:       "Write structured data",
			statusCode: http.StatusAccepted,
			body:       resourceStruct,
		},
		{
			name:       "Write byte slice",
			statusCode: http.StatusCreated,
			body:       resourceBytes,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()

			_, err := WriteJSONResponse(recorder, tt.statusCode, tt.body)
			require.NoError(t, err)

			result := recorder.Result()

			assert.Equal(t, tt.statusCode, result.StatusCode)

			contentType := result.Header.Get("Content-Type")
			if assert.NotEmpty(t, contentType, "Response is missing a Content-Type header") {
				assert.Equal(t, "application/json", contentType)
			}

			expectBody, err := MarshalJSON(resourceStruct)
			require.NoError(t, err)

			actualBody, err := io.ReadAll(result.Body)
			require.NoError(t, err)

			assert.Equal(t, string(expectBody), string(actualBody))
		})
	}
}
