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

	kubeapplierapi "github.com/Azure/ARO-HCP/internal/apis/kubeapplier"
)

// ApplyDesireLister lists and gets ApplyDesires from an informer's indexer.
type ApplyDesireLister interface {
	// List returns every ApplyDesire in the indexer.
	List(ctx context.Context) ([]*kubeapplierapi.ApplyDesire, error)

	// GetForCluster fetches a single cluster-scoped ApplyDesire by its
	// containing HCPOpenShiftCluster identity and the desire's name.
	GetForCluster(ctx context.Context, subscriptionID, resourceGroupName, clusterName, name string) (*kubeapplierapi.ApplyDesire, error)

	// GetForNodePool fetches a single nodepool-scoped ApplyDesire by its
	// containing NodePool identity and the desire's name.
	GetForNodePool(ctx context.Context, subscriptionID, resourceGroupName, clusterName, nodePoolName, name string) (*kubeapplierapi.ApplyDesire, error)

	// ListForManagementCluster returns every ApplyDesire whose
	// spec.managementCluster matches (case-insensitively).
	ListForManagementCluster(ctx context.Context, managementCluster string) ([]*kubeapplierapi.ApplyDesire, error)

	// ListForCluster returns every ApplyDesire under the given HCPOpenShiftCluster,
	// covering both cluster- and node-pool-scoped desires.
	ListForCluster(ctx context.Context, subscriptionID, resourceGroupName, clusterName string) ([]*kubeapplierapi.ApplyDesire, error)

	// ListForNodePool returns every node-pool-scoped ApplyDesire under the given NodePool.
	ListForNodePool(ctx context.Context, subscriptionID, resourceGroupName, clusterName, nodePoolName string) ([]*kubeapplierapi.ApplyDesire, error)
}

// applyDesireLister implements ApplyDesireLister backed by a SharedIndexInformer's indexer.
type applyDesireLister struct {
	indexer cache.Indexer
}

// NewApplyDesireLister creates an ApplyDesireLister from a SharedIndexInformer's indexer.
func NewApplyDesireLister(indexer cache.Indexer) ApplyDesireLister {
	return &applyDesireLister{indexer: indexer}
}

func (l *applyDesireLister) List(ctx context.Context) ([]*kubeapplierapi.ApplyDesire, error) {
	return listAll[kubeapplierapi.ApplyDesire](l.indexer)
}

func (l *applyDesireLister) GetForCluster(
	ctx context.Context, subscriptionID, resourceGroupName, clusterName, name string,
) (*kubeapplierapi.ApplyDesire, error) {
	key := kubeapplierapi.ToClusterScopedApplyDesireResourceIDString(subscriptionID, resourceGroupName, clusterName, name)
	return getByKey[kubeapplierapi.ApplyDesire](l.indexer, key)
}

func (l *applyDesireLister) GetForNodePool(
	ctx context.Context, subscriptionID, resourceGroupName, clusterName, nodePoolName, name string,
) (*kubeapplierapi.ApplyDesire, error) {
	key := kubeapplierapi.ToNodePoolScopedApplyDesireResourceIDString(
		subscriptionID, resourceGroupName, clusterName, nodePoolName, name,
	)
	return getByKey[kubeapplierapi.ApplyDesire](l.indexer, key)
}

func (l *applyDesireLister) ListForManagementCluster(
	ctx context.Context, managementCluster string,
) ([]*kubeapplierapi.ApplyDesire, error) {
	return listFromIndex[kubeapplierapi.ApplyDesire](l.indexer, ByManagementCluster, strings.ToLower(managementCluster))
}

func (l *applyDesireLister) ListForCluster(
	ctx context.Context, subscriptionID, resourceGroupName, clusterName string,
) ([]*kubeapplierapi.ApplyDesire, error) {
	return listFromIndex[kubeapplierapi.ApplyDesire](
		l.indexer, ByCluster, clusterIndexKey(subscriptionID, resourceGroupName, clusterName),
	)
}

func (l *applyDesireLister) ListForNodePool(
	ctx context.Context, subscriptionID, resourceGroupName, clusterName, nodePoolName string,
) ([]*kubeapplierapi.ApplyDesire, error) {
	return listFromIndex[kubeapplierapi.ApplyDesire](
		l.indexer, ByNodePool, nodePoolIndexKey(subscriptionID, resourceGroupName, clusterName, nodePoolName),
	)
}
