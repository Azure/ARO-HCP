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
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
)

// resourceGroupProbeTimeout bounds the existence probe, including SDK retries.
const resourceGroupProbeTimeout = 30 * time.Second

// resourceGroupExists issues ResourceGroups/CheckExistence. The alert APIs cannot
// answer this: listing alerts for a missing resource group returns an empty 200.
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
