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

package apply_desire

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/fake"
	clienttesting "k8s.io/client-go/testing"

	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/kube-applier/pkg/controllers/desirestatuswriter"
	"github.com/Azure/ARO-HCP/kube-applier/pkg/controllers/keys"
)

type retryStatusReplacer struct {
	fetcher *staticFetcher
	err     error
	writes  int
}

func (r *retryStatusReplacer) Replace(_ context.Context, d *kubeapplierapi.ApplyDesire) error {
	r.writes++
	if r.err != nil {
		return r.err
	}
	r.fetcher.desire = d.DeepCopy()
	return nil
}

func newRetryController(t *testing.T, dyn dynamic.Interface, content []byte) (*ApplyDesireController, *retryStatusReplacer, keys.ApplyDesireKey) {
	t.Helper()
	c := newCadenceController(t, Config{})
	t.Cleanup(c.queue.ShutDown)
	fetcher := &staticFetcher{desire: newApplyDesire(t, "retry", configMapTarget("hello"), content)}
	replacer := &retryStatusReplacer{fetcher: fetcher}
	c.dyn = dyn
	c.fetcher = fetcher
	c.writer = desirestatuswriter.New[kubeapplierapi.ApplyDesire, keys.ApplyDesireKey, *kubeapplierapi.ApplyDesire](fetcher, replacer)
	return c, replacer, mustKey(t, fetcher.desire)
}

func assertApplyResult(t *testing.T, d *kubeapplierapi.ApplyDesire, status metav1.ConditionStatus, reason string, degraded metav1.ConditionStatus) {
	t.Helper()
	for _, condType := range []string{kubeapplierapi.ConditionTypeSuccessfullyApplied, kubeapplierapi.ConditionTypeSuccessful} {
		if got := findCond(d.Status.Conditions, condType); got == nil || got.Status != status || got.Reason != reason {
			t.Errorf("%s = %v, want status %s, reason %s", condType, got, status, reason)
		}
	}
	if got := findCond(d.Status.Conditions, kubeapplierapi.ConditionTypeDegraded); got == nil || got.Status != degraded {
		t.Errorf("Degraded = %v, want %s", got, degraded)
	}
}

func waitForApplyRetry(t *testing.T, c *ApplyDesireController) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for c.queue.Len() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if c.queue.Len() == 0 {
		t.Fatal("apply retry was not queued before the periodic resync")
	}
}

func newNamespaceRetryAPI(t *testing.T) *fake.FakeDynamicClient {
	t.Helper()
	configMaps := schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}
	namespaces := schema.GroupVersionResource{Version: "v1", Resource: "namespaces"}
	dyn := fakeDynamic(t, map[schema.GroupVersionResource]string{
		configMaps: "ConfigMapList",
		namespaces: "NamespaceList",
	})
	// The fake tracker does not enforce namespace existence or implement SSA
	// creation. Supply those API semantics while retaining real stored objects.
	dyn.PrependReactor("patch", "configmaps", func(action clienttesting.Action) (bool, runtime.Object, error) {
		if _, err := dyn.Tracker().Get(namespaces, "", action.GetNamespace()); err != nil {
			return true, nil, err
		}
		obj := &unstructured.Unstructured{}
		if err := obj.UnmarshalJSON(action.(clienttesting.PatchAction).GetPatch()); err != nil {
			return true, nil, err
		}
		obj.SetGeneration(7)
		if err := dyn.Tracker().Create(configMaps, obj, action.GetNamespace()); err != nil {
			return true, nil, err
		}
		return true, obj, nil
	})
	return dyn
}

func TestProcessNext_ApplyRetriesUntilNamespaceAppears(t *testing.T) {
	ctx := context.Background()
	dyn := newNamespaceRetryAPI(t)
	c, replacer, key := newRetryController(t, dyn, []byte(`{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"hello","namespace":"default"}}`))
	previousGeneration := int64(3)
	replacer.fetcher.desire.Status.AppliedKubeGeneration = &previousGeneration
	c.queue.Add(key)

	// Reconcile again while the namespace is absent. The unchanged failure
	// status must not consume the retry when the status writer skips its write.
	for wantRetries := 1; wantRetries <= 2; wantRetries++ {
		if !c.processNext(ctx) {
			t.Fatal("queue shut down before apply")
		}
		assertApplyResult(t, replacer.fetcher.desire, metav1.ConditionFalse, kubeapplierapi.ConditionReasonKubeAPIError, metav1.ConditionFalse)
		if replacer.fetcher.desire.Status.AppliedKubeGeneration != nil {
			t.Fatal("failed apply retained a stale applied generation")
		}
		if replacer.writes != 1 {
			t.Fatalf("status writes after unchanged failure = %d, want 1", replacer.writes)
		}
		if got := c.queue.NumRequeues(key); got != wantRetries {
			t.Fatalf("NumRequeues = %d, want %d", got, wantRetries)
		}
		waitForApplyRetry(t, c)
	}

	// No desire update or informer resync occurs when the namespace appears.
	namespace := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "v1", "kind": "Namespace",
		"metadata": map[string]interface{}{"name": "default"},
	}}
	if _, err := dyn.Resource(schema.GroupVersionResource{Version: "v1", Resource: "namespaces"}).Create(ctx, namespace, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create namespace: %v", err)
	}
	if !c.processNext(ctx) {
		t.Fatal("queue shut down before recovery")
	}
	if replacer.writes != 2 {
		t.Errorf("status writes after recovery = %d, want 2", replacer.writes)
	}
	assertApplyResult(t, replacer.fetcher.desire, metav1.ConditionTrue, kubeapplierapi.ConditionReasonNoErrors, metav1.ConditionFalse)
	if got := c.queue.NumRequeues(key); got != 0 {
		t.Errorf("NumRequeues after recovery = %d, want 0", got)
	}
	if got := c.queue.Len(); got != 0 {
		t.Errorf("queued work after recovery = %d, want 0", got)
	}
}

