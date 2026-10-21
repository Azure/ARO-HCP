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

// newInitialServiceProviderNodePool returns a new ServiceProviderNodePool with
// the given resource ID as its parent. The resource ID is assumed to be a
// node pool resource ID.
// The returned value can be used to consistently initialize a new ServiceProviderNodePool
func newInitialServiceProviderNodePool(npResourceID *azcorearm.ResourceID) *coreapi.ServiceProviderNodePool {
	resourceID := metadataapi.Must(azcorearm.ParseResourceID(fmt.Sprintf("%s/%s/%s", npResourceID.String(), coreapi.ServiceProviderNodePoolResourceTypeName, coreapi.ServiceProviderNodePoolResourceName)))
	return &coreapi.ServiceProviderNodePool{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(resourceID.SubscriptionID),
		},
	}
}

// GetOrCreateServiceProviderNodePool gets the singleton ServiceProviderNodePool
// instance named `default` for the given node pool resource ID.
// If it doesn't exist, it creates a new one.
func GetOrCreateServiceProviderNodePool(
	ctx context.Context, dbClient ResourcesDBClient, nodePoolResourceID *azcorearm.ResourceID,
	secondAttempt ...bool,
) (*coreapi.ServiceProviderNodePool, error) {
	if !metadataapi.ResourceTypeEqual(nodePoolResourceID.ResourceType, coreapi.NodePoolResourceType) {
		return nil, utils.TrackError(fmt.Errorf("expected resource type %s, got %s", coreapi.NodePoolResourceType, nodePoolResourceID.ResourceType))
	}

	serviceProviderNodePoolsDBClient := dbClient.ServiceProviderNodePools(
		nodePoolResourceID.SubscriptionID,
		nodePoolResourceID.ResourceGroupName,
		nodePoolResourceID.Parent.Name,
		nodePoolResourceID.Name,
	)

	existingServiceProviderNodePool, err := serviceProviderNodePoolsDBClient.Get(ctx, coreapi.ServiceProviderNodePoolResourceName)
	switch {
	case err == nil:
		return existingServiceProviderNodePool, nil
	case cosmosstorageutils.IsNotFoundError(err):
		// fall through
	default:
		return nil, utils.TrackError(err)
	}

	initialServiceProviderNodePool := newInitialServiceProviderNodePool(nodePoolResourceID)
	existingServiceProviderNodePool, err = serviceProviderNodePoolsDBClient.Create(ctx, initialServiceProviderNodePool, nil)
	switch {
	case err == nil:
		return existingServiceProviderNodePool, nil
	case cosmosstorageutils.IsConflictError(err):
		// fall through
	default:
		return nil, utils.TrackError(err)
	}

	existingServiceProviderNodePool, err = serviceProviderNodePoolsDBClient.Get(ctx, coreapi.ServiceProviderNodePoolResourceName)
	switch {
	case err == nil:
		return existingServiceProviderNodePool, nil
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
			// This can happen when the soft-delete marks an item, the GET will return 404, the create will 409, the second get will 404.
			// By waiting longer than the cosmos TTL, we can re-enter the loop and try again later.  This is a rare case and will
			// only happen when the parent item exists and the controller was deleted.
			return GetOrCreateServiceProviderNodePool(ctx, dbClient, nodePoolResourceID, true)
		}
	default:
		return nil, utils.TrackError(err)
	}
}
