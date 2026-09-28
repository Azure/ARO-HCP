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
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/prometheus/prometheus/promql/parser"

	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/monitor/armmonitor"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/prometheusrulegroups/armprometheusrulegroups"
)

type alertRuleInventory struct {
	Names       []string
	Definitions []alertRuleDefinition
}

type alertRuleDefinition struct {
	Name       string
	Expression string
	Interval   string
	GroupID    string
}

func fetchAlertRules(ctx context.Context, cred azcore.TokenCredential, workspaceResourceID azcorearm.ResourceID) (alertRuleInventory, error) {
	client, err := armprometheusrulegroups.NewClient(workspaceResourceID.SubscriptionID, cred, nil)
	if err != nil {
		return alertRuleInventory{}, fmt.Errorf("failed to create prometheus rule groups client: %w", err)
	}

	return collectAlertRules(ctx, client.NewListByResourceGroupPager(workspaceResourceID.ResourceGroupName, nil), workspaceResourceID)
}

func collectAlertRules(ctx context.Context, pager *runtime.Pager[armprometheusrulegroups.ClientListByResourceGroupResponse], workspaceResourceID azcorearm.ResourceID) (inventory alertRuleInventory, err error) {
	defer func() {
		slices.Sort(inventory.Names)
		inventory.Names = slices.Compact(inventory.Names)
	}()
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return inventory, fmt.Errorf("failed to list prometheus rule groups: %w", err)
		}
		for _, group := range page.Value {
			if group == nil || group.Properties == nil || !scopeContainsWorkspace(group.Properties.Scopes, workspaceResourceID) {
				continue
			}
			for _, rule := range group.Properties.Rules {
				if rule != nil && rule.Alert != nil && *rule.Alert != "" {
					inventory.Names = append(inventory.Names, *rule.Alert)
					inventory.Definitions = append(inventory.Definitions, alertRuleDefinition{
						Name:       *rule.Alert,
						Expression: ptr.Deref(rule.Expression, ""),
						Interval:   ptr.Deref(group.Properties.Interval, ""),
						GroupID:    ptr.Deref(group.ID, ""),
					})
				}
			}
		}
	}

	return inventory, nil
}

// Only fixed time components are accepted, at Prometheus's millisecond precision.
var ruleIntervalPattern = regexp.MustCompile(`^PT(?:(\d+)H)?(?:(\d+)M)?(?:(\d+(?:\.\d{1,3})?)S)?$`)

func diagnosticStep(a alert, definitions []alertRuleDefinition) (step string, warning string) {
	const fallback = "60s"
	expression, err := parser.NewParser(parser.Options{}).ParseExpr(a.Alert.Expression)
	if err != nil || expression.Type() != parser.ValueTypeVector {
		return fallback, "Using fallback diagnostic step 60s: alert expression is missing or invalid."
	}

	// Alert fixtures identify the group itself. Do not infer a group from a
	// display name or an undocumented child-rule resource ID format.
	var groupID string
	if id, err := azcorearm.ParseResourceID(a.Alert.AlertRule); err == nil && strings.EqualFold(id.ResourceType.String(), "Microsoft.AlertsManagement/prometheusRuleGroups") {
		groupID = id.String()
	}
	var interval time.Duration
	for _, definition := range definitions {
		if definition.Name != a.Alert.Name || (groupID != "" && !strings.EqualFold(groupID, definition.GroupID)) {
			continue
		}
		current, err := parser.NewParser(parser.Options{}).ParseExpr(definition.Expression)
		if err != nil || current.String() != expression.String() {
			continue
		}
		parts := ruleIntervalPattern.FindStringSubmatch(definition.Interval)
		if parts == nil {
			return fallback, fmt.Sprintf("Using fallback diagnostic step 60s: matching rule has invalid interval %q.", definition.Interval)
		}
		var duration string
		for i, unit := range []string{"h", "m", "s"} {
			if parts[i+1] != "" {
				duration += parts[i+1] + unit
			}
		}
		parsed, err := time.ParseDuration(duration)
		if err != nil || parsed < time.Millisecond {
			return fallback, fmt.Sprintf("Using fallback diagnostic step 60s: matching rule has invalid interval %q.", definition.Interval)
		}
		if interval != 0 && interval != parsed {
			return fallback, "Using fallback diagnostic step 60s: matching rule groups have ambiguous intervals."
		}
		interval = parsed
	}
	if interval == 0 {
		return fallback, "Using fallback diagnostic step 60s: no rule metadata matches the alert name, expression, and known group."
	}
	if interval%time.Second == 0 {
		return fmt.Sprintf("%ds", interval/time.Second), ""
	}
	return fmt.Sprintf("%dms", interval/time.Millisecond), ""
}

func fetchMetricAlertRules(ctx context.Context, cred azcore.TokenCredential, subscriptionID, resourceGroup string) ([]string, error) {
	client, err := armmonitor.NewMetricAlertsClient(subscriptionID, cred, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create metric alerts client: %w", err)
	}

	var rules []string
	pager := client.NewListByResourceGroupPager(resourceGroup, nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			slices.Sort(rules)
			return rules, fmt.Errorf("failed to list metric alert rules: %w", err)
		}
		for _, alert := range page.Value {
			if alert.Name != nil && *alert.Name != "" {
				rules = append(rules, *alert.Name)
			}
		}
	}

	slices.Sort(rules)
	return rules, nil
}

func scopeContainsWorkspace(scopes []*string, ws azcorearm.ResourceID) bool {
	wsID := strings.ToLower(ws.String())
	for _, s := range scopes {
		if s != nil && strings.ToLower(*s) == wsID {
			return true
		}
	}
	return false
}
