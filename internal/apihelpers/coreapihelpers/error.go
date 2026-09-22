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
	"net/http"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
)

// WriteError constructs and writes a CloudError to the given ResponseWriter
func WriteError(w http.ResponseWriter, statusCode int, code, target, format string, a ...interface{}) {
	WriteCloudError(w, coreapi.NewCloudError(statusCode, code, target, format, a...))
}

// WriteCloudError writes a CloudError to the given ResponseWriter
func WriteCloudError(w http.ResponseWriter, err *coreapi.CloudError) {
	if err.Code == coreapi.CloudErrorCodeServiceUnavailable {
		w.Header().Set("Retry-After", "59") // never choose a round number
	}
	w.Header()[coreapi.HeaderNameErrorCode] = []string{err.Code}
	_, _ = WriteJSONResponse(w, err.StatusCode, err)
}

// WriteInternalServerError writes an internal server error to the given ResponseWriter
func WriteInternalServerError(w http.ResponseWriter) {
	WriteCloudError(w, coreapi.NewInternalServerError())
}

// WriteConflictError writes a conflict error to the given ResponseWriter
func WriteConflictError(w http.ResponseWriter, resourceID *azcorearm.ResourceID, format string, a ...interface{}) {
	WriteCloudError(w, coreapi.NewConflictError(resourceID, format, a...))
}

// WriteResourceNotFoundError writes a nonexistent resource error to the given ResponseWriter
func WriteResourceNotFoundError(w http.ResponseWriter, resourceID *azcorearm.ResourceID) {
	WriteCloudError(w, coreapi.NewResourceNotFoundError(resourceID))
}

// WriteInvalidRequestContentError writes an invalid request content error to the given ResponseWriter
func WriteInvalidRequestContentError(w http.ResponseWriter, err error) {
	WriteCloudError(w, coreapi.NewInvalidRequestContentError(err))
}
