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
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	kubetesting "k8s.io/client-go/testing"
)

func TestIngressCertificateOrderRequeueOnlyChangesOwnedOrderAnnotation(t *testing.T) {
	t.Parallel()
	for _, existingAnnotations := range []bool{false, true} {
		name := "without-annotations"
		if existingAnnotations {
			name = "preserves-annotations"
		}
		t.Run(name, func(t *testing.T) {
			certificate, objects := ingressOrderRequeueTestObjects()
			order := objects[1].(*unstructured.Unstructured)
			if existingAnnotations {
				order.SetAnnotations(map[string]string{"preserve.example/key": "untouched", ingressCertificateOrderRequeueAnnotation: "previous"})
			}
			unrelated := order.DeepCopy()
			unrelated.SetName("unrelated-order")
			unrelated.SetUID("unrelated-order-uid")
			owner := unrelated.GetOwnerReferences()
			owner[0].UID = "another-request-uid"
			unrelated.SetOwnerReferences(owner)
			objects = append(objects, unrelated)
			client := ingressOrderRequeueTestClient(objects...)
			now := time.Date(2026, time.October, 28, 12, 0, 0, 0, time.UTC)

			patched, err := newIngressCertificateOrderRequeuer(client).requeue(t.Context(), certificate, now)

			require.NoError(t, err)
			require.Equal(t, []string{order.GetName()}, patched)
			for _, object := range objects {
				before := object.(*unstructured.Unstructured)
				resource := ingressOrderRequeueTestResource(before.GetKind())
				after, getErr := client.Tracker().Get(resource, before.GetNamespace(), before.GetName())
				require.NoError(t, getErr)
				expected := before.DeepCopy()
				if before.GetUID() == order.GetUID() {
					annotations := expected.GetAnnotations()
					if annotations == nil {
						annotations = make(map[string]string)
					}
					annotations[ingressCertificateOrderRequeueAnnotation] = now.Format(time.RFC3339Nano)
					expected.SetAnnotations(annotations)
				}
				require.Equal(t, expected, after, "only the dedicated annotation on the owned Order may change")
			}
			patches := 0
			for _, action := range client.Actions() {
				require.Equal(t, certificate.GetNamespace(), action.GetNamespace())
				require.Empty(t, action.GetSubresource(), "must never mutate status")
				if action.GetVerb() == "patch" {
					patches++
					require.Equal(t, "orders", action.GetResource().Resource)
					require.Equal(t, types.JSONPatchType, action.(kubetesting.PatchAction).GetPatchType())
				} else {
					require.Equal(t, "list", action.GetVerb(), "no create, update, delete, or other mutations")
				}
			}
			require.Equal(t, 1, patches)
		})
	}
}

func TestIngressCertificateOrderRequeueRequiresControllerUIDChain(t *testing.T) {
	t.Parallel()
	for _, link := range []string{"Certificate", "CertificateRequest", "Order"} {
		for _, mismatch := range []string{"uid", "not-controller", "missing-controller", "kind", "api-version", "namespace", "missing-uid"} {
			t.Run(link+"/"+mismatch, func(t *testing.T) {
				certificate, objects := ingressOrderRequeueTestObjects()
				index := map[string]int{"Certificate": 0, "CertificateRequest": 1, "Order": 2}[link]
				object := objects[index].(*unstructured.Unstructured)
				owners := object.GetOwnerReferences()
				switch mismatch {
				case "uid":
					owners[0].UID = "same-name-but-replaced-owner"
				case "not-controller":
					controller := false
					owners[0].Controller = &controller
				case "missing-controller":
					owners[0].Controller = nil
				case "kind":
					owners[0].Kind = "OtherKind"
				case "api-version":
					owners[0].APIVersion = "unrelated.example/v1"
				case "namespace":
					object.SetNamespace("another-namespace")
				case "missing-uid":
					owners[0].UID = ""
				}
				object.SetOwnerReferences(owners)
				client := ingressOrderRequeueTestClient(objects...)

				patched, err := newIngressCertificateOrderRequeuer(client).requeue(t.Context(), certificate, time.Now())

				require.NoError(t, err)
				require.Empty(t, patched)
				ingressOrderRequeueRequireNoPatches(t, client)
			})
		}
	}
}

