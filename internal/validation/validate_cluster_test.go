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
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/api/operation"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/utils/ptr"
)

type expectedError struct {
	message   string // Expected error message (partial match)
	fieldPath string // Expected field path for the error
}

func verifyErrorsMatch(t *testing.T, expectedErrors []expectedError, errs field.ErrorList) {
	if len(expectedErrors) != len(errs) {
		t.Errorf("expected %d errors, got %d: %v", len(expectedErrors), len(errs), errs)
	}

	// Check that each expected error message and field path is found
	for _, expectedErr := range expectedErrors {
		if len(strings.TrimSpace(expectedErr.fieldPath)) == 0 {
			t.Errorf("expected error with path %s to be non-empty", expectedErr.fieldPath)
		}
		if len(strings.TrimSpace(expectedErr.message)) == 0 {
			t.Errorf("expected error with msg %s to be non-empty", expectedErr.message)
		}

		found := false
		for _, err := range errs {
			messageMatch := strings.Contains(err.Detail, expectedErr.message) || strings.Contains(err.Error(), expectedErr.message)
			fieldMatch := strings.Contains(err.Field, expectedErr.fieldPath)
			if messageMatch && fieldMatch {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected error containing message '%s' at field '%s' but not found in: %v", expectedErr.message, expectedErr.fieldPath, errs)
		}
	}

	for _, err := range errs {
		found := false
		for _, expectedErr := range expectedErrors {
			messageMatch := strings.Contains(err.Detail, expectedErr.message) || strings.Contains(err.Error(), expectedErr.message)
			fieldMatch := strings.Contains(err.Field, expectedErr.fieldPath)
			if messageMatch && fieldMatch {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("actual error '%v' but not found in expected", err)
		}
	}
}

func TestOpenshiftVersion(t *testing.T) {
	ctx := context.Background()
	op := operation.Operation{Type: operation.Create}
	fldPath := field.NewPath("version")

	tests := []struct {
		name         string
		value        *string
		expectErrors []expectedError
	}{
		{
			name:         "nil value - valid",
			value:        nil,
			expectErrors: []expectedError{},
		},
		{
			name:         "empty string - valid",
			value:        ptr.To(""),
			expectErrors: []expectedError{},
		},
		{
			name:  "semver with patch - invalid",
			value: ptr.To("4.15.1"),
			expectErrors: []expectedError{
				{fieldPath: "version", message: "must be specified as MAJOR.MINOR"},
			},
		},
		{
			name:         "valid major.minor - valid",
			value:        ptr.To("4.15"),
			expectErrors: []expectedError{},
		},
		{
			name:  "invalid version - invalid",
			value: ptr.To("not-a-version"),
			expectErrors: []expectedError{
				{fieldPath: "version", message: "Malformed version"},
			},
		},
		{
			name:  "invalid format - invalid",
			value: ptr.To("invalid.version.format"),
			expectErrors: []expectedError{
				{fieldPath: "version", message: "Malformed version"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := OpenshiftVersionWithoutMicro(ctx, op, fldPath, tt.value, nil)
			verifyErrorsMatch(t, tt.expectErrors, errs)
		})
	}
}

func TestMaxItems(t *testing.T) {
	ctx := context.Background()
	op := operation.Operation{Type: operation.Create}
	fldPath := field.NewPath("items")

	tests := []struct {
		name         string
		value        []string
		maxLen       int
		expectErrors []expectedError
	}{
		{
			name:         "nil slice - valid",
			value:        nil,
			maxLen:       5,
			expectErrors: []expectedError{},
		},
		{
			name:         "empty slice - valid",
			value:        []string{},
			maxLen:       5,
			expectErrors: []expectedError{},
		},
		{
			name:         "under limit - valid",
			value:        []string{"a", "b", "c"},
			maxLen:       5,
			expectErrors: []expectedError{},
		},
		{
			name:         "at limit - valid",
			value:        []string{"a", "b", "c", "d", "e"},
			maxLen:       5,
			expectErrors: []expectedError{},
		},
		{
			name:   "over limit - invalid",
			value:  []string{"a", "b", "c", "d", "e", "f"},
			maxLen: 5,
			expectErrors: []expectedError{
				{fieldPath: "items", message: "must have at most 5 items"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := MaxItems(ctx, op, fldPath, tt.value, nil, tt.maxLen)
			verifyErrorsMatch(t, tt.expectErrors, errs)
		})
	}
}

func TestMaxLen(t *testing.T) {
	ctx := context.Background()
	op := operation.Operation{Type: operation.Create}
	fldPath := field.NewPath("field")

	tests := []struct {
		name         string
		value        *string
		maxLen       int
		expectErrors []expectedError
	}{
		{
			name:         "nil value - valid",
			value:        nil,
			maxLen:       10,
			expectErrors: []expectedError{},
		},
		{
			name:         "empty string - valid",
			value:        ptr.To(""),
			maxLen:       10,
			expectErrors: []expectedError{},
		},
		{
			name:         "under limit - valid",
			value:        ptr.To("test"),
			maxLen:       10,
			expectErrors: []expectedError{},
		},
		{
			name:         "at limit - valid",
			value:        ptr.To("1234567890"),
			maxLen:       10,
			expectErrors: []expectedError{},
		},
		{
			name:   "over limit - invalid",
			value:  ptr.To("12345678901"),
			maxLen: 10,
			expectErrors: []expectedError{
				{fieldPath: "field", message: "may not be more than 10 bytes"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := MaxLen(ctx, op, fldPath, tt.value, nil, tt.maxLen)
			verifyErrorsMatch(t, tt.expectErrors, errs)
		})
	}
}

func TestMinLen(t *testing.T) {
	ctx := context.Background()
	op := operation.Operation{Type: operation.Create}
	fldPath := field.NewPath("field")

	tests := []struct {
		name         string
		value        *string
		minLen       int
		expectErrors []expectedError
	}{
		{
			name:         "nil value - valid",
			value:        nil,
			minLen:       5,
			expectErrors: []expectedError{},
		},
		{
			name:   "under limit - invalid",
			value:  ptr.To("test"),
			minLen: 5,
			expectErrors: []expectedError{
				{fieldPath: "field", message: "must be at least 5 characters long"},
			},
		},
		{
			name:         "at limit - valid",
			value:        ptr.To("tests"),
			minLen:       5,
			expectErrors: []expectedError{},
		},
		{
			name:         "over limit - valid",
			value:        ptr.To("testing"),
			minLen:       5,
			expectErrors: []expectedError{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := MinLen(ctx, op, fldPath, tt.value, nil, tt.minLen)
			verifyErrorsMatch(t, tt.expectErrors, errs)
		})
	}
}

func TestMaximum(t *testing.T) {
	ctx := context.Background()
	op := operation.Operation{Type: operation.Create}
	fldPath := field.NewPath("field")

	tests := []struct {
		name         string
		value        *int32
		max          int32
		expectErrors []expectedError
	}{
		{
			name:         "nil value - valid",
			value:        nil,
			max:          100,
			expectErrors: []expectedError{},
		},
		{
			name:         "under limit - valid",
			value:        ptr.To(int32(50)),
			max:          100,
			expectErrors: []expectedError{},
		},
		{
			name:         "at limit - valid",
			value:        ptr.To(int32(100)),
			max:          100,
			expectErrors: []expectedError{},
		},
		{
			name:  "over limit - invalid",
			value: ptr.To(int32(101)),
			max:   100,
			expectErrors: []expectedError{
				{fieldPath: "field", message: "must be less than or equal to 100"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := Maximum(ctx, op, fldPath, tt.value, nil, tt.max)
			verifyErrorsMatch(t, tt.expectErrors, errs)
		})
	}
}

func TestMatchesRegex(t *testing.T) {
	ctx := context.Background()
	op := operation.Operation{Type: operation.Create}
	fldPath := field.NewPath("field")

	tests := []struct {
		name         string
		value        *string
		expectErrors []expectedError
	}{
		{
			name:         "nil value - valid",
			value:        nil,
			expectErrors: []expectedError{},
		},
		{
			name:         "valid rfc1035 label - valid",
			value:        ptr.To("test-label"),
			expectErrors: []expectedError{},
		},
		{
			name:         "valid single char - valid",
			value:        ptr.To("a"),
			expectErrors: []expectedError{},
		},
		{
			name:  "starts with number - invalid",
			value: ptr.To("1test"),
			expectErrors: []expectedError{
				{fieldPath: "field", message: "must be a valid DNS RFC 1035 label"},
			},
		},
		{
			name:  "contains uppercase - invalid",
			value: ptr.To("Test"),
			expectErrors: []expectedError{
				{fieldPath: "field", message: "must be a valid DNS RFC 1035 label"},
			},
		},
		{
			name:  "starts with hyphen - invalid",
			value: ptr.To("-test"),
			expectErrors: []expectedError{
				{fieldPath: "field", message: "must be a valid DNS RFC 1035 label"},
			},
		},
		{
			name:  "ends with hyphen - invalid",
			value: ptr.To("test-"),
			expectErrors: []expectedError{
				{fieldPath: "field", message: "must be a valid DNS RFC 1035 label"},
			},
		},
		{
			name:  "contains special chars - invalid",
			value: ptr.To("test_label"),
			expectErrors: []expectedError{
				{fieldPath: "field", message: "must be a valid DNS RFC 1035 label"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := MatchesRegex(ctx, op, fldPath, tt.value, nil, rfc1035LabelRegex, rfc1035ErrorString)
			verifyErrorsMatch(t, tt.expectErrors, errs)

		})
	}
}

func TestCIDRv4(t *testing.T) {
	ctx := context.Background()
	op := operation.Operation{Type: operation.Create}
	fldPath := field.NewPath("cidr")

	tests := []struct {
		name         string
		value        *string
		expectErrors []expectedError
	}{
		{
			name:         "nil value - valid",
			value:        nil,
			expectErrors: []expectedError{},
		},
		{
			name:         "empty string - valid",
			value:        ptr.To(""),
			expectErrors: []expectedError{},
		},
		{
			name:         "valid IPv4 CIDR - valid",
			value:        ptr.To("10.0.0.0/16"),
			expectErrors: []expectedError{},
		},
		{
			name:         "valid /24 CIDR - valid",
			value:        ptr.To("192.168.1.0/24"),
			expectErrors: []expectedError{},
		},
		{
			name:         "valid /32 CIDR - valid",
			value:        ptr.To("172.16.0.1/32"),
			expectErrors: []expectedError{},
		},
		{
			name:  "IPv6 CIDR - invalid",
			value: ptr.To("2001:db8::/32"),
			expectErrors: []expectedError{
				{fieldPath: "cidr", message: "not IPv4"},
			},
		},
		{
			name:  "invalid CIDR format - invalid",
			value: ptr.To("10.0.0.0"),
			expectErrors: []expectedError{
				{fieldPath: "cidr", message: "invalid CIDR address"},
			},
		},
		{
			name:  "invalid IP - invalid",
			value: ptr.To("300.0.0.0/16"),
			expectErrors: []expectedError{
				{fieldPath: "cidr", message: "invalid CIDR address"},
			},
		},
		{
			name:  "host IP instead of network - invalid",
			value: ptr.To("10.0.0.1/16"),
			expectErrors: []expectedError{
				{fieldPath: "cidr", message: "not IPv4 CIDR"},
			},
		},
		{
			name:  "invalid prefix length - invalid",
			value: ptr.To("10.0.0.0/33"),
			expectErrors: []expectedError{
				{fieldPath: "cidr", message: "invalid CIDR address"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := CIDRv4(ctx, op, fldPath, tt.value, nil)
			verifyErrorsMatch(t, tt.expectErrors, errs)

		})
	}
}

func TestIPv4(t *testing.T) {
	ctx := context.Background()
	op := operation.Operation{Type: operation.Create}
	fldPath := field.NewPath("ip")

	tests := []struct {
		name      string
		value     *string
		expectErr bool
	}{
		{
			name:      "nil value - valid",
			value:     nil,
			expectErr: false,
		},
		{
			name:      "empty string - valid",
			value:     ptr.To(""),
			expectErr: false,
		},
		{
			name:      "valid IPv4 - valid",
			value:     ptr.To("192.168.1.1"),
			expectErr: false,
		},
		{
			name:      "valid localhost - valid",
			value:     ptr.To("127.0.0.1"),
			expectErr: false,
		},
		{
			name:      "valid zero IP - valid",
			value:     ptr.To("0.0.0.0"),
			expectErr: false,
		},
		{
			name:      "IPv6 address - invalid",
			value:     ptr.To("2001:db8::1"),
			expectErr: true,
		},
		{
			name:      "invalid IP format - invalid",
			value:     ptr.To("300.0.0.1"),
			expectErr: true,
		},
		{
			name:      "not an IP - invalid",
			value:     ptr.To("not-an-ip"),
			expectErr: true,
		},
		{
			name:      "CIDR notation - invalid",
			value:     ptr.To("192.168.1.1/24"),
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := IPv4(ctx, op, fldPath, tt.value, nil)

			if tt.expectErr && len(errs) == 0 {
				t.Error("expected error but got none")
			}
			if !tt.expectErr && len(errs) > 0 {
				t.Errorf("expected no error but got: %v", errs)
			}
		})
	}
}

func TestResourceID(t *testing.T) {
	ctx := context.Background()
	op := operation.Operation{Type: operation.Create}
	fldPath := field.NewPath("resourceId")

	tests := []struct {
		name      string
		value     *string
		expectErr bool
	}{
		{
			name:      "nil value - valid",
			value:     nil,
			expectErr: false,
		},
		{
			name:      "empty string - valid",
			value:     ptr.To(""),
			expectErr: false,
		},
		{
			name:      "valid resource ID - valid",
			value:     ptr.To("/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/test-vnet"),
			expectErr: false,
		},
		{
			name:      "valid subnet resource ID - valid",
			value:     ptr.To("/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/test-vnet/subnets/test-subnet"),
			expectErr: false,
		},
		{
			name:      "missing subscription - invalid",
			value:     ptr.To("/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/test-vnet"),
			expectErr: true,
		},
		{
			name:      "missing resource group - invalid",
			value:     ptr.To("/subscriptions/12345678-1234-1234-1234-123456789012/providers/Microsoft.Network/virtualNetworks/test-vnet"),
			expectErr: true,
		},
		{
			name:      "missing resource name - invalid",
			value:     ptr.To("/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks"),
			expectErr: true,
		},
		{
			name:      "invalid format - invalid",
			value:     ptr.To("not-a-resource-id"),
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := ResourceIDString(ctx, op, fldPath, tt.value, nil)

			if tt.expectErr && len(errs) == 0 {
				t.Error("expected error but got none")
			}
			if !tt.expectErr && len(errs) > 0 {
				t.Errorf("expected no error but got: %v", errs)
			}
		})
	}
}

func TestRestrictedResourceID(t *testing.T) {
	ctx := context.Background()
	op := operation.Operation{Type: operation.Create}
	fldPath := field.NewPath("resourceId")
	restrictedType := "Microsoft.Network/virtualNetworks"

	tests := []struct {
		name      string
		value     *string
		expectErr bool
	}{
		{
			name:      "nil value - valid",
			value:     nil,
			expectErr: false,
		},
		{
			name:      "empty string - valid",
			value:     ptr.To(""),
			expectErr: false,
		},
		{
			name:      "valid matching resource type - valid",
			value:     ptr.To("/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/test-vnet"),
			expectErr: false,
		},
		{
			name:      "wrong resource type - invalid",
			value:     ptr.To("/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/test-rg/providers/Microsoft.Network/networkSecurityGroups/test-nsg"),
			expectErr: true,
		},
		{
			name:      "missing subscription - invalid",
			value:     ptr.To("/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/test-vnet"),
			expectErr: true,
		},
		{
			name:      "invalid format - invalid",
			value:     ptr.To("not-a-resource-id"),
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := RestrictedResourceIDString(ctx, op, fldPath, tt.value, nil, restrictedType)

			if tt.expectErr && len(errs) == 0 {
				t.Error("expected error but got none")
			}
			if !tt.expectErr && len(errs) > 0 {
				t.Errorf("expected no error but got: %v", errs)
			}
		})
	}
}
