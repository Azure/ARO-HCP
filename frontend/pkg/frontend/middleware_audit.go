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
)

type AuditResponseWriter struct {
	http.ResponseWriter
	statusCode int
}

// MiddlewareAudit writes audit messages upon receiving a request.
func MiddlewareAudit(w http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
	ctx := r.Context()
	logger := LoggerFromContext(ctx)
	auditClient, err := AuditClientFromContext(ctx)
	if err != nil {
		logger.Error("error getting audit client", "error", err.Error())
		next(w, r)
		return
	}

	msg := audit.CreateOtelAuditMsg(logger, r)
	correlationData := arm.NewCorrelationData(r)
	msg.Record.CallerIdentities = getCallerIdentitesMap(correlationData)

	next(w, r)

	responseWriter, ok := w.(*AuditResponseWriter)
	if ok && responseWriter.statusCode >= http.StatusBadRequest {
		msg.Record.OperationResult = msgs.Failure
		msg.Record.OperationResultDescription = fmt.Sprintf("Status code: %d", responseWriter.statusCode)
	}

	if err := auditClient.Send(ctx, msg); err != nil {
		logger.Error("error sending audit log", "error", err.Error())
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