func TestProcessNext_ApplyFailureClassification(t *testing.T) {
	resource := schema.GroupResource{Resource: "configmaps"}
	for _, tc := range []struct {
		name     string
		applyErr error
		preCheck bool
		retry    bool
		degraded metav1.ConditionStatus
	}{
		{name: "namespace absent", applyErr: apierrors.NewNotFound(schema.GroupResource{Resource: "namespaces"}, "default"), retry: true, degraded: metav1.ConditionFalse},
		{name: "unstructured 404", applyErr: &apierrors.StatusError{ErrStatus: metav1.Status{Code: 404}}, retry: true, degraded: metav1.ConditionFalse},
		{name: "conflict", applyErr: apierrors.NewConflict(resource, "hello", errors.New("conflicting update")), retry: true, degraded: metav1.ConditionFalse},
		{name: "throttled", applyErr: apierrors.NewTooManyRequests("throttled", 1), retry: true, degraded: metav1.ConditionFalse},
		{name: "server error", applyErr: apierrors.NewInternalError(errors.New("server failed")), retry: true, degraded: metav1.ConditionTrue},
		{name: "service unavailable", applyErr: apierrors.NewServiceUnavailable("unavailable"), retry: true, degraded: metav1.ConditionTrue},
		{name: "server timeout", applyErr: apierrors.NewServerTimeout(resource, "apply", 1), retry: true, degraded: metav1.ConditionTrue},
		{name: "timeout", applyErr: apierrors.NewTimeoutError("timed out", 1), retry: true, degraded: metav1.ConditionTrue},
		{name: "network error", applyErr: &net.OpError{Op: "write", Net: "tcp", Err: io.EOF}, retry: true, degraded: metav1.ConditionTrue},
		{name: "invalid spec", applyErr: apierrors.NewInvalid(schema.GroupKind{Kind: "ConfigMap"}, "hello", field.ErrorList{field.Invalid(field.NewPath("data"), 1, "must be a string")}), degraded: metav1.ConditionFalse},
		{name: "bad request", applyErr: apierrors.NewBadRequest("invalid content"), degraded: metav1.ConditionFalse},
		{name: "forbidden", applyErr: apierrors.NewForbidden(resource, "hello", errors.New("forbidden")), degraded: metav1.ConditionFalse},
		{name: "precheck", preCheck: true, degraded: metav1.ConditionFalse},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dyn := fakeDynamic(t, nil)
			dyn.PrependReactor("patch", "configmaps", func(clienttesting.Action) (bool, runtime.Object, error) {
				return true, nil, tc.applyErr
			})
			content := []byte(`{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"hello"}}`)
			if tc.preCheck {
				content = nil
			}
			c, replacer, key := newRetryController(t, dyn, content)
			c.queue.Add(key)
			if !c.processNext(context.Background()) {
				t.Fatal("queue shut down before apply")
			}
			reason := kubeapplierapi.ConditionReasonKubeAPIError
			if tc.preCheck {
				reason = kubeapplierapi.ConditionReasonPreCheckFailed
			}
			assertApplyResult(t, replacer.fetcher.desire, metav1.ConditionFalse, reason, tc.degraded)
			if got := c.queue.NumRequeues(key); (got > 0) != tc.retry {
				t.Errorf("NumRequeues = %d, want retry %t", got, tc.retry)
			}
			if !tc.retry && c.queue.Len() != 0 {
				t.Error("permanent failure queued another apply")
			}
		})
	}
}

func TestSyncOnce_ApplyPreservesStatusWriteErrors(t *testing.T) {
	statusErr := errors.New("status persistence failed")
	for _, tc := range []struct {
		name     string
		applyErr error
	}{
		{name: "success"},
		{name: "transient failure", applyErr: apierrors.NewServiceUnavailable("unavailable")},
		{name: "permanent failure", applyErr: apierrors.NewBadRequest("invalid content")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dyn := fakeDynamic(t, nil)
			dyn.PrependReactor("patch", "configmaps", func(clienttesting.Action) (bool, runtime.Object, error) {
				return true, &unstructured.Unstructured{}, tc.applyErr
			})
			c, replacer, key := newRetryController(t, dyn, []byte(`{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"hello"}}`))
			replacer.err = statusErr
			err := c.SyncOnce(context.Background(), key)
			if !errors.Is(err, statusErr) {
				t.Errorf("SyncOnce error = %v, want status write error", err)
			}
			if tc.applyErr != nil && !errors.Is(err, tc.applyErr) {
				t.Errorf("SyncOnce error = %v, want simultaneous apply error preserved", err)
			}
			// The same persistence failure must reach the workqueue even when
			// the apply failure itself is permanent or the apply succeeds.
			c.queue.Add(key)
			if !c.processNext(context.Background()) {
				t.Fatal("queue shut down before apply")
			}
			if got := c.queue.NumRequeues(key); got != 1 {
				t.Errorf("NumRequeues after status write failure = %d, want 1", got)
			}
		})
	}
}
