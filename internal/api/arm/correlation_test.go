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
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

func TestNewCorrelationData(t *testing.T) {
	const (
		client_request_id             = "random_client_request_id"
		correlation_request_id string = "random_correlation_request_id"
	)

	tests := []struct {
		name    string
		request *http.Request
		want    *CorrelationData
	}{
		{
			name: "NewCorrelationData returns the appropriate correlation data from request",
			request: &http.Request{
				Header: http.Header{
					HeaderNameClientRequestID:      []string{client_request_id},
					HeaderNameCorrelationRequestID: []string{correlation_request_id},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			correlationData := NewCorrelationData(tt.request)

			assert.NotEqual(t, uuid.Nil, correlationData.RequestID)
			assert.Equal(t, client_request_id, correlationData.ClientRequestID)
			assert.Equal(t, correlation_request_id, correlationData.CorrelationRequestID)
		})
	}
}
