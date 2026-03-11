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

// ActiveOperationLister lists and gets active (non-terminal) operations from an informer's indexer.
type ActiveOperationLister interface {
	List(ctx context.Context) ([]*api.Operation, error)
	Get(ctx context.Context, subscriptionID, name string) (*api.Operation, error)
	ListActiveOperationsForCluster(ctx context.Context, subscriptionName, resourceGroupName, clusterName string) ([]*api.Operation, error)
}

// activeOperationLister implements ActiveOperationLister backed by a SharedIndexInformer.
type activeOperationLister struct {
	indexer cache.Indexer
}

// NewActiveOperationLister creates an ActiveOperationLister from a SharedIndexInformer's indexer.
func NewActiveOperationLister(indexer cache.Indexer) ActiveOperationLister {
	return &activeOperationLister{
		indexer: indexer,
	}
}

func (l *activeOperationLister) List(ctx context.Context) ([]*api.Operation, error) {
	return listAll[api.Operation](l.indexer)
}

// Get retrieves a single active operation by subscription ID and name.
// The store key is the lowercased ResourceID string:
//
//	/subscriptions/<sub>/providers/microsoft.redhatopenshift/hcpoperationstatuses/<name>
func (l *activeOperationLister) Get(ctx context.Context, subscriptionID, name string) (*api.Operation, error) {
	key := api.ToOperationResourceIDString(subscriptionID, name)
	return getByKey[api.Operation](l.indexer, key)
}

func (l *activeOperationLister) ListActiveOperationsForCluster(ctx context.Context, subscriptionName, resourceGroupName, clusterName string) ([]*api.Operation, error) {
	key := api.ToClusterResourceIDString(subscriptionName, resourceGroupName, clusterName)
	return listFromIndex[api.Operation](l.indexer, ByCluster, key)
}
