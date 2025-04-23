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

	"github.com/google/uuid"
)

// CorrelationData represents any data used for metrics or tracing.
// See: https://github.com/Azure/azure-resource-manager-rpc/blob/master/v1.0/common-api-details.md
type CorrelationData struct {
	// RequestID is a generated unique identifier for the current operation.
	RequestID uuid.UUID

	// ClientRequestID contains the value of header "x-ms-client-request-id".
	ClientRequestID string `json:"clientRequestId,omitempty"`

	// CorrelationRequestID contains the value of header "x-ms-correlation-request-id".
	CorrelationRequestID string `json:"correlationRequestId,omitempty"`
}

// NewCorrelationData allocates and initializes a new CorrelationData from
// HTTP request headers.
func NewCorrelationData(r *http.Request) *CorrelationData {
	return &CorrelationData{
		RequestID:            uuid.New(),
		ClientRequestID:      r.Header.Get(HeaderNameClientRequestID),
		CorrelationRequestID: r.Header.Get(HeaderNameCorrelationRequestID),
	}
}
