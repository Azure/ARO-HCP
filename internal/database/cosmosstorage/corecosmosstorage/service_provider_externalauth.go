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

package corecosmosstorage

import (
	"context"
	"fmt"
	"strings"
	"time"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// newInitialServiceProviderExternalAuth returns a new ServiceProviderExternalAuth with
// the given resource ID as its parent. The resource ID is assumed to be an
// external auth resource ID.
func newInitialServiceProviderExternalAuth(externalAuthResourceID *azcorearm.ResourceID) *coreapi.ServiceProviderExternalAuth {
	resourceID := metadataapi.Must(azcorearm.ParseResourceID(fmt.Sprintf("%s/%s/%s", externalAuthResourceID.String(), coreapi.ServiceProviderExternalAuthResourceTypeName, coreapi.ServiceProviderExternalAuthResourceName)))
	return &coreapi.ServiceProviderExternalAuth{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(resourceID.SubscriptionID),
		},
	}
}

// GetOrCreateServiceProviderExternalAuth gets the singleton ServiceProviderExternalAuth
// instance named `default` for the given external auth resource ID.
// If it doesn't exist, it creates a new one.
func GetOrCreateServiceProviderExternalAuth(
	ctx context.Context, dbClient ResourcesDBClient, externalAuthResourceID *azcorearm.ResourceID,
	secondAttempt ...bool,
) (*coreapi.ServiceProviderExternalAuth, error) {
	if !metadataapi.ResourceTypeEqual(externalAuthResourceID.ResourceType, coreapi.ExternalAuthResourceType) {
		return nil, utils.TrackError(fmt.Errorf("expected resource type %s, got %s", coreapi.ExternalAuthResourceType, externalAuthResourceID.ResourceType))
	}

	serviceProviderExternalAuthsDBClient := dbClient.ServiceProviderExternalAuths(
		externalAuthResourceID.SubscriptionID,
		externalAuthResourceID.ResourceGroupName,
		externalAuthResourceID.Parent.Name,
		externalAuthResourceID.Name,
	)

	existingServiceProviderExternalAuth, err := serviceProviderExternalAuthsDBClient.Get(ctx, coreapi.ServiceProviderExternalAuthResourceName)
	switch {
	case err == nil:
		return existingServiceProviderExternalAuth, nil
	case cosmosstorageutils.IsNotFoundError(err):
		// fall through
	default:
		return nil, utils.TrackError(err)
	}

	initialServiceProviderExternalAuth := newInitialServiceProviderExternalAuth(externalAuthResourceID)
	existingServiceProviderExternalAuth, err = serviceProviderExternalAuthsDBClient.Create(ctx, initialServiceProviderExternalAuth, nil)
	switch {
	case err == nil:
		return existingServiceProviderExternalAuth, nil
	case cosmosstorageutils.IsConflictError(err):
		// fall through
	default:
		return nil, utils.TrackError(err)
	}

	existingServiceProviderExternalAuth, err = serviceProviderExternalAuthsDBClient.Get(ctx, coreapi.ServiceProviderExternalAuthResourceName)
	switch {
	case err == nil:
		return existingServiceProviderExternalAuth, nil
	case cosmosstorageutils.IsNotFoundError(err):
		if len(secondAttempt) >= 1 && secondAttempt[0] {
			return nil, utils.TrackError(fmt.Errorf("second NotFound, Conflict, NotFound error: %w", err))
		}
		timer := time.NewTimer((cosmosstorageutils.SoftDeleteTTLSeconds + 1) * time.Second)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return nil, utils.TrackError(ctx.Err())
		case <-timer.C:
			return GetOrCreateServiceProviderExternalAuth(ctx, dbClient, externalAuthResourceID, true)
		}
	default:
		return nil, utils.TrackError(err)
	}
}
