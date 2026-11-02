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

package azureresourceslisters

import (
	"context"
	"strings"

	"k8s.io/client-go/tools/cache"

	"github.com/Azure/ARO-HCP/internal/api/azureresourcesapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/coreapihelpers"
	"github.com/Azure/ARO-HCP/internal/database/listers/listerutils"
)

// DeploymentStackDesireLister returns read-only objects from an informer's indexer.
type DeploymentStackDesireLister interface {
	List(ctx context.Context) ([]*azureresourcesapi.DeploymentStackDesire, error)
	// GetByResourceID fetches a single DeploymentStackDesire by its full resource
	// ID string — the same lower-cased key the informer stores under. Callers
	// that derive the key generically (e.g. from a scope/parent resource ID) use
	// this parent-kind-agnostic accessor.
	GetByResourceID(ctx context.Context, resourceID string) (*azureresourcesapi.DeploymentStackDesire, error)
	// ListForCluster returns every DeploymentStackDesire whose ResourceID is a
	// direct child of the given Cluster. If in the future we need to match desires at the cluster scope level and below
	// in a single call, we should create a new index function and index name like for
	// separately named method and index (e.g. ListForClusterAndDescendants) instead of redefining this one.
	ListForCluster(ctx context.Context, subscriptionID, resourceGroupName, clusterName string) ([]*azureresourcesapi.DeploymentStackDesire, error)
}

type deploymentStackDesireLister struct {
	indexer cache.Indexer
}

func NewDeploymentStackDesireLister(indexer cache.Indexer) DeploymentStackDesireLister {
	return &deploymentStackDesireLister{indexer: indexer}
}

func (l *deploymentStackDesireLister) List(ctx context.Context) ([]*azureresourcesapi.DeploymentStackDesire, error) {
	return listerutils.ListAll[azureresourcesapi.DeploymentStackDesire](l.indexer)
}

func (l *deploymentStackDesireLister) GetByResourceID(ctx context.Context, resourceID string) (*azureresourcesapi.DeploymentStackDesire, error) {
	return listerutils.GetByKey[azureresourcesapi.DeploymentStackDesire](l.indexer, strings.ToLower(resourceID))
}

func (l *deploymentStackDesireLister) ListForCluster(ctx context.Context, subscriptionID, resourceGroupName, clusterName string) ([]*azureresourcesapi.DeploymentStackDesire, error) {
	key := coreapihelpers.ToClusterResourceIDString(subscriptionID, resourceGroupName, clusterName)
	return listerutils.ListFromIndex[azureresourcesapi.DeploymentStackDesire](l.indexer, ByCluster, key)
}
