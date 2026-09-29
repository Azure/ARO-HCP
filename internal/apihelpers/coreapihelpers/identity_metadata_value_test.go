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

package coreapihelpers

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"k8s.io/utils/ptr"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
)

func TestIdentityMetadataValueHasResolvedIdentityInformation(t *testing.T) {
	tests := []struct {
		name     string
		value    *coreapi.IdentityMetadataValue
		expected bool
	}{
		{
			name: "all fields resolved result in resolved identity information",
			value: &coreapi.IdentityMetadataValue{
				ClientID:    ptr.To("client-id"),
				PrincipalID: ptr.To("principal-id"),
				TenantID:    ptr.To("tenant-id"),
			},
			expected: true,
		},
		{
			name: "empty value results in unresolved identity metadata information",
			// The inner attributes are nil in this case, which the test covers
			value:    &coreapi.IdentityMetadataValue{},
			expected: false,
		},
		{
			name: "retrieval error set even with fields populated results in resolved identity metadata information",
			value: &coreapi.IdentityMetadataValue{
				ClientID:       ptr.To("client-id"),
				PrincipalID:    ptr.To("principal-id"),
				TenantID:       ptr.To("tenant-id"),
				RetrievalError: ptr.To("boom"),
			},
			expected: false,
		},
		{
			name: "missing client ID results in unresolved identity metadata information",
			value: &coreapi.IdentityMetadataValue{
				PrincipalID: ptr.To("principal-id"),
				TenantID:    ptr.To("tenant-id"),
			},
			expected: false,
		},
		{
			name: "missing principal ID results in unresolved identity metadata information",
			value: &coreapi.IdentityMetadataValue{
				ClientID: ptr.To("client-id"),
				TenantID: ptr.To("tenant-id"),
			},
			expected: false,
		},
		{
			name: "missing tenant ID results in unresolved identity metadata information",
			value: &coreapi.IdentityMetadataValue{
				ClientID:    ptr.To("client-id"),
				PrincipalID: ptr.To("principal-id"),
			},
			expected: false,
		},
		{
			name: "empty string fields are treated as unresolved",
			value: &coreapi.IdentityMetadataValue{
				ClientID:    ptr.To(""),
				PrincipalID: ptr.To(""),
				TenantID:    ptr.To(""),
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, IdentityMetadataValueHasResolvedIdentityInformation(tt.value))
		})
	}
}
