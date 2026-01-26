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
	"fmt"
	"net/http"

	"github.com/microsoft/go-otel-audit/audit/msgs"

	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/audit"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type AuditResponseWriter struct {
	http.ResponseWriter
	statusCode int
}

// WriteHeader captures the status code and delegates to the underlying ResponseWriter
func (w *AuditResponseWriter) WriteHeader(statusCode int) {
	w.statusCode = statusCode
	w.ResponseWriter.WriteHeader(statusCode)
}

type middlewareAudit struct {
	auditClient audit.Client
}

func newMiddlewareAudit(auditClient audit.Client) *middlewareAudit {
	return &middlewareAudit{
		auditClient: auditClient,
	}
}

// MiddlewareAudit writes audit messages upon receiving a request.
func (h *middlewareAudit) handleRequest(w http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
	ctx := r.Context()
	logger := utils.LoggerFromContext(ctx)

	msg := audit.CreateOtelAuditMsg(ctx, r)
	correlationData := arm.NewCorrelationData(r)
	msg.Record.CallerIdentities = getCallerIdentitesMap(correlationData)

	// Wrap the response writer to capture status code
	auditWriter := &AuditResponseWriter{
		ResponseWriter: w,
	}

	next(auditWriter, r)

	statusCode := auditWriter.statusCode
	if statusCode >= http.StatusBadRequest {
		msg.Record.OperationResult = msgs.Failure
		msg.Record.OperationResultDescription = fmt.Sprintf("Status code: %d", statusCode)
	}

	if err := h.auditClient.Send(ctx, msg); err != nil {
		logger.Error(err, "error sending audit log")
	}
}

// used for otelaudit via "github.com/microsoft/go-otel-audit/audit/msgs"
func getCallerIdentitesMap(correlationData *arm.CorrelationData) map[msgs.CallerIdentityType][]msgs.CallerIdentityEntry {
	caller := make(map[msgs.CallerIdentityType][]msgs.CallerIdentityEntry)
	if correlationData.ClientPrincipalName != "" {
		caller[msgs.UPN] = []msgs.CallerIdentityEntry{
			{
				Identity:    correlationData.ClientPrincipalName,
				Description: "client principal name",
			},
		}
	}

	if correlationData.ClientRequestID != "" {
		caller[msgs.CIOther] = []msgs.CallerIdentityEntry{
			{
				Identity:    correlationData.ClientRequestID,
				Description: "client request CorrelationID",
			},
		}
	}

	return caller
}
