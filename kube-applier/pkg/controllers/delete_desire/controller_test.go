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

package delete_desire

import (
	"context"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic/fake"
	clienttesting "k8s.io/client-go/testing"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	kubeapplierapi "github.com/Azure/ARO-HCP/internal/apis/kubeapplier"
	resourcesapi "github.com/Azure/ARO-HCP/internal/apis/resources"
)

func mustParseID(t *testing.T, s string) *azcorearm.ResourceID {
	t.Helper()
	id, err := azcorearm.ParseResourceID(s)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return id
}

func newDeleteDesire(t *testing.T, name string, target kubeapplierapi.ResourceReference) *kubeapplierapi.DeleteDesire {
	t.Helper()
	return &kubeapplierapi.DeleteDesire{
		CosmosMetadata: resourcesapi.CosmosMetadata{
			ResourceID: mustParseID(t, kubeapplierapi.ToClusterScopedDeleteDesireResourceIDString(
				"00000000-0000-0000-0000-000000000001", "rg", "cluster", name,
			)),
		},
		Spec: kubeapplierapi.DeleteDesireSpec{
			ManagementCluster: "mgmt-1",
			TargetItem:        target,
		},
	}
}

func newConfigMap(name, ns string, withDeletionTS bool) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(schema.GroupVersionKind{Version: "v1", Kind: "ConfigMap"})
	obj.SetName(name)
	obj.SetNamespace(ns)
	obj.SetUID(types.UID(name + "-uid"))
	if withDeletionTS {
		dt := metav1.NewTime(time.Now().Add(-time.Second))
		obj.SetDeletionTimestamp(&dt)
	}
	return obj
}

func TestEvaluate_TargetGoneIsSuccessful(t *testing.T) {
	dyn := fake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{
		{Version: "v1", Resource: "configmaps"}: "ConfigMapList",
	})
	c := &DeleteDesireController{dyn: dyn}

	desire := newDeleteDesire(t, "d", kubeapplierapi.ResourceReference{
		Version: "v1", Resource: "configmaps", Namespace: "default", Name: "missing",
	})
	mutate, err := c.evaluate(context.Background(), desire)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	mutate(desire)
	if got := findCond(desire.Status.Conditions, kubeapplierapi.ConditionTypeSuccessful); got == nil ||
		got.Status != metav1.ConditionTrue {
		t.Errorf("Successful=%v, want True (target absent)", got)
	}
}

func TestEvaluate_TargetWithDeletionTimestampWaits(t *testing.T) {
	dyn := fake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(),
		map[schema.GroupVersionResource]string{
			{Version: "v1", Resource: "configmaps"}: "ConfigMapList",
		},
		newConfigMap("doomed", "default", true))
	c := &DeleteDesireController{dyn: dyn}

	desire := newDeleteDesire(t, "d", kubeapplierapi.ResourceReference{
		Version: "v1", Resource: "configmaps", Namespace: "default", Name: "doomed",
	})
	mutate, err := c.evaluate(context.Background(), desire)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	mutate(desire)
	got := findCond(desire.Status.Conditions, kubeapplierapi.ConditionTypeSuccessful)
	if got == nil || got.Status != metav1.ConditionFalse {
		t.Fatalf("Successful=%v, want False (waiting)", got)
	}
	if got.Reason != kubeapplierapi.ConditionReasonWaitingForDeletion {
		t.Errorf("Reason = %q, want %q", got.Reason, kubeapplierapi.ConditionReasonWaitingForDeletion)
	}
	if !contains(got.Message, "doomed-uid") {
		t.Errorf("Message %q does not contain UID", got.Message)
	}
}

