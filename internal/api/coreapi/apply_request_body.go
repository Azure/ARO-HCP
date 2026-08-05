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

package coreapi

import (
	"encoding/json"
	"net/http"
	"reflect"

	"dario.cat/mergo"
	jsonpatch "github.com/evanphx/json-patch"

	"github.com/Azure/ARO-HCP/internal/utils"
)

// ApplyRequestBody applies a JSON request body to the value pointed to by v.
// If the request method is PATCH, the request body is applied to v using JSON
// Merge Patch (RFC 7396) semantics. Otherwise the request body is unmarshalled
// directly to v.
func ApplyRequestBody(requestMethod string, body []byte, v any) error {
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return NewInvalidRequestContentError(&json.InvalidUnmarshalError{Type: rv.Type()})
	}

	switch requestMethod {
	case http.MethodPatch:
		originalData, err := json.Marshal(v)
		if err != nil {
			return utils.TrackError(err)
		}

		modifiedData, err := jsonpatch.MergePatch(originalData, body)
		if err != nil {
			return utils.TrackError(err)
		}

		// Reset *v to its zero value.
		rv.Elem().SetZero()

		err = json.Unmarshal(modifiedData, v)
		if err != nil {
			return NewInvalidRequestContentError(err)
		}

	default:
		// We need to unmarshal in two phases because Unmarshal in
		// encoding/json (v1) replaces Go maps instead of merging JSON
		// keys into them. This is critical for UserAssignedIdentities.
		//
		// First we unmarshal the request body into a newly-allocated
		// struct of v's type, then merge the allocated struct into v.
		//
		// FIXME encoding/json/v2 claims to handle this better but is
		//       currently experimental. Its "Unmarshal" docs state:
		//
		//      "Maps are not cleared. If the Go map is nil, then a
		//       new map is allocated to decode into. If the decoded
		//       key matches an existing Go map entry, the entry
		//       value is reused by decoding the JSON object value
		//       into it."

		src := reflect.New(rv.Elem().Type()).Interface()

		err := json.Unmarshal(body, src)
		if err != nil {
			return NewInvalidRequestContentError(err)
		}

		err = mergo.Merge(v, src, mergo.WithOverride)
		if err != nil {
			return utils.TrackError(err)
		}
	}

	return nil
}
