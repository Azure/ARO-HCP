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

package istio

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

type CheckResult struct {
	Passed bool
	Issues []string
}

func (c *CheckResult) addIssue(format string, args ...any) {
	c.Passed = false
	c.Issues = append(c.Issues, fmt.Sprintf(format, args...))
}

func namespaceExists(ctx context.Context, client kubernetes.Interface, name string) (bool, error) {
	_, err := client.CoreV1().Namespaces().Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("failed to check namespace %s: %w", name, err)
	}
	return true, nil
}

func HealthCheck(ctx context.Context, client kubernetes.Interface) (*CheckResult, error) {
	summary := &CheckResult{Passed: true}

	for _, ns := range []string{istioSystemNamespace, istioIngressNamespace} {
		exists, err := namespaceExists(ctx, client, ns)
		if err != nil {
			return nil, err
		}
		if !exists {
			summary.addIssue("namespace %s does not exist", ns)
		}
	}
	if !summary.Passed {
		return summary, nil
	}

	cpStatus, err := GetControlPlaneStatus(ctx, client)
	if err != nil {
		return nil, fmt.Errorf("control plane check: %w", err)
	}
	if len(cpStatus) == 0 {
		summary.addIssue("no istiod deployments found")
	}
	for _, cp := range cpStatus {
		if !cp.Ready {
			summary.addIssue("istiod-%s not ready (%d/%d)", cp.Revision, cp.Available, cp.Replicas)
		}
	}

	gwStatus, err := GetIngressGatewayStatus(ctx, client)
	if err != nil {
		return nil, fmt.Errorf("ingress check: %w", err)
	}
	if len(gwStatus) == 0 {
		summary.addIssue("no ingress gateway services found")
	}
	for _, gw := range gwStatus {
		if gw.ExternalIP == "" {
			summary.addIssue("gateway %s has no external IP", gw.ServiceName)
		}
		if gw.HealthyPods == 0 {
			summary.addIssue("gateway %s has no healthy pods", gw.ServiceName)
		}
	}

	return summary, nil
}

func VerifyUpgrade(ctx context.Context, client kubernetes.Interface, targetRevision, tag string) (*CheckResult, error) {
	v := &CheckResult{Passed: true}

	cmName := revisionConfigMapName(targetRevision)
	_, err := client.CoreV1().ConfigMaps(istioSystemNamespace).Get(ctx, cmName, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			v.addIssue("ConfigMap %s not found in %s", cmName, istioSystemNamespace)
		} else {
			return nil, fmt.Errorf("failed to get ConfigMap %s: %w", cmName, err)
		}
	}

	namespaces, err := GetMeshNamespaces(ctx, client)
	if err != nil {
		return nil, fmt.Errorf("failed to list mesh namespaces: %w", err)
	}
	for _, ns := range namespaces {
		validLabel := ns.RevisionLabel == targetRevision || (tag != "" && ns.RevisionLabel == tag)
		if !validLabel {
			expected := targetRevision
			if tag != "" {
				expected = fmt.Sprintf("%s or %s", targetRevision, tag)
			}
			v.addIssue("namespace %s has label %s, expected %s", ns.Name, ns.RevisionLabel, expected)
		}
	}

	if tag != "" {
		webhookName := revisionTagWebhookName(tag)
		wh, err := client.AdmissionregistrationV1().MutatingWebhookConfigurations().Get(ctx, webhookName, metav1.GetOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) {
				v.addIssue("tag webhook %s not found", webhookName)
			} else {
				return nil, fmt.Errorf("failed to get tag webhook %s: %w", webhookName, err)
			}
		} else {
			expectedSvc := istiodServiceName(targetRevision)
			for _, w := range wh.Webhooks {
				if w.ClientConfig.Service != nil && w.ClientConfig.Service.Name != expectedSvc {
					v.addIssue("tag webhook %s points at %s, expected %s", webhookName, w.ClientConfig.Service.Name, expectedSvc)
				}
			}
		}
	}

	for _, ns := range namespaces {
		podInfos, err := listRunningPodsWithSidecar(ctx, client, ns.Name)
		if err != nil {
			return nil, err
		}
		for _, pi := range podInfos {
			if pi.Revision != targetRevision {
				v.addIssue("pod %s/%s has stale sidecar revision %s", pi.Pod.Namespace, pi.Pod.Name, pi.Revision)
			}
		}
	}

	return v, nil
}

func CheckOrphanedWorkloads(ctx context.Context, client kubernetes.Interface, targetRevision string, retiringRevisions []string) ([]string, error) {
	retiring := make(map[string]bool)
	for _, rev := range retiringRevisions {
		if rev != targetRevision {
			retiring[rev] = true
		}
	}
	if len(retiring) == 0 {
		return nil, nil
	}

	namespaces, err := GetMeshNamespaces(ctx, client)
	if err != nil {
		return nil, fmt.Errorf("failed to list mesh namespaces: %w", err)
	}

	var orphaned []string
	for _, ns := range namespaces {
		podInfos, err := listRunningPodsWithSidecar(ctx, client, ns.Name)
		if err != nil {
			return nil, err
		}
		for _, pi := range podInfos {
			if retiring[pi.Revision] {
				orphaned = append(orphaned, fmt.Sprintf("%s/%s (sidecar revision: %s)", pi.Pod.Namespace, pi.Pod.Name, pi.Revision))
			}
		}
	}
	return orphaned, nil
}

func matchesSelector(labels, selector map[string]string) bool {
	if len(selector) == 0 {
		return false
	}
	for k, v := range selector {
		if labels[k] != v {
			return false
		}
	}
	return true
}

func isPodReady(pod corev1.Pod) bool {
	for _, c := range pod.Status.Conditions {
		if c.Type == corev1.PodReady && c.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}
