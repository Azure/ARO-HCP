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

func TestApplyOverrides(t *testing.T) {
	tests := []struct {
		name            string
		objects         []unstructured.Unstructured
		overrides       []ManifestOverride
		wantErr         bool
		wantErrContains string
		validate        func(t *testing.T, result []unstructured.Unstructured)
	}{
		{
			name: "AddOperation",
			objects: []unstructured.Unstructured{
				{
					Object: map[string]interface{}{
						"apiVersion": "v1",
						"kind":       "ServiceAccount",
						"metadata": map[string]interface{}{
							"name": "test-sa",
						},
					},
				},
			},
			overrides: []ManifestOverride{
				{
					Selector: Selector{
						Kind: "ServiceAccount",
						Name: "test-sa",
					},
					Operations: []Operation{
						{
							Op:   "add",
							Path: "metadata.annotations",
							Value: map[string]interface{}{
								"test-annotation": "test-value",
							},
						},
					},
				},
			},
			validate: func(t *testing.T, result []unstructured.Unstructured) {
				require.Len(t, result, 1)
				annotations, err := GetNestedField(result[0], "metadata.annotations")
				require.NoError(t, err)
				assert.Equal(t, map[string]interface{}{"test-annotation": "test-value"}, annotations)
			},
		},
		{
			name: "AddWithMerge",
			objects: []unstructured.Unstructured{
				{
					Object: map[string]interface{}{
						"apiVersion": "apps/v1",
						"kind":       "Deployment",
						"metadata": map[string]interface{}{
							"name": "test-deployment",
							"labels": map[string]interface{}{
								"existing-label": "existing-value",
							},
						},
					},
				},
			},
			overrides: []ManifestOverride{
				{
					Selector: Selector{
						Kind: "Deployment",
						Name: "test-deployment",
					},
					Operations: []Operation{
						{
							Op:    "add",
							Path:  "metadata.labels",
							Merge: true,
							Value: map[string]interface{}{
								"new-label": "new-value",
							},
						},
					},
				},
			},
			validate: func(t *testing.T, result []unstructured.Unstructured) {
				require.Len(t, result, 1)
				labels, err := GetNestedField(result[0], "metadata.labels")
				require.NoError(t, err)
				expectedLabels := map[string]interface{}{
					"existing-label": "existing-value",
					"new-label":      "new-value",
				}
				assert.Equal(t, expectedLabels, labels)
			},
		},
		{
			name: "ReplaceOperation",
			objects: []unstructured.Unstructured{
				{
					Object: map[string]interface{}{
						"apiVersion": "apps/v1",
						"kind":       "Deployment",
						"metadata": map[string]interface{}{
							"name": "test-deployment",
						},
						"spec": map[string]interface{}{
							"template": map[string]interface{}{
								"spec": map[string]interface{}{
									"containers": []interface{}{
										map[string]interface{}{
											"name": "manager",
											"env": []interface{}{
												map[string]interface{}{
													"name":  "WATCH_NAMESPACE",
													"value": "default",
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
			overrides: []ManifestOverride{
				{
					Selector: Selector{
						Kind: "Deployment",
						Name: "test-deployment",
					},
					Operations: []Operation{
						{
							Op:   "replace",
							Path: "spec.template.spec.containers[name=manager].env[name=WATCH_NAMESPACE]",
							Value: map[string]interface{}{
								"name":  "WATCH_NAMESPACE",
								"value": "{{ .Release.Namespace }}",
							},
						},
					},
				},
			},
			validate: func(t *testing.T, result []unstructured.Unstructured) {
				require.Len(t, result, 1)
				envVar, err := GetNestedField(result[0], "spec.template.spec.containers[name=manager].env[name=WATCH_NAMESPACE]")
				require.NoError(t, err)
				expected := map[string]interface{}{
					"name":  "WATCH_NAMESPACE",
					"value": "{{ .Release.Namespace }}",
				}
				assert.Equal(t, expected, envVar)
			},
		},
		{
			name: "RemoveOperation",
			objects: []unstructured.Unstructured{
				{
					Object: map[string]interface{}{
						"apiVersion": "v1",
						"kind":       "ServiceAccount",
						"metadata": map[string]interface{}{
							"name": "test-sa",
							"annotations": map[string]interface{}{
								"annotation-to-remove": "value",
								"annotation-to-keep":   "value",
							},
						},
					},
				},
			},
			overrides: []ManifestOverride{
				{
					Selector: Selector{
						Kind: "ServiceAccount",
						Name: "test-sa",
					},
					Operations: []Operation{
						{
							Op:   "remove",
							Path: "metadata.annotations.annotation-to-remove",
						},
					},
				},
			},
			validate: func(t *testing.T, result []unstructured.Unstructured) {
				require.Len(t, result, 1)
				annotations, err := GetNestedField(result[0], "metadata.annotations")
				require.NoError(t, err)
				expected := map[string]interface{}{
					"annotation-to-keep": "value",
				}
				assert.Equal(t, expected, annotations)
			},
		},
		{
			name: "MultipleOperations",
			objects: []unstructured.Unstructured{
				{
					Object: map[string]interface{}{
						"apiVersion": "apps/v1",
						"kind":       "Deployment",
						"metadata": map[string]interface{}{
							"name": "test-deployment",
						},
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
			},
			overrides: []ManifestOverride{
				{
					Selector: Selector{
						Kind: "Deployment",
						Name: "test-deployment",
					},
					Operations: []Operation{
						{
							Op:    "add",
							Path:  "spec.template.metadata.labels",
							Merge: true,
							Value: map[string]interface{}{
								"azure.workload.identity/use": "true",
							},
						},
						{
							Op:    "add",
							Path:  "metadata.annotations",
							Merge: false,
							Value: map[string]interface{}{
								"test-annotation": "test-value",
							},
						},
					},
				},
			},
			validate: func(t *testing.T, result []unstructured.Unstructured) {
				require.Len(t, result, 1)

				podLabels, err := GetNestedField(result[0], "spec.template.metadata.labels")
				require.NoError(t, err)
				expectedLabels := map[string]interface{}{
					"app":                         "test",
					"azure.workload.identity/use": "true",
				}
				assert.Equal(t, expectedLabels, podLabels)

				annotations, err := GetNestedField(result[0], "metadata.annotations")
				require.NoError(t, err)
				assert.Equal(t, map[string]interface{}{"test-annotation": "test-value"}, annotations)
			},
		},
		{
			name: "SelectorByKindOnly",
			objects: []unstructured.Unstructured{
				{
					Object: map[string]interface{}{
						"apiVersion": "v1",
						"kind":       "ServiceAccount",
						"metadata": map[string]interface{}{
							"name": "sa-1",
						},
					},
				},
				{
					Object: map[string]interface{}{
						"apiVersion": "v1",
						"kind":       "ServiceAccount",
						"metadata": map[string]interface{}{
							"name": "sa-2",
						},
					},
				},
			},
			overrides: []ManifestOverride{
				{
					Selector: Selector{
						Kind: "ServiceAccount",
						// No name - matches all ServiceAccounts
					},
					Operations: []Operation{
						{
							Op:   "add",
							Path: "metadata.annotations",
							Value: map[string]interface{}{
								"test": "value",
							},
						},
					},
				},
			},
			validate: func(t *testing.T, result []unstructured.Unstructured) {
				require.Len(t, result, 2)
				// Both ServiceAccounts should have the annotation
				for _, obj := range result {
					annotations, err := GetNestedField(obj, "metadata.annotations")
					require.NoError(t, err)
					assert.Equal(t, map[string]interface{}{"test": "value"}, annotations)
				}
			},
		},
		{
			name: "NoMatchingObjects",
			objects: []unstructured.Unstructured{
				{
					Object: map[string]interface{}{
						"apiVersion": "v1",
						"kind":       "ServiceAccount",
						"metadata": map[string]interface{}{
							"name": "test-sa",
						},
					},
				},
			},
			overrides: []ManifestOverride{
				{
					Selector: Selector{
						Kind: "Deployment", // No Deployment in objects
						Name: "test-deployment",
					},
					Operations: []Operation{
						{
							Op:   "add",
							Path: "metadata.annotations",
							Value: map[string]interface{}{
								"test": "value",
							},
						},
					},
				},
			},
			validate: func(t *testing.T, result []unstructured.Unstructured) {
				// Should return original objects unchanged
				require.Len(t, result, 1)
				assert.Equal(t, "ServiceAccount", result[0].GetKind())
				assert.Equal(t, "test-sa", result[0].GetName())
			},
		},
		{
			name: "InvalidPath",
			objects: []unstructured.Unstructured{
				{
					Object: map[string]interface{}{
						"apiVersion": "v1",
						"kind":       "ServiceAccount",
						"metadata": map[string]interface{}{
							"name": "test-sa", // name is a string, not a map
						},
					},
				},
			},
			overrides: []ManifestOverride{
				{
					Selector: Selector{
						Kind: "ServiceAccount",
						Name: "test-sa",
					},
					Operations: []Operation{
						{
							Op:    "add",
							Path:  "metadata.name.invalid", // trying to navigate into a string value
							Value: "value",
						},
					},
				},
			},
			wantErr:         true,
			wantErrContains: "failed to apply operation",
		},
		{
			name: "EmptyOverrides",
			objects: []unstructured.Unstructured{
				{
					Object: map[string]interface{}{
						"apiVersion": "v1",
						"kind":       "ServiceAccount",
						"metadata": map[string]interface{}{
							"name": "test-sa",
						},
					},
				},
			},
			overrides: []ManifestOverride{},
			validate: func(t *testing.T, result []unstructured.Unstructured) {
				require.Len(t, result, 1)
				assert.Equal(t, "ServiceAccount", result[0].GetKind())
				assert.Equal(t, "test-sa", result[0].GetName())
			},
		},
		{
			name: "NilOverrides",
			objects: []unstructured.Unstructured{
				{
					Object: map[string]interface{}{
						"apiVersion": "v1",
						"kind":       "ServiceAccount",
						"metadata": map[string]interface{}{
							"name": "test-sa",
						},
					},
				},
			},
			overrides: nil,
			validate: func(t *testing.T, result []unstructured.Unstructured) {
				require.Len(t, result, 1)
				assert.Equal(t, "ServiceAccount", result[0].GetKind())
				assert.Equal(t, "test-sa", result[0].GetName())
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ApplyOverrides(tt.objects, tt.overrides)

			if tt.wantErr {
				require.Error(t, err)
				if tt.wantErrContains != "" {
					assert.Contains(t, err.Error(), tt.wantErrContains)
				}
				return
			}

			require.NoError(t, err)
			if tt.validate != nil {
				tt.validate(t, result)
			}
		})
	}
}
