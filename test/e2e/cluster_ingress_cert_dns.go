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
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/utils/ptr"

	configv1 "github.com/openshift/api/config/v1"
	configv1client "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"
)

// The sample-app fixture uses the same fixed image, which includes dig and a shell.
const ingressCertificateDNSProbeImage = "registry.k8s.io/e2e-test-images/agnhost:2.39"

// A successful A answer proves resolution, rather than merely a successful DNS exchange.
const ingressCertificateDNSProbeScript = `echo "Resolving $1 using guest cluster DNS"
until dig +time=2 +tries=1 +search +noall +answer "$1" A | grep -qE '[[:space:]]IN[[:space:]]+A[[:space:]]'; do
  echo "Waiting for a DNS address answer for $1"
  sleep 2
done
echo "Resolved $1 using guest cluster DNS"`

// Waits for guest DNS endpoints and a pod-side lookup before ACME registration.
func waitForIngressCertificateDNS(ctx context.Context, kubeClient kubernetes.Interface, configClient configv1client.ConfigV1Interface, namespace, hostname string, timeout, interval time.Duration) (result error) {
	readinessCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var lastObservation error
	err := wait.PollUntilContextCancel(readinessCtx, interval, true, func(ctx context.Context) (bool, error) {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		lastObservation = checkIngressCertificateDNS(ctx, kubeClient, configClient)
		return lastObservation == nil, nil
	})
	if err != nil {
		return fmt.Errorf("guest DNS prerequisite for %s: %w", hostname, errors.Join(err, lastObservation))
	}

	deadline, _ := readinessCtx.Deadline()
	seconds := int64(time.Until(deadline).Seconds()) + 1
	probe, err := kubeClient.CoreV1().Pods(namespace).Create(readinessCtx, &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{GenerateName: "ingress-cert-dns-"},
		Spec: corev1.PodSpec{
			AutomountServiceAccountToken: ptr.To(false),
			DNSPolicy:                    corev1.DNSClusterFirst,
			RestartPolicy:                corev1.RestartPolicyNever,
			ActiveDeadlineSeconds:        &seconds,
			SecurityContext: &corev1.PodSecurityContext{
				RunAsNonRoot:   ptr.To(true),
				SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
			},
			Containers: []corev1.Container{{
				Name:    "dns-lookup",
				Image:   ingressCertificateDNSProbeImage,
				Command: []string{"/bin/sh", "-ec"},
				Args:    []string{ingressCertificateDNSProbeScript, "dns-lookup", hostname},
				SecurityContext: &corev1.SecurityContext{
					AllowPrivilegeEscalation: ptr.To(false),
					ReadOnlyRootFilesystem:   ptr.To(true),
					Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
				},
			}},
		},
	}, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("creating guest DNS probe for %s: %w", hostname, err)
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		if err := kubeClient.CoreV1().Pods(namespace).Delete(cleanupCtx, probe.Name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			result = errors.Join(result, fmt.Errorf("deleting guest DNS probe %s/%s: %w", namespace, probe.Name, err))
		}
	}()

	lastObservation = fmt.Errorf("guest DNS probe %s/%s has not resolved %s", namespace, probe.Name, hostname)
	err = wait.PollUntilContextCancel(readinessCtx, interval, true, func(ctx context.Context) (bool, error) {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		pod, err := kubeClient.CoreV1().Pods(namespace).Get(ctx, probe.Name, metav1.GetOptions{})
		if err != nil {
			lastObservation = fmt.Errorf("reading guest DNS probe %s/%s: %w", namespace, probe.Name, err)
			return false, nil
		}
		switch pod.Status.Phase {
		case corev1.PodSucceeded:
			return true, nil
		case corev1.PodFailed:
			return false, fmt.Errorf("guest DNS probe %s/%s failed resolving %s: %s", namespace, pod.Name, hostname, ingressCertificateDNSProbeStatus(pod))
		default:
			lastObservation = fmt.Errorf("guest DNS probe %s/%s has not resolved %s: %s", namespace, pod.Name, hostname, ingressCertificateDNSProbeStatus(pod))
			return false, nil
		}
	})
	if err != nil {
		return fmt.Errorf("guest DNS prerequisite for %s: %w", hostname, errors.Join(err, lastObservation))
	}
	return nil
}

// Checks only the operator and UDP endpoints needed by the guest DNS lookup.
func checkIngressCertificateDNS(ctx context.Context, kubeClient kubernetes.Interface, configClient configv1client.ConfigV1Interface) error {
	operator, err := configClient.ClusterOperators().Get(ctx, "dns", metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("reading DNS ClusterOperator: %w", err)
	}
	available := false
	for _, condition := range operator.Status.Conditions {
		if condition.Type == configv1.OperatorDegraded && condition.Status == configv1.ConditionTrue {
			return fmt.Errorf("DNS ClusterOperator is degraded: %s: %s", condition.Reason, condition.Message)
		}
		if condition.Type == configv1.OperatorAvailable && condition.Status == configv1.ConditionTrue {
			available = true
		}
	}
	if !available {
		return fmt.Errorf("DNS ClusterOperator is not Available: %v", operator.Status.Conditions)
	}

	slices, err := kubeClient.DiscoveryV1().EndpointSlices("openshift-dns").List(ctx, metav1.ListOptions{LabelSelector: "kubernetes.io/service-name=dns-default"})
	if err != nil {
		return fmt.Errorf("reading guest DNS EndpointSlices: %w", err)
	}
	for _, slice := range slices.Items {
		hasDNSPort := false
		for _, port := range slice.Ports {
			if port.Name != nil && *port.Name == "dns" && port.Protocol != nil && *port.Protocol == corev1.ProtocolUDP && port.Port != nil && *port.Port > 0 {
				hasDNSPort = true
				break
			}
		}
		if !hasDNSPort {
			continue
		}
		for _, endpoint := range slice.Endpoints {
			if len(endpoint.Addresses) > 0 && ptr.Deref(endpoint.Conditions.Ready, false) && !ptr.Deref(endpoint.Conditions.Terminating, false) {
				return nil
			}
		}
	}
	return errors.New("guest DNS service openshift-dns/dns-default has no ready UDP endpoints")
}

// Summarizes probe progress without including environment variables or token projections.
func ingressCertificateDNSProbeStatus(pod *corev1.Pod) string {
	status := fmt.Sprintf("phase=%s reason=%s message=%s", pod.Status.Phase, pod.Status.Reason, pod.Status.Message)
	for _, container := range pod.Status.ContainerStatuses {
		if container.State.Waiting != nil {
			status += fmt.Sprintf("; %s waiting: %s: %s", container.Name, container.State.Waiting.Reason, container.State.Waiting.Message)
		}
		if container.State.Terminated != nil {
			status += fmt.Sprintf("; %s exited %d: %s: %s", container.Name, container.State.Terminated.ExitCode, container.State.Terminated.Reason, container.State.Terminated.Message)
		}
	}
	return status
}
