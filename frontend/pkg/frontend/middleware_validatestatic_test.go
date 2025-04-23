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

package frontend

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/Azure/ARO-HCP/internal/api/arm"
)

type CloudErrorContainer struct {
	Error arm.CloudErrorBody `json:"error"`
}

func TestMiddlewareValidateStatic(t *testing.T) {
	// This will act as the next handler if middleware validation passes
	nextHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK) // indicate success
	})

	tests := []struct {
		name string
		path string

		operationsId       string
		expectedStatusCode int
		expectedBody       string
	}{
		{
			name:               "Valid request for a subscription resource",
			path:               "/Subscriptions/42d9eac4-d29a-4d6e-9e26-3439758b1491",
			expectedStatusCode: http.StatusOK,
		},
		{
			name:               "Invalid subscription ID",
			path:               "/Subscriptions/invalid!sub!id",
			expectedStatusCode: http.StatusBadRequest,
			expectedBody:       "The provided subscription identifier 'invalid!sub!id' is malformed or invalid.",
		},
		{
			name:               "Valid request for a hcpopenshiftcluster resource",
			path:               "/Subscriptions/42d9eac4-d29a-4d6e-9e26-3439758b1491/ResourceGroups/MyResourceGroup/Providers/Microsoft.RedHatOpenShift/HCPOpenShiftClusters/MyCluster",
			expectedStatusCode: http.StatusOK,
		},
		{
			name:               "Invalid hcpopenshiftcluster resource name",
			path:               "/SUBSCRIPTIONS/00000000-0000-0000-0000-000000000000/RESOURCEGROUPS/MyResourceGroup/PROVIDERS/MICROSOFT.REDHATOPENSHIFT/HCPOPENSHIFTCLUSTERS/$",
			expectedStatusCode: http.StatusBadRequest,
			expectedBody:       "The Resource 'MICROSOFT.REDHATOPENSHIFT/HCPOPENSHIFTCLUSTERS/$' under resource group 'MyResourceGroup' does not conform to the naming restriction.",
		},
		{
			name:               "Invalid hcpopenshiftcluster resource name, starts with a '-'",
			path:               "/SUBSCRIPTIONS/00000000-0000-0000-0000-000000000000/RESOURCEGROUPS/MyResourceGroup/PROVIDERS/MICROSOFT.REDHATOPENSHIFT/HCPOPENSHIFTCLUSTERS/-garbage",
			expectedStatusCode: http.StatusBadRequest,
			expectedBody:       "The Resource 'MICROSOFT.REDHATOPENSHIFT/HCPOPENSHIFTCLUSTERS/-garbage' under resource group 'MyResourceGroup' does not conform to the naming restriction.",
		},
		{
			name:               "Invalid hcpopenshiftcluster resource name, too long",
			path:               "/SUBSCRIPTIONS/00000000-0000-0000-0000-000000000000/RESOURCEGROUPS/MyResourceGroup/PROVIDERS/MICROSOFT.REDHATOPENSHIFT/HCPOPENSHIFTCLUSTERS/3a725v234c0Qd5bPfSYgk5okd2ps7UApyv8wtv810Y02ZvfAse0pgZemQ6dqE791QVKq6n6DAzU8bQTUOVCHwUOeq9fx92dpFebTgKEsx1Xl8Xrvs8NLehe3bj3h813B3j",
			expectedStatusCode: http.StatusBadRequest,
			expectedBody:       "The Resource 'MICROSOFT.REDHATOPENSHIFT/HCPOPENSHIFTCLUSTERS/3a725v234c0Qd5bPfSYgk5okd2ps7UApyv8wtv810Y02ZvfAse0pgZemQ6dqE791QVKq6n6DAzU8bQTUOVCHwUOeq9fx92dpFebTgKEsx1Xl8Xrvs8NLehe3bj3h813B3j' under resource group 'MyResourceGroup' does not conform to the naming restriction.",
		},
		{
			name:               "Invalid hcpopenshiftcluster resource name, too short",
			path:               "/SUBSCRIPTIONS/00000000-0000-0000-0000-000000000000/RESOURCEGROUPS/MyResourceGroup/PROVIDERS/MICROSOFT.REDHATOPENSHIFT/HCPOPENSHIFTCLUSTERS/a",
			expectedStatusCode: http.StatusBadRequest,
			expectedBody:       "The Resource 'MICROSOFT.REDHATOPENSHIFT/HCPOPENSHIFTCLUSTERS/a' under resource group 'MyResourceGroup' does not conform to the naming restriction.",
		},
		{
			name:               "Invalid node pool resource name",
			path:               "/SUBSCRIPTIONS/00000000-0000-0000-0000-000000000000/RESOURCEGROUPS/MyResourceGroup/PROVIDERS/MICROSOFT.REDHATOPENSHIFT/HCPOPENSHIFTCLUSTERS/myCluster/NODEPOOLS/$",
			expectedStatusCode: http.StatusBadRequest,
			expectedBody:       "The Resource 'MICROSOFT.REDHATOPENSHIFT/HCPOPENSHIFTCLUSTERS/NODEPOOLS/$' under resource group 'MyResourceGroup' does not conform to the naming restriction.",
		},
		{
			name:               "Invalid node pool resource name, starts with a '-'",
			path:               "/SUBSCRIPTIONS/00000000-0000-0000-0000-000000000000/RESOURCEGROUPS/MyResourceGroup/PROVIDERS/MICROSOFT.REDHATOPENSHIFT/HCPOPENSHIFTCLUSTERS/myCluster/NODEPOOLS/-abcde",
			expectedStatusCode: http.StatusBadRequest,
			expectedBody:       "The Resource 'MICROSOFT.REDHATOPENSHIFT/HCPOPENSHIFTCLUSTERS/NODEPOOLS/-abcde' under resource group 'MyResourceGroup' does not conform to the naming restriction.",
		},
		{
			name:               "Invalid node pool resource name, too long",
			path:               "/SUBSCRIPTIONS/00000000-0000-0000-0000-000000000000/RESOURCEGROUPS/MyResourceGroup/PROVIDERS/MICROSOFT.REDHATOPENSHIFT/HCPOPENSHIFTCLUSTERS/myCluster/NODEPOOLS/07B4gc00vjA2C8KL3Ns4No9fi",
			expectedStatusCode: http.StatusBadRequest,
			expectedBody:       "The Resource 'MICROSOFT.REDHATOPENSHIFT/HCPOPENSHIFTCLUSTERS/NODEPOOLS/07B4gc00vjA2C8KL3Ns4No9fi' under resource group 'MyResourceGroup' does not conform to the naming restriction.",
		},
		{
			name:               "Invalid node pool resource name, too short",
			path:               "/SUBSCRIPTIONS/00000000-0000-0000-0000-000000000000/RESOURCEGROUPS/MyResourceGroup/PROVIDERS/MICROSOFT.REDHATOPENSHIFT/HCPOPENSHIFTCLUSTERS/myCluster/NODEPOOLS/a",
			expectedStatusCode: http.StatusBadRequest,
			expectedBody:       "The Resource 'MICROSOFT.REDHATOPENSHIFT/HCPOPENSHIFTCLUSTERS/NODEPOOLS/a' under resource group 'MyResourceGroup' does not conform to the naming restriction.",
		},
		{
			name:               "Resource name is a valid subscription ID",
			path:               "/SUBSCRIPTIONS/00000000-0000-0000-0000-000000000000",
			expectedStatusCode: http.StatusOK,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "http://example.com"+tc.path, nil)
			req = req.WithContext(ContextWithOriginalPath(req.Context(), tc.path))

			// Use httptest.ResponseRecorder to record the response
			w := httptest.NewRecorder()

			// Execute the middleware
			MiddlewareValidateStatic(w, req, nextHandler)

			res := w.Result()

			// Check the response status code
			assert.Equal(t, tc.expectedStatusCode, res.StatusCode)

			if tc.expectedStatusCode != http.StatusOK {
				var resp CloudErrorContainer
				err := json.NewDecoder(res.Body).Decode(&resp)
				assert.NoError(t, err)

				// Check if the error message contains the expected text
				assert.Contains(t, tc.expectedBody, resp.Error.Message)
			}
		})
	}
}