func TestIngressCertificateOrderRequeueRequiresIdentity(t *testing.T) {
	t.Parallel()
	for _, scenario := range []string{"nil-certificate", "certificate-uid", "certificate-namespace", "request-uid", "order-uid", "order-resource-version"} {
		t.Run(scenario, func(t *testing.T) {
			certificate, objects := ingressOrderRequeueTestObjects()
			switch scenario {
			case "nil-certificate":
				certificate = nil
			case "certificate-uid":
				certificate.SetUID("")
			case "certificate-namespace":
				certificate.SetNamespace("")
			case "request-uid":
				objects[0].(*unstructured.Unstructured).SetUID("")
			case "order-uid":
				objects[1].(*unstructured.Unstructured).SetUID("")
			case "order-resource-version":
				objects[1].(*unstructured.Unstructured).SetResourceVersion("")
			}
			client := ingressOrderRequeueTestClient(objects...)

			patched, err := newIngressCertificateOrderRequeuer(client).requeue(t.Context(), certificate, time.Now())

			require.NoError(t, err)
			require.Empty(t, patched)
			ingressOrderRequeueRequireNoPatches(t, client)
			if certificate == nil || certificate.GetUID() == "" || certificate.GetNamespace() == "" {
				require.Empty(t, client.Actions(), "missing Certificate identity must not widen API scope")
			}
		})
	}
}

func TestIngressCertificateOrderRequeueRequiresProcessingAndAllChallengesValid(t *testing.T) {
	t.Parallel()
	for _, scenario := range []struct {
		name            string
		orderState      string
		challengeStates []string
		wantPatch       bool
	}{
		{name: "processing-valid", orderState: "processing", challengeStates: []string{"valid"}, wantPatch: true},
		{name: "multiple-valid", orderState: "processing", challengeStates: []string{"valid", "valid"}, wantPatch: true},
		{name: "none", orderState: "processing"},
		{name: "valid-then-pending", orderState: "processing", challengeStates: []string{"valid", "pending"}},
		{name: "pending-then-valid", orderState: "processing", challengeStates: []string{"pending", "valid"}},
		{name: "processing-challenge", orderState: "processing", challengeStates: []string{"processing"}},
		{name: "invalid-challenge", orderState: "processing", challengeStates: []string{"invalid"}},
		{name: "unknown-challenge", orderState: "processing", challengeStates: []string{""}},
		{name: "pending-order", orderState: "pending", challengeStates: []string{"valid"}},
		{name: "ready-order", orderState: "ready", challengeStates: []string{"valid"}},
		{name: "valid-order", orderState: "valid", challengeStates: []string{"valid"}},
		{name: "invalid-order", orderState: "invalid", challengeStates: []string{"valid"}},
		{name: "unknown-order", challengeStates: []string{"valid"}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			certificate, objects := ingressOrderRequeueTestObjects()
			order := objects[1].(*unstructured.Unstructured)
			require.NoError(t, unstructured.SetNestedField(order.Object, scenario.orderState, "status", "state"))
			challenge := objects[2].(*unstructured.Unstructured)
			objects = objects[:2]
			for index, state := range scenario.challengeStates {
				copy := challenge.DeepCopy()
				copy.SetName(challenge.GetName() + string(rune('a'+index)))
				copy.SetUID(types.UID(copy.GetName()))
				require.NoError(t, unstructured.SetNestedField(copy.Object, state, "status", "state"))
				objects = append(objects, copy)
			}
			// An invalid Challenge outside this Order's UID chain must not block it,
			// nor may an unrelated valid Challenge qualify an Order with none.
			for _, state := range []string{"invalid", "valid"} {
				unrelated := challenge.DeepCopy()
				unrelated.SetName("unrelated-" + state)
				owner := unrelated.GetOwnerReferences()
				owner[0].UID = "another-order-uid"
				unrelated.SetOwnerReferences(owner)
				require.NoError(t, unstructured.SetNestedField(unrelated.Object, state, "status", "state"))
				objects = append(objects, unrelated)
			}
			client := ingressOrderRequeueTestClient(objects...)

			patched, err := newIngressCertificateOrderRequeuer(client).requeue(t.Context(), certificate, time.Now())

			require.NoError(t, err)
			if scenario.wantPatch {
				require.Equal(t, []string{order.GetName()}, patched)
			} else {
				require.Empty(t, patched)
				ingressOrderRequeueRequireNoPatches(t, client)
			}
		})
	}
}

