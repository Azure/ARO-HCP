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

package validation

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/google/uuid"

	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/operation"
	"k8s.io/apimachinery/pkg/util/validation/field"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
)

// ImmutableByCompare and ImmutableByReflect are local copies of the helpers
// removed in k8s.io/apimachinery v0.35.1 (previously in pkg/api/validate/immutable.go).
// The upstream replacement (validate.Immutable) relies on the k8s ratcheting
// framework and always returns an error on update without comparing values.
// Our validation calls these without ratcheting, so we keep the old comparison
// semantics.
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

// ImmutableByReflectOnceSet allows a nil→non-nil transition (first-time set)
// but rejects any change or clearing once the field has been set.
func ImmutableByReflectOnceSet[T any](_ context.Context, op operation.Operation, fldPath *field.Path, value, oldValue *T) field.ErrorList {
	if op.Type != operation.Update {
		return nil
	}
	if oldValue == nil {
		return nil
	}
	if value == nil || !equality.Semantic.DeepEqual(value, oldValue) {
		return field.ErrorList{
			field.Forbidden(fldPath, "field is immutable once set"),
		}
	}
	return nil
}

func MatchesRegex(_ context.Context, _ operation.Operation, fldPath *field.Path, value, _ *string, regex *regexp.Regexp, errorString string) field.ErrorList {
	if value == nil {
		return nil
	}
	if len(*value) == 0 {
		return nil
	}
	if regex.MatchString(*value) {
		return nil
	}
	return field.ErrorList{field.Invalid(fldPath, *value, errorString)}
}

func ValidateUUID(_ context.Context, _ operation.Operation, fldPath *field.Path, value, _ *string) field.ErrorList {
	if value == nil {
		return nil
	}
	if len(*value) == 0 {
		return nil
	}
	if err := uuid.Validate(*value); err != nil {
		return field.ErrorList{field.Invalid(fldPath, *value, err.Error())}
	}
	return nil
}

func URL(_ context.Context, _ operation.Operation, fldPath *field.Path, value, _ *string) field.ErrorList {
	if value == nil {
		return nil
	}

	_, err := url.Parse(*value)
	if err != nil {
		return field.ErrorList{field.Invalid(fldPath, *value, err.Error())}
	}

	return nil
}

// ResourceID
// 1. has subscription
// 2. has name
// 3. has any resource type
// 4. has a resource group name
func ResourceID(ctx context.Context, op operation.Operation, fldPath *field.Path, value, oldValue *azcorearm.ResourceID) field.ErrorList {
	return restrictedResourceIDInResourceGroupCheck(ctx, op, fldPath, value, oldValue, "")
}

// GenericResourceID
// 1. has subscription
// 2. has name
// 3. has any resource type
// 4. may or may not have a resource group name
func GenericResourceID(ctx context.Context, op operation.Operation, fldPath *field.Path, value, oldValue *azcorearm.ResourceID) field.ErrorList {
	return restrictedGenericResourceIDCheck(ctx, op, fldPath, value, oldValue, "")
}

// RestrictedResourceIDWithResourceGroup
// 1. has subscription
// 2. has name
// 3. has a particular resource type
// 4. has a resource group name
func RestrictedResourceIDWithResourceGroup(ctx context.Context, op operation.Operation, fldPath *field.Path, value, oldValue *azcorearm.ResourceID, resourceTypeRestriction string) field.ErrorList {
	return restrictedResourceIDInResourceGroupCheck(ctx, op, fldPath, value, oldValue, resourceTypeRestriction)
}

// RestrictedResourceIDWithoutResourceGroup
// 1. has subscription
// 2. has name
// 3. has particular type
// 4. has no resource group name
func RestrictedResourceIDWithoutResourceGroup(ctx context.Context, op operation.Operation, fldPath *field.Path, value, oldValue *azcorearm.ResourceID, resourceTypeRestriction string) field.ErrorList {
	return restrictedResourceIDWithoutResourceGroupCheck(ctx, op, fldPath, value, oldValue, resourceTypeRestriction)
}

func restrictedResourceIDInResourceGroupCheck(ctx context.Context, op operation.Operation, fldPath *field.Path, resourceID, _ *azcorearm.ResourceID, resourceTypeRestriction string) field.ErrorList {
	if resourceID == nil {
		return nil
	}

	errs := field.ErrorList{}
	errs = append(errs, restrictedGenericResourceIDCheck(ctx, op, fldPath, resourceID, nil, resourceTypeRestriction)...)

	if len(resourceID.ResourceGroupName) == 0 {
		errs = append(errs, field.Invalid(fldPath, resourceID.String(), "resource group is required"))
	}

	return errs
}

func restrictedGenericResourceIDCheck(ctx context.Context, op operation.Operation, fldPath *field.Path, resourceID, _ *azcorearm.ResourceID, resourceTypeRestriction string) field.ErrorList {
	if resourceID == nil {
		return nil
	}

	errs := field.ErrorList{}

	// Check for required fields.
	if len(resourceID.SubscriptionID) == 0 {
		errs = append(errs, field.Invalid(fldPath, resourceID.String(), "subscription ID is required"))
	}
	if len(resourceID.Name) == 0 {
		errs = append(errs, field.Invalid(fldPath, resourceID.String(), "resource name is required"))
	}
	if len(resourceTypeRestriction) > 0 && !strings.EqualFold(resourceTypeRestriction, resourceID.ResourceType.String()) {
		errs = append(errs, field.Invalid(fldPath, resourceID.String(), fmt.Sprintf("resource ID must reference an instance of type %q", resourceTypeRestriction)))
	}

	return errs
}

func restrictedResourceIDWithoutResourceGroupCheck(ctx context.Context, op operation.Operation, fldPath *field.Path, resourceID, _ *azcorearm.ResourceID, resourceTypeRestriction string) field.ErrorList {
	if resourceID == nil {
		return nil
	}

	errs := field.ErrorList{}
	errs = append(errs, restrictedGenericResourceIDCheck(ctx, op, fldPath, resourceID, nil, resourceTypeRestriction)...)

	if len(resourceID.ResourceGroupName) != 0 {
		errs = append(errs, field.Invalid(fldPath, resourceID.String(), "resource group must be empty"))
	}

	return errs
}
