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

package common

import (
	"fmt"
)

// ParseStringField safely extracts a string field from a map[string]interface{}
func ParseStringField(data map[string]interface{}, field string) string {
	if value, ok := data[field].(string); ok {
		return value
	}
	return ""
}

// ParseTagsMap safely extracts tags from a Resource Graph result
func ParseTagsMap(data map[string]interface{}) map[string]string {
	tags := make(map[string]string)
	if tagsInterface, ok := data["tags"].(map[string]interface{}); ok {
		for key, value := range tagsInterface {
			if strValue, ok := value.(string); ok {
				tags[key] = strValue
			}
		}
	}
	return tags
}

// ParseResourceGraphResultData converts Resource Graph result data to a slice of maps
func ParseResourceGraphResultData(data interface{}) ([]map[string]interface{}, error) {
	if data == nil {
		return nil, nil
	}

	rows, ok := data.([]interface{})
	if !ok {
		return nil, fmt.Errorf("unexpected Resource Graph response format: expected []interface{}, got %T", data)
	}

	var results []map[string]interface{}
	for _, row := range rows {
		rowMap, ok := row.(map[string]interface{})
		if !ok {
			continue // Skip invalid rows
		}
		results = append(results, rowMap)
	}

	return results, nil
}

// ParsePropertiesField extracts a string field from the properties section of Azure Resource Graph results
func ParsePropertiesField(rowMap map[string]interface{}, field string) string {
	if properties, ok := rowMap["properties"].(map[string]interface{}); ok {
		return ParseStringField(properties, field)
	}
	return ""
}
