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

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
)

// WriteJSONResponse writes a JSON response body to the http.ResponseWriter in
// the proper sequence: first setting Content-Type to "application/json", then
// setting the HTTP status code, and finally writing a JSON encoding of body.
//
// The function accepts anything for the body argument that can be marshalled
// to JSON. One special case, however, is a byte slice. A byte slice will be
// written verbatim with the expectation that it was produced by Marshal.
func WriteJSONResponse(writer http.ResponseWriter, statusCode int, body any) (int, error) {
	var data []byte

	switch v := body.(type) {
	case []byte:
		data = v // write a byte slice verbatim
	default:
		var err error
		data, err = coreapi.MarshalJSON(body)
		if err != nil {
			return 0, err
		}
	}

	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(statusCode)
	return writer.Write(data)
}