func TestIngressCertificateOrderRequeueRejectsOrderReplacementAndStaleVersion(t *testing.T) {
	t.Parallel()
	for _, scenario := range []string{"replacement-uid", "changed-resource-version"} {
		t.Run(scenario, func(t *testing.T) {
			certificate, objects := ingressOrderRequeueTestObjects()
			order := objects[1].(*unstructured.Unstructured)
			client := ingressOrderRequeueTestClient(objects...)
			var concurrent *unstructured.Unstructured
			client.PrependReactor("patch", "orders", func(action kubetesting.Action) (bool, runtime.Object, error) {
				concurrent = order.DeepCopy()
				if scenario == "replacement-uid" {
					// Keep the RV equal to prove the UID guard independently.
					concurrent.SetUID("replacement-uid")
				} else {
					concurrent.SetResourceVersion("new-version")
				}
				require.NoError(t, client.Tracker().Update(action.GetResource(), concurrent, action.GetNamespace()))
				// The real fake tracker applies JSON Patch test operations atomically.
				return false, nil, nil
			})

			patched, err := newIngressCertificateOrderRequeuer(client).requeue(t.Context(), certificate, time.Now())

			require.Error(t, err)
			require.ErrorContains(t, err, "requeue Order "+order.GetName())
			require.Empty(t, patched)
			after, getErr := client.Tracker().Get(ingressOrderRequeueTestResource("Order"), order.GetNamespace(), order.GetName())
			require.NoError(t, getErr)
			require.Equal(t, concurrent, after, "failed preconditions must leave the replacement or concurrent update intact")
		})
	}
}

func TestIngressCertificateOrderRequeueThrottleBoundaryAndRetry(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		certificate, objects := ingressOrderRequeueTestObjects()
		order := objects[1].(*unstructured.Unstructured)
		client := ingressOrderRequeueTestClient(objects...)
		requeuer := newIngressCertificateOrderRequeuer(client)
		patchCalls := 0
		denied := errors.New("patch denied")
		client.PrependReactor("patch", "orders", func(kubetesting.Action) (bool, runtime.Object, error) {
			patchCalls++
			if patchCalls == 1 {
				return true, nil, denied
			}
			return false, nil, nil
		})

		patched, err := requeuer.requeue(t.Context(), certificate, time.Now())
		require.ErrorIs(t, err, denied)
		require.Empty(t, patched)
		patched, err = requeuer.requeue(t.Context(), certificate, time.Now())
		require.NoError(t, err)
		require.Equal(t, []string{order.GetName()}, patched, "failed patch must not consume the throttle")
		first, err := client.Tracker().Get(ingressOrderRequeueTestResource("Order"), order.GetNamespace(), order.GetName())
		require.NoError(t, err)
		firstValue := first.(*unstructured.Unstructured).GetAnnotations()[ingressCertificateOrderRequeueAnnotation]
		for _, delay := range []time.Duration{15 * time.Second, 15*time.Second - time.Nanosecond} {
			time.Sleep(delay)
			patched, err = requeuer.requeue(t.Context(), certificate, time.Now())
			require.NoError(t, err)
			require.Empty(t, patched)
			require.Equal(t, 2, patchCalls)
		}
		time.Sleep(time.Nanosecond)
		patched, err = requeuer.requeue(t.Context(), certificate, time.Now())
		require.NoError(t, err)
		require.Equal(t, []string{order.GetName()}, patched, "exactly thirty seconds permits the next wake")
		require.Equal(t, 3, patchCalls)
		second, err := client.Tracker().Get(ingressOrderRequeueTestResource("Order"), order.GetNamespace(), order.GetName())
		require.NoError(t, err)
		require.NotEqual(t, firstValue, second.(*unstructured.Unstructured).GetAnnotations()[ingressCertificateOrderRequeueAnnotation], "every successful wake must change metadata")
	})
}

func TestIngressCertificateOrderRequeueThrottleUsesOrderUID(t *testing.T) {
	t.Parallel()
	certificate, objects := ingressOrderRequeueTestObjects()
	order := objects[1].(*unstructured.Unstructured)
	challenge := objects[2].(*unstructured.Unstructured)
	client := ingressOrderRequeueTestClient(objects...)
	requeuer := newIngressCertificateOrderRequeuer(client)
	now := time.Now()
	patched, err := requeuer.requeue(t.Context(), certificate, now)
	require.NoError(t, err)
	require.Equal(t, []string{order.GetName()}, patched)
	order = order.DeepCopy()
	order.SetUID("replacement-uid")
	owners := challenge.GetOwnerReferences()
	owners[0].UID = order.GetUID()
	challenge.SetOwnerReferences(owners)
	require.NoError(t, client.Tracker().Update(ingressOrderRequeueTestResource("Order"), order, order.GetNamespace()))
	require.NoError(t, client.Tracker().Update(ingressOrderRequeueTestResource("Challenge"), challenge, challenge.GetNamespace()))

	patched, err = requeuer.requeue(t.Context(), certificate, now)

	require.NoError(t, err)
	require.Equal(t, []string{order.GetName()}, patched, "a previous incarnation's throttle must not suppress the new UID")
}

