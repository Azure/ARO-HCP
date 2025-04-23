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

package frontend

import (
	"net/http"
	"strings"

	"github.com/Azure/ARO-HCP/internal/api/arm"
)

// MiddlewareCorrelationData reads the correlation data from the incoming
// request, extends the contextual logger with correlation attributes and adds
// the necessary X-ms-* headers to the HTTP response.
func MiddlewareCorrelationData(w http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
	var (
		ctx    = r.Context()
		logger = LoggerFromContext(ctx)
	)

	correlationData := arm.NewCorrelationData(r)
	ctx = ContextWithCorrelationData(ctx, correlationData)

	logger = logger.With("request_id", correlationData.RequestID.String())
	if correlationData.ClientRequestID != "" {
		logger = logger.With("client_request_id", correlationData.ClientRequestID)
	}

	if correlationData.CorrelationRequestID != "" {
		logger = logger.With("correlation_request_id", correlationData.CorrelationRequestID)
	}
	ctx = ContextWithLogger(ctx, logger)
	r = r.WithContext(ctx)

	w.Header().Set(arm.HeaderNameRequestID, correlationData.RequestID.String())
	returnClientRequestID := r.Header.Get(arm.HeaderNameReturnClientRequestID)
	if strings.EqualFold(returnClientRequestID, "true") {
		w.Header().Set(arm.HeaderNameClientRequestID, correlationData.ClientRequestID)
	}

	next(w, r)
}
