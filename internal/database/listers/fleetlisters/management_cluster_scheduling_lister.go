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

package fleetlisters

import (
	"context"

	"k8s.io/client-go/tools/cache"

	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/database/listers/listerutils"
)

// ManagementClusterSchedulingLister lists and gets management cluster scheduling
// documents from an informer's indexer. A scheduling document is a singleton
// child of a management cluster, so it is keyed (and fetched) by the parent
// stamp identifier.
type ManagementClusterSchedulingLister interface {
	List(ctx context.Context) ([]*fleetapi.ManagementClusterScheduling, error)
	Get(ctx context.Context, stampIdentifier string) (*fleetapi.ManagementClusterScheduling, error)
}

type informerBasedManagementClusterSchedulingLister struct {
	indexer cache.Indexer
}

// NewManagementClusterSchedulingLister creates a ManagementClusterSchedulingLister
// from a SharedIndexInformer's indexer.
func NewManagementClusterSchedulingLister(indexer cache.Indexer) ManagementClusterSchedulingLister {
	return &informerBasedManagementClusterSchedulingLister{
		indexer: indexer,
	}
}

func (l *informerBasedManagementClusterSchedulingLister) List(ctx context.Context) ([]*fleetapi.ManagementClusterScheduling, error) {
	return listerutils.ListAll[fleetapi.ManagementClusterScheduling](l.indexer)
}

// Get retrieves a single management cluster scheduling document by stamp identifier.
func (l *informerBasedManagementClusterSchedulingLister) Get(ctx context.Context, stampIdentifier string) (*fleetapi.ManagementClusterScheduling, error) {
	key := fleetapi.ToManagementClusterSchedulingResourceIDString(stampIdentifier)
	return listerutils.GetByKey[fleetapi.ManagementClusterScheduling](l.indexer, key)
}
