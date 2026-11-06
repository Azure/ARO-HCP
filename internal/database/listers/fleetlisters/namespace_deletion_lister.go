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
	"github.com/Azure/ARO-HCP/internal/apihelpers/fleetapihelpers"
	"github.com/Azure/ARO-HCP/internal/database/listers/listerutils"
)

// NamespaceDeletionLister lists and gets stuck cluster cleanup documents from
// an informer's indexer. Unlike its singleton siblings under a management
// cluster, cleanups are a collection, so Get takes both the parent stamp
// identifier and the cleanup's own name.
type NamespaceDeletionLister interface {
	List(ctx context.Context) ([]*fleetapi.NamespaceDeletion, error)
	Get(ctx context.Context, stampIdentifier, name string) (*fleetapi.NamespaceDeletion, error)
}

type informerBasedNamespaceDeletionLister struct {
	indexer cache.Indexer
}

// NewNamespaceDeletionLister creates a NamespaceDeletionLister from a
// SharedIndexInformer's indexer.
func NewNamespaceDeletionLister(indexer cache.Indexer) NamespaceDeletionLister {
	return &informerBasedNamespaceDeletionLister{
		indexer: indexer,
	}
}

func (l *informerBasedNamespaceDeletionLister) List(ctx context.Context) ([]*fleetapi.NamespaceDeletion, error) {
	return listerutils.ListAll[fleetapi.NamespaceDeletion](l.indexer)
}

// Get retrieves a single stuck cluster cleanup document by stamp identifier and name.
func (l *informerBasedNamespaceDeletionLister) Get(ctx context.Context, stampIdentifier, name string) (*fleetapi.NamespaceDeletion, error) {
	key := fleetapihelpers.ToNamespaceDeletionResourceIDString(stampIdentifier, name)
	return listerutils.GetByKey[fleetapi.NamespaceDeletion](l.indexer, key)
}
