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
	"strings"

	"github.com/go-logr/logr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
)

const (
	pathSegmentResourceGroupName = "resourcegroupname"
	pathSegmentResourceName      = "resourcename"
	pathSegmentSubscriptionID    = "subscriptionid"
)

var patternPrefix = strings.ToLower(
	fmt.Sprintf(
		"/subscriptions/{%s}/resourcegroups/{%s}/providers/%s/%s/{%s}",
		pathSegmentSubscriptionID,
		pathSegmentResourceGroupName,
		api.ProviderNamespace,
		api.ClusterResourceTypeName,
		pathSegmentResourceName,
	),
)

type HCPResourceServerMux struct {
	mux *http.ServeMux
}

func (m *HCPResourceServerMux) Handler() http.Handler {
	return m.mux
}

func (m *HCPResourceServerMux) Handle(method string, pattern string, handler http.Handler) {
	m.mux.Handle(fmt.Sprintf("%s %s%s", method, patternPrefix, pattern), withHCPResourceID(handler))
}

func NewHCPResourceServerMux() *HCPResourceServerMux {
	return &HCPResourceServerMux{
		mux: http.NewServeMux(),
	}
}

func withHCPResourceID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logger, err := logr.FromContext(r.Context())
		if err != nil {
			http.Error(w, fmt.Sprintf("failed to create logger: %v", err), http.StatusInternalServerError)
			return
		}
		logger.Info("withHCPResourceID", "path", r.URL.Path)

		subscriptionID := r.PathValue(pathSegmentSubscriptionID)
		resourceGroupName := r.PathValue(pathSegmentResourceGroupName)
		resourceName := r.PathValue(pathSegmentResourceName)

		// Validate that path values were extracted correctly
		if subscriptionID == "" || resourceGroupName == "" || resourceName == "" {
			http.Error(w, fmt.Sprintf("failed to extract resource ID from path: subscriptionID=%q, resourceGroupName=%q, resourceName=%q, path=%q", subscriptionID, resourceGroupName, resourceName, r.URL.Path), http.StatusInternalServerError)
			return
		}

		resourceIDPath := strings.ToLower(
			fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/%s/%s/%s",
				subscriptionID,
				resourceGroupName,
				api.ProviderNamespace,
				api.ClusterResourceTypeName,
				resourceName,
			),
		)

		resourceID, err := azcorearm.ParseResourceID(resourceIDPath)
		if err != nil {
			http.Error(w, fmt.Sprintf("failed to parse resource ID: %v", err), http.StatusInternalServerError)
			return
		}

		ctx := ContextWithResourceID(r.Context(), resourceID)

		// Strip the static prefix and the resource ID path from the URL path
		strippedRequest := r.Clone(ctx)
		strippedRequest.URL.Path = strings.TrimPrefix(strippedRequest.URL.Path, resourceIDPath)

		next.ServeHTTP(w, strippedRequest)
	})
}
