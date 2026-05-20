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
	"strings"
	"testing"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/fleet"
)

func validStamp(t *testing.T) *fleet.Stamp {
	t.Helper()
	resourceID := api.Must(fleet.ToStampResourceID("1"))
	return &fleet.Stamp{
		CosmosMetadata: api.CosmosMetadata{
			ResourceID: resourceID,
		},
		ResourceID: resourceID,
	}
}

func TestValidateStampCreate(t *testing.T) {
	t.Parallel()

	type expectedError struct {
		message   string
		fieldPath string
	}

	tests := []struct {
		name         string
		modify       func(t *testing.T, s *fleet.Stamp)
		expectErrors []expectedError
	}{
		// Valid cases
		{
			name:         "valid single char digit",
			modify:       func(t *testing.T, s *fleet.Stamp) {},
			expectErrors: nil,
		},
		{
			name: "valid two chars letters",
			modify: func(t *testing.T, s *fleet.Stamp) {
				resourceID := api.Must(fleet.ToStampResourceID("ab"))
				s.CosmosMetadata.ResourceID = resourceID
				s.ResourceID = resourceID
			},
			expectErrors: nil,
		},
		{
			name: "valid three chars mixed",
			modify: func(t *testing.T, s *fleet.Stamp) {
				resourceID := api.Must(fleet.ToStampResourceID("1a2"))
				s.CosmosMetadata.ResourceID = resourceID
				s.ResourceID = resourceID
			},
			expectErrors: nil,
		},
		{
			name: "valid three chars all digits",
			modify: func(t *testing.T, s *fleet.Stamp) {
				resourceID := api.Must(fleet.ToStampResourceID("123"))
				s.CosmosMetadata.ResourceID = resourceID
				s.ResourceID = resourceID
			},
			expectErrors: nil,
		},
		{
			name: "valid three chars all letters",
			modify: func(t *testing.T, s *fleet.Stamp) {
				resourceID := api.Must(fleet.ToStampResourceID("abc"))
				s.CosmosMetadata.ResourceID = resourceID
				s.ResourceID = resourceID
			},
			expectErrors: nil,
		},
		// Invalid cases
		{
			name: "empty stamp identifier rejected",
			modify: func(t *testing.T, s *fleet.Stamp) {
				s.CosmosMetadata.ResourceID = nil
				s.ResourceID = nil
			},
			expectErrors: []expectedError{
				{fieldPath: "cosmosMetadata.resourceID", message: "stamp identifier must match"},
			},
		},
		{
			name: "four chars rejected",
			modify: func(t *testing.T, s *fleet.Stamp) {
				resourceID := api.Must(azcorearm.ParseResourceID("/providers/Microsoft.RedHatOpenShift/stamps/abcd"))
				s.CosmosMetadata.ResourceID = resourceID
				s.ResourceID = resourceID
			},
			expectErrors: []expectedError{
				{fieldPath: "cosmosMetadata.resourceID", message: "stamp identifier must match"},
			},
		},
		{
			name: "uppercase rejected",
			modify: func(t *testing.T, s *fleet.Stamp) {
				resourceID := api.Must(azcorearm.ParseResourceID("/providers/Microsoft.RedHatOpenShift/stamps/ABC"))
				s.CosmosMetadata.ResourceID = resourceID
				s.ResourceID = resourceID
			},
			expectErrors: []expectedError{
				{fieldPath: "cosmosMetadata.resourceID", message: "stamp identifier must match"},
			},
		},
		{
			name: "special chars rejected",
			modify: func(t *testing.T, s *fleet.Stamp) {
				resourceID := api.Must(azcorearm.ParseResourceID("/providers/Microsoft.RedHatOpenShift/stamps/a-b"))
				s.CosmosMetadata.ResourceID = resourceID
				s.ResourceID = resourceID
			},
			expectErrors: []expectedError{
				{fieldPath: "cosmosMetadata.resourceID", message: "stamp identifier must match"},
			},
		},
		{
			name: "spaces rejected",
			modify: func(t *testing.T, s *fleet.Stamp) {
				resourceID := api.Must(azcorearm.ParseResourceID("/providers/Microsoft.RedHatOpenShift/stamps/a b"))
				s.CosmosMetadata.ResourceID = resourceID
				s.ResourceID = resourceID
			},
			expectErrors: []expectedError{
				{fieldPath: "cosmosMetadata.resourceID", message: "stamp identifier must match"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := validStamp(t)
			tt.modify(t, s)
			errs := ValidateStampCreate(context.Background(), s)

			if len(tt.expectErrors) == 0 {
				if len(errs) != 0 {
					t.Errorf("expected no errors, got %d: %v", len(errs), errs)
				}
				return
			}
			for _, expectedErr := range tt.expectErrors {
				found := false
				for _, err := range errs {
					if strings.Contains(err.Error(), expectedErr.message) && strings.Contains(err.Field, expectedErr.fieldPath) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected error containing message %q at field %q but not found in: %v", expectedErr.message, expectedErr.fieldPath, errs)
				}
			}
		})
	}
}

func TestValidateStampUpdate(t *testing.T) {
	t.Parallel()

	type expectedError struct {
		message   string
		fieldPath string
	}

	tests := []struct {
		name         string
		modify       func(t *testing.T, s *fleet.Stamp)
		expectErrors []expectedError
	}{
		// Valid cases
		{
			name:         "valid update no changes",
			modify:       func(t *testing.T, s *fleet.Stamp) {},
			expectErrors: nil,
		},
		{
			name: "valid two chars letters",
			modify: func(t *testing.T, s *fleet.Stamp) {
				resourceID := api.Must(fleet.ToStampResourceID("ab"))
				s.CosmosMetadata.ResourceID = resourceID
				s.ResourceID = resourceID
			},
			expectErrors: nil,
		},
		{
			name: "valid three chars mixed",
			modify: func(t *testing.T, s *fleet.Stamp) {
				resourceID := api.Must(fleet.ToStampResourceID("1a2"))
				s.CosmosMetadata.ResourceID = resourceID
				s.ResourceID = resourceID
			},
			expectErrors: nil,
		},
		{
			name: "valid three chars all digits",
			modify: func(t *testing.T, s *fleet.Stamp) {
				resourceID := api.Must(fleet.ToStampResourceID("123"))
				s.CosmosMetadata.ResourceID = resourceID
				s.ResourceID = resourceID
			},
			expectErrors: nil,
		},
		{
			name: "valid three chars all letters",
			modify: func(t *testing.T, s *fleet.Stamp) {
				resourceID := api.Must(fleet.ToStampResourceID("abc"))
				s.CosmosMetadata.ResourceID = resourceID
				s.ResourceID = resourceID
			},
			expectErrors: nil,
		},
		// Invalid cases
		{
			name: "empty stamp identifier rejected",
			modify: func(t *testing.T, s *fleet.Stamp) {
				s.CosmosMetadata.ResourceID = nil
				s.ResourceID = nil
			},
			expectErrors: []expectedError{
				{fieldPath: "cosmosMetadata.resourceID", message: "stamp identifier must match"},
			},
		},
		{
			name: "four chars rejected",
			modify: func(t *testing.T, s *fleet.Stamp) {
				resourceID := api.Must(azcorearm.ParseResourceID("/providers/Microsoft.RedHatOpenShift/stamps/abcd"))
				s.CosmosMetadata.ResourceID = resourceID
				s.ResourceID = resourceID
			},
			expectErrors: []expectedError{
				{fieldPath: "cosmosMetadata.resourceID", message: "stamp identifier must match"},
			},
		},
		{
			name: "uppercase rejected",
			modify: func(t *testing.T, s *fleet.Stamp) {
				resourceID := api.Must(azcorearm.ParseResourceID("/providers/Microsoft.RedHatOpenShift/stamps/ABC"))
				s.CosmosMetadata.ResourceID = resourceID
				s.ResourceID = resourceID
			},
			expectErrors: []expectedError{
				{fieldPath: "cosmosMetadata.resourceID", message: "stamp identifier must match"},
			},
		},
		{
			name: "special chars rejected",
			modify: func(t *testing.T, s *fleet.Stamp) {
				resourceID := api.Must(azcorearm.ParseResourceID("/providers/Microsoft.RedHatOpenShift/stamps/a-b"))
				s.CosmosMetadata.ResourceID = resourceID
				s.ResourceID = resourceID
			},
			expectErrors: []expectedError{
				{fieldPath: "cosmosMetadata.resourceID", message: "stamp identifier must match"},
			},
		},
		{
			name: "spaces rejected",
			modify: func(t *testing.T, s *fleet.Stamp) {
				resourceID := api.Must(azcorearm.ParseResourceID("/providers/Microsoft.RedHatOpenShift/stamps/a b"))
				s.CosmosMetadata.ResourceID = resourceID
				s.ResourceID = resourceID
			},
			expectErrors: []expectedError{
				{fieldPath: "cosmosMetadata.resourceID", message: "stamp identifier must match"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			oldObj := validStamp(t)
			newObj := validStamp(t)
			tt.modify(t, newObj)
			errs := ValidateStampUpdate(context.Background(), newObj, oldObj)

			if len(tt.expectErrors) == 0 {
				if len(errs) != 0 {
					t.Errorf("expected no errors, got %d: %v", len(errs), errs)
				}
				return
			}
			for _, expectedErr := range tt.expectErrors {
				found := false
				for _, err := range errs {
					if strings.Contains(err.Error(), expectedErr.message) && strings.Contains(err.Field, expectedErr.fieldPath) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected error containing message %q at field %q but not found in: %v", expectedErr.message, expectedErr.fieldPath, errs)
				}
			}
		})
	}
}
