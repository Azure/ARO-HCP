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

// ReadDesireLister lists and gets ReadDesires from an informer's indexer.
type ReadDesireLister interface {
	List(ctx context.Context) ([]*kubeapplier.ReadDesire, error)
	GetForCluster(ctx context.Context, subscriptionID, resourceGroupName, clusterName, name string) (*kubeapplier.ReadDesire, error)
	GetForNodePool(ctx context.Context, subscriptionID, resourceGroupName, clusterName, nodePoolName, name string) (*kubeapplier.ReadDesire, error)
	ListForManagementCluster(ctx context.Context, managementCluster string) ([]*kubeapplier.ReadDesire, error)
	ListForCluster(ctx context.Context, subscriptionID, resourceGroupName, clusterName string) ([]*kubeapplier.ReadDesire, error)
	ListForNodePool(ctx context.Context, subscriptionID, resourceGroupName, clusterName, nodePoolName string) ([]*kubeapplier.ReadDesire, error)
}

type readDesireLister struct {
	indexer cache.Indexer
}

// NewReadDesireLister creates a ReadDesireLister from a SharedIndexInformer's indexer.
func NewReadDesireLister(indexer cache.Indexer) ReadDesireLister {
	return &readDesireLister{indexer: indexer}
}

func (l *readDesireLister) List(ctx context.Context) ([]*kubeapplier.ReadDesire, error) {
	return listAll[kubeapplier.ReadDesire](l.indexer)
}

func (l *readDesireLister) GetForCluster(
	ctx context.Context, subscriptionID, resourceGroupName, clusterName, name string,
) (*kubeapplier.ReadDesire, error) {
	key := kubeapplier.ToClusterScopedReadDesireResourceIDString(subscriptionID, resourceGroupName, clusterName, name)
	return getByKey[kubeapplier.ReadDesire](l.indexer, key)
}

func (l *readDesireLister) GetForNodePool(
	ctx context.Context, subscriptionID, resourceGroupName, clusterName, nodePoolName, name string,
) (*kubeapplier.ReadDesire, error) {
	key := kubeapplier.ToNodePoolScopedReadDesireResourceIDString(
		subscriptionID, resourceGroupName, clusterName, nodePoolName, name,
	)
	return getByKey[kubeapplier.ReadDesire](l.indexer, key)
}

func (l *readDesireLister) ListForManagementCluster(
	ctx context.Context, managementCluster string,
) ([]*kubeapplier.ReadDesire, error) {
	return listFromIndex[kubeapplier.ReadDesire](l.indexer, ByManagementCluster, strings.ToLower(managementCluster))
}

func (l *readDesireLister) ListForCluster(
	ctx context.Context, subscriptionID, resourceGroupName, clusterName string,
) ([]*kubeapplier.ReadDesire, error) {
	return listFromIndex[kubeapplier.ReadDesire](
		l.indexer, ByCluster, clusterIndexKey(subscriptionID, resourceGroupName, clusterName),
	)
}

func (l *readDesireLister) ListForNodePool(
	ctx context.Context, subscriptionID, resourceGroupName, clusterName, nodePoolName string,
) ([]*kubeapplier.ReadDesire, error) {
	return listFromIndex[kubeapplier.ReadDesire](
		l.indexer, ByNodePool, nodePoolIndexKey(subscriptionID, resourceGroupName, clusterName, nodePoolName),
	)
}
