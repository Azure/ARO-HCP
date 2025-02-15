package arm

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

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
