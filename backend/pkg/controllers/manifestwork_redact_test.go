package controllers

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

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	workv1 "open-cluster-management.io/api/work/v1"
)

func TestRedactManifestWork(t *testing.T) {
	tests := []struct {
		name   string
		mw     *workv1.ManifestWork
		verify func(t *testing.T, result *workv1.ManifestWork)
	}{
		{
			name: "empty ManifestWork passes through unchanged",
			mw:   &workv1.ManifestWork{},
			verify: func(t *testing.T, result *workv1.ManifestWork) {
				require.Empty(t, result.Spec.Workload.Manifests)
				require.Empty(t, result.Status.ResourceStatus.Manifests)
			},
		},
		{
			name: "a ConfigMap is left unchanged",
			mw: &workv1.ManifestWork{
				Spec: workv1.ManifestWorkSpec{
					Workload: workv1.ManifestsTemplate{
						Manifests: []workv1.Manifest{
							newRawManifest(t, newConfigMap("cm1", "default")),
						},
					},
				},
			},
			verify: func(t *testing.T, result *workv1.ManifestWork) {
				require.Len(t, result.Spec.Workload.Manifests, 1)
				// ConfigMap data should be intact
				var cm corev1.ConfigMap
				require.NoError(t, json.Unmarshal(result.Spec.Workload.Manifests[0].Raw, &cm))
				require.Equal(t, "value", cm.Data["key"])
			},
		},
		{
			name: "raw Secret with Data and StringData is redacted",
			mw: &workv1.ManifestWork{
				Spec: workv1.ManifestWorkSpec{
					Workload: workv1.ManifestsTemplate{
						Manifests: []workv1.Manifest{
							newRawManifest(t, newSecret("my-secret", "default",
								map[string][]byte{
									"password":  []byte("super-secret"),
									"api-token": []byte("tok-12345"),
								},
								map[string]string{
									"username": "admin",
								},
							)),
						},
					},
				},
			},
			verify: func(t *testing.T, result *workv1.ManifestWork) {
				require.Len(t, result.Spec.Workload.Manifests, 1)
				secret := unmarshalSecretFromManifest(t, result.Spec.Workload.Manifests[0])
				// Sensitive fields redacted
				requireSecretRedacted(t, secret)
				require.Len(t, secret.Data, 2, "keys should be preserved")
				require.Len(t, secret.StringData, 1, "keys should be preserved")
				// Non-sensitive metadata preserved
				require.Equal(t, "my-secret", secret.Name)
				require.Equal(t, "default", secret.Namespace)
				require.Equal(t, corev1.SecretTypeOpaque, secret.Type)
			},
		},
		{
			name: "typed Secret Object is redacted",
			mw: &workv1.ManifestWork{
				Spec: workv1.ManifestWorkSpec{
					Workload: workv1.ManifestsTemplate{
						Manifests: []workv1.Manifest{
							newTypedManifest(newSecret("typed-secret", "ns1",
								map[string][]byte{"token": []byte("secret-val")},
								map[string]string{"conn": "host=db password=abc"},
							)),
						},
					},
				},
			},
			verify: func(t *testing.T, result *workv1.ManifestWork) {
				require.Len(t, result.Spec.Workload.Manifests, 1)
				m := result.Spec.Workload.Manifests[0]
				// Typed path: Object should be the modified Secret, Raw should be nil
				require.NotNil(t, m.Object)
				require.Nil(t, m.Raw)
				secret, ok := m.Object.(*corev1.Secret)
				require.True(t, ok, "Object should still be *corev1.Secret")
				requireSecretRedacted(t, secret)
				require.Equal(t, "typed-secret", secret.Name)
			},
		},
		{
			name: "typed Secret with nil Data and StringData does not panic",
			mw: &workv1.ManifestWork{
				Spec: workv1.ManifestWorkSpec{
					Workload: workv1.ManifestsTemplate{
						Manifests: []workv1.Manifest{
							newTypedManifest(newSecret("empty-secret", "default", nil, nil)),
						},
					},
				},
			},
			verify: func(t *testing.T, result *workv1.ManifestWork) {
				secret := result.Spec.Workload.Manifests[0].Object.(*corev1.Secret)
				require.Nil(t, secret.Data)
				require.Nil(t, secret.StringData)
			},
		},
		{
			name: "raw nested ManifestWork containing a raw Secret",
			mw: func() *workv1.ManifestWork {
				innerSecretManifest := newRawManifest(t, newSecret("inner-secret", "default",
					map[string][]byte{"key": []byte("sensitive-data")}, nil,
				))
				innerMW := &workv1.ManifestWork{
					TypeMeta:   metav1.TypeMeta{APIVersion: "work.open-cluster-management.io/v1", Kind: "ManifestWork"},
					ObjectMeta: metav1.ObjectMeta{Name: "inner-mw"},
					Spec: workv1.ManifestWorkSpec{
						Workload: workv1.ManifestsTemplate{
							Manifests: []workv1.Manifest{innerSecretManifest},
						},
					},
				}
				return &workv1.ManifestWork{
					ObjectMeta: metav1.ObjectMeta{Name: "outer-mw"},
					Spec: workv1.ManifestWorkSpec{
						Workload: workv1.ManifestsTemplate{
							Manifests: []workv1.Manifest{
								newRawManifest(t, innerMW),
							},
						},
					},
				}
			}(),
			verify: func(t *testing.T, result *workv1.ManifestWork) {
				require.Len(t, result.Spec.Workload.Manifests, 1)
				innerMW := unmarshalManifestWorkFromManifest(t, result.Spec.Workload.Manifests[0])
				require.Len(t, innerMW.Spec.Workload.Manifests, 1)
				innerSecret := unmarshalSecretFromManifest(t, innerMW.Spec.Workload.Manifests[0])
				requireSecretRedacted(t, innerSecret)
				require.Contains(t, innerSecret.Data, "key", "key name should be preserved")
			},
		},
		{
			name: "typed nested ManifestWork containing a typed Secret",
			mw: &workv1.ManifestWork{
				ObjectMeta: metav1.ObjectMeta{Name: "outer-mw"},
				Spec: workv1.ManifestWorkSpec{
					Workload: workv1.ManifestsTemplate{
						Manifests: []workv1.Manifest{
							newTypedManifest(&workv1.ManifestWork{
								TypeMeta:   metav1.TypeMeta{APIVersion: "work.open-cluster-management.io/v1", Kind: "ManifestWork"},
								ObjectMeta: metav1.ObjectMeta{Name: "inner-mw"},
								Spec: workv1.ManifestWorkSpec{
									Workload: workv1.ManifestsTemplate{
										Manifests: []workv1.Manifest{
											newTypedManifest(newSecret("nested-secret", "default",
												map[string][]byte{"db-pass": []byte("p4ssw0rd")}, nil,
											)),
										},
									},
								},
							}),
						},
					},
				},
			},
			verify: func(t *testing.T, result *workv1.ManifestWork) {
				innerMW := result.Spec.Workload.Manifests[0].Object.(*workv1.ManifestWork)
				innerSecret := innerMW.Spec.Workload.Manifests[0].Object.(*corev1.Secret)
				requireSecretRedacted(t, innerSecret)
				require.Equal(t, "nested-secret", innerSecret.Name)
			},
		},
		{
			name: "3-level deep nesting (MW -> MW -> Secret)",
			mw: func() *workv1.ManifestWork {
				secretManifest := newRawManifest(t, newSecret("deep-secret", "default",
					map[string][]byte{"deep-key": []byte("deep-value")}, nil,
				))
				level2MW := &workv1.ManifestWork{
					TypeMeta:   metav1.TypeMeta{APIVersion: "work.open-cluster-management.io/v1", Kind: "ManifestWork"},
					ObjectMeta: metav1.ObjectMeta{Name: "level2-mw"},
					Spec: workv1.ManifestWorkSpec{
						Workload: workv1.ManifestsTemplate{
							Manifests: []workv1.Manifest{secretManifest},
						},
					},
				}
				level1MW := &workv1.ManifestWork{
					TypeMeta:   metav1.TypeMeta{APIVersion: "work.open-cluster-management.io/v1", Kind: "ManifestWork"},
					ObjectMeta: metav1.ObjectMeta{Name: "level1-mw"},
					Spec: workv1.ManifestWorkSpec{
						Workload: workv1.ManifestsTemplate{
							Manifests: []workv1.Manifest{
								newRawManifest(t, level2MW),
							},
						},
					},
				}
				return &workv1.ManifestWork{
					ObjectMeta: metav1.ObjectMeta{Name: "root-mw"},
					Spec: workv1.ManifestWorkSpec{
						Workload: workv1.ManifestsTemplate{
							Manifests: []workv1.Manifest{
								newRawManifest(t, level1MW),
							},
						},
					},
				}
			}(),
			verify: func(t *testing.T, result *workv1.ManifestWork) {
				level1 := unmarshalManifestWorkFromManifest(t, result.Spec.Workload.Manifests[0])
				level2 := unmarshalManifestWorkFromManifest(t, level1.Spec.Workload.Manifests[0])
				deepSecret := unmarshalSecretFromManifest(t, level2.Spec.Workload.Manifests[0])
				requireSecretRedacted(t, deepSecret)
				require.Equal(t, "deep-secret", deepSecret.Name)
			},
		},
		{
			name: "unstructured Secret Object uses fallback raw path and is redacted",
			mw: &workv1.ManifestWork{
				Spec: workv1.ManifestWorkSpec{
					Workload: workv1.ManifestsTemplate{
						Manifests: []workv1.Manifest{
							newTypedManifest(&unstructured.Unstructured{
								Object: map[string]interface{}{
									"apiVersion": "v1",
									"kind":       "Secret",
									"metadata": map[string]interface{}{
										"name":      "unstructured-secret",
										"namespace": "default",
									},
									"data": map[string]interface{}{
										// Value must be valid base64 because corev1.Secret.Data
										// is map[string][]byte, and json.Unmarshal decodes JSON
										// strings as base64 for []byte fields.
										"akey": "YXZhbHVl", // base64("avalue")
									},
									"type": "Opaque",
								},
							}),
						},
					},
				},
			},
			verify: func(t *testing.T, result *workv1.ManifestWork) {
				m := result.Spec.Workload.Manifests[0]
				// The default path converts Object to Raw, so Raw should be set
				require.NotEmpty(t, m.Raw)
				secret := unmarshalSecretFromManifest(t, m)
				requireSecretRedacted(t, secret)
				require.Equal(t, "unstructured-secret", secret.Name)
			},
		},
		{
			name: "raw JSON without apiVersion/kind is left unchanged",
			mw: &workv1.ManifestWork{
				Spec: workv1.ManifestWorkSpec{
					Workload: workv1.ManifestsTemplate{
						Manifests: []workv1.Manifest{
							{RawExtension: runtime.RawExtension{
								Raw: []byte(`{"name":"some-resource","data":{"key":"value"}}`),
							}},
						},
					},
				},
			},
			verify: func(t *testing.T, result *workv1.ManifestWork) {
				var parsed map[string]interface{}
				require.NoError(t, json.Unmarshal(result.Spec.Workload.Manifests[0].Raw, &parsed))
				data := parsed["data"].(map[string]interface{})
				require.Equal(t, "value", data["key"], "data should be untouched without GVK")
			},
		},
		{
			name: "multiple manifests with mixed types - only Secret is redacted",
			mw: &workv1.ManifestWork{
				Spec: workv1.ManifestWorkSpec{
					Workload: workv1.ManifestsTemplate{
						Manifests: []workv1.Manifest{
							newRawManifest(t, newConfigMap("cm1", "default")),
							newRawManifest(t, newSecret("secret1", "default",
								map[string][]byte{"pw": []byte("s3cret")}, nil,
							)),
						},
					},
				},
			},
			verify: func(t *testing.T, result *workv1.ManifestWork) {
				require.Len(t, result.Spec.Workload.Manifests, 2)
				// ConfigMap unchanged
				var cm corev1.ConfigMap
				require.NoError(t, json.Unmarshal(result.Spec.Workload.Manifests[0].Raw, &cm))
				require.Equal(t, "value", cm.Data["key"])
				// Secret redacted
				secret := unmarshalSecretFromManifest(t, result.Spec.Workload.Manifests[1])
				requireSecretRedacted(t, secret)
			},
		},
		{
			name: "status feedback with JsonRaw containing a Secret is redacted",
			mw: func() *workv1.ManifestWork {
				secretJSON, _ := json.Marshal(newSecret("feedback-secret", "default",
					map[string][]byte{"asecretkey": []byte("asecretvalue")}, nil,
				))
				jsonRawStr := string(secretJSON)
				return &workv1.ManifestWork{
					Status: workv1.ManifestWorkStatus{
						ResourceStatus: workv1.ManifestResourceStatus{
							Manifests: []workv1.ManifestCondition{
								{
									StatusFeedbacks: workv1.StatusFeedbackResult{
										Values: []workv1.FeedbackValue{
											{
												Name: "full-resource",
												Value: workv1.FieldValue{
													Type:    workv1.JsonRaw,
													JsonRaw: &jsonRawStr,
												},
											},
										},
									},
								},
							},
						},
					},
				}
			}(),
			verify: func(t *testing.T, result *workv1.ManifestWork) {
				fv := result.Status.ResourceStatus.Manifests[0].StatusFeedbacks.Values[0]
				require.NotNil(t, fv.Value.JsonRaw)
				var secret corev1.Secret
				require.NoError(t, json.Unmarshal([]byte(*fv.Value.JsonRaw), &secret))
				requireSecretRedacted(t, &secret)
				require.Equal(t, "feedback-secret", secret.Name)
			},
		},
		{
			name: "status feedback with JsonRaw ManifestWork containing Secret is redacted",
			mw: func() *workv1.ManifestWork {
				innerSecretManifest := newRawManifest(t, newSecret("inner-fb-secret", "default",
					map[string][]byte{"api-key": []byte("key-123")}, nil,
				))
				innerMW := &workv1.ManifestWork{
					TypeMeta:   metav1.TypeMeta{APIVersion: "work.open-cluster-management.io/v1", Kind: "ManifestWork"},
					ObjectMeta: metav1.ObjectMeta{Name: "feedback-mw"},
					Spec: workv1.ManifestWorkSpec{
						Workload: workv1.ManifestsTemplate{
							Manifests: []workv1.Manifest{innerSecretManifest},
						},
					},
				}
				mwJSON, _ := json.Marshal(innerMW)
				jsonRawStr := string(mwJSON)
				return &workv1.ManifestWork{
					Status: workv1.ManifestWorkStatus{
						ResourceStatus: workv1.ManifestResourceStatus{
							Manifests: []workv1.ManifestCondition{
								{
									StatusFeedbacks: workv1.StatusFeedbackResult{
										Values: []workv1.FeedbackValue{
											{
												Name: "nested-mw-feedback",
												Value: workv1.FieldValue{
													Type:    workv1.JsonRaw,
													JsonRaw: &jsonRawStr,
												},
											},
										},
									},
								},
							},
						},
					},
				}
			}(),
			verify: func(t *testing.T, result *workv1.ManifestWork) {
				fv := result.Status.ResourceStatus.Manifests[0].StatusFeedbacks.Values[0]
				require.NotNil(t, fv.Value.JsonRaw)
				var innerMW workv1.ManifestWork
				require.NoError(t, json.Unmarshal([]byte(*fv.Value.JsonRaw), &innerMW))
				innerSecret := unmarshalSecretFromManifest(t, innerMW.Spec.Workload.Manifests[0])
				requireSecretRedacted(t, innerSecret)
			},
		},
		{
			name: "status feedback with non-K8s JsonRaw is left unchanged",
			mw: func() *workv1.ManifestWork {
				rawStatus := `{"replicas":3,"readyReplicas":2}`
				return &workv1.ManifestWork{
					Status: workv1.ManifestWorkStatus{
						ResourceStatus: workv1.ManifestResourceStatus{
							Manifests: []workv1.ManifestCondition{
								{
									StatusFeedbacks: workv1.StatusFeedbackResult{
										Values: []workv1.FeedbackValue{
											{
												Name: "deployment-status",
												Value: workv1.FieldValue{
													Type:    workv1.JsonRaw,
													JsonRaw: &rawStatus,
												},
											},
										},
									},
								},
							},
						},
					},
				}
			}(),
			verify: func(t *testing.T, result *workv1.ManifestWork) {
				fv := result.Status.ResourceStatus.Manifests[0].StatusFeedbacks.Values[0]
				require.Equal(t, `{"replicas":3,"readyReplicas":2}`, *fv.Value.JsonRaw,
					"non-K8s JsonRaw should be untouched")
			},
		},
		{
			name: "status feedback with non-JsonRaw value types are left unchanged",
			mw: func() *workv1.ManifestWork {
				intVal := int64(42)
				boolVal := true
				strVal := "healthy"
				return &workv1.ManifestWork{
					Status: workv1.ManifestWorkStatus{
						ResourceStatus: workv1.ManifestResourceStatus{
							Manifests: []workv1.ManifestCondition{
								{
									StatusFeedbacks: workv1.StatusFeedbackResult{
										Values: []workv1.FeedbackValue{
											{
												Name:  "replicas",
												Value: workv1.FieldValue{Type: workv1.Integer, Integer: &intVal},
											},
											{
												Name:  "ready",
												Value: workv1.FieldValue{Type: workv1.Boolean, Boolean: &boolVal},
											},
											{
												Name:  "phase",
												Value: workv1.FieldValue{Type: workv1.String, String: &strVal},
											},
										},
									},
								},
							},
						},
					},
				}
			}(),
			verify: func(t *testing.T, result *workv1.ManifestWork) {
				values := result.Status.ResourceStatus.Manifests[0].StatusFeedbacks.Values
				require.Equal(t, int64(42), *values[0].Value.Integer)
				require.Equal(t, true, *values[1].Value.Boolean)
				require.Equal(t, "healthy", *values[2].Value.String)
			},
		},
		{
			name: "both Object and Raw set: typed Secret Object - Raw is nilled out",
			mw: func() *workv1.ManifestWork {
				secret := newSecret("dual-secret", "default",
					map[string][]byte{"password": []byte("typed-secret-value")}, nil,
				)
				rawSecret, _ := json.Marshal(newSecret("dual-secret", "default",
					map[string][]byte{"password": []byte("raw-secret-value")}, nil,
				))
				return &workv1.ManifestWork{
					Spec: workv1.ManifestWorkSpec{
						Workload: workv1.ManifestsTemplate{
							Manifests: []workv1.Manifest{
								{RawExtension: runtime.RawExtension{
									Object: secret,
									Raw:    rawSecret,
								}},
							},
						},
					},
				}
			}(),
			verify: func(t *testing.T, result *workv1.ManifestWork) {
				m := result.Spec.Workload.Manifests[0]
				// Object should be the redacted Secret
				require.NotNil(t, m.Object)
				secret := m.Object.(*corev1.Secret)
				requireSecretRedacted(t, secret)
				// Raw must be nil - not left with unredacted content
				require.Nil(t, m.Raw, "Raw must be nilled out to prevent leaking unredacted data")
			},
		},
		{
			name: "both Object and Raw set: typed ManifestWork Object - Raw is nilled out",
			mw: func() *workv1.ManifestWork {
				innerMW := &workv1.ManifestWork{
					TypeMeta:   metav1.TypeMeta{APIVersion: "work.open-cluster-management.io/v1", Kind: "ManifestWork"},
					ObjectMeta: metav1.ObjectMeta{Name: "inner-mw"},
					Spec: workv1.ManifestWorkSpec{
						Workload: workv1.ManifestsTemplate{
							Manifests: []workv1.Manifest{
								newTypedManifest(newSecret("nested-secret", "default",
									map[string][]byte{"key": []byte("nested-value")}, nil,
								)),
							},
						},
					},
				}
				// Simulate stale Raw containing unredacted data
				rawMW, _ := json.Marshal(innerMW)
				return &workv1.ManifestWork{
					Spec: workv1.ManifestWorkSpec{
						Workload: workv1.ManifestsTemplate{
							Manifests: []workv1.Manifest{
								{RawExtension: runtime.RawExtension{
									Object: innerMW,
									Raw:    rawMW,
								}},
							},
						},
					},
				}
			}(),
			verify: func(t *testing.T, result *workv1.ManifestWork) {
				m := result.Spec.Workload.Manifests[0]
				// Object should be the redacted ManifestWork
				require.NotNil(t, m.Object)
				innerMW := m.Object.(*workv1.ManifestWork)
				innerSecret := innerMW.Spec.Workload.Manifests[0].Object.(*corev1.Secret)
				requireSecretRedacted(t, innerSecret)
				// Raw must be nil - not left with unredacted content
				require.Nil(t, m.Raw, "Raw must be nilled out to prevent leaking unredacted data")
			},
		},
		{
			name: "both Object and Raw set: unrecognized Object type - Raw is overwritten and redacted",
			mw: func() *workv1.ManifestWork {
				// Object is an unstructured Secret (hits the default type-switch branch)
				unstructuredSecret := &unstructured.Unstructured{
					Object: map[string]interface{}{
						"apiVersion": "v1",
						"kind":       "Secret",
						"metadata": map[string]interface{}{
							"name":      "unstructured-dual",
							"namespace": "default",
						},
						"data": map[string]interface{}{
							"token": "dW5zdHJ1Y3R1cmVkLXNlY3JldA==",
						},
					},
				}
				// Stale Raw with different sensitive content
				staleRaw := []byte(`{"apiVersion":"v1","kind":"Secret","metadata":{"name":"stale"},"data":{"old-key":"c3RhbGUtZGF0YQ=="}}`)
				return &workv1.ManifestWork{
					Spec: workv1.ManifestWorkSpec{
						Workload: workv1.ManifestsTemplate{
							Manifests: []workv1.Manifest{
								{RawExtension: runtime.RawExtension{
									Object: unstructuredSecret,
									Raw:    staleRaw,
								}},
							},
						},
					},
				}
			}(),
			verify: func(t *testing.T, result *workv1.ManifestWork) {
				m := result.Spec.Workload.Manifests[0]
				// Object should be nil (default branch converts to Raw)
				require.Nil(t, m.Object)
				// Raw should contain the redacted Secret derived from Object, not the stale Raw
				require.NotEmpty(t, m.Raw)
				secret := unmarshalSecretFromManifest(t, m)
				requireSecretRedacted(t, secret)
				require.Equal(t, "unstructured-dual", secret.Name,
					"should contain the Object's resource, not the stale Raw")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := RedactManifestWork(tt.mw)
			require.NoError(t, err)
			require.NotNil(t, result)
			tt.verify(t, result)
		})
	}
}

