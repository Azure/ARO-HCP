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

package database

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/util/validation/field"

	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
)

// validatingCRUD wraps a ResourceCRUD and runs type-specific validation before
// Create and Replace. The wrapped CRUD is never exposed directly, so validation
// cannot be bypassed.
type validatingCRUD[InternalAPIType any] struct {
	inner           ResourceCRUD[InternalAPIType]
	validateCreate  func(context.Context, *InternalAPIType) field.ErrorList
	validateReplace func(context.Context, *InternalAPIType, *InternalAPIType) field.ErrorList
}

func NewValidatingCRUD[InternalAPIType any](
	inner ResourceCRUD[InternalAPIType],
	validateCreate func(context.Context, *InternalAPIType) field.ErrorList,
	validateReplace func(context.Context, *InternalAPIType, *InternalAPIType) field.ErrorList,
) ValidatingResourceCRUD[InternalAPIType] {
	return &validatingCRUD[InternalAPIType]{
		inner:           inner,
		validateCreate:  validateCreate,
		validateReplace: validateReplace,
	}
}

func (v *validatingCRUD[InternalAPIType]) GetByID(ctx context.Context, cosmosID string) (*InternalAPIType, error) {
	return v.inner.GetByID(ctx, cosmosID)
}

func (v *validatingCRUD[InternalAPIType]) Get(ctx context.Context, resourceID string) (*InternalAPIType, error) {
	return v.inner.Get(ctx, resourceID)
}

func (v *validatingCRUD[InternalAPIType]) List(ctx context.Context, opts *DBClientListResourceDocsOptions) (DBClientIterator[InternalAPIType], error) {
	return v.inner.List(ctx, opts)
}

func (v *validatingCRUD[InternalAPIType]) Create(ctx context.Context, newObj *InternalAPIType, options *azcosmos.ItemOptions) (*InternalAPIType, error) {
	if errs := v.validateCreate(ctx, newObj); errs.ToAggregate() != nil {
		return nil, fmt.Errorf("create validation failed: %w", errs.ToAggregate())
	}
	return v.inner.Create(ctx, newObj, options)
}

func (v *validatingCRUD[InternalAPIType]) Replace(ctx context.Context, newObj, oldObj *InternalAPIType, options *azcosmos.ItemOptions) (*InternalAPIType, error) {
	if errs := v.validateReplace(ctx, newObj, oldObj); errs.ToAggregate() != nil {
		return nil, fmt.Errorf("replace validation failed: %w", errs.ToAggregate())
	}
	return v.inner.Replace(ctx, newObj, options)
}

func (v *validatingCRUD[InternalAPIType]) Delete(ctx context.Context, resourceID string) error {
	return v.inner.Delete(ctx, resourceID)
}
