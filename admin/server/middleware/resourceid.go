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

package middleware

import (
	"fmt"
	"net/http"
	"path"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
)

func WithHCPResourceID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		subscriptionID := r.PathValue(PathSegmentSubscriptionID)
		resourceGroupName := r.PathValue(PathSegmentResourceGroupName)
		resourceName := r.PathValue(PathSegmentResourceName)

		// Validate that path values were extracted correctly
		if subscriptionID == "" || resourceGroupName == "" || resourceName == "" {
			http.Error(w, fmt.Sprintf("failed to extract resource ID from path: subscriptionID=%q, resourceGroupName=%q, resourceName=%q, path=%q", subscriptionID, resourceGroupName, resourceName, r.URL.Path), http.StatusInternalServerError)
			return
		}

		// Construct the full resource ID path and parse it to ensure String() method works correctly
		// ResourceID.String() only works when the ResourceID is created via ParseResourceID()
		resourceIDPath := path.Join(
			"/subscriptions",
			subscriptionID,
			"resourceGroups",
			resourceGroupName,
			"providers",
			api.ClusterResourceType.Namespace,
			api.ClusterResourceType.Types[0],
			resourceName,
		)

		resourceID, err := azcorearm.ParseResourceID(resourceIDPath)
		if err != nil {
			http.Error(w, fmt.Sprintf("failed to parse resource ID: %v", err), http.StatusInternalServerError)
			return
		}

		ctx := ContextWithResourceID(r.Context(), resourceID)

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
