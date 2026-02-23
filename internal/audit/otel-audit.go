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
	"strings"

	"github.com/microsoft/go-otel-audit/audit"
	"github.com/microsoft/go-otel-audit/audit/base"
	"github.com/microsoft/go-otel-audit/audit/conn"
	"github.com/microsoft/go-otel-audit/audit/msgs"

	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	Unknown = "Unknown"
)

type Client interface {
	Send(ctx context.Context, msg msgs.Msg, options ...base.SendOption) error
}

type AuditClient struct {
	client Client
}

func (c *AuditClient) Send(ctx context.Context, msg msgs.Msg, options ...base.SendOption) error {
	ensureDefaults(&msg.Record)
	return c.client.Send(ctx, msg)
}

func CreateConn(connectSocket bool) (createConn audit.CreateConn) {
	if connectSocket {
		createConn = func() (conn.Audit, error) {
			return conn.NewDomainSocket()
		}
	} else {
		createConn = func() (conn.Audit, error) {
			return conn.NewNoOP(), nil
		}
	}
	return createConn
}

func NewOtelAuditClient(createConn audit.CreateConn, options ...base.Option) (*AuditClient, error) {
	client, err := audit.New(createConn, audit.WithAuditOptions(options...))
	if err != nil {
		return nil, err
	}

	return &AuditClient{client: client}, nil
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

// ensureDefaults ensures that all required fields in the Record are set to default values if they are empty or invalid.
// It modifies the Record in place to ensure it meets the expected structure and data requirements.
func ensureDefaults(r *msgs.Record) {
	setDefault := func(value *string, defaultValue string) {
		if *value == "" {
			*value = defaultValue
		}
	}

	setDefault(&r.OperationName, Unknown)
	setDefault(&r.OperationAccessLevel, Unknown)
	setDefault(&r.CallerAgent, Unknown)

	if len(r.OperationCategories) == 0 {
		r.OperationCategories = []msgs.OperationCategory{msgs.ResourceManagement}
	}

	for _, category := range r.OperationCategories {
		if category == msgs.OCOther && r.OperationCategoryDescription == "" {
			r.OperationCategoryDescription = "Other"
		}
	}

	if r.OperationResult == msgs.Failure && r.OperationResultDescription == "" {
		r.OperationResultDescription = Unknown
	}

	if len(r.CallerIdentities) == 0 {
		r.CallerIdentities = map[msgs.CallerIdentityType][]msgs.CallerIdentityEntry{
			msgs.ApplicationID: {
				{Identity: Unknown, Description: Unknown},
			},
		}
	}

	for identityType, identities := range r.CallerIdentities {
		if len(identities) == 0 {
			r.CallerIdentities[identityType] = []msgs.CallerIdentityEntry{{Identity: Unknown, Description: Unknown}}
		} else {
			for i, identity := range identities {
				if strings.TrimSpace(identity.Identity) == "" {
					identities[i].Identity = Unknown
				}
				if strings.TrimSpace(identity.Description) == "" {
					identities[i].Description = Unknown
				}
			}
			r.CallerIdentities[identityType] = identities
		}
	}

	if !r.CallerIpAddress.IsValid() || r.CallerIpAddress.IsUnspecified() || r.CallerIpAddress.IsLoopback() || r.CallerIpAddress.IsMulticast() {
		r.CallerIpAddress, _ = msgs.ParseAddr("192.168.1.1")
	}

	if len(r.CallerAccessLevels) == 0 {
		r.CallerAccessLevels = []string{Unknown}
	}

	for i, k := range r.CallerAccessLevels {
		if strings.TrimSpace(k) == "" {
			r.CallerAccessLevels[i] = Unknown
		}
	}

	if len(r.TargetResources) == 0 {
		r.TargetResources = map[string][]msgs.TargetResourceEntry{
			Unknown: {
				{Name: Unknown, Region: Unknown},
			},
		}
	}

	for resourceType, resources := range r.TargetResources {
		if strings.TrimSpace(resourceType) == "" {
			r.TargetResources[Unknown] = resources
			delete(r.TargetResources, resourceType)
		}

		for _, resource := range resources {
			if err := resource.Validate(); err != nil {
				resource.Name = Unknown
			}
		}
	}
}
