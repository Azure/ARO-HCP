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

package namespacedeletion

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
)

// managedResourceGroupAbsent is a live Azure point read. Cosmos absence alone
// does not prove the managed resource group is gone: the orphaned MRG cleaner
// exists precisely because a resource group can outlive the cluster record.
func (s *namespaceDeletionSyncer) managedResourceGroupAbsent(ctx context.Context, hc hostedClusterObservation) (bool, error) {
	if hc.azureSubscriptionID == "" || hc.azureResourceGroup == "" {
		return false, fmt.Errorf("hosted cluster Azure subscription or managed resource group is missing")
	}
	if s.resourcesDBClient == nil || s.azureFPAClientBuilder == nil {
		return false, fmt.Errorf("managed resource group check is unavailable")
	}
	subscription, err := s.resourcesDBClient.Subscriptions().Get(ctx, hc.azureSubscriptionID)
	if err != nil {
		return false, fmt.Errorf("read subscription %q: %w", hc.azureSubscriptionID, err)
	}
	if subscription.Properties == nil || subscription.Properties.TenantId == nil || *subscription.Properties.TenantId == "" {
		return false, fmt.Errorf("subscription %q has no tenant ID", hc.azureSubscriptionID)
	}
	rgClient, err := s.azureFPAClientBuilder.ResourceGroupsClient(*subscription.Properties.TenantId, hc.azureSubscriptionID)
	if err != nil {
		return false, fmt.Errorf("create Azure resource groups client: %w", err)
	}
	_, err = rgClient.Get(ctx, hc.azureResourceGroup, nil)
	if err == nil {
		return false, nil
	}
	var responseError *azcore.ResponseError
	if errors.As(err, &responseError) && responseError.StatusCode == http.StatusNotFound {
		return true, nil
	}
	return false, fmt.Errorf("read managed resource group %q: %w", hc.azureResourceGroup, err)
}
