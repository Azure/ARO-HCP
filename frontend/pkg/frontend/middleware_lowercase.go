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
	"net/http"
	"strings"
)

// This middleware helps comply with Azure OpenAPI Specifications Guidelines
// around case sensitivity for resource IDs.  Specifically:
//
// OAPI012: Resource IDs must not be case sensitive
//
//	Any entity name in the URL or resource ID (resource group names, resource
//	names, resource provider names) must be treated case insensitively. RPs
//	should also persist the casing provided by the user for tags, resource
//	names, etc... and use that same casing in responses.
//
//	Example:
//
//	The following two resource IDs are both valid and point to the same
//	resource. Casing must be ignored.
//
//	/subscriptions/45cefb9a-1824-4c35-ab4b-05c78763c03e/resourceGroups/myResourceGroup/
//	providers/Microsoft.KeyVault/vaults/sample-vault?api-version=2019-09-01
//
//	/SUBSCRIPTIONS/45CEFB9A-1824-4C35-AB4B-05C78763C03E/RESOURCEGROUPS/myresourceGROUP/
//	PROVIDERS/MICROSOFT.keyvault/VAULTS/SAMPLE-VAULT?API-VERSION=2019-09-01
//
// The frontend uses ServeMux from Go's standard library (net/http), which
// matches literal (that is, non-wildcarded) path segments case-sensitively.
// For instance, in a resource ID, "subscriptions" and "resourcegroups" are
// literal path segments and therefore are matched case-sensitvely.
//
// So this middleware saves the original path casing in the request context,
// and then normalizes the casing for ServeMux by lowercasing it. At the same
// time, when registering URL patterns with ServeMux in routes.go, we use the
// helper function MuxPattern which also lowercases the path segments passed
// to it to ensure their normalized casing agrees with this middleware.
//
// When the resource ID, or parts of it, needs to be used in an HTTP response
// (such as an error message), be sure to retrieve the original path from the
// request context. Do not use the normalized request path nor path values in
// the request (via PathValue). If necessary, you can parse the original path
// into a resource ID using ParseResourceID from the azcore/arm package.
func MiddlewareLowercase(w http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
	ctx := ContextWithOriginalPath(r.Context(), r.URL.Path)
	r = r.WithContext(ctx)
	r.URL.Path = strings.ToLower(r.URL.Path)

	next(w, r)
}
