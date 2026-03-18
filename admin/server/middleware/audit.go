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

package middleware

import (
	"fmt"
	"net/http"

	"github.com/microsoft/go-otel-audit/audit/msgs"

	"github.com/Azure/ARO-HCP/internal/audit"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	operationCategoryDescription = "Client Resource Management via admin API"
	operationAccessLevel         = "Geneva Action ARO-HCP JIT Access"
)

type middlewareAudit struct {
	auditClient audit.Client
}

func NewMiddlewareAudit(auditClient audit.Client) *middlewareAudit {
	return &middlewareAudit{auditClient: auditClient}
}

func (m *middlewareAudit) HandleRequest(w http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
	ctx := r.Context()
	logger := utils.LoggerFromContext(ctx)

	callerIdentities := extractCallerIdentities(r)
	msg := audit.CreateOtelAuditMsg(ctx, r, operationCategoryDescription, operationAccessLevel, callerIdentities)

	auditWriter := audit.NewResponseWriter(w)

	next(auditWriter, r)

	if auditWriter.StatusCode() >= http.StatusBadRequest {
		msg.Record.OperationResult = msgs.Failure
		msg.Record.OperationResultDescription = fmt.Sprintf("Status code: %d", auditWriter.StatusCode())
	}

	if err := m.auditClient.Send(ctx, msg); err != nil {
		logger.Error(err, "error sending audit log", "operationName", msg.Record.OperationName)
	}
}

// extractCallerIdentities returns the caller identity from the request header.
// Reads directly from the X-Ms-Client-Principal-Name header rather than from
// context, since audit middleware runs before MiddlewareClientPrincipal because
// we want 401s in audit as well.
// Returns an empty map when the header is missing.
func extractCallerIdentities(request *http.Request) map[msgs.CallerIdentityType][]msgs.CallerIdentityEntry {
	clientPrincipalName := request.Header.Get(ClientPrincipalNameHeader)
	if clientPrincipalName == "" {
		return map[msgs.CallerIdentityType][]msgs.CallerIdentityEntry{}
	}
	return map[msgs.CallerIdentityType][]msgs.CallerIdentityEntry{
		msgs.UPN: {
			{
				Identity:    clientPrincipalName,
				Description: "client principal name",
			},
		},
	}
}
