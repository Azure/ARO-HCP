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

package gatherobservability

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
)

// resourceGroupExists reports whether a resource group currently exists. It is a
// single HEAD request (ResourceGroups/CheckExistence) against the resource
// group's own scope, which is the condition the collector actually cares about:
// "is there an environment to observe?".
//
// Do not infer this from the alert APIs. Listing alerts for a resource group
// that does not exist returns an empty 200, and only the metric-rule,
// prometheusRuleGroup and monitoring-workspace calls 404, so error-code sniffing
// on any single collector call is unreliable.
func resourceGroupExists(ctx context.Context, cred azcore.TokenCredential, scope string) (bool, error) {
	id, err := azcorearm.ParseResourceID(scope)
	if err != nil {
		return false, fmt.Errorf("failed to parse resource group ID %s: %w", scope, err)
	}
	client, err := armresources.NewResourceGroupsClient(id.SubscriptionID, cred, nil)
	if err != nil {
		return false, fmt.Errorf("failed to create resource group client: %w", err)
	}
	resp, err := client.CheckExistence(ctx, id.ResourceGroupName, nil)
	if err != nil {
		return false, err
	}
	return resp.Success, nil
}