func TestIngressCertificateOrderRequeueChangesExistingTimestamp(t *testing.T) {
	t.Parallel()
	certificate, objects := ingressOrderRequeueTestObjects()
	order := objects[1].(*unstructured.Unstructured)
	now := time.Now()
	previous := now.UTC().Format(time.RFC3339Nano)
	order.SetAnnotations(map[string]string{ingressCertificateOrderRequeueAnnotation: previous})
	client := ingressOrderRequeueTestClient(objects...)

	patched, err := newIngressCertificateOrderRequeuer(client).requeue(t.Context(), certificate, now)

	require.NoError(t, err)
	require.Equal(t, []string{order.GetName()}, patched)
	after, err := client.Tracker().Get(ingressOrderRequeueTestResource("Order"), order.GetNamespace(), order.GetName())
	require.NoError(t, err)
	require.NotEqual(t, previous, after.(*unstructured.Unstructured).GetAnnotations()[ingressCertificateOrderRequeueAnnotation], "a fresh helper must still emit a changed annotation if its clock matches an earlier wake")
}

func TestIngressCertificateOrderRequeueSurfacesAPIErrors(t *testing.T) {
	t.Parallel()
	for _, resource := range []string{"certificaterequests", "orders", "challenges"} {
		t.Run(resource, func(t *testing.T) {
			certificate, objects := ingressOrderRequeueTestObjects()
			client := ingressOrderRequeueTestClient(objects...)
			denied := errors.New("list denied")
			client.PrependReactor("list", resource, func(kubetesting.Action) (bool, runtime.Object, error) { return true, nil, denied })

			patched, err := newIngressCertificateOrderRequeuer(client).requeue(t.Context(), certificate, time.Now())

			require.ErrorIs(t, err, denied)
			require.Empty(t, patched)
			ingressOrderRequeueRequireNoPatches(t, client)
		})
	}
}

func TestIngressCertificateOrderRequeueCancellationUsesCallerContext(t *testing.T) {
	t.Parallel()
	for _, cancelAt := range []string{"before-start", "list-certificaterequests", "list-orders", "list-challenges", "patch-orders"} {
		t.Run(cancelAt, func(t *testing.T) {
			certificate, objects := ingressOrderRequeueTestObjects()
			client := ingressOrderRequeueTestClient(objects...)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			observedCancellation := false
			contextClient := &ingressOrderRequeueContextClient{Interface: client, inspect: func(requestCtx context.Context, operation string) error {
				require.Equal(t, ctx, requestCtx, "every Kubernetes call must retain the caller's cancellation and budget")
				require.False(t, observedCancellation, "no requests may follow a canceled operation")
				if operation == cancelAt {
					observedCancellation = true
					cancel()
				}
				return requestCtx.Err()
			}}
			if cancelAt == "before-start" {
				cancel()
			}

			patched, err := newIngressCertificateOrderRequeuer(contextClient).requeue(ctx, certificate, time.Now())

			require.ErrorIs(t, err, context.Canceled)
			require.Empty(t, patched)
			ingressOrderRequeueRequireNoPatches(t, client)
			if cancelAt == "before-start" {
				require.Empty(t, client.Actions())
			} else {
				require.True(t, observedCancellation)
			}
		})
	}
}

