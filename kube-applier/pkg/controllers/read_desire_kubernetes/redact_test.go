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

package read_desire_kubernetes

import (
	"context"
	"encoding/json"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/Azure/ARO-HCP/internal/api/kubeapplier"
)

func TestIsSecret(t *testing.T) {
	cases := []struct {
		name string
		ref  kubeapplier.ResourceReference
		want bool
	}{
		{
			name: "core v1 secrets",
			ref:  kubeapplier.ResourceReference{Group: "", Version: "v1", Resource: "secrets"},
			want: true,
		},
		{
			name: "configmaps are not secrets",
			ref:  kubeapplier.ResourceReference{Group: "", Version: "v1", Resource: "configmaps"},
			want: false,
		},
		{
			name: "non-core group with secrets resource name",
			ref:  kubeapplier.ResourceReference{Group: "example.com", Version: "v1", Resource: "secrets"},
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isSecret(tc.ref); got != tc.want {
				t.Errorf("isSecret(%+v) = %v, want %v", tc.ref, got, tc.want)
			}
		})
	}
}

func TestRedactSecret(t *testing.T) {
	cases := []struct {
		name         string
		obj          *unstructured.Unstructured
		wantDataKeys []string
	}{
		{
			name: "strips unsafe data keys, keeps tls.crt",
			obj: &unstructured.Unstructured{Object: map[string]any{
				"apiVersion": "v1",
				"kind":       "Secret",
				"metadata":   map[string]any{"name": "s", "namespace": "ns"},
				"data": map[string]any{
					"tls.crt": "Y2VydA==",
					"tls.key": "a2V5",
					"ca.crt":  "Y2E=",
				},
			}},
			wantDataKeys: []string{"tls.crt"},
		},
		{
			name: "no safe keys present removes data entirely",
			obj: &unstructured.Unstructured{Object: map[string]any{
				"apiVersion": "v1",
				"kind":       "Secret",
				"metadata":   map[string]any{"name": "s", "namespace": "ns"},
				"data": map[string]any{
					"password": "c2VjcmV0",
					"token":    "dG9rZW4=",
				},
			}},
			wantDataKeys: nil,
		},
		{
			name: "secret with no data field",
			obj: &unstructured.Unstructured{Object: map[string]any{
				"apiVersion": "v1",
				"kind":       "Secret",
				"metadata":   map[string]any{"name": "s", "namespace": "ns"},
			}},
			wantDataKeys: nil,
		},
		{
			name: "stringData is removed",
			obj: &unstructured.Unstructured{Object: map[string]any{
				"apiVersion": "v1",
				"kind":       "Secret",
				"metadata":   map[string]any{"name": "s", "namespace": "ns"},
				"data":       map[string]any{"tls.crt": "Y2VydA=="},
				"stringData": map[string]any{"extra": "value"},
			}},
			wantDataKeys: []string{"tls.crt"},
		},
		{
			name: "data error fails closed",
			obj: &unstructured.Unstructured{Object: map[string]any{
				"apiVersion": "v1",
				"kind":       "Secret",
				"metadata":   map[string]any{"name": "s", "namespace": "ns"},
				"data":       "not-a-map",
			}},
			wantDataKeys: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			redactSecret(tc.obj)

			data, found, _ := unstructured.NestedMap(tc.obj.Object, "data")
			if tc.wantDataKeys == nil {
				if found && len(data) > 0 {
					t.Errorf("expected no data keys, got %v", data)
				}
				return
			}
			if !found {
				t.Fatalf("expected data to be present")
			}
			if len(data) != len(tc.wantDataKeys) {
				t.Errorf("data has %d keys, want %d: %v", len(data), len(tc.wantDataKeys), data)
			}
			for _, key := range tc.wantDataKeys {
				if _, ok := data[key]; !ok {
					t.Errorf("expected data key %q to be present", key)
				}
			}

			if _, found, _ := unstructured.NestedMap(tc.obj.Object, "stringData"); found {
				t.Error("stringData should have been removed")
			}

			if tc.obj.Object["metadata"] == nil {
				t.Error("metadata should be preserved")
			}
		})
	}
}

func TestRedactSecret_StripsUnsafeAnnotations(t *testing.T) {
	cases := []struct {
		name            string
		annotations     map[string]any
		wantAnnotations map[string]string
	}{
		{
			name: "last-applied-configuration removed",
			annotations: map[string]any{
				"kubectl.kubernetes.io/last-applied-configuration": `{"data":{"tls.key":"PRIVATE"}}`,
				"safe-annotation": "keep",
			},
			wantAnnotations: map[string]string{"safe-annotation": "keep"},
		},
		{
			name: "annotations removed entirely when only unsafe ones exist",
			annotations: map[string]any{
				"kubectl.kubernetes.io/last-applied-configuration": `{"secret":"stuff"}`,
			},
			wantAnnotations: nil,
		},
		{
			name:            "no annotations is a no-op",
			annotations:     nil,
			wantAnnotations: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			md := map[string]any{"name": "s", "namespace": "ns"}
			if tc.annotations != nil {
				md["annotations"] = tc.annotations
			}
			obj := &unstructured.Unstructured{Object: map[string]any{
				"apiVersion": "v1",
				"kind":       "Secret",
				"metadata":   md,
			}}
			redactSecret(obj)

			got, found, _ := unstructured.NestedStringMap(obj.Object, "metadata", "annotations")
			if tc.wantAnnotations == nil {
				if found && len(got) > 0 {
					t.Errorf("expected no annotations, got %v", got)
				}
				return
			}
			if !found {
				t.Fatal("expected annotations to be present")
			}
			for k, v := range tc.wantAnnotations {
				if got[k] != v {
					t.Errorf("annotation %q = %q, want %q", k, got[k], v)
				}
			}
			if len(got) != len(tc.wantAnnotations) {
				t.Errorf("annotations has %d keys, want %d", len(got), len(tc.wantAnnotations))
			}
		})
	}
}

