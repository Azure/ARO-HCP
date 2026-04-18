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
	"net/url"
	"regexp"
	"slices"
	"strings"

	"github.com/blang/semver/v4"
	"github.com/google/uuid"

	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/operation"
	"k8s.io/apimachinery/pkg/api/validate"
	"k8s.io/apimachinery/pkg/api/validate/constraints"
	k8svalidation "k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/utils/apihelpers"
)

func immutableByCompare[T comparable](_ context.Context, op operation.Operation, fldPath *field.Path, value, oldValue *T) field.ErrorList {
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

func immutableByReflect[T any](_ context.Context, op operation.Operation, fldPath *field.Path, value, oldValue T) field.ErrorList {
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

func VersionMustBeAtLeast(_ context.Context, op operation.Operation, fldPath *field.Path, value, _ *string, minimumVersion string) field.ErrorList {
	if value == nil {
		return nil
	}
	if len(*value) == 0 {
		return nil
	}

	newVersion, err := semver.ParseTolerant(*value)
	if err != nil {
		return field.ErrorList{field.Invalid(fldPath, value, err.Error())}
	}
	minVersion, err := semver.ParseTolerant(minimumVersion)
	if err != nil {
		return field.ErrorList{field.Invalid(fldPath, value, err.Error())}
	}

	if newVersion.LT(minVersion) {
		return field.ErrorList{field.Invalid(fldPath, value, fmt.Sprintf("must be at least %s", minimumVersion))}
	}

	return nil
}

func VersionMayNotDecrease(_ context.Context, op operation.Operation, fldPath *field.Path, value, oldValue *string) field.ErrorList {
	if value == nil {
		return nil
	}
	if len(*value) == 0 {
		return nil
	}
	if oldValue == nil {
		return nil
	}
	if len(*oldValue) == 0 {
		return nil
	}

	newVersion, err := semver.ParseTolerant(*value)
	if err != nil {
		return field.ErrorList{field.Invalid(fldPath, value, err.Error())}
	}
	oldVersion, err := semver.ParseTolerant(*oldValue)
	if err != nil {
		return field.ErrorList{field.Invalid(fldPath, value, err.Error())}
	}

	if newVersion.LT(oldVersion) {
		return field.ErrorList{field.Invalid(fldPath, value, fmt.Sprintf("may not decrease from %s", *oldValue))}
	}

	return nil
}

// OpenshiftVersionAtMostOneMinorSkew returns nil if newVersionID is an allowed skew from previousVersionID
// (same rules as ARM validation: at most one minor within a major; cross-major only 4→5 via the supported pairings).
// Empty previous or new id is a no-op (nil). Parse failures return an error from semver.
func OpenshiftVersionAtMostOneMinorSkew(previousVersionID, newVersionID string) error {
	if len(previousVersionID) == 0 || len(newVersionID) == 0 {
		return nil
	}

	parsedDesiredVersion, err := semver.ParseTolerant(newVersionID)
	if err != nil {
		return err
	}
	parsedPreviousVersion, err := semver.ParseTolerant(previousVersionID)
	if err != nil {
		return err
	}

	if parsedDesiredVersion.Major != parsedPreviousVersion.Major {
		// Only a single major bump is considered; +2 or more (e.g. 4→6) is not supported.
		if parsedDesiredVersion.Major != parsedPreviousVersion.Major+1 {
			return fmt.Errorf("invalid upgrade path from %s to %s: skipping major versions is not allowed", previousVersionID, newVersionID)
		}
		previousVersionReleaseLine := fmt.Sprintf("%d.%d", parsedPreviousVersion.Major, parsedPreviousVersion.Minor)
		desiredVersionReleaseLine := fmt.Sprintf("%d.%d", parsedDesiredVersion.Major, parsedDesiredVersion.Minor)
		allowedTargetReleaseLine := api.AllowMajorUpgradePaths[previousVersionReleaseLine]
		if desiredVersionReleaseLine != allowedTargetReleaseLine {
			return fmt.Errorf("invalid upgrade path from %s to %s: cross-major upgrade from %s is only allowed to %s", previousVersionID, newVersionID, previousVersionReleaseLine, allowedTargetReleaseLine)
		}
		return nil
	}

	if parsedDesiredVersion.Minor > parsedPreviousVersion.Minor+1 {
		return fmt.Errorf("only upgrade to the next minor is allowed: expected %d.%d after %d.%d", parsedPreviousVersion.Major, parsedPreviousVersion.Minor+1, parsedPreviousVersion.Major, parsedPreviousVersion.Minor)
	}

	return nil
}

// OpenshiftVersionAtMostOneMinorSkewWithField reports OpenshiftVersionAtMostOneMinorSkew failures as field errors on newVersionID.
func OpenshiftVersionAtMostOneMinorSkewWithField(_ context.Context, _ operation.Operation, fldPath *field.Path, newVersionID, previousVersionID *string) field.ErrorList {
	newVersionIDString := ptr.Deref(newVersionID, "")
	previousVersionIDString := ptr.Deref(previousVersionID, "")
	if len(newVersionIDString) == 0 || len(previousVersionIDString) == 0 {
		return nil
	}

	if err := OpenshiftVersionAtMostOneMinorSkew(previousVersionIDString, newVersionIDString); err != nil {
		return field.ErrorList{field.Invalid(fldPath, newVersionID, err.Error())}
	}
	return nil
}

func OpenshiftVersionWithOptionalMicro(_ context.Context, op operation.Operation, fldPath *field.Path, value, _ *string) field.ErrorList {
	if value == nil {
		return nil
	}
	if len(*value) == 0 {
		return nil
	}

	_, err := semver.ParseTolerant(*value)
	if err != nil {
		return field.ErrorList{field.Invalid(fldPath, value, err.Error())}
	}

	return nil
}

func OpenShiftWithOptionalPrerelease(_ context.Context, op operation.Operation, fldPath *field.Path, value, _ *string) field.ErrorList {
	if value == nil {
		return nil
	}
	if len(*value) == 0 {
		return nil
	}

	_, err := semver.Parse(*value)
	if err != nil {
		return field.ErrorList{field.Invalid(fldPath, value, err.Error())}
	}

	// The version ID has already passed syntax validation so we know it's a valid semantic version.
	if len(strings.SplitN(*value, ".", 3)) < 3 {
		return field.ErrorList{field.Invalid(fldPath, value, "must be specified as MAJOR.MINOR.PATCH with optional pre-release")}
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

	_, err := semver.ParseTolerant(*value)
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

// EqualFold verifies that the specified string is equal to the required value ignoring case.
func EqualFold(_ context.Context, _ operation.Operation, fldPath *field.Path, value, _ *string, requiredValue string) field.ErrorList {
	if value == nil {
		return nil
	}
	if !strings.EqualFold(*value, requiredValue) {
		return field.ErrorList{field.Invalid(fldPath, *value, fmt.Sprintf("must be equal to %q", requiredValue))}
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

	clusterResourceName            = `^[a-zA-Z][-a-zA-Z0-9]{1,52}[a-zA-Z0-9]$`
	clusterResourceNameRegex       = regexp.MustCompile(clusterResourceName)
	clusterResourceNameErrorString = `(must be a valid DNS RFC 1035 label)`

	nodePoolResourceName            = `^[a-zA-Z][-a-zA-Z0-9]{1,13}[a-zA-Z0-9]$`
	nodePoolResourceNameRegex       = regexp.MustCompile(nodePoolResourceName)
	nodePoolResourceNameErrorString = `(must be a valid DNS RFC 1035 label)`

	// resourceGroupName See https://learn.microsoft.com/en-gb/azure/azure-resource-manager/management/resource-name-rules#microsoftresources
	resourceGroupName            = `^[\p{L}\p{N}_\-.()]{0,89}[\p{L}\p{N}_\-()]$`
	resourceGroupNameRegex       = regexp.MustCompile(resourceGroupName)
	resourceGroupNameErrorString = `it must be max 90 characters and only letters, digits, underscores (_), hyphens (-), periods (.), and parentheses (( )) are allowed, and it cannot end with a period '.'.`

	imageDigestSourceRegistry            = `^\*(?:\.(?:[a-zA-Z0-9]|[a-zA-Z0-9][a-zA-Z0-9-]*[a-zA-Z0-9]))+$|^((?:[a-zA-Z0-9]|[a-zA-Z0-9][a-zA-Z0-9-]*[a-zA-Z0-9])(?:(?:\.(?:[a-zA-Z0-9]|[a-zA-Z0-9][a-zA-Z0-9-]*[a-zA-Z0-9]))+)?(?::[0-9]+)?)(?:(?:/[a-z0-9]+(?:(?:(?:[._]|__|[-]*)[a-z0-9]+)+)?)+)?$`
	imageDigestSourceRegistryRegex       = regexp.MustCompile(imageDigestSourceRegistry)
	imageDigestSourceRegistryErrorString = `(must be a valid source image registry)`

	imageDigestMirroredRegistry            = `^((?:[a-zA-Z0-9]|[a-zA-Z0-9][a-zA-Z0-9-]*[a-zA-Z0-9])(?:(?:\.(?:[a-zA-Z0-9]|[a-zA-Z0-9][a-zA-Z0-9-]*[a-zA-Z0-9]))+)?(?::[0-9]+)?)(?:(?:/[a-z0-9]+(?:(?:(?:[._]|__|[-]*)[a-z0-9]+)+)?)+)?$`
	imageDigestMirroredRegistryRegex       = regexp.MustCompile(imageDigestMirroredRegistry)
	imageDigestMirroredRegistryErrorString = `(must be a valid mirrored image registry)`

	azureVMName      = `^[a-zA-Z0-9]([a-zA-Z0-9._-]{0,62}[a-zA-Z0-9_])?$`
	azureVMNameRegex = regexp.MustCompile(azureVMName)
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

func IsValidAzureVMName(name string) bool {
	return azureVMNameRegex.MatchString(name)
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

// isIpAddress checks if the host is a valid IP address (IPv4 or IPv6)
func isIpAddress(host string) bool {
	// Trim brackets for IPv6 addresses
	cleanHost := strings.Trim(host, "[]")
	return net.ParseIP(cleanHost) != nil
}

// hasProtocolPrefix checks if the registry contains http/https protocol prefix
func hasProtocolPrefix(registry string) bool {
	return strings.HasPrefix(registry, "http://") || strings.HasPrefix(registry, "https://")
}

// hasUppercaseLetters checks if the registry contains uppercase letters
func hasUppercaseLetters(registry string) bool {
	return registry != strings.ToLower(registry)
}

// hasWhitespaceCharacters checks if the registry contains whitespace characters
func hasWhitespaceCharacters(registry string) bool {
	return strings.ContainsAny(registry, " \t\n\r\f\v")
}

// extractHostFromPath extracts the host part from a registry path
func extractHostFromPath(registry string) string {
	// Extract host from registry (first part before any path)
	parts := strings.Split(registry, "/")
	return parts[0]
}

// removeWildcardPrefix removes wildcard prefix from host if present
func removeWildcardPrefix(host string) string {
	return strings.TrimPrefix(host, "*.")
}

func ImageRegistry(_ context.Context, _ operation.Operation, fldPath *field.Path, value, _ *string) field.ErrorList {
	// This and supporting functions are heavily based on Cluster
	// Service validation in validation_helpers_image_mirrors.go.
	if value == nil {
		return nil
	}

	if len(*value) == 0 {
		return field.ErrorList{field.Required(fldPath, *value)}
	}

	const maxLen = 255
	if len(*value) > maxLen {
		return field.ErrorList{field.TooLong(fldPath, *value, maxLen)}
	}

	// Basic format validation
	// Supported patterns:
	// host[:port], host[:port]/namespace[/namespace…], host[:port]/namespace[/namespace…]/repo, [*.]host
	//
	// Character restrictions:
	// No protocol prefixes (http/https), uppercase letters, spaces, or special characters
	// except hyphens, dots, colons, slashes

	// Check for protocol prefixes
	if hasProtocolPrefix(*value) {
		return field.ErrorList{field.Invalid(fldPath, *value, "cannot contain protocol prefix (http/https)")}
	}

	// Check for uppercase letters
	if hasUppercaseLetters(*value) {
		return field.ErrorList{field.Invalid(fldPath, *value, "must be lowercase")}
	}

	// Check for whitespace characters.
	if hasWhitespaceCharacters(*value) {
		return field.ErrorList{field.Invalid(fldPath, *value, "cannot contain whitespace characters")}
	}

	// Extract host[:port] from registry path.
	hostPort := extractHostFromPath(*value)

	// Remove wildcardprefix if present.
	hostPort = removeWildcardPrefix(hostPort)

	var host = hostPort

	// Detect and validate a host:port combination.
	if strings.Contains(hostPort, ":") {
		var port string
		var err error

		host, port, err = net.SplitHostPort(hostPort)
		if err != nil {
			// Accept a valid IPv4 or IPv6 address without a port.
			if isIpAddress(hostPort) {
				return nil
			}
			return field.ErrorList{field.Invalid(fldPath, *value, fmt.Sprintf("invalid host:port format: %s", err))}
		}

		// Validate port is not empty.
		if port == "" {
			return field.ErrorList{field.Invalid(fldPath, *value, "empty port")}
		}
	}

	// Accept a valid IPv4 or IPv6 address.
	if isIpAddress(host) {
		return nil
	}

	// Host must be a valid DNS subdomain.
	errMsgs := k8svalidation.IsDNS1123Subdomain(host)
	if len(errMsgs) > 0 {
		errs := field.ErrorList{}
		for _, msg := range errMsgs {
			errs = append(errs, field.Invalid(fldPath, *value, msg))
		}
		return errs
	}

	return nil
}

func ValidateUserAssignedIdentityLocation(ctx context.Context, op operation.Operation, fldPath *field.Path, value, _ *azcorearm.ResourceID, clusterSubscriptionID, managedResourceGroupName string) field.ErrorList {
	if value == nil {
		return nil
	}

	errs := field.ErrorList{}
	errs = append(errs, SameSubscription(ctx, op, fldPath, value, nil, clusterSubscriptionID)...)
	errs = append(errs, DifferentResourceGroupNameFromResourceID(ctx, op, fldPath, value, nil, managedResourceGroupName)...)

	return errs
}

func DifferentResourceGroupNameFromResourceID(ctx context.Context, op operation.Operation, fldPath *field.Path, value, _ *azcorearm.ResourceID, resourceGroupName string) field.ErrorList {
	if value == nil {
		return nil
	}

	if strings.EqualFold(value.ResourceGroupName, resourceGroupName) {
		return field.ErrorList{field.Invalid(fldPath, value.String(), fmt.Sprintf("must not be the same resource group name: %q", resourceGroupName))}
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

func SameSubscription(ctx context.Context, op operation.Operation, fldPath *field.Path, value, _ *azcorearm.ResourceID, subscriptionID string) field.ErrorList {
	if value == nil {
		return nil
	}

	if !strings.EqualFold(value.SubscriptionID, subscriptionID) {
		return field.ErrorList{field.Invalid(fldPath, value.String(), fmt.Sprintf("must be in the same Azure subscription: %q", subscriptionID))}
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

func newRestrictedResourceID(resourceTypeRestriction string) validate.ValidateFunc[**azcorearm.ResourceID] {
	return func(ctx context.Context, op operation.Operation, fldPath *field.Path, newValue, oldValue **azcorearm.ResourceID) field.ErrorList {
		switch {
		case newValue == nil && oldValue == nil:
			return nil
		case newValue == nil && oldValue != nil:
			return nil
		case newValue != nil && oldValue == nil:
			return RestrictedResourceIDWithResourceGroup(ctx, op, fldPath, *newValue, nil, resourceTypeRestriction)
		case newValue != nil && oldValue != nil:
			return RestrictedResourceIDWithResourceGroup(ctx, op, fldPath, *newValue, *oldValue, resourceTypeRestriction)
		}
		panic("unreachable")
	}
}

// newRestrictedResourceIDString exists because actual resourceIDs cannot be map keys
func newRestrictedResourceIDString(resourceTypeRestriction string) validate.ValidateFunc[*string] {
	return func(ctx context.Context, op operation.Operation, fldPath *field.Path, newValue, oldValue *string) field.ErrorList {
		switch {
		case newValue == nil && oldValue == nil:
			return nil
		case newValue == nil && oldValue != nil:
			return nil
		case newValue != nil && oldValue == nil:
			newValueResourceID, err := azcorearm.ParseResourceID(*newValue)
			if err != nil {
				return field.ErrorList{field.Invalid(fldPath, *newValue, err.Error())}
			}
			return RestrictedResourceIDWithResourceGroup(ctx, op, fldPath, newValueResourceID, nil, resourceTypeRestriction)
		case newValue != nil && oldValue != nil:
			newValueResourceID, err := azcorearm.ParseResourceID(*newValue)
			if err != nil {
				return field.ErrorList{field.Invalid(fldPath, *newValue, err.Error())}
			}
			oldValueResourceID, err := azcorearm.ParseResourceID(*oldValue)
			if err != nil {
				return field.ErrorList{field.Invalid(fldPath, *oldValue, err.Error())}
			}
			return RestrictedResourceIDWithResourceGroup(ctx, op, fldPath, newValueResourceID, oldValueResourceID, resourceTypeRestriction)
		}
		panic("unreachable")
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

// EQ matches validate.NEQ
func EQ[T comparable](_ context.Context, _ operation.Operation, fldPath *field.Path, value, _ *T, allowed T) field.ErrorList {
	if value == nil {
		return nil
	}
	if *value != allowed {
		return field.ErrorList{
			field.Invalid(fldPath, *value, fmt.Sprintf("must be equal to %v", allowed)),
		}
	}
	return nil
}

func KubeLabelValue(_ context.Context, _ operation.Operation, fldPath *field.Path, value, _ *string) field.ErrorList {
	if value == nil {
		return nil
	}
	if kubeErrs := k8svalidation.IsValidLabelValue(*value); len(kubeErrs) > 0 {
		errs := field.ErrorList{}
		for _, err := range kubeErrs {
			errs = append(errs, field.Invalid(fldPath, *value, err))
		}
		return errs
	}

	return nil
}

func KubeQualifiedName(_ context.Context, _ operation.Operation, fldPath *field.Path, value, _ *string) field.ErrorList {
	if value == nil {
		return nil
	}
	if kubeErrs := k8svalidation.IsQualifiedName(*value); len(kubeErrs) > 0 {
		errs := field.ErrorList{}
		for _, err := range kubeErrs {
			errs = append(errs, field.Invalid(fldPath, *value, err))
		}
		return errs
	}

	return nil
}

// MaximumIfNoAZ validates that a value doesn't exceed max ONLY when availabilityZone is empty.
// When availabilityZone is set, no maximum limit is enforced.
func MaximumIfNoAZ[T constraints.Integer](ctx context.Context, op operation.Operation, fldPath *field.Path, value, oldValue *T, max T, availabilityZone string) field.ErrorList {
	if value == nil {
		return nil
	}

	// If availability zone is set, no max limit applies
	if availabilityZone != "" {
		return nil
	}

	// If availability zone is NOT set, enforce the max limit using the existing Maximum validator
	return Maximum(ctx, op, fldPath, value, oldValue, max)
}

// ValidateMajorUpgrade validates that a cross-major version upgrade follows the allowed upgrade paths.
// Returns an error if the upgrade path is not allowed, nil otherwise.
// Use the n-1 skew
func ValidateMajorUpgrade(fromVersion, toVersion semver.Version) error {
	sourceKey := fmt.Sprintf("%d.%d", fromVersion.Major, fromVersion.Minor)
	targetKey := fmt.Sprintf("%d.%d", toVersion.Major, toVersion.Minor)

	allowedTargets, exists := api.AllowMajorUpgradePaths[sourceKey]
	if !exists {
		return fmt.Errorf("invalid upgrade path from %s to %s: major version upgrades are not supported",
			fromVersion.String(), toVersion.String())
	}

	if allowedTargets != targetKey {
		return fmt.Errorf("invalid upgrade path from %s to %s: %s can only upgrade to %s",
			fromVersion.String(), toVersion.String(), sourceKey, allowedTargets)
	}

	return nil
}

// ValidateNodePoolUpgrade performs common node pool version upgrade validation:
// - No downgrades from highest active version
// - Cannot exceed lowest control plane version
// - No major version changes without AFEC (uses existing ValidateMajorUpgrade)
// - No minor version skipping
func ValidateNodePoolUpgrade(desiredVersion semver.Version, activeVersions []api.HCPNodePoolActiveVersion, lowestCPVersion *semver.Version, allowMajorUpgrade bool) error {
	// Skip if already in active versions
	if slices.ContainsFunc(activeVersions, func(av api.HCPNodePoolActiveVersion) bool {
		return av.Version != nil && av.Version.EQ(desiredVersion)
	}) {
		return nil
	}

	lowest, highest := apihelpers.FindLowestAndHighestNodePoolVersion(activeVersions)

	// No partial downgrades: desiredVersion >= highest active version
	if highest != nil && desiredVersion.LT(*highest) {
		return fmt.Errorf(
			"invalid node pool version %s: cannot downgrade from current version %s",
			desiredVersion.String(), highest.String(),
		)
	}

	// Check if the desiredVersion <= control plane versions
	// TODO: We may relax this constraint in the future
	if lowestCPVersion != nil && desiredVersion.GT(*lowestCPVersion) {
		return fmt.Errorf(
			"invalid node pool version %s: cannot exceed control plane version %s",
			desiredVersion.String(), lowestCPVersion.String(),
		)
	}

	// No major version change unless FeatureExperimentalReleaseFeatures is registered
	if lowest != nil && desiredVersion.Major > lowest.Major {
		if !allowMajorUpgrade {
			return fmt.Errorf("major version changes are not supported")
		}
		return ValidateMajorUpgrade(*lowest, desiredVersion)
	}

	// Minor skip validation
	if lowest != nil && desiredVersion.Minor > lowest.Minor+1 {
		return fmt.Errorf(
			"invalid upgrade path from %s to %s: skipping minor versions is not allowed",
			lowest.String(), desiredVersion.String(),
		)
	}

	return nil
}
