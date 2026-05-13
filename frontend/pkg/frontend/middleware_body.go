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
	"io"
	"net/http"
	"strings"

	armresourcesapi "github.com/Azure/ARO-HCP/internal/apis/resources/arm"
)

const megabyte int64 = (1 << 20)

// MiddlewareBody ensures that the request's body doesn't exceed the maximum size of 4MB.
func MiddlewareBody(w http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
	switch r.Method {
	case http.MethodPatch, http.MethodPost, http.MethodPut:
		// Max request body size accepted by ARM is 4 MB (assuming units in powers of 2).
		// See https://github.com/Azure/azure-resource-manager-rpc/blob/master/v1.0/common-api-details.md#max-request-body-size
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4*megabyte))
		if err != nil {
			armresourcesapi.WriteError(
				w, http.StatusBadRequest,
				armresourcesapi.CloudErrorCodeInvalidResource, "",
				"The resource definition is invalid.")
			return
		}

		contentType := strings.SplitN(r.Header.Get("Content-Type"), ";", 2)[0]

		if !strings.EqualFold(contentType, "application/json") && (len(body) > 0 || contentType != "") {
			armresourcesapi.WriteError(
				w, http.StatusUnsupportedMediaType,
				armresourcesapi.CloudErrorCodeUnsupportedMediaType, "",
				"The content media type '%s' is not supported. Only 'application/json' is supported.",
				r.Header.Get("Content-Type"))
			return
		}

		ctx := ContextWithBody(r.Context(), body)
		r = r.WithContext(ctx)
	}

	next(w, r)
}
