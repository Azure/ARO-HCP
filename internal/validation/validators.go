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
	"crypto/x509"
	"fmt"
	"net"
	"regexp"
	"strings"
	"time"

	semver "github.com/hashicorp/go-version"

	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/operation"
	"k8s.io/apimachinery/pkg/api/validate"
	"k8s.io/apimachinery/pkg/api/validate/constraints"
	"k8s.io/apimachinery/pkg/util/validation/field"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
)

func init() {
	// we need some semantic equalities added for our types and this is the standard location
	api.Must("",
		equality.Semantic.AddFuncs(
			func(a, b time.Time) bool {
				return a.UTC().Equal(b.UTC())
			},
		),
	)
}

func NoExtraWhitespace(_ context.Context, _ operation.Operation, fldPath *field.Path, value, _ *string) field.ErrorList {
	if value == nil {
		return nil
	}
	if len(*value) == 0 {
		return nil
	}
	if strings.TrimSpace(*value) != *value {
		return field.ErrorList{field.Invalid(fldPath, *value, "must not contain extra whitespace")}
	}
	return nil
}

func OpenshiftVersionWithoutMicro(_ context.Context, op operation.Operation, fldPath *field.Path, value, _ *string) field.ErrorList {
	if value == nil {
		return nil
	}
	if len(*value) == 0 {
		return nil
	}

	_, err := semver.NewVersion(*value)
	if err != nil {
		return field.ErrorList{field.Invalid(fldPath, value, err.Error())}
	}

	// The version ID has already passed syntax validation so we know it's a valid semantic version.
	if len(strings.SplitN(*value, ".", 3)) > 2 {
		return field.ErrorList{field.Invalid(fldPath, value, "must be specified as MAJOR.MINOR; the PATCH value is managed")}
	}

	return nil
}

func MinItems[T any](_ context.Context, _ operation.Operation, fldPath *field.Path, value, _ []T, minLen int) field.ErrorList {
	if value == nil {
		return nil
	}
	if len(value) < minLen {
		return field.ErrorList{field.Invalid(fldPath, len(value), fmt.Sprintf("must have at least %d items", minLen))}
	}
	return nil
}

func MaxItems[T any](_ context.Context, _ operation.Operation, fldPath *field.Path, value, _ []T, maxLen int) field.ErrorList {
	if value == nil {
		return nil
	}
	if len(value) > maxLen {
		return field.ErrorList{field.TooMany(fldPath, len(value), maxLen)}
	}
	return nil
}

// MaxLen verifies that the specified string is less than or equal to maxLen long
func MaxLen(_ context.Context, _ operation.Operation, fldPath *field.Path, value, _ *string, maxLen int) field.ErrorList {
	if value == nil {
		return nil
	}
	if len(*value) > maxLen {
		return field.ErrorList{field.TooLong(fldPath, *value, maxLen)}
	}
	return nil
}

// MinLen verifies that the specified string is greater than or equal to minLen long
func MinLen(_ context.Context, _ operation.Operation, fldPath *field.Path, value, _ *string, minLen int) field.ErrorList {
	if value == nil {
		return nil
	}
	if len(*value) < minLen {
		return field.ErrorList{field.Invalid(fldPath, *value, fmt.Sprintf("must be at least %d characters long", minLen))}
	}
	return nil
}

// Minimum verifies that the specified value is less than or equal to max.
func Maximum[T constraints.Integer](_ context.Context, _ operation.Operation, fldPath *field.Path, value, _ *T, max T) field.ErrorList {
	if value == nil {
		return nil
	}
	if *value > max {
		return field.ErrorList{field.Invalid(fldPath, *value, fmt.Sprintf("must be less than or equal to %d", max))}
	}
	return nil
}

var (
	dnsRegexStringRFC1035Label = "^[a-z]([-a-z0-9]*[a-z0-9])?$"
	rfc1035LabelRegex          = regexp.MustCompile(dnsRegexStringRFC1035Label)
	rfc1035ErrorString         = `(must be a valid DNS RFC 1035 label)`
)

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

