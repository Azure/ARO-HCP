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

// ControllerLister lists and gets Controllers from an informer's indexer.
type ControllerLister interface {
	List(ctx context.Context) ([]*api.Controller, error)
	ListForResourceGroup(ctx context.Context, subscriptionName, resourceGroupName string) ([]*api.Controller, error)
	ListForCluster(ctx context.Context, subscriptionName, resourceGroupName, clusterName string) ([]*api.Controller, error)
	ListForNodePool(ctx context.Context, subscriptionName, resourceGroupName, clusterName, nodePoolName string) ([]*api.Controller, error)
	ListForExternalAuth(ctx context.Context, subscriptionName, resourceGroupName, clusterName, externalAuthName string) ([]*api.Controller, error)
}

// controllerLister implements ControllerLister backed by a SharedIndexInformer.
type controllerLister struct {
	indexer cache.Indexer
}

// NewControllerLister creates a ControllerLister from a SharedIndexInformer's indexer.
func NewControllerLister(indexer cache.Indexer) ControllerLister {
	return &controllerLister{
		indexer: indexer,
	}
}

func (l *controllerLister) List(ctx context.Context) ([]*api.Controller, error) {
	return listAll[api.Controller](l.indexer)
}

func (l *controllerLister) ListForResourceGroup(ctx context.Context, subscriptionName, resourceGroupName string) ([]*api.Controller, error) {
	key := api.ToResourceGroupResourceIDString(subscriptionName, resourceGroupName)
	return listFromIndex[api.Controller](l.indexer, ByResourceGroup, key)
}

func (l *controllerLister) ListForCluster(ctx context.Context, subscriptionName, resourceGroupName, clusterName string) ([]*api.Controller, error) {
	key := api.ToClusterResourceIDString(subscriptionName, resourceGroupName, clusterName)
	return listFromIndex[api.Controller](l.indexer, ByCluster, key)
}

func (l *controllerLister) ListForNodePool(ctx context.Context, subscriptionName, resourceGroupName, clusterName, nodePoolName string) ([]*api.Controller, error) {
	key := api.ToNodePoolResourceIDString(subscriptionName, resourceGroupName, clusterName, nodePoolName)
	return listFromIndex[api.Controller](l.indexer, ByNodePool, key)
}

func (l *controllerLister) ListForExternalAuth(ctx context.Context, subscriptionName, resourceGroupName, clusterName, externalAuthName string) ([]*api.Controller, error) {
	key := api.ToExternalAuthResourceIDString(subscriptionName, resourceGroupName, clusterName, externalAuthName)
	return listFromIndex[api.Controller](l.indexer, ByExternalAuth, key)
}