func TestRedactManifestWorkDoesNotMutateOriginal(t *testing.T) {
	t.Run("typed Secret path", func(t *testing.T) {
		originalSecret := newSecret("original", "default",
			map[string][]byte{"password": []byte("super-secret")},
			map[string]string{"conn-string": "host=db password=abc"},
		)
		mw := &workv1.ManifestWork{
			Spec: workv1.ManifestWorkSpec{
				Workload: workv1.ManifestsTemplate{
					Manifests: []workv1.Manifest{
						newTypedManifest(originalSecret),
					},
				},
			},
		}

		result, err := RedactManifestWork(mw)
		require.NoError(t, err)

		// Original Secret must be untouched
		require.Equal(t, []byte("super-secret"), originalSecret.Data["password"],
			"original Data should not be mutated")
		require.Equal(t, "host=db password=abc", originalSecret.StringData["conn-string"],
			"original StringData should not be mutated")

		// Result must be redacted
		redactedSecret := result.Spec.Workload.Manifests[0].Object.(*corev1.Secret)
		requireSecretRedacted(t, redactedSecret)
	})

	t.Run("raw Secret path", func(t *testing.T) {
		secretJSON, err := json.Marshal(newSecret("raw-original", "default",
			map[string][]byte{"token": []byte("raw-secret-token")}, nil,
		))
		require.NoError(t, err)

		originalRaw := make([]byte, len(secretJSON))
		copy(originalRaw, secretJSON)

		mw := &workv1.ManifestWork{
			Spec: workv1.ManifestWorkSpec{
				Workload: workv1.ManifestsTemplate{
					Manifests: []workv1.Manifest{
						{RawExtension: runtime.RawExtension{Raw: secretJSON}},
					},
				},
			},
		}

		result, err := RedactManifestWork(mw)
		require.NoError(t, err)

		// Original Raw bytes must be untouched (DeepCopy creates a new slice)
		require.Equal(t, originalRaw, mw.Spec.Workload.Manifests[0].Raw,
			"original Raw bytes should not be mutated")

		// Result must be redacted
		redactedSecret := unmarshalSecretFromManifest(t, result.Spec.Workload.Manifests[0])
		requireSecretRedacted(t, redactedSecret)
	})

	t.Run("status feedback JsonRaw path", func(t *testing.T) {
		secretJSON, err := json.Marshal(newSecret("fb-original", "default",
			map[string][]byte{"cert": []byte("original-cert")}, nil,
		))
		require.NoError(t, err)
		originalStr := string(secretJSON)
		jsonRawStr := string(secretJSON) // separate copy for the MW

		mw := &workv1.ManifestWork{
			Status: workv1.ManifestWorkStatus{
				ResourceStatus: workv1.ManifestResourceStatus{
					Manifests: []workv1.ManifestCondition{
						{
							StatusFeedbacks: workv1.StatusFeedbackResult{
								Values: []workv1.FeedbackValue{
									{
										Name: "resource",
										Value: workv1.FieldValue{
											Type:    workv1.JsonRaw,
											JsonRaw: &jsonRawStr,
										},
									},
								},
							},
						},
					},
				},
			},
		}

		result, err := RedactManifestWork(mw)
		require.NoError(t, err)

		// Original JsonRaw must be untouched
		require.Equal(t, originalStr, *mw.Status.ResourceStatus.Manifests[0].StatusFeedbacks.Values[0].Value.JsonRaw,
			"original JsonRaw should not be mutated")

		// Result must be redacted
		var redactedSecret corev1.Secret
		require.NoError(t, json.Unmarshal(
			[]byte(*result.Status.ResourceStatus.Manifests[0].StatusFeedbacks.Values[0].Value.JsonRaw),
			&redactedSecret,
		))
		requireSecretRedacted(t, &redactedSecret) //nolint:gosec // pointer to local is fine in test
	})
}