func CIDRv4(_ context.Context, _ operation.Operation, fldPath *field.Path, value, _ *string) field.ErrorList {
	if value == nil {
		return nil
	}
	if len(*value) == 0 {
		return nil
	}
	ip, net, err := net.ParseCIDR(*value)
	if err != nil {
		return field.ErrorList{field.Invalid(fldPath, *value, err.Error())}
	}
	if ip.To4() == nil {
		return field.ErrorList{field.Invalid(fldPath, *value, "not IPv4")}
	}
	if !net.IP.Equal(ip) {
		return field.ErrorList{field.Invalid(fldPath, *value, "not IPv4 CIDR")}
	}
	return nil
}

func IPv4(_ context.Context, _ operation.Operation, fldPath *field.Path, value, _ *string) field.ErrorList {
	if value == nil {
		return nil
	}
	if len(*value) == 0 {
		return nil
	}

	ip := net.ParseIP(*value)

	if ip == nil {
		return field.ErrorList{field.Invalid(fldPath, *value, "not an IP")}
	}
	if ip.To4() == nil {
		return field.ErrorList{field.Invalid(fldPath, *value, "not IPv4")}
	}

	return nil
}

func ValidateUserAssignedIdentityLocation(ctx context.Context, op operation.Operation, fldPath *field.Path, value, _ *string, clusterSubscriptionID, managedResourceGroupName string) field.ErrorList {
	if value == nil || len(*value) == 0 {
		return nil
	}

	errs := field.ErrorList{}
	errs = append(errs, SameSubscription(ctx, op, fldPath, value, nil, clusterSubscriptionID)...)
	errs = append(errs, DifferentResourceGroupNameFromResourceID(ctx, op, fldPath, value, nil, managedResourceGroupName)...)

	return errs
}

func DifferentResourceGroupNameFromResourceID(ctx context.Context, op operation.Operation, fldPath *field.Path, value, _ *string, resourceGroupName string) field.ErrorList {
	if value == nil || len(*value) == 0 {
		return nil
	}

	resourceID, err := azcorearm.ParseResourceID(*value)
	if err != nil {
		return field.ErrorList{field.Invalid(fldPath, *value, err.Error())}
	}

	if strings.EqualFold(resourceID.ResourceGroupName, resourceGroupName) {
		return field.ErrorList{field.Invalid(fldPath, *value, fmt.Sprintf("must not be the same resource group name: %q", resourceGroupName))}
	}

	return nil
}

func DifferentResourceGroupName(ctx context.Context, op operation.Operation, fldPath *field.Path, value, _ *string, resourceGroupName string) field.ErrorList {
	if value == nil || len(*value) == 0 {
		return nil
	}

	if strings.EqualFold(*value, resourceGroupName) {
		return field.ErrorList{field.Invalid(fldPath, *value, fmt.Sprintf("must not be the same resource group name: %q", resourceGroupName))}
	}

	return nil
}

func SameSubscription(ctx context.Context, op operation.Operation, fldPath *field.Path, value, _ *string, subscriptionID string) field.ErrorList {
	if value == nil || len(*value) == 0 {
		return nil
	}

	resourceID, err := azcorearm.ParseResourceID(*value)
	if err != nil {
		return field.ErrorList{field.Invalid(fldPath, *value, err.Error())}
	}

	if !strings.EqualFold(resourceID.SubscriptionID, subscriptionID) {
		return field.ErrorList{field.Invalid(fldPath, *value, fmt.Sprintf("must be in the same Azure subscription: %q", subscriptionID))}
	}

	return nil
}

func ResourceID(ctx context.Context, op operation.Operation, fldPath *field.Path, value, oldValue *string) field.ErrorList {
	return restrictedResourceIDCheck(ctx, op, fldPath, value, oldValue, "")
}

