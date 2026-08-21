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
	"strings"

	"github.com/go-logr/logr"

	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
)

const (
	workspaceSvc   = "svc"
	workspaceHcp   = "hcp"
	workspaceInfra = "infra"
)

type workspaceData struct {
	Type         string
	PromEndpoint string
	AlertRules   []string
	FiredAlerts  []alert
}

func fetchWorkspaceData(ctx context.Context, cred azcore.TokenCredential, wsType string, workspaceResourceID azcorearm.ResourceID, allAlerts []alert, severityThreshold int, categories []category) (*workspaceData, error) {
	logger, err := logr.FromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("logger not found in context: %w", err)
	}

	promEndpoint, err := lookupPrometheusEndpoint(ctx, cred, workspaceResourceID.SubscriptionID, workspaceResourceID.ResourceGroupName, workspaceResourceID.Name)
	if err != nil {
		return nil, fmt.Errorf("failed to look up Prometheus endpoint: %w", err)
	}
	logger.Info("resolved Prometheus endpoint", "workspace", wsType, "endpoint", promEndpoint)

	rules, err := fetchAlertRules(ctx, cred, workspaceResourceID)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch alert rules: %w", err)
	}
	logger.Info("fetched alert rules", "workspace", wsType, "count", len(rules))

	var alerts []alert
	for _, a := range allAlerts {
		if alertBelongsToWorkspace(a, workspaceResourceID) {
			a.Metadata.MonitoringWorkspaceType = wsType
			alerts = append(alerts, a)
		}
	}

	alerts = filterAlertsBySeverity(alerts, severityThreshold)
	alerts = categorizeAlerts(alerts, categories)
	logger.Info("fetched fired alerts", "workspace", wsType, "count", len(alerts))

	return &workspaceData{
		Type:         wsType,
		PromEndpoint: promEndpoint,
		AlertRules:   rules,
		FiredAlerts:  alerts,
	}, nil
}

const azureMonitorResourceType = "microsoft.monitor/accounts"

func buildInfraAlertData(allAlerts []alert, metricAlertRules []string, severityThreshold int, categories []category) *workspaceData {
	var alerts []alert
	for _, a := range allAlerts {
		if isWorkspaceTargeted(a) {
			continue
		}
		a.Metadata.MonitoringWorkspaceType = workspaceInfra
		alerts = append(alerts, a)
	}

	alerts = filterAlertsBySeverity(alerts, severityThreshold)
	alerts = categorizeAlerts(alerts, categories)

	return &workspaceData{
		Type:        workspaceInfra,
		AlertRules:  metricAlertRules,
		FiredAlerts: alerts,
	}
}

func isWorkspaceTargeted(a alert) bool {
	if a.Metadata.MonitoringWorkspace == "" {
		return false
	}
	targetID, err := azcorearm.ParseResourceID(a.Metadata.MonitoringWorkspace)
	if err != nil {
		return false
	}
	return strings.EqualFold(targetID.ResourceType.String(), azureMonitorResourceType)
}

func uniqueResourceGroups(workspaces map[string]azcorearm.ResourceID) sets.Set[string] {
	result := sets.New[string]()
	for _, ws := range workspaces {
		result.Insert(fmt.Sprintf("/subscriptions/%s/resourceGroups/%s", ws.SubscriptionID, ws.ResourceGroupName))
	}
	return result
}

func alertBelongsToWorkspace(a alert, ws azcorearm.ResourceID) bool {
	return strings.EqualFold(a.Metadata.MonitoringWorkspace, ws.String())
}
