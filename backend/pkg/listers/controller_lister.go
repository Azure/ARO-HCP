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

	resourcesapi "github.com/Azure/ARO-HCP/internal/apis/resources"
)

// ControllerLister lists and gets Controllers from an informer's indexer.
type ControllerLister interface {
	List(ctx context.Context) ([]*resourcesapi.Controller, error)
	ListForResourceGroup(ctx context.Context, subscriptionName, resourceGroupName string) ([]*resourcesapi.Controller, error)
	ListForCluster(ctx context.Context, subscriptionName, resourceGroupName, clusterName string) ([]*resourcesapi.Controller, error)
	ListForNodePool(ctx context.Context, subscriptionName, resourceGroupName, clusterName, nodePoolName string) ([]*resourcesapi.Controller, error)
	ListForExternalAuth(ctx context.Context, subscriptionName, resourceGroupName, clusterName, externalAuthName string) ([]*resourcesapi.Controller, error)
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

func (l *controllerLister) List(ctx context.Context) ([]*resourcesapi.Controller, error) {
	return listAll[resourcesapi.Controller](l.indexer)
}

func (l *controllerLister) ListForResourceGroup(ctx context.Context, subscriptionName, resourceGroupName string) ([]*resourcesapi.Controller, error) {
	key := resourcesapi.ToResourceGroupResourceIDString(subscriptionName, resourceGroupName)
	return listFromIndex[resourcesapi.Controller](l.indexer, ByResourceGroup, key)
}

func (l *controllerLister) ListForCluster(ctx context.Context, subscriptionName, resourceGroupName, clusterName string) ([]*resourcesapi.Controller, error) {
	key := resourcesapi.ToClusterResourceIDString(subscriptionName, resourceGroupName, clusterName)
	return listFromIndex[resourcesapi.Controller](l.indexer, ByCluster, key)
}

func (l *controllerLister) ListForNodePool(ctx context.Context, subscriptionName, resourceGroupName, clusterName, nodePoolName string) ([]*resourcesapi.Controller, error) {
	key := resourcesapi.ToNodePoolResourceIDString(subscriptionName, resourceGroupName, clusterName, nodePoolName)
	return listFromIndex[resourcesapi.Controller](l.indexer, ByNodePool, key)
}

func (l *controllerLister) ListForExternalAuth(ctx context.Context, subscriptionName, resourceGroupName, clusterName, externalAuthName string) ([]*resourcesapi.Controller, error) {
	key := resourcesapi.ToExternalAuthResourceIDString(subscriptionName, resourceGroupName, clusterName, externalAuthName)
	return listFromIndex[resourcesapi.Controller](l.indexer, ByExternalAuth, key)
}
