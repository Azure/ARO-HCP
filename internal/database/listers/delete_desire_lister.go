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
	"strings"

	"k8s.io/client-go/tools/cache"

	"github.com/Azure/ARO-HCP/internal/api/kubeapplier"
)

// DeleteDesireLister lists and gets DeleteDesires from an informer's indexer.
type DeleteDesireLister interface {
	List(ctx context.Context) ([]*kubeapplier.DeleteDesire, error)
	GetForCluster(ctx context.Context, subscriptionID, resourceGroupName, clusterName, name string) (*kubeapplier.DeleteDesire, error)
	GetForNodePool(ctx context.Context, subscriptionID, resourceGroupName, clusterName, nodePoolName, name string) (*kubeapplier.DeleteDesire, error)
	ListForManagementCluster(ctx context.Context, managementCluster string) ([]*kubeapplier.DeleteDesire, error)
	ListForCluster(ctx context.Context, subscriptionID, resourceGroupName, clusterName string) ([]*kubeapplier.DeleteDesire, error)
	ListForNodePool(ctx context.Context, subscriptionID, resourceGroupName, clusterName, nodePoolName string) ([]*kubeapplier.DeleteDesire, error)
}

type deleteDesireLister struct {
	indexer cache.Indexer
}

// NewDeleteDesireLister creates a DeleteDesireLister from a SharedIndexInformer's indexer.
func NewDeleteDesireLister(indexer cache.Indexer) DeleteDesireLister {
	return &deleteDesireLister{indexer: indexer}
}

func (l *deleteDesireLister) List(ctx context.Context) ([]*kubeapplier.DeleteDesire, error) {
	return listAll[kubeapplier.DeleteDesire](l.indexer)
}

func (l *deleteDesireLister) GetForCluster(
	ctx context.Context, subscriptionID, resourceGroupName, clusterName, name string,
) (*kubeapplier.DeleteDesire, error) {
	key := kubeapplier.ToClusterScopedDeleteDesireResourceIDString(subscriptionID, resourceGroupName, clusterName, name)
	return getByKey[kubeapplier.DeleteDesire](l.indexer, key)
}

func (l *deleteDesireLister) GetForNodePool(
	ctx context.Context, subscriptionID, resourceGroupName, clusterName, nodePoolName, name string,
) (*kubeapplier.DeleteDesire, error) {
	key := kubeapplier.ToNodePoolScopedDeleteDesireResourceIDString(
		subscriptionID, resourceGroupName, clusterName, nodePoolName, name,
	)
	return getByKey[kubeapplier.DeleteDesire](l.indexer, key)
}

func (l *deleteDesireLister) ListForManagementCluster(
	ctx context.Context, managementCluster string,
) ([]*kubeapplier.DeleteDesire, error) {
	return listFromIndex[kubeapplier.DeleteDesire](l.indexer, ByManagementCluster, strings.ToLower(managementCluster))
}

func (l *deleteDesireLister) ListForCluster(
	ctx context.Context, subscriptionID, resourceGroupName, clusterName string,
) ([]*kubeapplier.DeleteDesire, error) {
	return listFromIndex[kubeapplier.DeleteDesire](
		l.indexer, ByCluster, clusterIndexKey(subscriptionID, resourceGroupName, clusterName),
	)
}

func (l *deleteDesireLister) ListForNodePool(
	ctx context.Context, subscriptionID, resourceGroupName, clusterName, nodePoolName string,
) ([]*kubeapplier.DeleteDesire, error) {
	return listFromIndex[kubeapplier.DeleteDesire](
		l.indexer, ByNodePool, nodePoolIndexKey(subscriptionID, resourceGroupName, clusterName, nodePoolName),
	)
}
