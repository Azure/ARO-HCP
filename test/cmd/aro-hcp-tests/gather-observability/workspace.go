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
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/util/sets"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
)

const (
	workspaceSvc   = "svc"
	workspaceHcp   = "hcp"
	workspaceInfra = "infra"
)

type workspaceData struct {
	Type            string
	PromEndpoint    string
	PromError       error
	CollectionError error
	AlertRules      []string
	FiredAlerts     []alert
}

func buildWorkspaceAlertData(wsType string, workspaceResourceID azcorearm.ResourceID, allAlerts []alert, severityThreshold int, knownIssues []knownIssue) *workspaceData {
	var alerts []alert
	for _, a := range allAlerts {
		if alertBelongsToWorkspace(a, workspaceResourceID) {
			a.Metadata.MonitoringWorkspaceType = wsType
			alerts = append(alerts, a)
		}
	}

	alerts = filterAlertsBySeverity(alerts, severityThreshold)
	alerts = classifyAlerts(alerts, knownIssues)

	return &workspaceData{
		Type:        wsType,
		FiredAlerts: alerts,
	}
}

const azureMonitorResourceType = "microsoft.monitor/accounts"

func buildInfraAlertData(allAlerts []alert, metricAlertRules []string, severityThreshold int, knownIssues []knownIssue) *workspaceData {
	var alerts []alert
	for _, a := range allAlerts {
		if isWorkspaceTargeted(a) {
			continue
		}
		a.Metadata.MonitoringWorkspaceType = workspaceInfra
		alerts = append(alerts, a)
	}

	alerts = filterAlertsBySeverity(alerts, severityThreshold)
	alerts = classifyAlerts(alerts, knownIssues)

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
