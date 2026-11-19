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

package azureresourceslistertesting

import (
	"context"
	"strings"

	"github.com/Azure/ARO-HCP/internal/api/azureresourcesapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/coreapihelpers"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/azureresourcescosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/listers/azureresourceslisters"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/listertestingutils"
)

// DBDeploymentStackDesireLister implements
// azureresourceslisters.DeploymentStackDesireLister backed by an
// azureresourcescosmosstorage.AzureResourcesDBClient (e.g. the AzureResources
// mock). Reads go through the backing store so the returned objects carry the
// store's current etags, which lets a controller's read-modify-Replace cycle
// round-trip against the same mock.
type DBDeploymentStackDesireLister struct {
	AzureResourcesDBClient azureresourcescosmosstorage.AzureResourcesDBClient
}

var _ azureresourceslisters.DeploymentStackDesireLister = &DBDeploymentStackDesireLister{}

func (l *DBDeploymentStackDesireLister) List(ctx context.Context) ([]*azureresourcesapi.DeploymentStackDesire, error) {
	iter, err := l.AzureResourcesDBClient.GlobalListers().DeploymentStackDesires().List(ctx, nil)
	if err != nil {
		return nil, err
	}
	return listertestingutils.CollectFromIterator(ctx, iter)
}

func (l *DBDeploymentStackDesireLister) GetByResourceID(ctx context.Context, resourceID string) (*azureresourcesapi.DeploymentStackDesire, error) {
	all, err := l.List(ctx)
	if err != nil {
		return nil, err
	}
	for _, d := range all {
		if id := listertestingutils.ResourceIDOf(d); id != nil && strings.EqualFold(id.String(), resourceID) {
			return d, nil
		}
	}
	return nil, cosmosstorageutils.NewNotFoundError()
}

func (l *DBDeploymentStackDesireLister) GetForCluster(ctx context.Context, subscriptionID, resourceGroupName, clusterName, name string) (*azureresourcesapi.DeploymentStackDesire, error) {
	crud, err := l.AzureResourcesDBClient.DeploymentStackDesiresForCluster(subscriptionID, resourceGroupName, clusterName)
	if err != nil {
		return nil, err
	}
	return crud.Get(ctx, name)
}

// ListForCluster returns every DeploymentStackDesire whose ResourceID is a
// direct child of the given Cluster, matching the production index-backed
// lister's ByCluster semantics (direct children only, not nested scopes).
func (l *DBDeploymentStackDesireLister) ListForCluster(ctx context.Context, subscriptionID, resourceGroupName, clusterName string) ([]*azureresourcesapi.DeploymentStackDesire, error) {
	all, err := l.List(ctx)
	if err != nil {
		return nil, err
	}
	clusterKey := coreapihelpers.ToClusterResourceIDString(subscriptionID, resourceGroupName, clusterName)
	var out []*azureresourcesapi.DeploymentStackDesire
	for _, d := range all {
		rid := listertestingutils.ResourceIDOf(d)
		if rid != nil && rid.Parent != nil && strings.EqualFold(rid.Parent.String(), clusterKey) {
			out = append(out, d)
		}
	}
	return out, nil
}
