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
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	operatorv1 "github.com/openshift/api/operator/v1"
	operatorclient "github.com/openshift/client-go/operator/clientset/versioned"
)

type verifyIngressControllerScope struct {
	expectedScope operatorv1.LoadBalancerScope
}

func (v verifyIngressControllerScope) Name() string {
	return fmt.Sprintf("VerifyIngressControllerScope(%s)", v.expectedScope)
}

func (v verifyIngressControllerScope) Verify(ctx context.Context, adminRESTConfig *rest.Config) error {
	opClient, err := operatorclient.NewForConfig(adminRESTConfig)
	if err != nil {
		return fmt.Errorf("failed to create operator client: %w", err)
	}

	ic, err := opClient.OperatorV1().IngressControllers("openshift-ingress-operator").Get(ctx, "default", metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get default IngressController: %w", err)
	}

	if ic.Spec.EndpointPublishingStrategy == nil {
		return fmt.Errorf("IngressController endpointPublishingStrategy is nil")
	}
	if ic.Spec.EndpointPublishingStrategy.LoadBalancer == nil {
		return fmt.Errorf("IngressController loadBalancer config is nil")
	}
	if ic.Spec.EndpointPublishingStrategy.LoadBalancer.Scope != v.expectedScope {
		return fmt.Errorf("IngressController loadBalancer scope is %q, expected %q",
			ic.Spec.EndpointPublishingStrategy.LoadBalancer.Scope, v.expectedScope)
	}

	return nil
}

// VerifyIngressControllerScope returns a verifier that checks the default
// IngressController's load balancer scope matches the expected value.
// Use operatorv1.InternalLoadBalancer for private ingress clusters
// or operatorv1.ExternalLoadBalancer for public ingress clusters.
func VerifyIngressControllerScope(expectedScope operatorv1.LoadBalancerScope) HostedClusterVerifier {
	return verifyIngressControllerScope{expectedScope: expectedScope}
}

type verifyHyperShiftIngressCertificate struct {
	timeout time.Duration
}

func VerifyHyperShiftIngressCertificate(timeout time.Duration) HostedClusterVerifier {
	return verifyHyperShiftIngressCertificate{timeout: timeout}
}

func (verifier verifyHyperShiftIngressCertificate) Name() string {
	return "VerifyHyperShiftIngressCertificate"
}

func (verifier verifyHyperShiftIngressCertificate) Verify(ctx context.Context, adminRESTConfig *rest.Config) error {
	kubeClient, err := kubernetes.NewForConfig(adminRESTConfig)
	if err != nil {
		return err
	}
	operatorClient, err := operatorclient.NewForConfig(adminRESTConfig)
	if err != nil {
		return err
	}
	return pollUntilReady(ctx, verifier.Name(), verifier.timeout, DefaultPollInterval, adminRESTConfig, DefaultDiagnoseTimeout, nil,
		func(ctx context.Context) error {
			return verifyHyperShiftIngressCertificateWiring(ctx, kubeClient, operatorClient)
		})
}

func verifyHyperShiftIngressCertificateWiring(ctx context.Context, kubeClient kubernetes.Interface, operatorClient operatorclient.Interface) error {
	const secretName = "default-ingress-cert"
	secret, err := kubeClient.CoreV1().Secrets("openshift-ingress").Get(ctx, secretName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("getting HyperShift ingress secret openshift-ingress/%s: %w", secretName, err)
	}
	if len(secret.Data[corev1.TLSCertKey]) == 0 || len(secret.Data[corev1.TLSPrivateKeyKey]) == 0 {
		return fmt.Errorf("HyperShift ingress secret openshift-ingress/%s must contain non-empty tls.crt and tls.key data", secretName)
	}
	controller, err := operatorClient.OperatorV1().IngressControllers("openshift-ingress-operator").Get(ctx, "default", metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("getting IngressController/default: %w", err)
	}
	if controller.Spec.DefaultCertificate == nil || controller.Spec.DefaultCertificate.Name != secretName {
		return fmt.Errorf("IngressController/default references %v, expected secret %q", controller.Spec.DefaultCertificate, secretName)
	}
	return nil
}

type verifyACMIngressCertificateAbsent struct{}

func VerifyACMIngressCertificateAbsent() HostedClusterVerifier {
	return verifyACMIngressCertificateAbsent{}
}

func (verifier verifyACMIngressCertificateAbsent) Name() string {
	return "VerifyACMIngressCertificateAbsent"
}

func (verifier verifyACMIngressCertificateAbsent) Verify(ctx context.Context, adminRESTConfig *rest.Config) error {
	kubeClient, err := kubernetes.NewForConfig(adminRESTConfig)
	if err != nil {
		return err
	}
	return verifyACMIngressSecretAbsent(ctx, kubeClient)
}

func verifyACMIngressSecretAbsent(ctx context.Context, kubeClient kubernetes.Interface) error {
	_, err := kubeClient.CoreV1().Secrets("openshift-ingress").Get(ctx, "cluster-ingress-cert", metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("checking ACM ingress secret absence: %w", err)
	}
	return fmt.Errorf("ACM ingress secret openshift-ingress/cluster-ingress-cert exists; expected ACM certificate delivery to be suppressed")
}
