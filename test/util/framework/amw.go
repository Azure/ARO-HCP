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

package framework

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/go-logr/logr"

	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/alertsmanagement/armalertsmanagement"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"

	"github.com/Azure/ARO-HCP/test/util/config"
)

// AzureMonitorWorkspace contains the workspace details and clients used to deploy rules
// and observe alert instances for an Azure Monitor workspace.
type AzureMonitorWorkspace struct {
	SubscriptionID string
	ResourceGroup  string
	WorkspaceName  string
	Resources      *armresources.Client
	alerts         *armalertsmanagement.AlertsClient
}

// ResourceID returns the workspace's Azure resource ID.
func (w *AzureMonitorWorkspace) ResourceID() string {
	return fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Monitor/accounts/%s", w.SubscriptionID, w.ResourceGroup, w.WorkspaceName)
}

// GetHCPMonitoringWorkspace resolves the configured HCP workspace and creates its clients.
func (tc *perItOrDescribeTestContext) GetHCPMonitoringWorkspace(ctx context.Context) (*AzureMonitorWorkspace, error) {
	serviceConfig, err := config.GetServiceConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to load service configuration: %w", err)
	}
	regionRG, err := config.GetStringByPath(serviceConfig, "regionRG")
	if err != nil {
		return nil, fmt.Errorf("failed to resolve regional resource group: %w", err)
	}
	workspaceName, err := config.GetStringByPath(serviceConfig, "monitoring.hcpWorkspaceName")
	if err != nil {
		return nil, fmt.Errorf("failed to resolve HCP AMW name: %w", err)
	}
	subscriptionName, err := config.GetStringByPath(serviceConfig, "svc.subscription.key")
	if err != nil {
		return nil, fmt.Errorf("failed to resolve AMW subscription: %w", err)
	}
	subscriptions, err := tc.GetARMSubscriptionsClientFactory()
	if err != nil {
		return nil, fmt.Errorf("failed to create subscription client: %w", err)
	}
	subscriptionID, err := GetSubscriptionID(ctx, subscriptions.NewClient(), subscriptionName)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve AMW subscription ID: %w", err)
	}
	cred, err := tc.AzureCredential()
	if err != nil {
		return nil, fmt.Errorf("failed to obtain Azure credentials: %w", err)
	}
	resources, err := armresources.NewClient(subscriptionID, cred, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create rule deployment client: %w", err)
	}
	workspace := &AzureMonitorWorkspace{
		SubscriptionID: subscriptionID,
		ResourceGroup:  regionRG,
		WorkspaceName:  workspaceName,
		Resources:      resources,
	}
	workspace.alerts, err = armalertsmanagement.NewAlertsClient(workspace.ResourceID(), cred, nil)
	if err != nil {
		return nil, fmt.Errorf("create Azure alerts client: %w", err)
	}
	return workspace, nil
}

// RuleGroupResourceID returns the ID of a rule group in the workspace's resource group.
func (w *AzureMonitorWorkspace) RuleGroupResourceID(name string) string {
	return fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.AlertsManagement/prometheusRuleGroups/%s", w.SubscriptionID, w.ResourceGroup, name)
}

// CreateOrUpdateRuleGroup scopes a rule group to this workspace and waits for its deployment.
// The caller supplies the definition and controls the deadline through ctx.
func (w *AzureMonitorWorkspace) CreateOrUpdateRuleGroup(ctx context.Context, name string, definition armresources.GenericResource) error {
	properties, ok := definition.Properties.(map[string]any)
	if !ok {
		return fmt.Errorf("rule group %s properties must be a map", name)
	}
	properties = maps.Clone(properties)
	properties["scopes"] = []string{w.ResourceID()}
	definition.Properties = properties
	poller, err := w.Resources.BeginCreateOrUpdateByID(ctx, w.RuleGroupResourceID(name), "2023-03-01", definition, nil)
	if err != nil {
		return fmt.Errorf("deploy rule group %s: %w", name, err)
	}
	_, err = poller.PollUntilDone(ctx, nil)
	if err != nil {
		return fmt.Errorf("wait for rule group %s deployment: %w", name, err)
	}
	return nil
}

