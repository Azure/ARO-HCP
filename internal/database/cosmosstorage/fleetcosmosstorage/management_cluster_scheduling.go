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

package fleetcosmosstorage

import (
	"context"
	"strings"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func newInitialManagementClusterScheduling(stampIdentifier string) *fleetapi.ManagementClusterScheduling {
	resourceID := metadataapi.Must(fleetapi.ToManagementClusterSchedulingResourceID(stampIdentifier))
	return &fleetapi.ManagementClusterScheduling{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(stampIdentifier),
		},
	}
}

// GetOrCreateManagementClusterScheduling gets the singleton
// ManagementClusterScheduling instance named "default" for the given stamp.
// If it doesn't exist, it creates a new one.
func GetOrCreateManagementClusterScheduling(
	ctx context.Context, fleetDBClient FleetDBClient, stampIdentifier string,
) (*fleetapi.ManagementClusterScheduling, error) {
	schedulingCRUD := fleetDBClient.Stamps().ManagementClusters(stampIdentifier).Scheduling()

	existing, err := schedulingCRUD.Get(ctx, fleetapi.SchedulingResourceName)
	switch {
	case err == nil:
		return existing, nil
	case cosmosstorageutils.IsNotFoundError(err):
		// fall through
	default:
		return nil, utils.TrackError(err)
	}

	initial := newInitialManagementClusterScheduling(stampIdentifier)
	existing, err = schedulingCRUD.Create(ctx, initial, nil)
	switch {
	case err == nil:
		return existing, nil
	case cosmosstorageutils.IsConflictError(err):
		// fall through — another controller won the race
	default:
		return nil, utils.TrackError(err)
	}

	existing, err = schedulingCRUD.Get(ctx, fleetapi.SchedulingResourceName)
	if err != nil {
		return nil, utils.TrackError(err)
	}
	return existing, nil
}
