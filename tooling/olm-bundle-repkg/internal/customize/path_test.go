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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestGetNestedField(t *testing.T) {
	tests := []struct {
		name            string
		obj             unstructured.Unstructured
		path            string
		wantErr         bool
		wantErrContains string
		validate        func(t *testing.T, value interface{})
	}{
		{
			name: "SimpleNested",
			obj: unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{
						"name": "test-name",
					},
				},
			},
			path: "metadata.name",
			validate: func(t *testing.T, value interface{}) {
				assert.Equal(t, "test-name", value)
			},
		},
		{
			name: "DeepNested",
			obj: unstructured.Unstructured{
				Object: map[string]interface{}{
					"spec": map[string]interface{}{
						"template": map[string]interface{}{
							"metadata": map[string]interface{}{
								"labels": map[string]interface{}{
									"app": "test",
								},
							},
						},
					},
				},
			},
			path: "spec.template.metadata.labels",
			validate: func(t *testing.T, value interface{}) {
				assert.Equal(t, map[string]interface{}{"app": "test"}, value)
			},
		},
		{
			name: "ArrayIndexing_0",
			obj: unstructured.Unstructured{
				Object: map[string]interface{}{
					"spec": map[string]interface{}{
						"containers": []interface{}{
							map[string]interface{}{
								"name":  "container-0",
								"image": "image-0",
							},
							map[string]interface{}{
								"name":  "container-1",
								"image": "image-1",
							},
						},
					},
				},
			},
			path: "spec.containers[0].image",
			validate: func(t *testing.T, value interface{}) {
				assert.Equal(t, "image-0", value)
			},
		},
		{
			name: "ArrayIndexing_1",
			obj: unstructured.Unstructured{
				Object: map[string]interface{}{
					"spec": map[string]interface{}{
						"containers": []interface{}{
							map[string]interface{}{
								"name":  "container-0",
								"image": "image-0",
							},
							map[string]interface{}{
								"name":  "container-1",
								"image": "image-1",
							},
						},
					},
				},
			},
			path: "spec.containers[1].name",
			validate: func(t *testing.T, value interface{}) {
				assert.Equal(t, "container-1", value)
			},
		},
		{
			name: "ArrayFiltering",
			obj: unstructured.Unstructured{
				Object: map[string]interface{}{
					"spec": map[string]interface{}{
						"containers": []interface{}{
							map[string]interface{}{
								"name":  "manager",
								"image": "manager-image",
							},
							map[string]interface{}{
								"name":  "sidecar",
								"image": "sidecar-image",
							},
						},
					},
				},
			},
			path: "spec.containers[name=manager].image",
			validate: func(t *testing.T, value interface{}) {
				assert.Equal(t, "manager-image", value)
			},
		},
		{
			name: "ArrayFilteringEnv",
			obj: unstructured.Unstructured{
				Object: map[string]interface{}{
					"spec": map[string]interface{}{
						"containers": []interface{}{
							map[string]interface{}{
								"name": "manager",
								"env": []interface{}{
									map[string]interface{}{
										"name":  "WATCH_NAMESPACE",
										"value": "default",
									},
									map[string]interface{}{
										"name":  "OTHER_VAR",
										"value": "other",
									},
								},
							},
						},
					},
				},
			},
			path: "spec.containers[name=manager].env[name=WATCH_NAMESPACE]",
			validate: func(t *testing.T, value interface{}) {
				envVar, ok := value.(map[string]interface{})
				require.True(t, ok)
				assert.Equal(t, "WATCH_NAMESPACE", envVar["name"])
				assert.Equal(t, "default", envVar["value"])
			},
		},
		{
			name: "ArrayFilteringNoMatch",
			obj: unstructured.Unstructured{
				Object: map[string]interface{}{
					"spec": map[string]interface{}{
						"containers": []interface{}{
							map[string]interface{}{
								"name":  "manager",
								"image": "manager-image",
							},
						},
					},
				},
			},
			path:            "spec.containers[name=nonexistent].image",
			wantErr:         true,
			wantErrContains: "no array element found matching",
		},
		{
			name: "NonExistentPath",
			obj: unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{
						"name": "test-name",
					},
				},
			},
			path:            "spec.nonexistent",
			wantErr:         true,
			wantErrContains: "field not found",
		},
		{
			name: "FilterValueWithHyphen",
			obj: unstructured.Unstructured{
				Object: map[string]interface{}{
					"spec": map[string]interface{}{
						"containers": []interface{}{
							map[string]interface{}{
								"name":  "manager-v1",
								"image": "manager-image",
							},
						},
					},
				},
			},
			path: "spec.containers[name=manager-v1].image",
			validate: func(t *testing.T, value interface{}) {
				assert.Equal(t, "manager-image", value)
			},
		},
		{
			name: "FilterValueWithDotFails",
			obj: unstructured.Unstructured{
				Object: map[string]interface{}{
					"spec": map[string]interface{}{
						"containers": []interface{}{
							map[string]interface{}{
								"name":  "manager.v1",
								"image": "manager-image",
							},
						},
					},
				},
			},
			path: "spec.containers[name=manager.v1].image",
			// This fails because dots are path separators, so the parser sees:
			// "spec", "containers[name=manager", "v1]", "image"
			wantErr:         true,
			wantErrContains: "field not found",
		},
		{
			name: "FilterValueWithBracketShouldFail",
			obj: unstructured.Unstructured{
				Object: map[string]interface{}{
					"spec": map[string]interface{}{
						"containers": []interface{}{
							map[string]interface{}{
								"name":  "manager[0]",
								"image": "manager-image",
							},
						},
					},
				},
			},
			path:            "spec.containers[name=manager[0]].image",
			wantErr:         true,
			wantErrContains: "field not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			value, err := GetNestedField(tt.obj, tt.path)

			if tt.wantErr {
				assert.Error(t, err)
				if tt.wantErrContains != "" {
					assert.Contains(t, err.Error(), tt.wantErrContains)
				}
				return
			}

			require.NoError(t, err)
			if tt.validate != nil {
				tt.validate(t, value)
			}
		})
	}
}

