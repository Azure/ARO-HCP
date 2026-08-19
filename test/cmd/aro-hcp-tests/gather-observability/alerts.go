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

package gatherobservability

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/go-logr/logr"

	"sigs.k8s.io/yaml"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/alertsmanagement/armalertsmanagement"
)

// alertData holds the Prometheus-native and Azure alert fields.
type alertData struct {
	Name        string                       `json:"name"`
	Severity    armalertsmanagement.Severity `json:"severity"`
	State       string                       `json:"state"`
	Condition   string                       `json:"condition"`
	AlertRule   string                       `json:"alertRule,omitempty"`
	SignalType  string                       `json:"signalType,omitempty"`
	StartsAt    *time.Time                   `json:"startsAt,omitempty"`
	EndsAt      *time.Time                   `json:"endsAt,omitempty"`
	Description string                       `json:"description,omitempty"`
	Labels      map[string]string            `json:"labels,omitempty"`
	Annotations map[string]string            `json:"annotations,omitempty"`
	Expression  string                       `json:"expression,omitempty"`
}

// alertMetadata holds enrichments added by our tooling.
type alertMetadata struct {
	// Category, CategoryTier, CategoryPolicy and CategoryReason record the
	// blast-radius category assigned by categorizeAlerts (see categories.go).
	// CategoryMinFirings/CategoryMinDurationSeconds are copied from the
	// matched category's threshold so junit.go can evaluate a
	// "fail-over-threshold" policy without needing the categories config.
	Category                   string  `json:"category,omitempty"`
	CategoryTier               int     `json:"categoryTier"`
	CategoryPolicy             string  `json:"categoryPolicy,omitempty"`
	CategoryReason             string  `json:"categoryReason,omitempty"`
	CategoryMinFirings         int     `json:"categoryMinFirings,omitempty"`
	CategoryMinDurationSeconds float64 `json:"categoryMinDurationSeconds,omitempty"`
	// KnownIssue/KnownIssueReason are retained for the HTML dashboard's
	// existing known/unknown display and are derived from the category:
	// true (with the category's reason) iff CategoryPolicy is "ignore".
	// This is the successor to the old known-issues skip list, now folded
	// into the "expected-noise" category.
	KnownIssue              bool   `json:"knownIssue"`
	KnownIssueReason        string `json:"knownIssueReason,omitempty"`
	MonitoringWorkspace     string `json:"monitoringWorkspace,omitempty"`
	MonitoringWorkspaceType string `json:"monitoringWorkspaceType,omitempty"`
}

// alert combines the alert data with our metadata.
type alert struct {
	Alert    alertData     `json:"alert"`
	Metadata alertMetadata `json:"metadata"`
}

func fetchAlerts(ctx context.Context, cred azcore.TokenCredential, scope string, start, end time.Time) ([]alert, error) {
	logger, err := logr.FromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("logger not found in context: %w", err)
	}
	logger = logger.WithValues("scope", scope)

	client, err := armalertsmanagement.NewAlertsClient(scope, cred, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create alerts client: %w", err)
	}

	var allAlerts []alert

	customTimeRange := fmt.Sprintf("%s/%s",
		start.UTC().Format(time.RFC3339),
		end.UTC().Format(time.RFC3339),
	)
	logger.Info("querying alerts fired within window", "timeRange", customTimeRange)

	includeContext := true
	pager := client.NewGetAllPager(&armalertsmanagement.AlertsClientGetAllOptions{
		CustomTimeRange: &customTimeRange,
		IncludeContext:  &includeContext,
	})
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to list alerts: %w", err)
		}
		for _, alert := range page.Value {
			allAlerts = append(allAlerts, toAlert(alert))
		}
	}
	sortAlerts(allAlerts)
	logger.Info("alerts fetched", "count", len(allAlerts))
	return allAlerts, nil
}

func sortAlerts(alerts []alert) {
	slices.SortFunc(alerts, func(a, b alert) int {
		switch {
		case a.Alert.StartsAt == nil && b.Alert.StartsAt == nil:
			return 0
		case a.Alert.StartsAt == nil:
			return -1
		case b.Alert.StartsAt == nil:
			return 1
		default:
			return a.Alert.StartsAt.Compare(*b.Alert.StartsAt)
		}
	})
}

func toAlert(raw *armalertsmanagement.Alert) alert {
	var a alertData
	var m alertMetadata
	if raw.Name != nil {
		a.Name = *raw.Name
	}
	if raw.Properties == nil || raw.Properties.Essentials == nil {
		return alert{Alert: a, Metadata: m}
	}
	e := raw.Properties.Essentials
	if e.Severity != nil {
		a.Severity = *e.Severity
	}
	if e.AlertState != nil {
		a.State = string(*e.AlertState)
	}
	if e.MonitorCondition != nil {
		a.Condition = string(*e.MonitorCondition)
	}
	if e.StartDateTime != nil {
		a.StartsAt = e.StartDateTime
	}
	if e.MonitorConditionResolvedDateTime != nil {
		a.EndsAt = e.MonitorConditionResolvedDateTime
	}
	if e.Description != nil {
		a.Description = *e.Description
	}
	if e.AlertRule != nil {
		a.AlertRule = *e.AlertRule
	}
	if e.TargetResource != nil {
		m.MonitoringWorkspace = *e.TargetResource
	}
	if e.SignalType != nil {
		a.SignalType = string(*e.SignalType)
	}
	ctx := parseContext(raw.Properties.Context)
	a.Labels = ctx.Labels
	a.Annotations = ctx.Annotations
	a.Expression = ctx.Expression
	if alertname, ok := a.Labels["alertname"]; ok {
		a.Name = alertname
	} else if a.AlertRule != "" {
		if ruleID, err := azcorearm.ParseResourceID(a.AlertRule); err == nil {
			a.Name = ruleID.Name
		}
	}
	return alert{Alert: a, Metadata: m}
}

// prometheusAlertContext represents the typed shape of the Prometheus alert
// context that the Azure SDK returns as untyped any.
type prometheusAlertContext struct {
	Labels      map[string]string `json:"labels"`
	Annotations map[string]string `json:"annotations"`
	Expression  string            `json:"expression"`
}

func parseContext(ctx any) prometheusAlertContext {
	data, err := yaml.Marshal(ctx)
	if err != nil {
		return prometheusAlertContext{}
	}
	var c prometheusAlertContext
	if err := yaml.Unmarshal(data, &c); err != nil {
		return prometheusAlertContext{}
	}
	return c
}
