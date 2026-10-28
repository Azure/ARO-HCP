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
	"fmt"
	"time"

	"github.com/onsi/ginkgo/v2"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
)

// Store authentication and initial synchronization have their own readiness
// budgets: ESO backs off errors independently of ExternalSecret.refreshInterval.
func waitForExternalSecretsReady(ctx context.Context, kubeClient kubernetes.Interface, dyn dynamic.Interface, gvr schema.GroupVersionResource, namespace, name string, timeout, interval time.Duration) error {
	if timeout <= 0 || interval <= 0 {
		return fmt.Errorf("External Secrets readiness timeout and interval must be positive")
	}
	var lastState string
	err := wait.PollUntilContextTimeout(ctx, interval, timeout, true, func(ctx context.Context) (bool, error) {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		ready, state := externalSecretsReadyState(ctx, kubeClient, dyn, gvr, namespace, name)
		if !ready && state != lastState {
			ginkgo.GinkgoLogr.Info("waiting for External Secrets readiness", "resource", gvr.Resource, "namespace", namespace, "name", name, "state", state)
		}
		lastState = state
		return ready, nil
	})
	if err != nil {
		return fmt.Errorf("waiting for %s %s/%s Ready=True: %w; last observation: %s", gvr.Resource, namespace, name, err, lastState)
	}
	return nil
}

// Conditions can be generic (e.g. "unable to create client"); the corresponding
// Warning event carries the Azure authentication, authorization, or template error.
// Read no Secret contents or resource specs when reporting synchronization state.
func externalSecretsReadyState(ctx context.Context, kubeClient kubernetes.Interface, dyn dynamic.Interface, gvr schema.GroupVersionResource, namespace, name string) (bool, string) {
	object, err := dyn.Resource(gvr).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return false, fmt.Sprintf("reading resource: %v", err)
	}
	ready, state := isResourceReady(object)
	if ready {
		return true, "Ready=True"
	}
	events, err := kubeClient.CoreV1().Events(namespace).List(ctx, metav1.ListOptions{
		FieldSelector: fmt.Sprintf("involvedObject.kind=%s,involvedObject.name=%s,type=Warning", object.GetKind(), name),
	})
	if err != nil {
		return false, fmt.Sprintf("%s; reading Warning events: %v", state, err)
	}
	var latest *corev1.Event
	for i := range events.Items {
		event := &events.Items[i]
		if event.Type != corev1.EventTypeWarning || event.InvolvedObject.Name != name || event.InvolvedObject.Kind != object.GetKind() || event.InvolvedObject.Namespace != namespace || event.InvolvedObject.UID != object.GetUID() {
			continue
		}
		if latest == nil || event.LastTimestamp.After(latest.LastTimestamp.Time) || event.EventTime.After(latest.EventTime.Time) {
			latest = event
		}
	}
	if latest != nil {
		state = fmt.Sprintf("%s; latest Warning: %s: %s", state, latest.Reason, latest.Message)
	}
	return false, state
}
