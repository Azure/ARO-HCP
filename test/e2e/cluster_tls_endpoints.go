// Copyright 2025 Microsoft Corporation
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
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
)

var _ = Describe("Customer", func() {
	BeforeEach(func() {
		// do nothing.  per test initialization usually ages better than shared.
	})

	It("should create an HCP cluster and validate TLS certificates", labels.RequireNothing, labels.Critical, labels.Positive, func(ctx context.Context) {

		const (
			customerNetworkSecurityGroupName = "customer-nsg-name"
			customerVnetName                 = "customer-vnet-name"
			customerVnetSubnetName           = "customer-vnet-subnet1"
			customerClusterName              = "tls-endpoint-hcp-cluster"
			customerNodePoolName             = "np-1"
			openshiftControlPlaneVersionId   = "4.19"
			openshiftNodeVersionId           = "4.19.7"
		)
		tc := framework.NewTestContext()

		By("creating a resource group")
		resourceGroup, err := tc.NewResourceGroup(ctx, "tls-endpoint-cluster", tc.Location())
		Expect(err).NotTo(HaveOccurred())

		By("creating a customer-infra")
		customerInfraDeploymentResult, err := framework.CreateBicepTemplateAndWait(ctx,
			tc.GetARMResourcesClientFactoryOrDie(ctx).NewDeploymentsClient(),
			*resourceGroup.Name,
			"customer-infra",
			framework.Must(TestArtifactsFS.ReadFile("test-artifacts/generated-test-artifacts/modules/customer-infra.json")),
			map[string]interface{}{
				"persistTagValue":        false,
				"customerNsgName":        customerNetworkSecurityGroupName,
				"customerVnetName":       customerVnetName,
				"customerVnetSubnetName": customerVnetSubnetName,
			},
			45*time.Minute,
		)
		Expect(err).NotTo(HaveOccurred())

		By("creating a managed identities")
		keyVaultName, err := framework.GetOutputValue(customerInfraDeploymentResult, "keyVaultName")
		Expect(err).NotTo(HaveOccurred())

		managedIdentityDeploymentResult, err := framework.CreateBicepTemplateAndWait(ctx,
			tc.GetARMResourcesClientFactoryOrDie(ctx).NewDeploymentsClient(),
			*resourceGroup.Name,
			"managed-identities",
			framework.Must(TestArtifactsFS.ReadFile("test-artifacts/generated-test-artifacts/modules/managed-identities.json")),
			map[string]interface{}{
				"clusterName":  customerClusterName,
				"nsgName":      customerNetworkSecurityGroupName,
				"vnetName":     customerVnetName,
				"subnetName":   customerVnetSubnetName,
				"keyVaultName": keyVaultName,
			},
			45*time.Minute,
		)
		Expect(err).NotTo(HaveOccurred())

		By("creating a standard hcp cluster")
		userAssignedIdentities, err := framework.GetOutputValue(managedIdentityDeploymentResult, "userAssignedIdentitiesValue")
		Expect(err).NotTo(HaveOccurred())
		identity, err := framework.GetOutputValue(managedIdentityDeploymentResult, "identityValue")
		Expect(err).NotTo(HaveOccurred())
		etcdEncryptionKeyName, err := framework.GetOutputValue(customerInfraDeploymentResult, "etcdEncryptionKeyName")
		Expect(err).NotTo(HaveOccurred())
		managedResourceGroupName := framework.SuffixName(*resourceGroup.Name, "managed", 64)
		_, err = framework.CreateBicepTemplateAndWait(ctx,
			tc.GetARMResourcesClientFactoryOrDie(ctx).NewDeploymentsClient(),
			*resourceGroup.Name,
			"hcp-cluster",
			framework.Must(TestArtifactsFS.ReadFile("test-artifacts/generated-test-artifacts/modules/cluster.json")),
			map[string]interface{}{
				"openshiftVersionId":          openshiftControlPlaneVersionId,
				"clusterName":                 customerClusterName,
				"managedResourceGroupName":    managedResourceGroupName,
				"nsgName":                     customerNetworkSecurityGroupName,
				"subnetName":                  customerVnetSubnetName,
				"vnetName":                    customerVnetName,
				"userAssignedIdentitiesValue": userAssignedIdentities,
				"identityValue":               identity,
				"keyVaultName":                keyVaultName,
				"etcdEncryptionKeyName":       etcdEncryptionKeyName,
			},
			45*time.Minute,
		)
		Expect(err).NotTo(HaveOccurred())

		By("ensuring the API TLS certificate issued is not an OpenShift root CA")
		clusterResp, err := tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient().Get(ctx, *resourceGroup.Name, customerClusterName, nil)
		Expect(err).NotTo(HaveOccurred())

		Expect(clusterResp.Properties).NotTo(BeNil())
		Expect(clusterResp.Properties.API).NotTo(BeNil())
		Expect(clusterResp.Properties.API.URL).NotTo(BeNil())

		apiServerURL := clusterResp.Properties.API.URL
		actualAPICert, err := tlsCertFromURL(ctx, *apiServerURL)
		Expect(err).NotTo(HaveOccurred())

		fmt.Fprintf(GinkgoWriter, "Issuer: %v\n", actualAPICert.Issuer)
		Expect(actualAPICert.Issuer).To(SatisfyAll(
			HaveField("CommonName", ContainElements("Microsoft Azure RSA TLS Issuing CA")),
			HaveField("Organization", ContainElements("Microsoft Corporation")),
		), "expect certificate to be issued by Microsoft")

		By("creating the node pool")
		_, err = framework.CreateBicepTemplateAndWait(ctx,
			tc.GetARMResourcesClientFactoryOrDie(ctx).NewDeploymentsClient(),
			*resourceGroup.Name,
			"node-pool",
			framework.Must(TestArtifactsFS.ReadFile("test-artifacts/generated-test-artifacts/modules/nodepool.json")),
			map[string]interface{}{
				"openshiftVersionId": openshiftNodeVersionId,
				"clusterName":        customerClusterName,
				"nodePoolName":       customerNodePoolName,
				"replicas":           2,
			},
			45*time.Minute,
		)
		Expect(err).NotTo(HaveOccurred())

		By("ensuring the ingress TLS certificate issued by an OpenShift root CA")
		hcpOpenShiftClustersClient := tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient()

		By("waiting for the console URL to become available")
		var consoleURL string
		Eventually(func() bool {
			resp, err := hcpOpenShiftClustersClient.Get(ctx, *resourceGroup.Name, customerClusterName, nil)
			if err != nil || resp.Properties == nil || resp.Properties.Console == nil || resp.Properties.Console.URL == nil {
				return false
			}
			consoleURL = *resp.Properties.Console.URL
			fmt.Fprintln(GinkgoWriter, "Ingress URL found:", consoleURL)
			return true
		}).WithTimeout(15 * time.Minute).WithPolling(10 * time.Second).Should(BeTrue())

		By("examining the server certificate returned by the default ingress when routing the console URL")
		// Wait for the certificate to be loaded after console starts
		sslPort := 443
		consoleUrlWithPort := fmt.Sprintf("%s:%s", consoleURL, strconv.Itoa(sslPort))

		Eventually(func() (*x509.Certificate, error) {
			actualCert, err := tlsCertFromURL(ctx, consoleUrlWithPort)
			if err != nil {
				fmt.Fprintf(GinkgoWriter, "error fetching cert: %v\n", err)
				return nil, err
			}
			fmt.Fprintf(GinkgoWriter, "Issuer OU: %v", actualCert.Issuer.OrganizationalUnit)
			return actualCert, nil
		}).WithTimeout(2*time.Minute).WithPolling(10*time.Second).To(SatisfyAll(
			HaveField("CommonName", ContainElements("Microsoft Azure RSA TLS Issuing CA")),
			HaveField("Organization", ContainElements("Microsoft Corporation")),
		), "expect certificate to be issued by Microsoft")
	})
})

func tlsCertFromURL(ctx context.Context, u string) (*x509.Certificate, error) {
	url, err := url.Parse(u)
	if err != nil {
		return nil, err
	}
	dialer := &tls.Dialer{
		NetDialer: &net.Dialer{Timeout: 10 * time.Second},
		Config:    &tls.Config{InsecureSkipVerify: true},
	}
	conn, err := dialer.DialContext(ctx, "tcp", url.Host)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	state := conn.(*tls.Conn).ConnectionState()
	if len(state.PeerCertificates) > 0 {
		return state.PeerCertificates[0], nil
	}
	return nil, fmt.Errorf("no certificates served")
}
