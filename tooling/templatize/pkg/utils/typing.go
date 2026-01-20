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

package utils

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// AnyToString maps some types to strings, as they are used in OS Env.
// For complex types (slices, maps, structs), it marshals them to minified JSON.
func AnyToString(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case int:
		return fmt.Sprintf("%d", v)
	case bool:
		return fmt.Sprintf("%t", v)
	default:
		// For complex types (maps, slices, structs), marshal to JSON
		// This provides better readability than Go's default %v format
		jsonBytes, err := json.Marshal(v)
		if err != nil {
			// Fallback to %v if JSON marshaling fails
			return fmt.Sprintf("%v", v)
		}
		// Compact the JSON to ensure no extra whitespace
		var compactBuf bytes.Buffer
		if err := json.Compact(&compactBuf, jsonBytes); err != nil {
			// If compacting fails, use the marshaled output as-is
			return string(jsonBytes)
		}
		return compactBuf.String()
	}
}
