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
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// ApplyOverrides applies manifest overrides to a set of Kubernetes objects
func ApplyOverrides(objects []unstructured.Unstructured, overrides []ManifestOverride) ([]unstructured.Unstructured, error) {
	if len(overrides) == 0 {
		return objects, nil
	}

	result := make([]unstructured.Unstructured, len(objects))
	copy(result, objects)

	for overrideIdx, override := range overrides {
		matchingIndices := selectObjects(result, override.Selector)
		if len(matchingIndices) == 0 {
			continue
		}

		for _, objIdx := range matchingIndices {
			for opIdx, op := range override.Operations {
				if err := applyOperation(&result[objIdx], op); err != nil {
					return nil, fmt.Errorf("failed to apply operation[%d] from override[%d] to %s/%s: %w",
						opIdx, overrideIdx, result[objIdx].GetKind(), result[objIdx].GetName(), err)
				}
			}
		}
	}

	return result, nil
}

// selectObjects returns indices of objects matching the selector
func selectObjects(objects []unstructured.Unstructured, selector Selector) []int {
	var indices []int

	for i, obj := range objects {
		if obj.GetKind() != selector.Kind {
			continue
		}

		if selector.Name != "" && obj.GetName() != selector.Name {
			continue
		}

		if selector.APIVersion != "" && obj.GetAPIVersion() != selector.APIVersion {
			continue
		}

		indices = append(indices, i)
	}

	return indices
}

// applyOperation applies a single operation to an object
func applyOperation(obj *unstructured.Unstructured, op Operation) error {
	switch op.Op {
	case "add":
		return applyAddOperation(obj, op)
	case "replace":
		return applyReplaceOperation(obj, op)
	case "remove":
		return applyRemoveOperation(obj, op)
	default:
		return fmt.Errorf("unknown operation type: %s", op.Op)
	}
}

// applyAddOperation adds a value at the specified path
// If merge is true and the target is a map, it merges with existing value
func applyAddOperation(obj *unstructured.Unstructured, op Operation) error {
	if op.Merge {
		existingValue, err := GetNestedField(*obj, op.Path)
		if err != nil {
			return SetNestedField(obj, op.Path, op.Value)
		}

		existingMap, existingIsMap := existingValue.(map[string]interface{})
		newMap, newIsMap := op.Value.(map[string]interface{})

		if existingIsMap && newIsMap {
			mergedMap := MergeMaps(existingMap, newMap)
			return SetNestedField(obj, op.Path, mergedMap)
		}

		return SetNestedField(obj, op.Path, op.Value)
	}

	return SetNestedField(obj, op.Path, op.Value)
}

// applyReplaceOperation replaces a value at the specified path
func applyReplaceOperation(obj *unstructured.Unstructured, op Operation) error {
	return SetNestedField(obj, op.Path, op.Value)
}

// applyRemoveOperation removes a field at the specified path
func applyRemoveOperation(obj *unstructured.Unstructured, op Operation) error {
	segments, err := parsePath(op.Path)
	if err != nil {
		return err
	}

	if len(segments) == 0 {
		return fmt.Errorf("cannot remove root object")
	}

	current := obj.Object
	for i := 0; i < len(segments)-1; i++ {
		segment := segments[i]

		value, found := current[segment.field]
		if !found {
			return nil
		}

		if segment.isArrayIndex || segment.isFilter {
			arr, ok := value.([]interface{})
			if !ok {
				return fmt.Errorf("expected array at '%s'", segment.field)
			}

			if segment.isArrayIndex {
				if segment.arrayIndex < 0 || segment.arrayIndex >= len(arr) {
					return nil
				}
				value = arr[segment.arrayIndex]
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
					return nil
				}
				value = foundItem
			}
		}

		nextMap, ok := value.(map[string]interface{})
		if !ok {
			return fmt.Errorf("expected map at path segment '%s'", segment.field)
		}
		current = nextMap
	}

	lastSegment := segments[len(segments)-1]

	if lastSegment.isArrayIndex || lastSegment.isFilter {
		fieldValue, found := current[lastSegment.field]
		if !found {
			return nil
		}

		arr, ok := fieldValue.([]interface{})
		if !ok {
			return fmt.Errorf("expected array at '%s'", lastSegment.field)
		}

		if lastSegment.isArrayIndex {
			if lastSegment.arrayIndex >= 0 && lastSegment.arrayIndex < len(arr) {
				arr = append(arr[:lastSegment.arrayIndex], arr[lastSegment.arrayIndex+1:]...)
				current[lastSegment.field] = arr
			}
		} else {
			for i, item := range arr {
				itemMap, ok := item.(map[string]interface{})
				if !ok {
					continue
				}
				if itemMap[lastSegment.filterField] == lastSegment.filterValue {
					arr = append(arr[:i], arr[i+1:]...)
					current[lastSegment.field] = arr
					break
				}
			}
		}
	} else {
		pathParts := strings.Split(op.Path, ".")
		if len(pathParts) > 1 {
			delete(current, lastSegment.field)
		} else {
			delete(current, lastSegment.field)
		}
	}

	return nil
}
