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

package e2e

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"
)

func externalSecretsTestResource(ready bool) *unstructured.Unstructured {
	status := "False"
	if ready {
		status = "True"
	}
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "external-secrets.io/v1",
		"kind":       "ExternalSecret",
		"metadata": map[string]interface{}{
			"name": "ingress-tls", "namespace": "openshift-ingress", "uid": "current-uid",
		},
		"spec": map[string]interface{}{"sensitive-test-data": "never-print-resource-specs"},
		"status": map[string]interface{}{
			"conditions": []interface{}{map[string]interface{}{
				"type": "Ready", "status": status, "reason": "SecretSyncedError", "message": "could not get secret data from provider",
			}},
		},
	}}
}

func TestExternalSecretsReadyStateReportsProviderFailure(t *testing.T) {
	object := externalSecretsTestResource(false)
	gvr := schema.GroupVersionResource{Group: "external-secrets.io", Version: "v1", Resource: "externalsecrets"}
	reference := corev1.ObjectReference{Kind: object.GetKind(), Namespace: object.GetNamespace(), Name: object.GetName(), UID: object.GetUID()}
	now := time.Now()
	event := &corev1.Event{
		ObjectMeta:     metav1.ObjectMeta{Namespace: object.GetNamespace(), Name: "latest"},
		InvolvedObject: reference, Type: corev1.EventTypeWarning, Reason: "UpdateFailed",
		Message:       "AADSTS70021: No matching federated identity record found",
		LastTimestamp: metav1.NewTime(now),
	}
	old := event.DeepCopy()
	old.Name, old.Message, old.LastTimestamp = "old", "old error", metav1.NewTime(now.Add(-time.Minute))
	stale := event.DeepCopy()
	stale.Name, stale.Message, stale.InvolvedObject.UID = "stale", "previous resource's error", "old-uid"
	stale.LastTimestamp = metav1.NewTime(now.Add(time.Minute))
	other := stale.DeepCopy()
	other.Name, other.InvolvedObject.Name, other.InvolvedObject.UID = "other", "another-secret", object.GetUID()
	client := fake.NewSimpleClientset(event, old, stale, other)
	dyn := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), object)

	ready, state := externalSecretsReadyState(t.Context(), client, dyn, gvr, object.GetNamespace(), object.GetName())
	require.False(t, ready)
	require.Contains(t, state, "reason=SecretSyncedError")
	require.Contains(t, state, event.Message, "provider failure must appear alongside the generic condition message")
	require.NotContains(t, state, old.Message)
	require.NotContains(t, state, stale.Message)
	require.NotContains(t, state, "never-print-resource-specs")
}

func TestWaitForExternalSecretsReady(t *testing.T) {
	object := externalSecretsTestResource(false)
	gvr := schema.GroupVersionResource{Group: "external-secrets.io", Version: "v1", Resource: "externalsecrets"}
	client := fake.NewSimpleClientset()
	dyn := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), object)
	polls := 0
	dyn.PrependReactor("get", gvr.Resource, func(clienttesting.Action) (bool, runtime.Object, error) {
		polls++
		return true, externalSecretsTestResource(polls >= 2), nil
	})
	err := waitForExternalSecretsReady(t.Context(), client, dyn, gvr, object.GetNamespace(), object.GetName(), time.Second, time.Millisecond)
	require.NoError(t, err)
	require.GreaterOrEqual(t, polls, 2, "creating the resource must not be treated as readiness")
}

func TestWaitForExternalSecretsReadyFailure(t *testing.T) {
	for _, cancelEarly := range []bool{false, true} {
		name := "timeout preserves provider error"
		if cancelEarly {
			name = "cancellation stops polling"
		}
		t.Run(name, func(t *testing.T) {
			object := externalSecretsTestResource(cancelEarly)
			gvr := schema.GroupVersionResource{Group: "external-secrets.io", Version: "v1", Resource: "externalsecrets"}
			event := &corev1.Event{
				ObjectMeta:     metav1.ObjectMeta{Namespace: object.GetNamespace(), Name: "failure"},
				InvolvedObject: corev1.ObjectReference{Kind: object.GetKind(), Namespace: object.GetNamespace(), Name: object.GetName(), UID: object.GetUID()},
				Type:           corev1.EventTypeWarning, Reason: "UpdateFailed", Message: "ForbiddenByRbac: Key Vault permission has not propagated",
			}
			client := fake.NewSimpleClientset(event)
			dyn := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), object)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if cancelEarly {
				cancel()
			}
			err := waitForExternalSecretsReady(ctx, client, dyn, gvr, object.GetNamespace(), object.GetName(), 30*time.Millisecond, time.Millisecond)
			if cancelEarly {
				require.ErrorIs(t, err, context.Canceled)
			} else {
				require.ErrorIs(t, err, context.DeadlineExceeded)
				require.ErrorContains(t, err, event.Message)
				require.ErrorContains(t, err, "Ready=True")
			}
		})
	}
}

func TestExternalSecretsReadyStateReadsClusterScopedStore(t *testing.T) {
	object := unstructuredClusterSecretStore("customer-akv", "https://test.vault.azure.net", "external-secrets", "eso-azure-kv")
	gvr := schema.GroupVersionResource{Group: "external-secrets.io", Version: "v1", Resource: "clustersecretstores"}
	client := fake.NewSimpleClientset()
	dyn := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), object)
	ready, state := externalSecretsReadyState(t.Context(), client, dyn, gvr, "", object.GetName())
	require.False(t, ready)
	require.Equal(t, "no status.conditions found", state)

	_, state = externalSecretsReadyState(t.Context(), client, dyn, gvr, "", "missing-store")
	require.Contains(t, state, "not found")
}
