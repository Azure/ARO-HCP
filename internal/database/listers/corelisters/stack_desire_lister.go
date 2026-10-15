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

package corelisters

import (
	"context"

	"k8s.io/client-go/tools/cache"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/coreapihelpers"
	"github.com/Azure/ARO-HCP/internal/database/listers/listerutils"
)

// StackDesireLister returns read-only objects from an informer's indexer.
type StackDesireLister interface {
	List(ctx context.Context) ([]*coreapi.StackDesire, error)
	Get(ctx context.Context, subscriptionID, resourceGroupName, clusterName, name string) (*coreapi.StackDesire, error)
	ListForCluster(ctx context.Context, subscriptionID, resourceGroupName, clusterName string) ([]*coreapi.StackDesire, error)
}

type stackDesireLister struct {
	indexer cache.Indexer
}

func NewStackDesireLister(indexer cache.Indexer) StackDesireLister {
	return &stackDesireLister{indexer: indexer}
}

func (l *stackDesireLister) List(ctx context.Context) ([]*coreapi.StackDesire, error) {
	return listerutils.ListAll[coreapi.StackDesire](l.indexer)
}

func (l *stackDesireLister) Get(ctx context.Context, subscriptionID, resourceGroupName, clusterName, name string) (*coreapi.StackDesire, error) {
	key := coreapihelpers.ToStackDesireResourceIDString(subscriptionID, resourceGroupName, clusterName, name)
	return listerutils.GetByKey[coreapi.StackDesire](l.indexer, key)
}

func (l *stackDesireLister) ListForCluster(ctx context.Context, subscriptionID, resourceGroupName, clusterName string) ([]*coreapi.StackDesire, error) {
	key := coreapihelpers.ToClusterResourceIDString(subscriptionID, resourceGroupName, clusterName)
	return listerutils.ListFromIndex[coreapi.StackDesire](l.indexer, ByCluster, key)
}