func TestRedactSecret_RemovesBinaryData(t *testing.T) {
	obj := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata":   map[string]any{"name": "s", "namespace": "ns"},
		"data":       map[string]any{"tls.crt": "Y2VydA=="},
		// binaryData can carry the same sensitive payloads as data and must be
		// removed entirely, even though data[tls.crt] is preserved.
		"binaryData": map[string]any{
			"tls.key":              "cHJpdmF0ZQ==",
			"bootstrap.kubeconfig": "a3ViZWNvbmZpZw==",
		},
	}}

	redactSecret(obj)

	if _, found, _ := unstructured.NestedMap(obj.Object, "binaryData"); found {
		t.Error("binaryData should have been removed entirely")
	}

	data, found, err := unstructured.NestedMap(obj.Object, "data")
	if err != nil || !found {
		t.Fatalf("expected data to be preserved, found=%v err=%v", found, err)
	}
	if _, ok := data["tls.crt"]; !ok {
		t.Error("data[tls.crt] should be preserved")
	}
}

func TestRedactSecret_FailsClosedOnUnparseableAnnotations(t *testing.T) {
	obj := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata": map[string]any{
			"name":      "s",
			"namespace": "ns",
			"annotations": map[string]any{
				// A non-string annotation value makes this not a strict
				// map[string]string, so NestedStringMap errors. Redaction must
				// fail closed and drop annotations entirely rather than leave
				// the unsafe last-applied-configuration in place.
				"kubectl.kubernetes.io/last-applied-configuration": `{"data":{"tls.key":"PRIVATE"}}`,
				"malformed": map[string]any{"unexpected": "object"},
			},
		},
	}}

	redactSecret(obj)

	md, ok := obj.Object["metadata"].(map[string]any)
	if !ok {
		t.Fatal("metadata should be preserved as a map")
	}
	if got, present := md["annotations"]; present {
		t.Errorf("annotations should have been removed entirely (fail closed), got %v", got)
	}
}

func secretTarget(name string) kubeapplier.ResourceReference {
	return kubeapplier.ResourceReference{
		Group: "", Version: "v1", Resource: "secrets", Namespace: testTargetNs, Name: name,
	}
}

func TestSyncOnce_SecretTarget_RedactsUnsafeKeys(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	target := secretTarget("my-tls-secret")
	desire := newReadDesire(t, target)
	dyn := dynamicForTestdata(t, "testdata/secret_present")

	c, w := startSyncedController(t, ctx, target, desire, dyn)
	if err := c.SyncOnce(ctx); err != nil {
		t.Fatalf("SyncOnce: %v", err)
	}
	if len(w.updates) == 0 {
		t.Fatal("no status update recorded")
	}
	last := w.updates[len(w.updates)-1]
	if last.Status.KubeContent == nil || len(last.Status.KubeContent.Raw) == 0 {
		t.Fatal("KubeContent is empty after sync")
	}

	var got map[string]any
	if err := json.Unmarshal(last.Status.KubeContent.Raw, &got); err != nil {
		t.Fatalf("unmarshal kubeContent: %v", err)
	}

	if got["kind"] != "Secret" {
		t.Errorf("kind = %v, want Secret", got["kind"])
	}

	md, _ := got["metadata"].(map[string]any)
	if md == nil || md["name"] != "my-tls-secret" {
		t.Errorf("metadata.name = %v, want my-tls-secret", md["name"])
	}

	data, _ := got["data"].(map[string]any)
	if data == nil {
		t.Fatal("data should be present (tls.crt is safe)")
	}
	if _, ok := data["tls.crt"]; !ok {
		t.Error("data[tls.crt] should be preserved")
	}
	if _, ok := data["tls.key"]; ok {
		t.Error("data[tls.key] should have been redacted")
	}
	if _, ok := data["ca.crt"]; ok {
		t.Error("data[ca.crt] should have been redacted")
	}

	annotations, _ := md["annotations"].(map[string]any)
	if annotations != nil {
		if _, ok := annotations["kubectl.kubernetes.io/last-applied-configuration"]; ok {
			t.Error("last-applied-configuration annotation should have been stripped")
		}
	}

	cond := findCond(last.Status.Conditions, kubeapplier.ConditionTypeSuccessful)
	if cond == nil || cond.Status != metav1.ConditionTrue {
		t.Errorf("Successful=%v, want True", cond)
	}
}
