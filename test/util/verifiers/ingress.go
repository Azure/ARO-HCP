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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
