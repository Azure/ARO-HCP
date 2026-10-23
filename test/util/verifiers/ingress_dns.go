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

	"github.com/onsi/ginkgo/v2"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/client-go/rest"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/dns/armdns"

	operatorv1 "github.com/openshift/api/operator/v1"
	operatorclient "github.com/openshift/client-go/operator/clientset/versioned"
)

const ingressControllerNamespace = "openshift-ingress-operator"

// verifyIngressDNSRecord creates a secondary IngressController with a unique
// domain under the cluster's delegated DNS zone and polls Azure DNS until the
// ingress operator writes the corresponding wildcard A record, exercising the
// identity's Microsoft.Network/dnsZones/A/write permission with a fresh,
// test-attributable write.
type verifyIngressDNSRecord struct {
	cred                     azcore.TokenCredential
	subscriptionID           string
	managedResourceGroupName string
	dnsZoneName              string
	timeout                  time.Duration
}

func (v verifyIngressDNSRecord) Name() string {
	return fmt.Sprintf("VerifyIngressDNSRecord(%s)", v.dnsZoneName)
}

func (v verifyIngressDNSRecord) Verify(ctx context.Context, adminRESTConfig *rest.Config) error {
	opClient, err := operatorclient.NewForConfig(adminRESTConfig)
	if err != nil {
		return fmt.Errorf("failed to create operator client: %w", err)
	}

	subdomain := "e2e-dns-" + rand.String(6)
	icName := subdomain
	domain := fmt.Sprintf("%s.%s", subdomain, v.dnsZoneName)
	wildcardRecordName := fmt.Sprintf("*.%s", subdomain)

	ginkgo.GinkgoWriter.Printf("[%s] creating secondary IngressController %q with domain %q\n", v.Name(), icName, domain)

	ic := v.buildIngressController(icName, domain, 1)
	_, err = opClient.OperatorV1().IngressControllers(ingressControllerNamespace).Create(ctx, ic, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create IngressController %q: %w", icName, err)
	}
	defer func() {
		if delErr := opClient.OperatorV1().IngressControllers(ingressControllerNamespace).Delete(context.Background(), icName, metav1.DeleteOptions{}); delErr != nil {
			ginkgo.GinkgoLogr.Error(delErr, "failed to delete IngressController", "name", icName)
		}
	}()

	recordSetsClient, err := armdns.NewRecordSetsClient(v.subscriptionID, v.cred, nil)
	if err != nil {
		return fmt.Errorf("failed to create DNS record sets client: %w", err)
	}

	pollErr := pollUntilReady(ctx, v.Name(), v.timeout, DefaultPollInterval, adminRESTConfig, DefaultDiagnoseTimeout, nil,
		func(ctx context.Context) error {
			resp, getErr := recordSetsClient.Get(ctx, v.managedResourceGroupName, v.dnsZoneName, wildcardRecordName, armdns.RecordTypeA, nil)
			if getErr != nil {
				return fmt.Errorf("DNS A record %q not yet present in zone %s/%s: %w", wildcardRecordName, v.managedResourceGroupName, v.dnsZoneName, getErr)
			}
			if resp.Properties == nil || len(resp.Properties.ARecords) == 0 {
				return fmt.Errorf("DNS A record %q in zone %s/%s exists but has no A records yet", wildcardRecordName, v.managedResourceGroupName, v.dnsZoneName)
			}

			ginkgo.GinkgoWriter.Printf("[%s] confirmed DNS A record %q in zone %s with %d IP(s)\n", v.Name(), wildcardRecordName, v.dnsZoneName, len(resp.Properties.ARecords))
			return nil
		},
	)

	return pollErr
}

// buildIngressController returns an IngressController CR for the given domain
// with a LoadBalancer publishing strategy and a RouteSelector scoped to the
// given name so it does not admit Routes from the default IngressController.
func (v verifyIngressDNSRecord) buildIngressController(name, domain string, replicas int32) *operatorv1.IngressController {
	return &operatorv1.IngressController{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ingressControllerNamespace,
		},
		Spec: operatorv1.IngressControllerSpec{
			Domain:   domain,
			Replicas: &replicas,
			EndpointPublishingStrategy: &operatorv1.EndpointPublishingStrategy{
				Type: operatorv1.LoadBalancerServiceStrategyType,
				LoadBalancer: &operatorv1.LoadBalancerStrategy{
					Scope: operatorv1.ExternalLoadBalancer,
				},
			},
			RouteSelector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"e2e-ingress-controller": name,
				},
			},
		},
	}
}

// VerifyIngressDNSRecord returns a verifier that creates a secondary
// IngressController with a unique domain and confirms, via a direct Azure DNS
// API call against dnsZoneName (in managedResourceGroupName), that the ingress
// operator identity wrote the corresponding wildcard A record.
func VerifyIngressDNSRecord(cred azcore.TokenCredential, subscriptionID, managedResourceGroupName, dnsZoneName string, timeout time.Duration) HostedClusterVerifier {
	return verifyIngressDNSRecord{
		cred:                     cred,
		subscriptionID:           subscriptionID,
		managedResourceGroupName: managedResourceGroupName,
		dnsZoneName:              dnsZoneName,
		timeout:                  timeout,
	}
}
