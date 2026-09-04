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

package v20260901preview

import (
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/azureapi/v20260901preview/generated"
)

func TestNormalizeContainerRegistry(t *testing.T) {
	validMIResourceID := "/subscriptions/00000000-0000-0000-0000-000000000001/resourceGroups/rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/mi"
	fldPath := field.NewPath("properties", "platform", "containerRegistry")

	tests := []struct {
		name      string
		input     *generated.ContainerRegistryProfile
		wantNil   bool
		wantError string
	}{
		{
			name:    "nil profile clears output",
			input:   nil,
			wantNil: true,
		},
		{
			name:    "nil managedIdentity clears output",
			input:   &generated.ContainerRegistryProfile{ManagedIdentity: nil},
			wantNil: true,
		},
		{
			name:      "empty string rejected",
			input:     &generated.ContainerRegistryProfile{ManagedIdentity: ptr.To("")},
			wantError: "must be a non-empty resource ID or null to clear",
		},
		{
			name:      "whitespace-only string rejected",
			input:     &generated.ContainerRegistryProfile{ManagedIdentity: ptr.To("   ")},
			wantError: "must be a non-empty resource ID or null to clear",
		},
		{
			name:    "valid resource ID accepted",
			input:   &generated.ContainerRegistryProfile{ManagedIdentity: ptr.To(validMIResourceID)},
			wantNil: false,
		},
		{
			name:      "invalid resource ID returns parse error",
			input:     &generated.ContainerRegistryProfile{ManagedIdentity: ptr.To("not-a-resource-id")},
			wantError: "not-a-resource-id",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out *azcorearm.ResourceID
			errs := normalizeContainerRegistry(fldPath, tt.input, &out)

			if tt.wantError != "" {
				if len(errs) == 0 {
					t.Fatalf("expected error containing %q, got none", tt.wantError)
				}
				for _, e := range errs {
					if strings.Contains(e.Error(), tt.wantError) {
						return
					}
				}
				t.Fatalf("expected error containing %q, got: %v", tt.wantError, errs)
			}

			if tt.input != nil && tt.input.ManagedIdentity != nil && !tt.wantNil && tt.wantError == "" {
				// valid resource ID case — parse error test skips out check
				if len(errs) == 0 && out == nil {
					t.Error("expected out to be set for valid resource ID, got nil")
				}
				return
			}

			if tt.wantNil && out != nil {
				t.Errorf("expected out to be nil, got %v", out)
			}
			if len(errs) != 0 {
				t.Errorf("expected no errors, got: %v", errs)
			}
		})
	}
}
