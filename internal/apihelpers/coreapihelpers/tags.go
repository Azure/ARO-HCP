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
	"encoding/json"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
)

// TagsFromBody returns the tags specified by a JSON body.
// It exists because the ARM contract requires tags to replace rather than merge with
// existing tags when the request method is PATCH.
//
// A nil return means the body does not specify a set of tags, either because "tags"
// was omitted or because it was explicitly null. A non-nil return, including an empty
// map for "tags": {}, is the complete set of tags the body asks for.
//
// Tag values are read as pointers and null values are dropped, matching the behavior of
// the versioned models, which type tags as map[string]*string and drop nil values when
// converting to the internal type.
func TagsFromBody(body []byte) (map[string]string, error) {
	var patch map[string]json.RawMessage
	if err := json.Unmarshal(body, &patch); err != nil {
		return nil, coreapi.NewInvalidRequestContentError(err)
	}
	rawTags, ok := patch["tags"]
	if !ok {
		return nil, nil
	}
	var tagPtrs map[string]*string
	if err := json.Unmarshal(rawTags, &tagPtrs); err != nil {
		return nil, coreapi.NewInvalidRequestContentError(err)
	}
	if tagPtrs == nil {
		return nil, nil
	}
	tags := make(map[string]string, len(tagPtrs))
	for key, val := range tagPtrs {
		if val != nil {
			tags[key] = *val
		}
	}
	return tags, nil
}
