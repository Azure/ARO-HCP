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

// Package azureresourceslistertesting provides DB-backed and slice-backed test
// implementations of the AzureResources listers (DeploymentStackDesire).
package azureresourceslistertesting

import (
	"context"
	"strings"

	"github.com/Azure/ARO-HCP/internal/api/azureresourcesapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/azureresourcesapihelpers"
	"github.com/Azure/ARO-HCP/internal/apihelpers/coreapihelpers"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/listers/azureresourceslisters"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/listertestingutils"
)

// SliceDeploymentStackDesireLister implements
// azureresourceslisters.DeploymentStackDesireLister backed by a slice.
type SliceDeploymentStackDesireLister struct {
	DeploymentStackDesires []*azureresourcesapi.DeploymentStackDesire
}

var _ azureresourceslisters.DeploymentStackDesireLister = &SliceDeploymentStackDesireLister{}

func (l *SliceDeploymentStackDesireLister) List(ctx context.Context) ([]*azureresourcesapi.DeploymentStackDesire, error) {
	return l.DeploymentStackDesires, nil
}

func (l *SliceDeploymentStackDesireLister) GetByResourceID(ctx context.Context, resourceID string) (*azureresourcesapi.DeploymentStackDesire, error) {
	for _, d := range l.DeploymentStackDesires {
		if id := listertestingutils.ResourceIDOf(d); id != nil && strings.EqualFold(id.String(), resourceID) {
			return d, nil
		}
	}
	return nil, cosmosstorageutils.NewNotFoundError()
}

func (l *SliceDeploymentStackDesireLister) GetForCluster(ctx context.Context, subscriptionID, resourceGroupName, clusterName, name string) (*azureresourcesapi.DeploymentStackDesire, error) {
	key := azureresourcesapihelpers.ToClusterScopedDeploymentStackDesireResourceIDString(subscriptionID, resourceGroupName, clusterName, name)
	for _, d := range l.DeploymentStackDesires {
		if id := listertestingutils.ResourceIDOf(d); id != nil && strings.EqualFold(id.String(), key) {
			return d, nil
		}
	}
	return nil, cosmosstorageutils.NewNotFoundError()
}

// ListForCluster returns every DeploymentStackDesire whose ResourceID is a
// direct child of the given Cluster, matching the production index-backed
// lister's ByCluster semantics (direct children only, not nested scopes).
func (l *SliceDeploymentStackDesireLister) ListForCluster(ctx context.Context, subscriptionID, resourceGroupName, clusterName string) ([]*azureresourcesapi.DeploymentStackDesire, error) {
	clusterKey := coreapihelpers.ToClusterResourceIDString(subscriptionID, resourceGroupName, clusterName)
	var out []*azureresourcesapi.DeploymentStackDesire
	for _, d := range l.DeploymentStackDesires {
		rid := listertestingutils.ResourceIDOf(d)
		if rid != nil && rid.Parent != nil && strings.EqualFold(rid.Parent.String(), clusterKey) {
			out = append(out, d)
		}
	}
	return out, nil
}