func RestrictedResourceID(ctx context.Context, op operation.Operation, fldPath *field.Path, value, oldValue *string, resourceTypeRestriction string) field.ErrorList {
	return restrictedResourceIDCheck(ctx, op, fldPath, value, oldValue, resourceTypeRestriction)
}

// if resourceTypeRestriction is not set, then any kind of resourceType is allowed
func restrictedResourceIDCheck(_ context.Context, _ operation.Operation, fldPath *field.Path, value, _ *string, resourceTypeRestriction string) field.ErrorList {
	if value == nil || len(*value) == 0 {
		return nil
	}
	resourceID, err := azcorearm.ParseResourceID(*value)
	if err != nil {
		return field.ErrorList{field.Invalid(fldPath, *value, err.Error())}
	}
	// Check for required fields.
	if len(resourceID.SubscriptionID) == 0 {
		return field.ErrorList{field.Invalid(fldPath, *value, "subscription ID is required")}
	}
	if len(resourceID.ResourceGroupName) == 0 {
		return field.ErrorList{field.Invalid(fldPath, *value, "resource group is required")}
	}
	if len(resourceID.Name) == 0 {
		return field.ErrorList{field.Invalid(fldPath, *value, "resource name is required")}
	}
	if len(resourceTypeRestriction) == 0 {
		return nil
	}

	if !strings.EqualFold(resourceTypeRestriction, resourceID.ResourceType.String()) {
		return field.ErrorList{field.Invalid(fldPath, *value, fmt.Sprintf("resource ID must reference an instance of type %q", resourceTypeRestriction))}
	}
	return nil
}

func newRestrictedResourceID(resourceTypeRestriction string) validate.ValidateFunc[*string] {
	return func(ctx context.Context, op operation.Operation, fldPath *field.Path, newValue, oldValue *string) field.ErrorList {
		return RestrictedResourceID(ctx, op, fldPath, newValue, oldValue, resourceTypeRestriction)
	}
}

func Or[T any](ctx context.Context, op operation.Operation, fldPath *field.Path, newValue, oldValue T, validateFns ...validate.ValidateFunc[T]) field.ErrorList {
	errs := field.ErrorList{}

	for _, validateFn := range validateFns {
		currErrs := validateFn(ctx, op, fldPath, newValue, oldValue)
		if len(currErrs) == 0 {
			return nil
		}
		errs = append(errs, currErrs...)
	}

	return errs
}

func newOr[T any](validateFns ...validate.ValidateFunc[T]) validate.ValidateFunc[T] {
	return func(ctx context.Context, op operation.Operation, fldPath *field.Path, newValue, oldValue T) field.ErrorList {
		return Or(ctx, op, fldPath, newValue, oldValue, validateFns...)
	}
}

// TODO this is compatible with what existed before, but still allows much invalid content
func ValidatePEM(ctx context.Context, op operation.Operation, fldPath *field.Path, value, _ *string) field.ErrorList {
	if value == nil {
		return nil
	}
	if len(*value) == 0 {
		return nil
	}
	if !x509.NewCertPool().AppendCertsFromPEM([]byte(*value)) {
		return field.ErrorList{field.Invalid(fldPath, *value, "not a valid PEM")}
	}

	return nil
}

// EachMapKey validates each element of newMap with the specified validation function.
// This is a copy from validate.EachMapKey except that the field path includes the key because its more useful for us that way.
func EachMapKey[K ~string, T any](ctx context.Context, op operation.Operation, fldPath *field.Path, newMap, oldMap map[K]T, validator validate.ValidateFunc[*K]) field.ErrorList {
	var errs field.ErrorList
	for key := range newMap {
		var old *K
		if _, found := oldMap[key]; found {
			old = &key
		}
		// If the operation is an update, for validation ratcheting, skip re-validating if
		// the key is found in oldMap.
		if op.Type == operation.Update && old != nil {
			continue
		}
		// Note: the field path is the field, not the key.
		keyString := fmt.Sprintf("%v", key)
		errs = append(errs, validator(ctx, op, fldPath.Key(keyString), &key, nil)...)
	}
	return errs
}
