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

package engine

import (
	"fmt"
	"net/http"

	"github.com/go-logr/logr"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

type deleteAuditPolicy struct{}
type deleteAttempt int

// Do records each ARM DELETE attempt without exposing request bodies or credentials.
func (deleteAuditPolicy) Do(req *policy.Request) (*http.Response, error) {
	if req.Raw().Method != http.MethodDelete {
		return req.Next()
	}
	var attempt deleteAttempt
	req.OperationValue(&attempt)
	attempt++
	req.SetOperationValue(attempt)

	logger := logr.FromContextOrDiscard(req.Raw().Context()).WithValues(
		"method", req.Raw().Method,
		"requestPath", req.Raw().URL.Path,
		"clientRequestID", req.Raw().Header.Get("x-ms-client-request-id"),
		"attempt", int(attempt),
	)
	logger.Info("Sending ARM DELETE request")
	response, err := req.Next()
	if response != nil {
		logger.Info("Received ARM DELETE response",
			"statusCode", response.StatusCode,
			"azureRequestID", response.Header.Get("x-ms-request-id"),
			"azureCorrelationRequestID", response.Header.Get("x-ms-correlation-request-id"),
			"azureRoutingRequestID", response.Header.Get("x-ms-routing-request-id"),
		)
	}
	if err != nil {
		// Transport errors can contain URLs or bodies; only record their type here.
		logger.Info("ARM DELETE attempt failed", "errorType", fmt.Sprintf("%T", err))
	}
	return response, err
}
