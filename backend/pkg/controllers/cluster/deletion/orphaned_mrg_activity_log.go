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

package deletion

import (
	"context"
	"fmt"
	"strings"
	"time"

	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/monitor/armmonitor"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	activityLogLookback = 24 * time.Hour
	activityLogTimeout  = 20 * time.Second
	resourceGroupDelete = "Microsoft.Resources/subscriptions/resourceGroups/delete"
)

// Activity Log submission can lag behind the ARM operation. A missing or unreadable
// event is diagnostic only and must not prevent another deletion attempt.
func (c *orphanedManagedResourceGroupController) logFailedMRGDeletion(
	ctx context.Context, tenantID string, key controllerutils.ManagedResourceGroupKey, resourceID *string,
) {
	logger := utils.LoggerFromContext(ctx)
	client, err := c.azureFPAClientBuilder.ActivityLogsClient(tenantID, key.SubscriptionID)
	if err != nil {
		logger.Error(err, "Unable to create Activity Log client for failed MRG deletion")
		return
	}

	rgID := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s", key.SubscriptionID, key.ResourceGroupName)
	if resourceID != nil && *resourceID != "" {
		rgID = *resourceID
	}
	queryCtx, cancel := context.WithTimeout(ctx, activityLogTimeout)
	defer cancel()
	event, err := latestFailedResourceGroupDelete(queryCtx, client, rgID, time.Now().UTC())
	if err != nil {
		logger.Error(err, "Unable to read Activity Log for failed MRG deletion")
		return
	}
	if event == nil {
		logger.Info("MRG is Failed, but no failed resource group deletion event is available in the Activity Log")
		return
	}

	var timestamp any
	if event.EventTimestamp != nil {
		timestamp = event.EventTimestamp.UTC()
	}
	logger.Info("MRG deletion failure found in Activity Log",
		"eventTimestamp", timestamp,
		"correlationID", ptr.Deref(event.CorrelationID, ""),
		"status", localizableValue(event.Status),
		"subStatus", localizableValue(event.SubStatus),
		"statusCode", ptr.Deref(event.Properties["statusCode"], ""),
		"description", ptr.Deref(event.Description, ""),
		"statusMessage", ptr.Deref(event.Properties["statusMessage"], ""),
	)
}

func latestFailedResourceGroupDelete(
	ctx context.Context, client azureclient.ActivityLogsClient, resourceID string, now time.Time,
) (*armmonitor.EventData, error) {
	start := now.Add(-activityLogLookback).Format(time.RFC3339)
	end := now.Format(time.RFC3339)
	filter := fmt.Sprintf("eventTimestamp ge '%s' and eventTimestamp le '%s' and resourceUri eq '%s'",
		start, end, strings.ReplaceAll(resourceID, "'", "''"))
	pager := client.NewListPager(filter, &armmonitor.ActivityLogsClientListOptions{
		Select: ptr.To("eventTimestamp,operationName,status,subStatus,description,properties,correlationId,resourceId"),
	})
	var latest *armmonitor.EventData
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, event := range page.Value {
			if event == nil || !strings.EqualFold(localizableValue(event.OperationName), resourceGroupDelete) ||
				!strings.EqualFold(localizableValue(event.Status), "Failed") {
				continue
			}
			if latest == nil || (event.EventTimestamp != nil &&
				(latest.EventTimestamp == nil || event.EventTimestamp.After(*latest.EventTimestamp))) {
				latest = event
			}
		}
	}
	return latest, nil
}

func localizableValue(value *armmonitor.LocalizableString) string {
	if value == nil {
		return ""
	}
	return ptr.Deref(value.Value, "")
}
