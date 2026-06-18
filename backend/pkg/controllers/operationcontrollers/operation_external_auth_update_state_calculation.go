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

package operationcontrollers

import (
	"context"
	"fmt"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// cosmosHashOperationState checks whether the ServiceProviderExternalAuth hash
// matches the desired hash computed from the ExternalAuth in Cosmos. The
// comparison uses the stored version's field list so that a code deploy that
// changes the hash version does not produce a false "updating" state. If the
// hashes differ, the dispatch controller has not yet sent the CS PATCH, so the
// operation is still updating.
func (c *operationExternalAuthUpdate) cosmosHashOperationState(ctx context.Context, externalAuth *api.HCPOpenShiftClusterExternalAuth) (*operationState, error) {
	logger := utils.LoggerFromContext(ctx)

	serviceProviderExternalAuth, err := database.GetOrCreateServiceProviderExternalAuth(ctx, c.resourcesDBClient, externalAuth.ID)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to get service provider external auth: %w", err))
	}

	storedHash := serviceProviderExternalAuth.Status.ClusterServiceUpdatableConfigHashForUpdateDispatch
	storedVersionPtr := serviceProviderExternalAuth.Status.ClusterServiceUpdatableConfigHashVersionForUpdateDispatch
	storedVersion := ocm.ExternalAuthUpdatableConfigHashVersion
	if storedVersionPtr != nil {
		storedVersion = *storedVersionPtr
	}

	desiredHash, err := ocm.ExternalAuthUpdatableConfigHashForVersion(externalAuth, storedVersion)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to compute desired external auth hash: %w", err))
	}

	if storedHash != desiredHash {
		message := fmt.Sprintf("ServiceProviderExternalAuth hash %q does not match desired hash %q (version %d), waiting for dispatch", storedHash, desiredHash, storedVersion)
		logger.Info(message)
		return newOperationState(arm.ProvisioningStateUpdating, message), nil
	}

	return newOperationState(arm.ProvisioningStateSucceeded, ""), nil
}
