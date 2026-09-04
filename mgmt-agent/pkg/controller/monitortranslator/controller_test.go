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

package monitortranslator

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"

	"github.com/Azure/ARO-Tools/testutil"
)

func TestTranslateServiceMonitor(t *testing.T) {
	source := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "monitoring.coreos.com/v1",
			"kind":       "ServiceMonitor",
			"metadata": map[string]any{
				"name":      "test-service",
				"namespace": "ocm-arohcppers-abc123-xyz",
				"uid":       "source-uid-123",
				"labels": map[string]any{
					"app": "test-service",
				},
			},
			"spec": map[string]any{
				"endpoints": []any{
					map[string]any{
						"port":     "metrics",
						"interval": "30s",
						"path":     "/metrics",
					},
				},
				"selector": map[string]any{
					"matchLabels": map[string]any{
						"app": "test-service",
					},
				},
				"namespaceSelector": map[string]any{
					"matchNames": []any{"ocm-arohcppers-abc123-xyz"},
				},
			},
		},
	}

	result := Translate(source, SourceServiceMonitorGVR, TargetServiceMonitorGVR)
	testutil.CompareWithFixture(t, result)
}

func TestTranslatePodMonitor(t *testing.T) {
	source := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "monitoring.coreos.com/v1",
			"kind":       "PodMonitor",
			"metadata": map[string]any{
				"name":      "test-pods",
				"namespace": "ocm-arohcppers-abc123-xyz",
				"uid":       "source-uid-456",
				"labels": map[string]any{
					"app": "test-pods",
				},
			},
			"spec": map[string]any{
				"podMetricsEndpoints": []any{
					map[string]any{
						"port": "metrics",
						"path": "/metrics",
					},
				},
				"selector": map[string]any{
					"matchLabels": map[string]any{
						"app": "test-pods",
					},
				},
			},
		},
	}

	result := Translate(source, SourcePodMonitorGVR, TargetPodMonitorGVR)
	testutil.CompareWithFixture(t, result)
}

func TestTranslatePreservesOwnerReference(t *testing.T) {
	source := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "monitoring.coreos.com/v1",
			"kind":       "ServiceMonitor",
			"metadata": map[string]any{
				"name":      "owned-monitor",
				"namespace": "ocm-test",
				"uid":       "uid-789",
			},
			"spec": map[string]any{},
		},
	}

	result := Translate(source, SourceServiceMonitorGVR, TargetServiceMonitorGVR)

	ownerRefs := result.GetOwnerReferences()
	if len(ownerRefs) != 1 {
		t.Fatalf("expected 1 owner reference, got %d", len(ownerRefs))
	}

	expected := metav1.OwnerReference{
		APIVersion: "monitoring.coreos.com/v1",
		Kind:       "ServiceMonitor",
		Name:       "owned-monitor",
		UID:        types.UID("uid-789"),
	}
	if ownerRefs[0] != expected {
		t.Errorf("owner reference mismatch:\ngot:  %+v\nwant: %+v", ownerRefs[0], expected)
	}
}

func TestTranslateNoLabels(t *testing.T) {
	source := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "monitoring.coreos.com/v1",
			"kind":       "ServiceMonitor",
			"metadata": map[string]any{
				"name":      "no-labels",
				"namespace": "ocm-test",
				"uid":       "uid-nolabel",
			},
			"spec": map[string]any{
				"endpoints": []any{
					map[string]any{
						"port": "metrics",
					},
				},
			},
		},
	}

	result := Translate(source, SourceServiceMonitorGVR, TargetServiceMonitorGVR)

	if labels := result.GetLabels(); len(labels) != 0 {
		t.Errorf("expected no labels, got %v", labels)
	}
	if result.GetAPIVersion() != "azmonitoring.coreos.com/v1" {
		t.Errorf("expected apiVersion azmonitoring.coreos.com/v1, got %s", result.GetAPIVersion())
	}
}