// DeleteRuleGroup waits for deletion; an already absent group counts as success.
func (w *AzureMonitorWorkspace) DeleteRuleGroup(ctx context.Context, name string) error {
	poller, err := w.Resources.BeginDeleteByID(ctx, w.RuleGroupResourceID(name), "2023-03-01", nil)
	if err == nil {
		_, err = poller.PollUntilDone(ctx, nil)
	}
	var responseError *azcore.ResponseError
	if errors.As(err, &responseError) && responseError.StatusCode == http.StatusNotFound {
		return nil
	}
	if err != nil {
		return fmt.Errorf("delete rule group %s: %w", name, err)
	}
	return nil
}

// WaitForAlerts polls Azure alert instances scoped to this workspace every 30 seconds.
// Alerts are matched by the context's HCP resource_id and alertname, ignoring case.
// Each map value specifies whether any matching instance must be Fired; false
// accepts Resolved or no instance. Azure does not expose Prometheus pending state here.
// State changes, instance IDs, Azure timestamps and elapsed wait time are logged.
func (w *AzureMonitorWorkspace) WaitForAlerts(ctx context.Context, resourceID string, expected map[string]bool, timeout time.Duration, logger logr.Logger) error {
	formatTime := func(value *time.Time) string {
		if value == nil {
			return "not reported"
		}
		return value.UTC().Format(time.RFC3339)
	}
	started := time.Now()
	last := ""
	logger.Info("Waiting for Azure alert states", "expected_firing", expected, "resource_id", resourceID)
	err := wait.PollUntilContextTimeout(ctx, 30*time.Second, timeout, true, func(ctx context.Context) (bool, error) {
		observed := map[string][]string{}
		firing := map[string]bool{}
		queryErr := func() error {
			pager := w.alerts.NewGetAllPager(&armalertsmanagement.AlertsClientGetAllOptions{IncludeContext: to.Ptr(true)})
			for pager.More() {
				page, err := pager.NextPage(ctx)
				if err != nil {
					return err
				}
				for _, alert := range page.Value {
					if alert.Properties == nil {
						continue
					}
					raw, err := json.Marshal(alert.Properties.Context)
					if err != nil {
						return fmt.Errorf("read alert context: %w", err)
					}
					var details struct {
						Labels map[string]string `json:"labels"`
					}
					if err := json.Unmarshal(raw, &details); err != nil {
						return fmt.Errorf("read alert labels: %w", err)
					}
					if !strings.EqualFold(details.Labels["resource_id"], resourceID) {
						continue
					}
					for name := range expected {
						if !strings.EqualFold(details.Labels["alertname"], name) {
							continue
						}
						instanceID := "not reported"
						if alert.ID != nil {
							instanceID = *alert.ID
						}
						essentials := alert.Properties.Essentials
						if essentials == nil || essentials.MonitorCondition == nil {
							return fmt.Errorf("alert %s has no monitor condition", instanceID)
						}
						condition := *essentials.MonitorCondition
						if condition != armalertsmanagement.MonitorConditionFired && condition != armalertsmanagement.MonitorConditionResolved {
							return fmt.Errorf("alert %s has unexpected monitor condition %s", instanceID, condition)
						}
						if condition == armalertsmanagement.MonitorConditionFired {
							firing[name] = true
						}
						observed[name] = append(observed[name], fmt.Sprintf("%s id=%s started=%s modified=%s resolved=%s", condition, instanceID,
							formatTime(essentials.StartDateTime),
							formatTime(essentials.LastModifiedDateTime),
							formatTime(essentials.MonitorConditionResolvedDateTime)))
					}
				}
			}
			return nil
		}()
		state := ""
		matched := queryErr == nil
		if queryErr != nil {
			state = "query failed: " + queryErr.Error()
		} else {
			observations := make([]string, 0, len(expected))
			for name, wantFiring := range expected {
				instances := observed[name]
				slices.Sort(instances)
				detail := "absent"
				if len(instances) > 0 {
					detail = strings.Join(instances, ", ")
				}
				observations = append(observations, name+": "+detail)
				if firing[name] != wantFiring {
					matched = false
				}
			}
			slices.Sort(observations)
			state = strings.Join(observations, "; ")
		}
		if state != last {
			logger.Info("Observed Azure alert states", "elapsed", time.Since(started).String(), "alerts", state)
			last = state
		}
		return matched, nil
	})
	if err != nil {
		return fmt.Errorf("waiting for Azure alert states %v: %w; last observation: %s", expected, err, last)
	}
	logger.Info("Expected Azure alert states observed", "duration", time.Since(started).String(), "expected_firing", expected)
	return nil
}
