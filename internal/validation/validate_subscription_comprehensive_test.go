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

	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// Comprehensive tests for ValidateSubscriptionCreate
func TestValidateSubscriptionCreate(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name         string
		subscription *arm.Subscription
		expectErrors []utils.ExpectedError
	}{
		{
			name:         "valid subscription - create",
			subscription: createValidSubscription(),
			expectErrors: []utils.ExpectedError{},
		},
		{
			name: "valid subscription with all fields - create",
			subscription: func() *arm.Subscription {
				s := createValidSubscription()
				s.Properties = &arm.SubscriptionProperties{
					TenantId: ptr.To("tenant-id-123"),
				}
				return s
			}(),
			expectErrors: []utils.ExpectedError{},
		},
		{
			name: "valid subscription - all valid states",
			subscription: func() *arm.Subscription {
				s := createValidSubscription()
				s.State = arm.SubscriptionStateRegistered
				return s
			}(),
			expectErrors: []utils.ExpectedError{},
		},
		{
			name: "missing required resource ID",
			subscription: func() *arm.Subscription {
				s := createValidSubscription()
				s.ResourceID = nil
				return s
			}(),
			expectErrors: []utils.ExpectedError{
				{Message: "Required value", FieldPath: "id"},
			},
		},
		{
			name: "invalid resource ID type",
			subscription: func() *arm.Subscription {
				s := createValidSubscription()
				// Use a cluster resource ID instead of subscription
				s.ResourceID = api.Must(azcorearm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster"))
				return s
			}(),
			expectErrors: []utils.ExpectedError{
				{Message: "resource ID must reference an instance of type \"Microsoft.Resources/subscriptions\"", FieldPath: "id"},
				{Message: "resource group must be empty", FieldPath: "id"}, // Should not have resource group
				{Message: "invalid UUID length", FieldPath: "id"},          // test-sub is not a valid UUID
			},
		},
		{
			name: "missing subscription ID in resource ID",
			subscription: func() *arm.Subscription {
				s := createValidSubscription()
				s.ResourceID = &azcorearm.ResourceID{} // Empty ResourceID to test missing fields
				return s
			}(),
			expectErrors: []utils.ExpectedError{
				{Message: "subscription ID is required", FieldPath: "id"},
				{Message: "resource name is required", FieldPath: "id"},
				{Message: "resource ID must reference an instance of type \"Microsoft.Resources/subscriptions\"", FieldPath: "id"},
			},
		},
		{
			name: "invalid subscription ID format",
			subscription: func() *arm.Subscription {
				s := createValidSubscription()
				s.ResourceID = api.Must(azcorearm.ParseResourceID("/subscriptions/invalid-uuid"))
				return s
			}(),
			expectErrors: []utils.ExpectedError{
				{Message: "invalid UUID length", FieldPath: "id"},
			},
		},
		{
			name: "missing registration date",
			subscription: func() *arm.Subscription {
				s := createValidSubscription()
				s.RegistrationDate = nil
				return s
			}(),
			expectErrors: []utils.ExpectedError{
				{Message: "Required value", FieldPath: "registrationDate"},
			},
		},
		{
			name: "registration date with extra whitespace",
			subscription: func() *arm.Subscription {
				s := createValidSubscription()
				s.RegistrationDate = ptr.To("  2023-01-01T00:00:00Z  ")
				return s
			}(),
			expectErrors: []utils.ExpectedError{
				{Message: "must not contain extra whitespace", FieldPath: "registrationDate"},
			},
		},
		{
			name: "invalid subscription state",
			subscription: func() *arm.Subscription {
				s := createValidSubscription()
				s.State = arm.SubscriptionState("InvalidState")
				return s
			}(),
			expectErrors: []utils.ExpectedError{
				{Message: "supported values:", FieldPath: "required"},
			},
		},
		{
			name: "empty subscription state",
			subscription: func() *arm.Subscription {
				s := createValidSubscription()
				s.State = arm.SubscriptionState("")
				return s
			}(),
			expectErrors: []utils.ExpectedError{
				{Message: "supported values:", FieldPath: "required"},
			},
		},
		{
			name: "multiple validation errors",
			subscription: func() *arm.Subscription {
				s := createValidSubscription()
				s.ResourceID = nil
				s.RegistrationDate = nil
				s.State = arm.SubscriptionState("InvalidState")
				return s
			}(),
			expectErrors: []utils.ExpectedError{
				{Message: "supported values:", FieldPath: "required"},
				{Message: "Required value", FieldPath: "id"},
				{Message: "Required value", FieldPath: "registrationDate"},
			},
		},
		// Test all valid subscription states
		{
			name: "subscription state - Registered",
			subscription: func() *arm.Subscription {
				s := createValidSubscription()
				s.State = arm.SubscriptionStateRegistered
				return s
			}(),
			expectErrors: []utils.ExpectedError{},
		},
		{
			name: "subscription state - Unregistered",
			subscription: func() *arm.Subscription {
				s := createValidSubscription()
				s.State = arm.SubscriptionStateUnregistered
				return s
			}(),
			expectErrors: []utils.ExpectedError{},
		},
		{
			name: "subscription state - Warned",
			subscription: func() *arm.Subscription {
				s := createValidSubscription()
				s.State = arm.SubscriptionStateWarned
				return s
			}(),
			expectErrors: []utils.ExpectedError{},
		},
		{
			name: "subscription state - Deleted",
			subscription: func() *arm.Subscription {
				s := createValidSubscription()
				s.State = arm.SubscriptionStateDeleted
				return s
			}(),
			expectErrors: []utils.ExpectedError{},
		},
		{
			name: "subscription state - Suspended",
			subscription: func() *arm.Subscription {
				s := createValidSubscription()
				s.State = arm.SubscriptionStateSuspended
				return s
			}(),
			expectErrors: []utils.ExpectedError{},
		},
		// Resource naming validation tests (covering middleware_validatestatic_test.go patterns)
		{
			name: "subscription with valid UUID",
			subscription: func() *arm.Subscription {
				s := createValidSubscription()
				s.ResourceID = api.Must(azcorearm.ParseResourceID("/subscriptions/12345678-1234-1234-1234-123456789012"))
				return s
			}(),
			expectErrors: []utils.ExpectedError{},
		},
		{
			name: "subscription with invalid UUID - too short",
			subscription: func() *arm.Subscription {
				s := createValidSubscription()
				s.ResourceID = api.Must(azcorearm.ParseResourceID("/subscriptions/123"))
				return s
			}(),
			expectErrors: []utils.ExpectedError{
				{Message: "invalid UUID length", FieldPath: "id"},
			},
		},
		{
			name: "subscription with invalid UUID - wrong format",
			subscription: func() *arm.Subscription {
				s := createValidSubscription()
				s.ResourceID = api.Must(azcorearm.ParseResourceID("/subscriptions/not-a-uuid-at-all"))
				return s
			}(),
			expectErrors: []utils.ExpectedError{
				{Message: "invalid UUID length", FieldPath: "id"},
			},
		},
		// Test that resource group validation works properly
		{
			name: "subscription with resource group should fail",
			subscription: func() *arm.Subscription {
				s := createValidSubscription()
				s.ResourceID = api.Must(azcorearm.ParseResourceID("/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/should-not-exist"))
				return s
			}(),
			expectErrors: []utils.ExpectedError{
				{Message: "resource ID must reference an instance of type \"Microsoft.Resources/subscriptions\"", FieldPath: "id"},
				{Message: "resource group must be empty", FieldPath: "id"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := ValidateSubscriptionCreate(ctx, tt.subscription)
			utils.VerifyErrorsMatch(t, tt.expectErrors, errs)
		})
	}
}

func createValidSubscription() *arm.Subscription {
	return &arm.Subscription{
		ResourceID:       api.Must(azcorearm.ParseResourceID("/subscriptions/12345678-1234-1234-1234-123456789012")),
		State:            arm.SubscriptionStateRegistered,
		RegistrationDate: ptr.To("2023-01-01T00:00:00Z"),
		Properties:       nil, // Properties are optional
	}
}

// Test registration date validation edge cases
func TestSubscriptionRegistrationDateValidation(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name             string
		registrationDate *string
		expectErrors     []utils.ExpectedError
	}{
		{
			name:             "valid registration date",
			registrationDate: ptr.To("2023-01-01T00:00:00Z"),
			expectErrors:     []utils.ExpectedError{},
		},
		{
			name:             "registration date with leading whitespace",
			registrationDate: ptr.To(" 2023-01-01T00:00:00Z"),
			expectErrors: []utils.ExpectedError{
				{Message: "must not contain extra whitespace", FieldPath: "registrationDate"},
			},
		},
		{
			name:             "registration date with trailing whitespace",
			registrationDate: ptr.To("2023-01-01T00:00:00Z "),
			expectErrors: []utils.ExpectedError{
				{Message: "must not contain extra whitespace", FieldPath: "registrationDate"},
			},
		},
		{
			name:             "registration date with both leading and trailing whitespace",
			registrationDate: ptr.To("  2023-01-01T00:00:00Z  "),
			expectErrors: []utils.ExpectedError{
				{Message: "must not contain extra whitespace", FieldPath: "registrationDate"},
			},
		},
		{
			name:             "registration date with tabs",
			registrationDate: ptr.To("\t2023-01-01T00:00:00Z\t"),
			expectErrors: []utils.ExpectedError{
				{Message: "must not contain extra whitespace", FieldPath: "registrationDate"},
			},
		},
		{
			name:             "empty registration date string",
			registrationDate: ptr.To(""),
			expectErrors:     []utils.ExpectedError{},
		},
		{
			name:             "registration date with only whitespace",
			registrationDate: ptr.To("   "),
			expectErrors: []utils.ExpectedError{
				{Message: "must not contain extra whitespace", FieldPath: "registrationDate"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sub := createValidSubscription()
			sub.RegistrationDate = tt.registrationDate

			errs := ValidateSubscriptionCreate(ctx, sub)

			// Filter only registration date errors
			var registrationDateErrors []utils.ExpectedError
			for _, err := range tt.expectErrors {
				if strings.Contains(err.FieldPath, "registrationDate") {
					registrationDateErrors = append(registrationDateErrors, err)
				}
			}

			if len(registrationDateErrors) == 0 {
				// If no registration date errors expected, verify no registration date errors occurred
				for _, err := range errs {
					if strings.Contains(err.Field, "registrationDate") {
						t.Errorf("Unexpected registration date error: %v", err)
					}
				}
			} else {
				utils.VerifyErrorsMatch(t, registrationDateErrors, errs)
			}
		})
	}
}