func TestSetNestedField(t *testing.T) {
	tests := []struct {
		name         string
		initialObj   unstructured.Unstructured
		path         string
		setValue     interface{}
		expectedGet  interface{}
		validatePath string // Optional, defaults to path if empty
	}{
		{
			name: "SimpleNested",
			initialObj: unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{},
				},
			},
			path:        "metadata.name",
			setValue:    "new-name",
			expectedGet: "new-name",
		},
		{
			name: "CreatePath",
			initialObj: unstructured.Unstructured{
				Object: map[string]interface{}{},
			},
			path:        "spec.template.metadata.labels",
			setValue:    map[string]interface{}{"app": "test"},
			expectedGet: map[string]interface{}{"app": "test"},
		},
		{
			name: "UpdateExisting",
			initialObj: unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{
						"name": "old-name",
					},
				},
			},
			path:        "metadata.name",
			setValue:    "new-name",
			expectedGet: "new-name",
		},
		{
			name: "ArrayIndexing",
			initialObj: unstructured.Unstructured{
				Object: map[string]interface{}{
					"spec": map[string]interface{}{
						"containers": []interface{}{
							map[string]interface{}{
								"name":  "container-0",
								"image": "old-image",
							},
						},
					},
				},
			},
			path:        "spec.containers[0].image",
			setValue:    "new-image",
			expectedGet: "new-image",
		},
		{
			name: "ArrayFiltering",
			initialObj: unstructured.Unstructured{
				Object: map[string]interface{}{
					"spec": map[string]interface{}{
						"containers": []interface{}{
							map[string]interface{}{
								"name":  "manager",
								"image": "old-image",
							},
							map[string]interface{}{
								"name":  "sidecar",
								"image": "sidecar-image",
							},
						},
					},
				},
			},
			path: "spec.containers[name=manager].env[name=WATCH_NAMESPACE]",
			setValue: map[string]interface{}{
				"name":  "WATCH_NAMESPACE",
				"value": "{{ .Release.Namespace }}",
			},
			expectedGet: map[string]interface{}{
				"name":  "WATCH_NAMESPACE",
				"value": "{{ .Release.Namespace }}",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := SetNestedField(&tt.initialObj, tt.path, tt.setValue)
			require.NoError(t, err)

			validatePath := tt.path
			if tt.validatePath != "" {
				validatePath = tt.validatePath
			}

			value, err := GetNestedField(tt.initialObj, validatePath)
			require.NoError(t, err)
			assert.Equal(t, tt.expectedGet, value)
		})
	}
}

func TestMergeMaps(t *testing.T) {
	tests := []struct {
		name     string
		existing map[string]interface{}
		new      map[string]interface{}
		expected map[string]interface{}
	}{
		{
			name: "merge non-overlapping keys",
			existing: map[string]interface{}{
				"key1": "value1",
			},
			new: map[string]interface{}{
				"key2": "value2",
			},
			expected: map[string]interface{}{
				"key1": "value1",
				"key2": "value2",
			},
		},
		{
			name: "merge overlapping keys - new wins",
			existing: map[string]interface{}{
				"key1": "old-value",
			},
			new: map[string]interface{}{
				"key1": "new-value",
			},
			expected: map[string]interface{}{
				"key1": "new-value",
			},
		},
		{
			name: "deep merge nested maps",
			existing: map[string]interface{}{
				"nested": map[string]interface{}{
					"key1": "value1",
				},
			},
			new: map[string]interface{}{
				"nested": map[string]interface{}{
					"key2": "value2",
				},
			},
			expected: map[string]interface{}{
				"nested": map[string]interface{}{
					"key1": "value1",
					"key2": "value2",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := MergeMaps(tt.existing, tt.new)
			assert.Equal(t, tt.expected, result)
		})
	}
}
