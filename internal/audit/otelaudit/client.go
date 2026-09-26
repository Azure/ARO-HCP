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

package otelaudit

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/microsoft/go-otel-audit/audit"
	"github.com/microsoft/go-otel-audit/audit/conn"
	"github.com/microsoft/go-otel-audit/audit/msgs"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	auditapi "github.com/Azure/ARO-HCP/internal/audit"
)

const (
	Unknown = "Unknown"

	MetricAuditLogRecordsTotal       = "otel_audit_log_records_total"
	MetricAuditLogSendErrorsTotal    = "otel_audit_log_send_errors_total"
	MetricAuditLogConnectionDegraded = "otel_audit_log_connection_degraded"
)

var _ auditapi.Client = (*AuditClient)(nil)

type AuditClient struct {
	client     *audit.Client
	totalSend  prometheus.Counter
	sendErrors prometheus.Counter
}

func (c *AuditClient) Send(ctx context.Context, msg msgs.Msg) error {
	c.totalSend.Inc()
	if c.client == nil {
		return nil
	}
	ensureDefaults(&msg.Record)
	err := c.client.Send(ctx, msg)
	if err != nil {
		c.sendErrors.Inc()
	}
	return err
}

// NewOtelAuditClient delegates asynchronous delivery and background recovery to
// go-otel-audit. Enabled forwarding requires the service's Service Tree UUID.
// Disabled forwarding is a no-op and does not create a client or require an identity.
// The client lives until process exit: cancelling ctx must not stop auditing
// while the service's in-flight HTTP requests are still draining.
func NewOtelAuditClient(ctx context.Context, connectSocket bool, serviceTreeID uuid.UUID, registerer prometheus.Registerer, options ...audit.Option) (*AuditClient, error) {
	var createConn audit.CreateConn
	if connectSocket {
		createConn = func() (conn.Audit, error) {
			return conn.NewDomainSocket()
		}
	}
	return newOtelAuditClient(ctx, serviceTreeID, createConn, registerer, options...)
}

// A nil factory explicitly disables forwarding.
func newOtelAuditClient(ctx context.Context, serviceTreeID uuid.UUID, connectionFactory audit.CreateConn, registerer prometheus.Registerer, options ...audit.Option) (*AuditClient, error) {
	degraded := promauto.With(registerer).NewGauge(prometheus.GaugeOpts{
		Name: MetricAuditLogConnectionDegraded,
		Help: "State of the audit logs forwarding: 1 for degraded when the intended connection to the audit server failed, 0 otherwise",
	})
	var client *audit.Client
	if connectionFactory != nil {
		observedFactory := func() (conn.Audit, error) {
			connection, err := connectionFactory()
			if err != nil {
				degraded.Set(1)
				return nil, err
			}
			degraded.Set(0)
			return connection, nil
		}
		options = append(options, audit.WithDeferredConnection())
		var err error
		client, err = audit.New(ctx, serviceTreeID, observedFactory, options...)
		if err != nil {
			return nil, fmt.Errorf("failed to create audit client: %w", err)
		}
	}

	return &AuditClient{
		client: client,
		totalSend: promauto.With(registerer).NewCounter(prometheus.CounterOpts{
			Name: MetricAuditLogRecordsTotal,
			Help: "Total number of audit records attempted to be sent.",
		}),
		sendErrors: promauto.With(registerer).NewCounter(prometheus.CounterOpts{
			Name: MetricAuditLogSendErrorsTotal,
			Help: "Total number of audit records that failed to send.",
		}),
	}, nil
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
