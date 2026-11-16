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

package azureresourcesinformers

import (
	"fmt"
	"strings"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// clusterResourceIDIndexFunc returns the lower-cased resource ID of the object
// itself if it is a Cluster, or of its direct parent if that parent is a
// Cluster.
func clusterResourceIDIndexFunc(obj interface{}) ([]string, error) {
	switch castObj := obj.(type) {
	case coreapi.CosmosMetadataAccessor:
		return clusterResourceIDFromResourceID(castObj.GetResourceID())
	case coreapi.CosmosPersistable:
		return clusterResourceIDFromResourceID(castObj.GetCosmosData().ResourceID)
	default:
		return nil, utils.TrackError(fmt.Errorf("unexpected type %T, expected coreapi.CosmosMetadataAccessor or coreapi.CosmosPersistable", obj))
	}
}

func clusterResourceIDFromResourceID(resourceID *azcorearm.ResourceID) ([]string, error) {
	return selfOrDirectParentResourceID(coreapi.ClusterResourceType, resourceID)
}

// selfOrDirectParentResourceID returns the lowercased resource ID string of
// either resourceID itself (when its type matches) or its direct Parent (when
// the parent's type matches). It is non-recursive on purpose: indexing by
// "self-or-direct-parent" gives ListFor<Cluster|NodePool|ExternalAuth> the
// "direct child only" semantics we want, so e.g. a Controller hanging off a
// NodePool is indexed under that NodePool but NOT under the grandparent
// Cluster. If a future caller needs a deeper-ancestor lookup, add a separate
// helper for that case rather than reintroducing recursion here.
func selfOrDirectParentResourceID(resourceType azcorearm.ResourceType, resourceID *azcorearm.ResourceID) ([]string, error) {
	if resourceID == nil {
		return nil, nil
	}
	if metadataapi.ResourceTypeEqual(resourceID.ResourceType, resourceType) {
		return []string{strings.ToLower(resourceID.String())}, nil
	}
	if resourceID.Parent == nil {
		return nil, nil
	}
	if metadataapi.ResourceTypeEqual(resourceID.Parent.ResourceType, resourceType) {
		return []string{strings.ToLower(resourceID.Parent.String())}, nil
	}
	return nil, nil
}
