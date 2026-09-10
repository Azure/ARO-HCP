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
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"

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

func newTestMonitorController(t *testing.T, dynamicClient *dynamicfake.FakeDynamicClient, sources ...*unstructured.Unstructured) *MonitorTranslatorController {
	t.Helper()

	smIndexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc})
	pmIndexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc})

	for _, source := range sources {
		switch source.GetKind() {
		case "ServiceMonitor":
			if err := smIndexer.Add(source); err != nil {
				t.Fatalf("failed to add ServiceMonitor to indexer: %v", err)
			}
		case "PodMonitor":
			if err := pmIndexer.Add(source); err != nil {
				t.Fatalf("failed to add PodMonitor to indexer: %v", err)
			}
		}
	}

	return &MonitorTranslatorController{
		dynamicClient: dynamicClient,
		smLister:      cache.NewGenericLister(smIndexer, schema.GroupResource{Group: SourceServiceMonitorGVR.Group, Resource: SourceServiceMonitorGVR.Resource}),
		pmLister:      cache.NewGenericLister(pmIndexer, schema.GroupResource{Group: SourcePodMonitorGVR.Group, Resource: SourcePodMonitorGVR.Resource}),
		workqueue: workqueue.NewTypedRateLimitingQueueWithConfig(
			workqueue.DefaultTypedControllerRateLimiter[string](),
			workqueue.TypedRateLimitingQueueConfig[string]{Name: "test"},
		),
	}
}

func hasPatchAction(actions []k8stesting.Action, gvr schema.GroupVersionResource, patchType types.PatchType) bool {
	for _, a := range actions {
		pa, ok := a.(k8stesting.PatchAction)
		if ok && pa.GetResource() == gvr && pa.GetPatchType() == patchType {
			return true
		}
	}
	return false
}

func TestSyncHandler(t *testing.T) {
	hcpOwnerRef := map[string]any{
		"apiVersion":         "hypershift.openshift.io/v1beta1",
		"kind":               "HostedControlPlane",
		"name":               "test-hcp",
		"uid":                "hcp-uid-001",
		"controller":         true,
		"blockOwnerDeletion": true,
	}

	tests := []struct {
		name                 string
		key                  string
		source               *unstructured.Unstructured
		wantErr              bool
		wantApplyPatchTarget bool
	}{
		{
			name: "source exists with HCP owner — translates",
			key:  "servicemonitors/ocm-test/test-sm",
			source: &unstructured.Unstructured{
				Object: map[string]any{
					"apiVersion": "monitoring.coreos.com/v1",
					"kind":       "ServiceMonitor",
					"metadata": map[string]any{
						"name":            "test-sm",
						"namespace":       "ocm-test",
						"uid":             "uid-123",
						"ownerReferences": []any{hcpOwnerRef},
					},
					"spec": map[string]any{},
				},
			},
			wantApplyPatchTarget: true,
		},
		{
			name: "source not found — no-op",
			key:  "servicemonitors/ocm-test/missing-sm",
		},
		{
			name: "source has no HCP owner — skip",
			key:  "servicemonitors/other-ns/test-sm",
			source: &unstructured.Unstructured{
				Object: map[string]any{
					"apiVersion": "monitoring.coreos.com/v1",
					"kind":       "ServiceMonitor",
					"metadata": map[string]any{
						"name":      "test-sm",
						"namespace": "other-ns",
						"uid":       "uid-no-owner",
					},
					"spec": map[string]any{},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			fakeDynClient := dynamicfake.NewSimpleDynamicClient(scheme)

			// Accept SSA patches without needing target in tracker
			fakeDynClient.PrependReactor("patch", "*", func(action k8stesting.Action) (bool, runtime.Object, error) {
				pa := action.(k8stesting.PatchAction)
				if pa.GetPatchType() == types.ApplyPatchType {
					return true, &unstructured.Unstructured{}, nil
				}
				return false, nil, nil
			})

			if tt.source != nil {
				if err := fakeDynClient.Tracker().Create(SourceServiceMonitorGVR, tt.source.DeepCopy(), tt.source.GetNamespace()); err != nil {
					t.Fatalf("failed to add source to tracker: %v", err)
				}
			}

			var sources []*unstructured.Unstructured
			if tt.source != nil {
				sources = append(sources, tt.source)
			}

			c := newTestMonitorController(t, fakeDynClient, sources...)

			ctx := context.Background()
			err := c.syncHandler(ctx, tt.key)
			if (err != nil) != tt.wantErr {
				t.Errorf("syncHandler() error = %v, wantErr %v", err, tt.wantErr)
			}

			actions := fakeDynClient.Actions()

			if tt.wantApplyPatchTarget {
				if !hasPatchAction(actions, TargetServiceMonitorGVR, types.ApplyPatchType) {
					t.Error("expected SSA apply patch on target ServiceMonitor")
				}
			}

			if !tt.wantApplyPatchTarget {
				for _, a := range actions {
					if a.GetVerb() != "get" && a.GetVerb() != "list" && a.GetVerb() != "watch" {
						t.Errorf("expected no mutating actions, got %s %s", a.GetVerb(), a.GetResource().Resource)
					}
				}
			}
		})
	}
}
