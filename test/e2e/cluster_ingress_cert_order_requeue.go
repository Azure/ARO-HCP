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
	"encoding/json"
	"fmt"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
)

const ingressCertificateOrderRequeueAnnotation = "e2e.aro-hcp.microsoft.com/acme-order-requeue"
const ingressCertificateOrderRequeueInterval = 30 * time.Second

// Holds the success-only throttle for serial calls from the certificate readiness poll.
type ingressCertificateOrderRequeuer struct {
	client      dynamic.Interface
	lastRequeue map[types.UID]time.Time
}

// Creates an Order requeuer scoped by the Certificate supplied on each poll.
func newIngressCertificateOrderRequeuer(client dynamic.Interface) *ingressCertificateOrderRequeuer {
	return &ingressCertificateOrderRequeuer{client: client, lastRequeue: make(map[types.UID]time.Time)}
}

// Requeues only processing Orders in the Certificate's controller-owner UID chain
// whose owned Challenges are all valid. cert-manager v1.20.2 can stop polling these
// Orders; changing an annotation emits an update event that wakes its controller
// without altering ACME state. Remove this test-only mitigation once the pinned
// cert-manager includes https://github.com/cert-manager/cert-manager/pull/9463.
func (r *ingressCertificateOrderRequeuer) requeue(ctx context.Context, certificate *unstructured.Unstructured, now time.Time) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if certificate == nil || certificate.GetUID() == "" || certificate.GetNamespace() == "" {
		return nil, nil
	}
	namespace := certificate.GetNamespace()
	requestsResource := schema.GroupVersionResource{Group: "cert-manager.io", Version: "v1", Resource: "certificaterequests"}
	ordersResource := schema.GroupVersionResource{Group: "acme.cert-manager.io", Version: "v1", Resource: "orders"}
	challengesResource := schema.GroupVersionResource{Group: "acme.cert-manager.io", Version: "v1", Resource: "challenges"}
	requests, err := r.client.Resource(requestsResource).Namespace(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list CertificateRequests for certificate %s: %w", certificate.GetName(), err)
	}
	requestUIDs := make(map[types.UID]bool)
	for _, request := range requests.Items {
		if request.GetNamespace() == namespace && request.GetUID() != "" && ingressCertificateControllerUID(&request, "cert-manager.io/v1", "Certificate") == certificate.GetUID() {
			requestUIDs[request.GetUID()] = true
		}
	}
	if len(requestUIDs) == 0 {
		return nil, ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	orders, err := r.client.Resource(ordersResource).Namespace(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list Orders for certificate %s: %w", certificate.GetName(), err)
	}
	var candidates []unstructured.Unstructured
	for _, order := range orders.Items {
		if order.GetNamespace() != namespace || order.GetUID() == "" || order.GetResourceVersion() == "" || !requestUIDs[ingressCertificateControllerUID(&order, "cert-manager.io/v1", "CertificateRequest")] {
			continue
		}
		state, _, stateErr := unstructured.NestedString(order.Object, "status", "state")
		if stateErr != nil {
			return nil, fmt.Errorf("read Order %s state: %w", order.GetName(), stateErr)
		}
		last, requeued := r.lastRequeue[order.GetUID()]
		if state == "processing" && (!requeued || now.Sub(last) >= ingressCertificateOrderRequeueInterval) {
			candidates = append(candidates, order)
		}
	}
	if len(candidates) == 0 {
		return nil, ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	challenges, err := r.client.Resource(challengesResource).Namespace(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list Challenges for certificate %s: %w", certificate.GetName(), err)
	}
	allValid := make(map[types.UID]bool)
	candidateUIDs := make(map[types.UID]bool, len(candidates))
	for _, order := range candidates {
		candidateUIDs[order.GetUID()] = true
	}
	for _, challenge := range challenges.Items {
		ownerUID := ingressCertificateControllerUID(&challenge, "acme.cert-manager.io/v1", "Order")
		if challenge.GetNamespace() != namespace || !candidateUIDs[ownerUID] {
			continue
		}
		state, _, stateErr := unstructured.NestedString(challenge.Object, "status", "state")
		if stateErr != nil {
			return nil, fmt.Errorf("read Challenge %s state: %w", challenge.GetName(), stateErr)
		}
		// No entry means no owned Challenges were observed. Once any Challenge
		// is non-valid, later valid Challenges cannot qualify this Order.
		previous, found := allValid[ownerUID]
		allValid[ownerUID] = state == "valid" && (!found || previous)
	}
	var patched []string
	for _, order := range candidates {
		if !allValid[order.GetUID()] {
			continue
		}
		if err := ctx.Err(); err != nil {
			return patched, err
		}
		if err := r.patchRequeueAnnotation(ctx, &order, now); err != nil {
			return patched, err
		}
		r.lastRequeue[order.GetUID()] = now
		patched = append(patched, order.GetName())
	}
	return patched, ctx.Err()
}

// Changes only the wake annotation, rejecting replaced or concurrently updated Orders.
func (r *ingressCertificateOrderRequeuer) patchRequeueAnnotation(ctx context.Context, order *unstructured.Unstructured, now time.Time) error {
	value := now.UTC().Format(time.RFC3339Nano)
	annotations := order.GetAnnotations()
	if annotations[ingressCertificateOrderRequeueAnnotation] == value {
		value = now.Add(time.Nanosecond).UTC().Format(time.RFC3339Nano)
	}
	path := "/metadata/annotations/" + strings.ReplaceAll(ingressCertificateOrderRequeueAnnotation, "/", "~1")
	var annotationValue any = value
	if annotations == nil {
		path = "/metadata/annotations"
		annotationValue = map[string]string{ingressCertificateOrderRequeueAnnotation: value}
	}
	patch, err := json.Marshal([]map[string]any{
		{"op": "test", "path": "/metadata/uid", "value": order.GetUID()},
		{"op": "test", "path": "/metadata/resourceVersion", "value": order.GetResourceVersion()},
		{"op": "add", "path": path, "value": annotationValue},
	})
	if err != nil {
		return fmt.Errorf("encode Order %s requeue patch: %w", order.GetName(), err)
	}
	resource := schema.GroupVersionResource{Group: "acme.cert-manager.io", Version: "v1", Resource: "orders"}
	if _, err := r.client.Resource(resource).Namespace(order.GetNamespace()).Patch(ctx, order.GetName(), types.JSONPatchType, patch, metav1.PatchOptions{}); err != nil {
		return fmt.Errorf("requeue Order %s: %w", order.GetName(), err)
	}
	return nil
}

// Returns only a nonempty UID from a controller owner of the expected API and kind.
func ingressCertificateControllerUID(object *unstructured.Unstructured, apiVersion, kind string) types.UID {
	owner := metav1.GetControllerOf(object)
	if owner == nil || owner.APIVersion != apiVersion || owner.Kind != kind {
		return ""
	}
	return owner.UID
}