// Creates a complete owner chain with otherwise meaningful Order spec and status.
func ingressOrderRequeueTestObjects() (*unstructured.Unstructured, []runtime.Object) {
	certificate := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "cert-manager.io/v1",
		"kind":       "Certificate",
		"metadata": map[string]any{
			"name": "ingress", "namespace": "openshift-ingress", "uid": "certificate-uid",
		},
	}}
	request := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "cert-manager.io/v1",
		"kind":       "CertificateRequest",
		"metadata": map[string]any{
			"name": "ingress-1", "namespace": certificate.GetNamespace(), "uid": "request-uid",
		},
		"spec": map[string]any{"request": "unchanged-csr"},
	}}
	order := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "acme.cert-manager.io/v1",
		"kind":       "Order",
		"metadata": map[string]any{
			"name":            "ingress-1-order",
			"namespace":       certificate.GetNamespace(),
			"uid":             "order-uid",
			"resourceVersion": "42",
			"labels":          map[string]any{"preserve": "label"},
			"finalizers":      []any{"preserve-finalizer"},
		},
		"spec": map[string]any{
			"dnsNames": []any{"apps.example.com"},
			"request":  "unchanged-csr",
		},
		"status": map[string]any{
			"state":          "processing",
			"url":            "https://acme.example/order/1",
			"authorizations": []any{map[string]any{"url": "https://acme.example/auth/1"}},
		},
	}}
	challenge := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "acme.cert-manager.io/v1",
		"kind":       "Challenge",
		"metadata": map[string]any{
			"name": "ingress-1-challenge", "namespace": certificate.GetNamespace(), "uid": "challenge-uid",
		},
		"spec":   map[string]any{"token": "unchanged-token"},
		"status": map[string]any{"state": "valid"},
	}}
	controller := true
	for _, pair := range [][2]*unstructured.Unstructured{{request, certificate}, {order, request}, {challenge, order}} {
		pair[0].SetOwnerReferences([]metav1.OwnerReference{{APIVersion: pair[1].GetAPIVersion(), Kind: pair[1].GetKind(), Name: pair[1].GetName(), UID: pair[1].GetUID(), Controller: &controller}})
	}
	return certificate, []runtime.Object{request, order, challenge}
}

// Registers all list kinds, including resources that a safety scenario leaves empty.
func ingressOrderRequeueTestClient(objects ...runtime.Object) *dynamicfake.FakeDynamicClient {
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{
		ingressOrderRequeueTestResource("CertificateRequest"): "CertificateRequestList",
		ingressOrderRequeueTestResource("Order"):              "OrderList",
		ingressOrderRequeueTestResource("Challenge"):          "ChallengeList",
	}, objects...)
}

// Maps the three fixture kinds to their real API resources.
func ingressOrderRequeueTestResource(kind string) schema.GroupVersionResource {
	if kind == "CertificateRequest" {
		return schema.GroupVersionResource{Group: "cert-manager.io", Version: "v1", Resource: "certificaterequests"}
	}
	resource := "orders"
	if kind == "Challenge" {
		resource = "challenges"
	}
	return schema.GroupVersionResource{Group: "acme.cert-manager.io", Version: "v1", Resource: resource}
}

// Asserts that a safety rejection cannot mutate even a temporary annotation.
func ingressOrderRequeueRequireNoPatches(t *testing.T, client *dynamicfake.FakeDynamicClient) {
	t.Helper()
	for _, action := range client.Actions() {
		require.Equal(t, "list", action.GetVerb(), "a rejected Order must not trigger any mutation")
	}
}

// Preserves the fake tracker while observing contexts, which fake actions omit.
type ingressOrderRequeueContextClient struct {
	dynamic.Interface
	inspect func(context.Context, string) error
}

type ingressOrderRequeueContextNamespaceable struct {
	dynamic.NamespaceableResourceInterface
	resource string
	inspect  func(context.Context, string) error
}

type ingressOrderRequeueContextResource struct {
	dynamic.ResourceInterface
	resource string
	inspect  func(context.Context, string) error
}

// Wraps resource selection without changing the fake's operation behavior.
func (c *ingressOrderRequeueContextClient) Resource(resource schema.GroupVersionResource) dynamic.NamespaceableResourceInterface {
	return &ingressOrderRequeueContextNamespaceable{NamespaceableResourceInterface: c.Interface.Resource(resource), resource: resource.Resource, inspect: c.inspect}
}

// Wraps the namespaced client so its API calls expose their original context.
func (c *ingressOrderRequeueContextNamespaceable) Namespace(namespace string) dynamic.ResourceInterface {
	return &ingressOrderRequeueContextResource{ResourceInterface: c.NamespaceableResourceInterface.Namespace(namespace), resource: c.resource, inspect: c.inspect}
}

// Observes list cancellation before allowing the fake to serve a response.
func (c *ingressOrderRequeueContextResource) List(ctx context.Context, options metav1.ListOptions) (*unstructured.UnstructuredList, error) {
	if err := c.inspect(ctx, "list-"+c.resource); err != nil {
		return nil, err
	}
	return c.ResourceInterface.List(ctx, options)
}

// Observes patch cancellation before allowing the fake to apply any mutation.
func (c *ingressOrderRequeueContextResource) Patch(ctx context.Context, name string, patchType types.PatchType, data []byte, options metav1.PatchOptions, subresources ...string) (*unstructured.Unstructured, error) {
	if err := c.inspect(ctx, "patch-"+c.resource); err != nil {
		return nil, err
	}
	return c.ResourceInterface.Patch(ctx, name, patchType, data, options, subresources...)
}
