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

package azureresourcescosmosstorage

import (
	"errors"
	"fmt"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/coreapihelpers"
)

// DeploymentStackDesireScope is a validated parent resource that scopes a
// DeploymentStackDesire. Its resourceID is unexported and only set by the
// constructors below or by ParseDeploymentStackDesireScope, so an invalid
// scope cannot be represented. Unlike kubeappliercosmosstorage.DesireScope,
// there is no Ancestry concept here: every known and anticipated parent
// (Cluster today, NodePool in the future) is ARM/subscription-rooted and
// uses the same partition-key and resource-ID-path strategies.
type DeploymentStackDesireScope struct {
	resourceID *azcorearm.ResourceID
}

// ResourceID returns the scope's resource ID.
func (s DeploymentStackDesireScope) ResourceID() *azcorearm.ResourceID { return s.resourceID }

// ClusterScope returns a DeploymentStackDesireScope for a cluster parent.
func ClusterScope(subscriptionID, resourceGroupName, clusterName string) (DeploymentStackDesireScope, error) {
	id, err := coreapihelpers.ToClusterResourceID(subscriptionID, resourceGroupName, clusterName)
	if err != nil {
		return DeploymentStackDesireScope{}, err
	}
	return ParseDeploymentStackDesireScope(id)
}

// ParseDeploymentStackDesireScope categorizes a raw parent resource ID into a
// DeploymentStackDesireScope, rejecting any resource type not allowed to scope
// a DeploymentStackDesire.
func ParseDeploymentStackDesireScope(id *azcorearm.ResourceID) (DeploymentStackDesireScope, error) {
	if id == nil {
		return DeploymentStackDesireScope{}, errors.New("deployment stack desire scope resource ID is nil")
	}
	if !metadataapi.ResourceTypeEqual(id.ResourceType, coreapi.ClusterResourceType) {
		// extend this check when other desire scopes are added.
		return DeploymentStackDesireScope{}, fmt.Errorf("resource type %q is not a valid deployment stack desire scope", id.ResourceType)
	}
	return DeploymentStackDesireScope{resourceID: id}, nil
}
