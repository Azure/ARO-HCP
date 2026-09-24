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

package cosmosstorageutils

import (
	"context"

	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
)

// ResourceCRUDLayer wraps a non-transactional CRUD operation. The layer must
// invoke operation synchronously with the context to use for that operation,
// or return an error without invoking it. Layers may be shared by multiple CRUDs
// and must support concurrent calls. List iteration is wrapped separately because
// its requests are deferred until Items is consumed.
//
// Layers can be composed by wrapping a CRUD more than once. Transaction assembly
// methods are forwarded unchanged: they do not issue requests, and the eventual
// transaction execution is outside this interface.
type ResourceCRUDLayer interface {
	Do(ctx context.Context, operation func(context.Context) error) error
}

// NewLayeredResourceCRUD applies layer to the non-transactional methods of inner.
func NewLayeredResourceCRUD[T any, PT coreapi.CosmosMetadataAccessorPtr[T]](
	inner ResourceCRUD[T, PT], layer ResourceCRUDLayer,
) ResourceCRUD[T, PT] {
	return &layeredResourceCRUD[T, PT]{ResourceCRUD: inner, layer: layer}
}

// NewLayeredValidatingResourceCRUD preserves validation, including the old
// object passed to Replace, while applying layer to the CRUD's operations.
func NewLayeredValidatingResourceCRUD[T any, PT coreapi.CosmosMetadataAccessorPtr[T]](
	inner ValidatingResourceCRUD[T, PT], layer ResourceCRUDLayer,
) ValidatingResourceCRUD[T, PT] {
	return &layeredValidatingResourceCRUD[T, PT]{ValidatingResourceCRUD: inner, layer: layer}
}

type layeredResourceCRUD[T any, PT coreapi.CosmosMetadataAccessorPtr[T]] struct {
	ResourceCRUD[T, PT]
	layer ResourceCRUDLayer
}

type layeredValidatingResourceCRUD[T any, PT coreapi.CosmosMetadataAccessorPtr[T]] struct {
	ValidatingResourceCRUD[T, PT]
	layer ResourceCRUDLayer
}

func layeredResult[T any](ctx context.Context, layer ResourceCRUDLayer, operation func(context.Context) (T, error)) (T, error) {
	var result T
	err := layer.Do(ctx, func(ctx context.Context) error {
		var err error
		result, err = operation(ctx)
		return err
	})
	return result, err
}

// layeredIterator applies the layer to the Items context, not the original List
// context, so cancellation and attribution follow the caller consuming the list.
type layeredIterator[T any] struct {
	DBClientIterator[T]
	layer ResourceCRUDLayer
	err   error
}

func (i *layeredIterator[T]) Items(ctx context.Context) DBClientIteratorItem[T] {
	return func(yield func(string, *T) bool) {
		i.err = i.layer.Do(ctx, func(ctx context.Context) error {
			i.DBClientIterator.Items(ctx)(yield)
			return i.DBClientIterator.GetError()
		})
	}
}

func (i *layeredIterator[T]) GetError() error {
	if i.err != nil {
		return i.err
	}
	return i.DBClientIterator.GetError()
}

func (c *layeredResourceCRUD[T, PT]) GetByID(ctx context.Context, resourceID string) (*T, error) {
	return layeredResult(ctx, c.layer, func(ctx context.Context) (*T, error) {
		return c.ResourceCRUD.GetByID(ctx, resourceID)
	})
}

func (c *layeredResourceCRUD[T, PT]) Get(ctx context.Context, resourceID string) (*T, error) {
	return layeredResult(ctx, c.layer, func(ctx context.Context) (*T, error) {
		return c.ResourceCRUD.Get(ctx, resourceID)
	})
}

func (c *layeredResourceCRUD[T, PT]) Create(ctx context.Context, newObj *T, options *azcosmos.ItemOptions) (*T, error) {
	return layeredResult(ctx, c.layer, func(ctx context.Context) (*T, error) {
		return c.ResourceCRUD.Create(ctx, newObj, options)
	})
}

func (c *layeredResourceCRUD[T, PT]) Replace(ctx context.Context, newObj *T, options *azcosmos.ItemOptions) (*T, error) {
	return layeredResult(ctx, c.layer, func(ctx context.Context) (*T, error) {
		return c.ResourceCRUD.Replace(ctx, newObj, options)
	})
}

func (c *layeredResourceCRUD[T, PT]) Delete(ctx context.Context, resourceID string) error {
	return c.layer.Do(ctx, func(ctx context.Context) error { return c.ResourceCRUD.Delete(ctx, resourceID) })
}

func (c *layeredResourceCRUD[T, PT]) List(ctx context.Context, options *DBClientListResourceDocsOptions) (DBClientIterator[T], error) {
	iterator, err := layeredResult(ctx, c.layer, func(ctx context.Context) (DBClientIterator[T], error) {
		return c.ResourceCRUD.List(ctx, options)
	})
	if err != nil {
		return nil, err
	}
	return &layeredIterator[T]{DBClientIterator: iterator, layer: c.layer}, nil
}

func (c *layeredValidatingResourceCRUD[T, PT]) GetByID(ctx context.Context, resourceID string) (*T, error) {
	return layeredResult(ctx, c.layer, func(ctx context.Context) (*T, error) {
		return c.ValidatingResourceCRUD.GetByID(ctx, resourceID)
	})
}

func (c *layeredValidatingResourceCRUD[T, PT]) Get(ctx context.Context, resourceID string) (*T, error) {
	return layeredResult(ctx, c.layer, func(ctx context.Context) (*T, error) {
		return c.ValidatingResourceCRUD.Get(ctx, resourceID)
	})
}

func (c *layeredValidatingResourceCRUD[T, PT]) Create(ctx context.Context, newObj *T, options *azcosmos.ItemOptions) (*T, error) {
	return layeredResult(ctx, c.layer, func(ctx context.Context) (*T, error) {
		return c.ValidatingResourceCRUD.Create(ctx, newObj, options)
	})
}

func (c *layeredValidatingResourceCRUD[T, PT]) Replace(ctx context.Context, newObj, oldObj *T, options *azcosmos.ItemOptions) (*T, error) {
	return layeredResult(ctx, c.layer, func(ctx context.Context) (*T, error) {
		return c.ValidatingResourceCRUD.Replace(ctx, newObj, oldObj, options)
	})
}

func (c *layeredValidatingResourceCRUD[T, PT]) Delete(ctx context.Context, resourceID string) error {
	return c.layer.Do(ctx, func(ctx context.Context) error { return c.ValidatingResourceCRUD.Delete(ctx, resourceID) })
}

func (c *layeredValidatingResourceCRUD[T, PT]) List(ctx context.Context, options *DBClientListResourceDocsOptions) (DBClientIterator[T], error) {
	iterator, err := layeredResult(ctx, c.layer, func(ctx context.Context) (DBClientIterator[T], error) {
		return c.ValidatingResourceCRUD.List(ctx, options)
	})
	if err != nil {
		return nil, err
	}
	return &layeredIterator[T]{DBClientIterator: iterator, layer: c.layer}, nil
}
