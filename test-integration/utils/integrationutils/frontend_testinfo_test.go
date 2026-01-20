// Copyright 2025 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package integrationutils

import (
	"context"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
)

func TestIsClusterCreateOrUpdateRequest(t *testing.T) {
	tests := []struct {
		name     string
		method   string
		path     string
		expected bool
	}{
		{
			name:     "PUT cluster",
			method:   http.MethodPut,
			path:     "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/my-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/my-cluster",
			expected: true,
		},
		{
			name:     "PATCH cluster",
			method:   http.MethodPatch,
			path:     "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/my-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/my-cluster",
			expected: true,
		},
		{
			name:     "PUT cluster - case insensitive check",
			method:   http.MethodPut,
			path:     "/SUBSCRIPTIONS/12345678-1234-1234-1234-123456789012/RESOURCEGROUPS/MY-RG/PROVIDERS/MICROSOFT.REDHATOPENSHIFT/HCPOPENSHIFTCLUSTERS/MY-CLUSTER",
			expected: true,
		},
		{
			name:     "GET cluster",
			method:   http.MethodGet,
			path:     "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/my-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/my-cluster",
			expected: false,
		},
		{
			name:     "DELETE cluster",
			method:   http.MethodDelete,
			path:     "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/my-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/my-cluster",
			expected: false,
		},
		{
			name:     "PUT preflight",
			method:   http.MethodPut,
			path:     "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/my-rg/providers/Microsoft.RedHatOpenShift/deployments/my-deployment/preflight",
			expected: false,
		},
		{
			name:     "PUT nodePool",
			method:   http.MethodPut,
			path:     "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/my-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/my-cluster/nodePools/my-nodepool",
			expected: false,
		},
	}

	d := emptySystemData{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u, err := url.Parse("http://testurl.com" + tt.path)
			require.NoError(t, err)
			req, err := http.NewRequestWithContext(context.Background(), tt.method, u.String(), nil)
			require.NoError(t, err)
			policyReq, _ := runtime.NewRequestFromRequest(req)
			result := d.isClusterCreateOrUpdateRequest(policyReq)
			assert.Equal(t, tt.expected, result, "method=%s, path=%s", tt.method, tt.path)
		})
	}
}