func TestEvaluate_PresentNoTSIssuesDelete_ThenWaitsForFinalizers(t *testing.T) {
	cm := newConfigMap("d1", "default", false)
	dyn := fake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(),
		map[schema.GroupVersionResource]string{
			{Version: "v1", Resource: "configmaps"}: "ConfigMapList",
		},
		cm)

	// Reactor: when delete is issued, instead of removing the object, set
	// deletionTimestamp + UID — simulating a finalizer in flight.
	dyn.PrependReactor("delete", "configmaps", func(action clienttesting.Action) (bool, runtime.Object, error) {
		// mark the object as terminating in the tracker by replacing it with a tombstoned copy.
		da := action.(clienttesting.DeleteAction)
		dt := metav1.NewTime(time.Now())
		updated := newConfigMap(da.GetName(), da.GetNamespace(), false)
		updated.SetDeletionTimestamp(&dt)
		// Don't actually delete; the next Get will return the updated object.
		return true, updated, nil
	})
	dyn.PrependReactor("get", "configmaps", func(action clienttesting.Action) (bool, runtime.Object, error) {
		// Track invocation count so the second Get (post-delete) returns the terminating object.
		// First Get sees the original (no DT). Second Get (post-delete) sees terminating.
		// We use a closure-state counter via a local var.
		return false, nil, nil // fall through on the first call
	})
	// Wire a counter via two-stage reactor: first call passes through (default tracker
	// returns cm without DT), second call returns terminating object.
	calls := 0
	dyn.PrependReactor("get", "configmaps", func(action clienttesting.Action) (bool, runtime.Object, error) {
		calls++
		if calls < 2 {
			return false, nil, nil // let default reactor handle it
		}
		dt := metav1.NewTime(time.Now())
		obj := newConfigMap("d1", "default", false)
		obj.SetDeletionTimestamp(&dt)
		return true, obj, nil
	})

	c := &DeleteDesireController{dyn: dyn}
	desire := newDeleteDesire(t, "d", kubeapplierapi.ResourceReference{
		Version: "v1", Resource: "configmaps", Namespace: "default", Name: "d1",
	})
	mutate, err := c.evaluate(context.Background(), desire)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	mutate(desire)
	got := findCond(desire.Status.Conditions, kubeapplierapi.ConditionTypeSuccessful)
	if got == nil || got.Status != metav1.ConditionFalse || got.Reason != kubeapplierapi.ConditionReasonWaitingForDeletion {
		t.Errorf("Successful=%v, want False/WaitingForDeletion", got)
	}
}

func TestEvaluate_DeleteAPIErrorClassifiesAsKubeAPIError(t *testing.T) {
	cm := newConfigMap("d2", "default", false)
	dyn := fake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(),
		map[schema.GroupVersionResource]string{
			{Version: "v1", Resource: "configmaps"}: "ConfigMapList",
		},
		cm)
	dyn.PrependReactor("delete", "configmaps", func(action clienttesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewServiceUnavailable("apiserver unavailable")
	})
	c := &DeleteDesireController{dyn: dyn}
	desire := newDeleteDesire(t, "d", kubeapplierapi.ResourceReference{
		Version: "v1", Resource: "configmaps", Namespace: "default", Name: "d2",
	})
	mutate, _ := c.evaluate(context.Background(), desire)
	mutate(desire)
	got := findCond(desire.Status.Conditions, kubeapplierapi.ConditionTypeSuccessful)
	if got == nil || got.Status != metav1.ConditionFalse || got.Reason != kubeapplierapi.ConditionReasonKubeAPIError {
		t.Errorf("Successful=%v, want False/KubeAPIError", got)
	}
}

func TestEvaluate_BadTargetIsPreCheckFailed(t *testing.T) {
	dyn := fake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), nil)
	c := &DeleteDesireController{dyn: dyn}
	desire := newDeleteDesire(t, "d", kubeapplierapi.ResourceReference{
		// Missing Resource and Name.
	})
	mutate, _ := c.evaluate(context.Background(), desire)
	mutate(desire)
	got := findCond(desire.Status.Conditions, kubeapplierapi.ConditionTypeSuccessful)
	if got == nil || got.Reason != kubeapplierapi.ConditionReasonPreCheckFailed {
		t.Errorf("Successful=%v, want PreCheckFailed", got)
	}
}

func findCond(conds []metav1.Condition, t string) *metav1.Condition {
	for i := range conds {
		if conds[i].Type == t {
			return &conds[i]
		}
	}
	return nil
}

func contains(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
