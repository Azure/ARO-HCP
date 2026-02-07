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

package listers

import (
	"context"

	"k8s.io/client-go/tools/cache"

	"github.com/Azure/ARO-HCP/internal/api"
)

// ExternalAuthLister lists and gets ExternalAuths from an informer's indexer.
type ExternalAuthLister interface {
	List(ctx context.Context) ([]*api.HCPOpenShiftClusterExternalAuth, error)
	Get(ctx context.Context, subscriptionID, resourceGroupName, clusterName, externalAuthName string) (*api.HCPOpenShiftClusterExternalAuth, error)
	ListForResourceGroup(ctx context.Context, subscriptionName, resourceGroupName string) ([]*api.HCPOpenShiftClusterExternalAuth, error)
	ListForCluster(ctx context.Context, subscriptionName, resourceGroupName, clusterName string) ([]*api.HCPOpenShiftClusterExternalAuth, error)
}

// hcpOpenShiftClusterExternalAuthLister implements ExternalAuthLister backed by a SharedIndexInformer.
type hcpOpenShiftClusterExternalAuthLister struct {
	indexer cache.Indexer
}

// NewExternalAuthLister creates an ExternalAuthLister from a SharedIndexInformer's indexer.
func NewExternalAuthLister(indexer cache.Indexer) ExternalAuthLister {
	return &hcpOpenShiftClusterExternalAuthLister{
		indexer: indexer,
	}
}

func (l *hcpOpenShiftClusterExternalAuthLister) List(ctx context.Context) ([]*api.HCPOpenShiftClusterExternalAuth, error) {
	return listAll[api.HCPOpenShiftClusterExternalAuth](l.indexer)
}

// Get retrieves a single HCPOpenShiftClusterExternalAuth by subscription ID, resource group name, cluster name, and external auth name.
// The store key is the lowercased ResourceID string:
//
//	/subscriptions/<sub>/resourcegroups/<rg>/providers/microsoft.redhatopenshift/hcpopenshiftclusters/<cluster>/externalauths/<name>
func (l *hcpOpenShiftClusterExternalAuthLister) Get(ctx context.Context, subscriptionID, resourceGroupName, clusterName, externalAuthName string) (*api.HCPOpenShiftClusterExternalAuth, error) {
	key := api.ToExternalAuthResourceIDString(subscriptionID, resourceGroupName, clusterName, externalAuthName)
	return getByKey[api.HCPOpenShiftClusterExternalAuth](l.indexer, key)
}

func (l *hcpOpenShiftClusterExternalAuthLister) ListForResourceGroup(ctx context.Context, subscriptionName, resourceGroupName string) ([]*api.HCPOpenShiftClusterExternalAuth, error) {
	key := api.ToResourceGroupResourceIDString(subscriptionName, resourceGroupName)
	return listFromIndex[api.HCPOpenShiftClusterExternalAuth](l.indexer, ByResourceGroup, key)
}

func (l *hcpOpenShiftClusterExternalAuthLister) ListForCluster(ctx context.Context, subscriptionName, resourceGroupName, clusterName string) ([]*api.HCPOpenShiftClusterExternalAuth, error) {
	key := api.ToClusterResourceIDString(subscriptionName, resourceGroupName, clusterName)
	return listFromIndex[api.HCPOpenShiftClusterExternalAuth](l.indexer, ByCluster, key)
}
