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
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"

	"github.com/Azure/ARO-HCP/internal/utils"
)

type managedResourceGroupState int

const (
	managedResourceGroupStable managedResourceGroupState = iota
	managedResourceGroupDeleting
	managedResourceGroupInProgress
	managedResourceGroupAbsent
	// managedResourceGroupDeletionFailed is a distinct terminal state from
	// stable: the resource group is not deleting because a previous deletion
	// failed or was canceled. Treating it as stable would tell an operator to
	// wait and retry a deletion that Azure already rejected.
	managedResourceGroupDeletionFailed
)

// managedResourceGroupState is a live Azure point read. Cosmos absence alone
// does not prove the managed resource group is gone: the orphaned MRG cleaner
// exists precisely because a resource group can outlive the cluster record.
func (s *namespaceDeletionSyncer) readManagedResourceGroupState(ctx context.Context, hc hostedClusterObservation) (managedResourceGroupState, error) {
	if len(hc.azureSubscriptionID) == 0 || len(hc.azureResourceGroup) == 0 {
		return managedResourceGroupStable, utils.TrackError(fmt.Errorf("hosted cluster Azure subscription or managed resource group is missing"))
	}
	if s.resourcesDBClient == nil || s.azureFPAClientBuilder == nil {
		return managedResourceGroupStable, utils.TrackError(fmt.Errorf("managed resource group check is unavailable"))
	}
	subscription, err := s.resourcesDBClient.Subscriptions().Get(ctx, hc.azureSubscriptionID)
	if err != nil {
		return managedResourceGroupStable, utils.TrackError(fmt.Errorf("read subscription %q: %w", hc.azureSubscriptionID, err))
	}
	if subscription.Properties == nil || subscription.Properties.TenantId == nil || len(*subscription.Properties.TenantId) == 0 {
		return managedResourceGroupStable, utils.TrackError(fmt.Errorf("subscription %q has no tenant ID", hc.azureSubscriptionID))
	}
	rgClient, err := s.azureFPAClientBuilder.ResourceGroupsClient(*subscription.Properties.TenantId, hc.azureSubscriptionID)
	if err != nil {
		return managedResourceGroupStable, utils.TrackError(fmt.Errorf("create Azure resource groups client: %w", err))
	}
	response, err := rgClient.Get(ctx, hc.azureResourceGroup, nil)
	if err == nil {
		if response.Properties == nil || response.Properties.ProvisioningState == nil {
			return managedResourceGroupStable, utils.TrackError(fmt.Errorf("managed resource group %q response has no provisioning state", hc.azureResourceGroup))
		}
		switch strings.ToLower(*response.Properties.ProvisioningState) {
		case "deleting":
			return managedResourceGroupDeleting, nil
		case "creating", "updating", "accepted":
			return managedResourceGroupInProgress, nil
		case "succeeded":
			return managedResourceGroupStable, nil
		case "failed", "canceled":
			return managedResourceGroupDeletionFailed, nil
		default:
			return managedResourceGroupStable, utils.TrackError(fmt.Errorf("managed resource group %q has unrecognized provisioning state %q", hc.azureResourceGroup, *response.Properties.ProvisioningState))
		}
	}
	var responseError *azcore.ResponseError
	if errors.As(err, &responseError) && responseError.StatusCode == http.StatusNotFound {
		return managedResourceGroupAbsent, nil
	}
	return managedResourceGroupStable, utils.TrackError(fmt.Errorf("read managed resource group %q: %w", hc.azureResourceGroup, err))
}