func TestDetectGVK(t *testing.T) {
	tests := []struct {
		name    string
		raw     []byte
		wantGVK schema.GroupVersionKind
		wantOK  bool
	}{
		{
			name:    "valid core/v1 Secret",
			raw:     []byte(`{"apiVersion":"v1","kind":"Secret"}`),
			wantGVK: schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Secret"},
			wantOK:  true,
		},
		{
			name:    "valid ManifestWork",
			raw:     []byte(`{"apiVersion":"work.open-cluster-management.io/v1","kind":"ManifestWork"}`),
			wantGVK: schema.GroupVersionKind{Group: "work.open-cluster-management.io", Version: "v1", Kind: "ManifestWork"},
			wantOK:  true,
		},
		{
			name:    "valid apps/v1 Deployment",
			raw:     []byte(`{"apiVersion":"apps/v1","kind":"Deployment"}`),
			wantGVK: schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"},
			wantOK:  true,
		},
		{
			name:   "missing kind returns false",
			raw:    []byte(`{"apiVersion":"v1"}`),
			wantOK: false,
		},
		{
			name:   "empty kind returns false",
			raw:    []byte(`{"apiVersion":"v1","kind":""}`),
			wantOK: false,
		},
		{
			name:   "invalid JSON returns false",
			raw:    []byte(`not json`),
			wantOK: false,
		},
		{
			name:   "empty bytes returns false",
			raw:    []byte{},
			wantOK: false,
		},
		{
			name:   "JSON without K8s fields returns false",
			raw:    []byte(`{"name":"foo","value":"bar"}`),
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gvk, ok := detectGVKFromRaw(tt.raw)
			require.Equal(t, tt.wantOK, ok)
			if ok {
				require.Equal(t, tt.wantGVK, gvk)
			}
		})
	}
}

