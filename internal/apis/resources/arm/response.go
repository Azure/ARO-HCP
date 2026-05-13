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

package arm

import (
	"encoding/json"
	"net/http"
	"net/url"
)

const (
	prefix string = ""     // no prefix
	indent string = "    " // 4 spaces
)

// MarshalJSON returns the JSON encoding of v.
//
// Call this function instead of the marshal functions in "encoding/json" for
// HTTP responses to ensure the formatting is consistent.
//
// Note, there is nothing ARM-specific about this function other than all ARM
// response bodies are JSON-formatted. But the "arm" package is currently the
// lowest layer insofar as it has no dependencies on other ARO-HCP packages.
func MarshalJSON(v any) ([]byte, error) {
	return json.MarshalIndent(v, prefix, indent)
}

// WriteJSONResponse writes a JSON response body to the http.ResponseWriter in
// the proper sequence: first setting Content-Type to "application/json", then
// setting the HTTP status code, and finally writing a JSON encoding of body.
//
// The function accepts anything for the body argument that can be marshalled
// to JSON. One special case, however, is a byte slice. A byte slice will be
// written verbatim with the expectation that it was produced by Marshal.
//
// Note, there is nothing ARM-specific about this function other than all ARM
// response bodies are JSON-formatted. But the "arm" package is currently the
// lowest layer insofar as it has no dependencies on other ARO-HCP packages.
func WriteJSONResponse(writer http.ResponseWriter, statusCode int, body any) (int, error) {
	var data []byte

	switch v := body.(type) {
	case []byte:
		data = v // write a byte slice verbatim
	default:
		var err error
		data, err = MarshalJSON(body)
		if err != nil {
			return 0, err
		}
	}

	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(statusCode)
	return writer.Write(data)
}

// PagedResponse is the response format for resource collection requests.
type PagedResponse struct {
	Value    []json.RawMessage `json:"value"`
	NextLink string            `json:"nextLink,omitempty"`
}

// NewPagedResponse returns a new PagedResponse instance.
func NewPagedResponse() PagedResponse {
	return PagedResponse{Value: []json.RawMessage{}}
}

// AddValue adds a JSON encoded value to a PagedResponse.
func (r *PagedResponse) AddValue(value json.RawMessage) {
	r.Value = append(r.Value, value)
}

// SetNextLink sets NextLink to a URL with a $skipToken parameter.
// If skipToken is empty, the function does nothing and returns nil.
func (r *PagedResponse) SetNextLink(baseURL, skipToken string) error {
	if skipToken == "" {
		return nil
	}

	u, err := url.ParseRequestURI(baseURL)
	if err != nil {
		return err
	}

	values := u.Query()
	values.Set("$skipToken", skipToken)
	u.RawQuery = values.Encode()

	r.NextLink = u.String()
	return nil
}
