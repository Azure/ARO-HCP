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

package verifiers

import (
	"context"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

const (
	natGatewayEgressImage          = "mcr.microsoft.com/azurelinux/distroless/debug:3.0"
	natGatewayEgressPodName        = "e2e-nat-gateway-egress"
	natGatewayEgressServiceAccount = "e2e-nat-gateway-egress"
	// natGatewayEgressCheckURL is a plain-HTTP (not HTTPS) "what is my IP" echo
	// endpoint. Plain HTTP is used deliberately: the minimal busybox build in
	// natGatewayEgressImage is not guaranteed to be compiled with TLS support.
	natGatewayEgressCheckURL = "http://checkip.amazonaws.com"
)

// verifyNATGatewayEgressIP runs a one-shot pod that fetches its own observed
// public IP from an external echo service and asserts it equals
// expectedPublicIP. This is the feature-level proof for UserDefinedRouting:
// the OutboundType ARM round trip and nodes reaching Ready only prove
// plumbing — this is the assertion that pod egress actually leaves through
// the customer's NAT Gateway, not an ARO-owned address.
type verifyNATGatewayEgressIP struct {
	nsGenerateName   string
	expectedPublicIP string
	timeout          time.Duration
}

func (v verifyNATGatewayEgressIP) Name() string {
	return fmt.Sprintf("VerifyNATGatewayEgressIP(%s)", v.expectedPublicIP)
}

func (v verifyNATGatewayEgressIP) Verify(ctx context.Context, adminRESTConfig *rest.Config) error {
	if v.expectedPublicIP == "" {
		return fmt.Errorf("expected NAT Gateway public IP was empty")
	}

	kubeClient, err := kubernetes.NewForConfig(adminRESTConfig)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	ns, err := kubeClient.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{GenerateName: v.nsGenerateName},
	}, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create namespace: %w", err)
	}
	defer func() {
		delCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = kubeClient.CoreV1().Namespaces().Delete(delCtx, ns.Name, metav1.DeleteOptions{})
	}()

	// A dedicated ServiceAccount is created explicitly rather than relying on
	// the namespace's auto-provisioned "default" one: that account is created
	// asynchronously by the service-account controller, so a pod referencing
	// it immediately after namespace creation can race it and fail admission
	// with "serviceaccount \"default\" not found". Creating our own is a
	// synchronous API call, so it is guaranteed to exist once this returns.
	if _, err := kubeClient.CoreV1().ServiceAccounts(ns.Name).Create(ctx, &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{Name: natGatewayEgressServiceAccount},
	}, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("failed to create service account %s/%s: %w", ns.Name, natGatewayEgressServiceAccount, err)
	}

	automountSAToken := false
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      natGatewayEgressPodName,
			Namespace: ns.Name,
		},
		Spec: corev1.PodSpec{
			RestartPolicy:                corev1.RestartPolicyNever,
			ServiceAccountName:           natGatewayEgressServiceAccount,
			AutomountServiceAccountToken: &automountSAToken,
			Containers: []corev1.Container{{
				Name:    "egress-check",
				Image:   natGatewayEgressImage,
				Command: []string{"busybox", "wget", "-qO-", natGatewayEgressCheckURL},
				Env: []corev1.EnvVar{
					{Name: "HOME", Value: "/tmp"},
				},
			}},
		},
	}
	if _, err := kubeClient.CoreV1().Pods(ns.Name).Create(ctx, pod, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("failed to create pod %s/%s: %w", ns.Name, natGatewayEgressPodName, err)
	}

	if err := pollUntilReady(
		ctx,
		v.Name(),
		v.timeout,
		DefaultPollInterval,
		adminRESTConfig,
		DefaultDiagnoseTimeout,
		func(ctx context.Context, _ *rest.Config) string {
			pod, err := kubeClient.CoreV1().Pods(ns.Name).Get(ctx, natGatewayEgressPodName, metav1.GetOptions{})
			if err != nil {
				return fmt.Sprintf("[pod] failed to get %s/%s: %v", ns.Name, natGatewayEgressPodName, err)
			}
			return strings.Join(podDiagnosticLines(pod), "\n")
		},
		func(ctx context.Context) error {
			pod, err := kubeClient.CoreV1().Pods(ns.Name).Get(ctx, natGatewayEgressPodName, metav1.GetOptions{})
			if err != nil {
				return fmt.Errorf("failed to get pod %s/%s: %w", ns.Name, natGatewayEgressPodName, err)
			}
			if pod.Status.Phase == corev1.PodFailed {
				return fmt.Errorf("pod %s/%s failed: %s", ns.Name, natGatewayEgressPodName, podStatusSummary(ctx, kubeClient, pod))
			}
			if pod.Status.Phase != corev1.PodSucceeded {
				return fmt.Errorf("pod %s/%s phase is %q, expected Succeeded", ns.Name, natGatewayEgressPodName, pod.Status.Phase)
			}
			return nil
		},
	); err != nil {
		return err
	}

	logs, err := getPodLogsWithRetry(ctx, kubeClient, ns.Name, natGatewayEgressPodName)
	if err != nil {
		return fmt.Errorf("failed to get logs for pod %s/%s after it succeeded: %w", ns.Name, natGatewayEgressPodName, err)
	}
	observedIP := strings.TrimSpace(logs)
	if observedIP != v.expectedPublicIP {
		return fmt.Errorf("pod egress source IP %q does not match NAT Gateway public IP %q: egress did not route through the customer's NAT Gateway",
			observedIP, v.expectedPublicIP)
	}
	return nil
}

// VerifyNATGatewayEgressIP creates a verifier that asserts a pod's observed
// public egress IP equals expectedPublicIP (the customer NAT Gateway's public
// IP). Use this to prove outbound pod traffic under
// outboundType=UserDefinedRouting actually leaves via the customer's NAT
// Gateway rather than any ARO-owned address.
func VerifyNATGatewayEgressIP(expectedPublicIP string, timeout time.Duration) HostedClusterVerifier {
	return verifyNATGatewayEgressIP{
		nsGenerateName:   "e2e-udr-egress-",
		expectedPublicIP: expectedPublicIP,
		timeout:          timeout,
	}
}
