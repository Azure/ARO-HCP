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
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/monitor/armmonitor"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/prometheusrulegroups/armprometheusrulegroups"
)

func fetchAlertRules(ctx context.Context, cred azcore.TokenCredential, workspaceResourceID azcorearm.ResourceID) ([]string, error) {
	client, err := armprometheusrulegroups.NewClient(workspaceResourceID.SubscriptionID, cred, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create prometheus rule groups client: %w", err)
	}

	seen := make(map[string]bool)
	pager := client.NewListByResourceGroupPager(workspaceResourceID.ResourceGroupName, nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to list prometheus rule groups: %w", err)
		}
		for _, group := range page.Value {
			if group.Properties == nil || !scopeContainsWorkspace(group.Properties.Scopes, workspaceResourceID) {
				continue
			}
			for _, rule := range group.Properties.Rules {
				if rule.Alert != nil && *rule.Alert != "" {
					seen[*rule.Alert] = true
				}
			}
		}
	}

	rules := make([]string, 0, len(seen))
	for name := range seen {
		rules = append(rules, name)
	}
	slices.Sort(rules)
	return rules, nil
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
			return nil, fmt.Errorf("failed to list metric alert rules: %w", err)
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
