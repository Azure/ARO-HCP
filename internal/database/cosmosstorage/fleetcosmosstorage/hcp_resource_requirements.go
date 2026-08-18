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

func newInitialHCPResourceRequirements(name string) *fleetapi.HCPResourceRequirements {
	resourceID := metadataapi.Must(fleetapi.ToHCPResourceRequirementsResourceID(name))
	return &fleetapi.HCPResourceRequirements{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(name),
		},
	}
}

// GetOrCreateHCPResourceRequirements gets the HCPResourceRequirements
// document with the given name. If it doesn't exist, it creates a new one.
func GetOrCreateHCPResourceRequirements(
	ctx context.Context, fleetDBClient FleetDBClient, name string,
) (*fleetapi.HCPResourceRequirements, error) {
	crud := fleetDBClient.HCPResourceRequirements()

	existing, err := crud.Get(ctx, name)
	switch {
	case err == nil:
		return existing, nil
	case cosmosstorageutils.IsNotFoundError(err):
		// fall through
	default:
		return nil, utils.TrackError(err)
	}

	initial := newInitialHCPResourceRequirements(name)
	existing, err = crud.Create(ctx, initial, nil)
	switch {
	case err == nil:
		return existing, nil
	case cosmosstorageutils.IsConflictError(err):
		// fall through — another controller won the race
	default:
		return nil, utils.TrackError(err)
	}

	existing, err = crud.Get(ctx, name)
	if err != nil {
		return nil, utils.TrackError(err)
	}
	return existing, nil
}
