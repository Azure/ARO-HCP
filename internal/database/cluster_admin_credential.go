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

package database

import (
	"fmt"
	"strings"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/internal/utils/armhelpers"
)

// NewClusterAdminCredential builds a ClusterAdminCredential document keyed by
// the CS break-glass credential ID (csInternalID.ID()).
// TODO do we want this in this package?
func NewClusterAdminCredential(clusterResourceID *azcorearm.ResourceID, csInternalID api.InternalID, operationID string) (*api.ClusterAdminCredential, error) {
	if !armhelpers.ResourceTypeEqual(clusterResourceID.ResourceType, api.ClusterResourceType) {
		return nil, utils.TrackError(fmt.Errorf("expected resource type %s, got %s", api.ClusterResourceType, clusterResourceID.ResourceType))
	}

	name := csInternalID.ID()
	resourceID, err := azcorearm.ParseResourceID(api.ToAdminCredentialResourceIDString(
		clusterResourceID.SubscriptionID,
		clusterResourceID.ResourceGroupName,
		clusterResourceID.Name,
		name,
	))
	if err != nil {
		return nil, utils.TrackError(err)
	}
	return &api.ClusterAdminCredential{
		CosmosMetadata: api.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(resourceID.SubscriptionID),
		},
		OperationID:              operationID,
		ClusterServiceInternalID: csInternalID,
	}, nil
}
