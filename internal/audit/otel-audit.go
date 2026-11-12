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

package audit

import (
	"context"
	"fmt"
	"net"
	"net/http"

	"github.com/microsoft/go-otel-audit/audit/msgs"

	"github.com/Azure/ARO-HCP/internal/utils"
)

type Client interface {
	Send(ctx context.Context, msg msgs.Msg) error
}

func GetOperationType(method string) msgs.OperationType {
	switch method {
	case http.MethodGet:
		return msgs.Read
	case http.MethodPost:
		return msgs.Create
	case http.MethodPut:
		return msgs.Update
	case http.MethodDelete:
		return msgs.Delete
	default:
		return msgs.UnknownOperationType
	}
}

// ResponseWriter wraps http.ResponseWriter to capture the status code
// for audit logging.
type ResponseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (w *ResponseWriter) WriteHeader(statusCode int) {
	w.statusCode = statusCode
	w.ResponseWriter.WriteHeader(statusCode)
}

// StatusCode returns the HTTP status code that was written.
// Returns http.StatusOK if WriteHeader was never called.
func (w *ResponseWriter) StatusCode() int {
	if w.statusCode == 0 {
		return http.StatusOK
	}
	return w.statusCode
}

func NewResponseWriter(w http.ResponseWriter) *ResponseWriter {
	return &ResponseWriter{ResponseWriter: w}
}

func CreateOtelAuditMsg(ctx context.Context, r *http.Request, categoryDescription string, accessLevel string, callerIdentities map[msgs.CallerIdentityType][]msgs.CallerIdentityEntry) msgs.Msg {
	logger := utils.LoggerFromContext(ctx)

	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		logger.Error(err, "failed to split host and port for remote request addr", "addr", r.RemoteAddr)
	}

	addr, err := msgs.ParseAddr(host)
	if err != nil {
		logger.Error(err, "failed to parse address for host", "host", host)
	}

	record := msgs.Record{
		CallerIpAddress:              addr,
		OperationCategories:          []msgs.OperationCategory{msgs.ResourceManagement},
		OperationCategoryDescription: categoryDescription,
		OperationAccessLevel:         accessLevel,
		OperationName:                fmt.Sprintf("%s %s", r.Method, r.URL.Path),
		CallerAgent:                  r.UserAgent(),
		OperationType:                GetOperationType(r.Method),
		OperationResult:              msgs.Success,
		CallerIdentities:             callerIdentities,
	}

	msg := msgs.Msg{
		Type:   msgs.ControlPlane,
		Record: record,
	}

	return msg
}
