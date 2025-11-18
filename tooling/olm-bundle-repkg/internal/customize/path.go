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

package customize

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// pathSegment represents a single segment in a path
type pathSegment struct {
	field        string // field name
	arrayIndex   int    // -1 if not array access
	filterField  string // empty if not filtering
	filterValue  string // empty if not filtering
	isArrayIndex bool   // true if this is array indexing
	isFilter     bool   // true if this is array filtering
}

// parsePath parses a JSONPath-like path into segments
// Supports:
// - Simple nested: metadata.name
// - Array indexing: containers[0]
// - Array filtering: containers[name=manager], env[name=WATCH_NAMESPACE]
func parsePath(path string) ([]pathSegment, error) {
	if path == "" {
		return nil, fmt.Errorf("path cannot be empty")
	}

	parts := strings.Split(path, ".")
	segments := make([]pathSegment, 0, len(parts))

	arrayIndexRegex := regexp.MustCompile(`^(\w+)\[(\d+)\]$`)
	arrayFilterRegex := regexp.MustCompile(`^(\w+)\[(\w+)=([^\]]+)\]$`)

	for _, part := range parts {
		segment := pathSegment{
			arrayIndex: -1,
		}

		if matches := arrayFilterRegex.FindStringSubmatch(part); matches != nil {
			segment.field = matches[1]
			segment.filterField = matches[2]
			segment.filterValue = matches[3]
			segment.isFilter = true
		} else if matches := arrayIndexRegex.FindStringSubmatch(part); matches != nil {
			segment.field = matches[1]
			index, err := strconv.Atoi(matches[2])
			if err != nil {
				return nil, fmt.Errorf("invalid array index: %s", matches[2])
			}
			segment.arrayIndex = index
			segment.isArrayIndex = true
		} else {
			segment.field = part
		}

		segments = append(segments, segment)
	}

	return segments, nil
}

// GetNestedField retrieves a value from an unstructured object using a path
func GetNestedField(obj unstructured.Unstructured, path string) (interface{}, error) {
	segments, err := parsePath(path)
	if err != nil {
		return nil, err
	}

	current := obj.Object
	for i, segment := range segments {
		value, found := current[segment.field]
		if !found {
			return nil, fmt.Errorf("field not found at path segment '%s'", segment.field)
		}

		if segment.isArrayIndex || segment.isFilter {
			arr, ok := value.([]interface{})
			if !ok {
				return nil, fmt.Errorf("expected array at '%s', got %T", segment.field, value)
			}

			if segment.isArrayIndex {
				if segment.arrayIndex < 0 || segment.arrayIndex >= len(arr) {
					return nil, fmt.Errorf("array index out of bounds: %d (length: %d)", segment.arrayIndex, len(arr))
				}
				value = arr[segment.arrayIndex]
			} else {
				var found bool
				for _, item := range arr {
					itemMap, ok := item.(map[string]interface{})
					if !ok {
						continue
					}
					if itemMap[segment.filterField] == segment.filterValue {
						value = item
						found = true
						break
					}
				}
				if !found {
					return nil, fmt.Errorf("no array element found matching %s=%s", segment.filterField, segment.filterValue)
				}
			}
		}

		if i == len(segments)-1 {
			return value, nil
		}

		nextMap, ok := value.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("expected map at path segment '%s', got %T", segment.field, value)
		}
		current = nextMap
	}

	return nil, fmt.Errorf("path exhausted without finding value")
}

// SetNestedField sets a value in an unstructured object using a path
// Creates intermediate maps if they don't exist
func SetNestedField(obj *unstructured.Unstructured, path string, value interface{}) error {
	segments, err := parsePath(path)
	if err != nil {
		return err
	}

	if len(segments) == 0 {
		return fmt.Errorf("path must have at least one segment")
	}

	current := obj.Object
	for i := 0; i < len(segments)-1; i++ {
		segment := segments[i]

		fieldValue, found := current[segment.field]
		if !found {
			newMap := make(map[string]interface{})
			current[segment.field] = newMap
			fieldValue = newMap
		}

		if segment.isArrayIndex || segment.isFilter {
			arr, ok := fieldValue.([]interface{})
			if !ok {
				return fmt.Errorf("expected array at '%s', got %T", segment.field, fieldValue)
			}

			if segment.isArrayIndex {
				if segment.arrayIndex < 0 || segment.arrayIndex >= len(arr) {
					return fmt.Errorf("array index out of bounds: %d", segment.arrayIndex)
				}
				fieldValue = arr[segment.arrayIndex]
			} else {
				var foundItem interface{}
				for _, item := range arr {
					itemMap, ok := item.(map[string]interface{})
					if !ok {
						continue
					}
					if itemMap[segment.filterField] == segment.filterValue {
						foundItem = item
						break
					}
				}
				if foundItem == nil {
					return fmt.Errorf("no array element found matching %s=%s", segment.filterField, segment.filterValue)
				}
				fieldValue = foundItem
			}
		}

		nextMap, ok := fieldValue.(map[string]interface{})
		if !ok {
			return fmt.Errorf("expected map at path segment '%s', got %T", segment.field, fieldValue)
		}
		current = nextMap
	}

	lastSegment := segments[len(segments)-1]

	if lastSegment.isArrayIndex || lastSegment.isFilter {
		fieldValue, found := current[lastSegment.field]
		if !found {
			current[lastSegment.field] = []interface{}{}
			fieldValue = current[lastSegment.field]
		}

		arr, ok := fieldValue.([]interface{})
		if !ok {
			return fmt.Errorf("expected array at '%s', got %T", lastSegment.field, fieldValue)
		}

		if lastSegment.isArrayIndex {
			if lastSegment.arrayIndex < 0 || lastSegment.arrayIndex >= len(arr) {
				return fmt.Errorf("array index out of bounds: %d", lastSegment.arrayIndex)
			}
			arr[lastSegment.arrayIndex] = value
		} else {
			found := false
			for i, item := range arr {
				itemMap, ok := item.(map[string]interface{})
				if !ok {
					continue
				}
				if itemMap[lastSegment.filterField] == lastSegment.filterValue {
					arr[i] = value
					found = true
					break
				}
			}
			if !found {
				arr = append(arr, value)
				current[lastSegment.field] = arr
			}
		}
	} else {
		current[lastSegment.field] = value
	}

	return nil
}

// MergeMaps deeply merges two maps, with values from 'new' taking precedence
func MergeMaps(existing, new map[string]interface{}) map[string]interface{} {
	result := make(map[string]interface{})

	for k, v := range existing {
		result[k] = v
	}

	for k, newValue := range new {
		existingValue, exists := result[k]
		if !exists {
			result[k] = newValue
			continue
		}

		existingMap, existingIsMap := existingValue.(map[string]interface{})
		newMap, newIsMap := newValue.(map[string]interface{})

		if existingIsMap && newIsMap {
			result[k] = MergeMaps(existingMap, newMap)
		} else {
			result[k] = newValue
		}
	}

	return result
}