// --- Test helpers ---

func newRawManifest(t *testing.T, obj any) workv1.Manifest {
	t.Helper()
	raw, err := json.Marshal(obj)
	require.NoError(t, err)
	return workv1.Manifest{
		RawExtension: runtime.RawExtension{Raw: raw},
	}
}

func newTypedManifest(obj runtime.Object) workv1.Manifest {
	return workv1.Manifest{
		RawExtension: runtime.RawExtension{Object: obj},
	}
}

func unmarshalSecretFromManifest(t *testing.T, m workv1.Manifest) *corev1.Secret {
	t.Helper()
	raw := manifestRaw(t, m)
	var s corev1.Secret
	require.NoError(t, json.Unmarshal(raw, &s))
	return &s
}

func unmarshalManifestWorkFromManifest(t *testing.T, m workv1.Manifest) *workv1.ManifestWork {
	t.Helper()
	raw := manifestRaw(t, m)
	var mw workv1.ManifestWork
	require.NoError(t, json.Unmarshal(raw, &mw))
	return &mw
}

func manifestRaw(t *testing.T, m workv1.Manifest) []byte {
	t.Helper()
	raw, err := m.MarshalJSON()
	require.NoError(t, err)
	return raw
}

func newSecret(name, namespace string, data map[string][]byte, stringData map[string]string) *corev1.Secret {
	return &corev1.Secret{
		TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "Secret"},
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Data:       data,
		StringData: stringData,
		Type:       corev1.SecretTypeOpaque,
	}
}

func newConfigMap(name, namespace string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "ConfigMap"},
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Data:       map[string]string{"key": "value"},
	}
}

// requireSecretRedacted asserts that all Data values are the redacted placeholder
// and all StringData values are the redacted placeholder.
func requireSecretRedacted(t *testing.T, secret *corev1.Secret) {
	t.Helper()
	for k, v := range secret.Data {
		require.Equal(t, []byte(redactedValue), v, "Data[%q] should be redacted", k)
	}
	for k, v := range secret.StringData {
		require.Equal(t, redactedValue, v, "StringData[%q] should be redacted", k)
	}
}
