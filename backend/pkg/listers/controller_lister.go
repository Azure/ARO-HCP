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
