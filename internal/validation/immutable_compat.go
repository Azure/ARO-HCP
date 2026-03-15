// Copyright 2025 Microsoft Corporation
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

package validation

import (
	"context"

	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/operation"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

// ImmutableByCompare verifies that a comparable value has not changed during
// an update operation. It does nothing if the old value is not provided.
// This preserves the behavior of the k8s.io/apimachinery v0.34 function that
// was removed in v0.35 in favor of validate.Immutable (which relies on
// ratcheting instead of explicit comparison).
func ImmutableByCompare[T comparable](_ context.Context, op operation.Operation, fldPath *field.Path, value, oldValue *T) field.ErrorList {
	if op.Type != operation.Update {
		return nil
	}
	if value == nil && oldValue == nil {
		return nil
	}
	if value == nil || oldValue == nil || *value != *oldValue {
		return field.ErrorList{
			field.Forbidden(fldPath, "field is immutable"),
		}
	}
	return nil
}

// ImmutableByReflect verifies that a value has not changed during an update
// operation using semantic equality. This preserves the behavior of the
// k8s.io/apimachinery v0.34 function that was removed in v0.35.
func ImmutableByReflect[T any](_ context.Context, op operation.Operation, fldPath *field.Path, value, oldValue T) field.ErrorList {
	if op.Type != operation.Update {
		return nil
	}
	if !equality.Semantic.DeepEqual(value, oldValue) {
		return field.ErrorList{
			field.Forbidden(fldPath, "field is immutable"),
		}
	}
	return nil
}
