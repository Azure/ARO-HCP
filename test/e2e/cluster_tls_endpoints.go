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
	"embed"
	"fmt"
	"net"
	"net/url"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
)

// Based on our OneCert configuration, the PKIs we need in this directory come from
// https://eng.ms/docs/products/onecert-certificates-key-vault-and-dsms/key-vault-dsms/reference/ca-details
//
//go:embed azure-cas/*.crt
var azureCAs embed.FS

var _ = Describe("Customer", func() {
	BeforeEach(func() {
		// do nothing.  per test initialization usually ages better than shared.
	})

	It("should create an HCP cluster and validate TLS certificates",
		labels.RequireNothing,
		labels.Critical,
		labels.Positive,
		func(ctx context.Context) {

			const (
				customerClusterName  = "tls-endpoint-hcp-cluster"
				customerNodePoolName = "np-1"
			)

			tc := framework.NewTestContext()

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, 60*time.Second)
				Expect(err).NotTo(HaveOccurred(), "failed to assign pooled identity containers")
			}

			// Load CAs early to fail fast if there's an issue with the test setup, rather than waiting until after cluster creation
			trustedCAs, err := loadAzureCAs("azure-cas")
			Expect(err).NotTo(HaveOccurred(), "loading trusted Azure CAs from embedded directory")

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "tls-endpoint-cluster", tc.Location())
			Expect(err).NotTo(HaveOccurred(), "failed to create resource group for TLS endpoint test")

			// creating cluster parameters
			clusterParams := framework.NewDefaultClusterParams()
			clusterParams.ClusterName = customerClusterName
			managedResourceGroupName := framework.SuffixName(*resourceGroup.Name, "-managed", 64)
			clusterParams.ManagedResourceGroupName = managedResourceGroupName

			By("creating customer resources (infrastructure and managed identities) for cluster")
			clusterParams, err = tc.CreateClusterCustomerResources(ctx,
				resourceGroup,
				clusterParams,
				map[string]interface{}{},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create customer resources for TLS endpoint cluster")

			By("creating a standard hcp cluster")
			err = tc.CreateHCPClusterFromParam(ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
				45*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create HCP cluster for TLS endpoint test")

			By("ensuring the API TLS certificate is signed by a trusted Azure CA")
			clusterClient := tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient()
			Eventually(func(ctx context.Context) error {
				clusterResp, err := clusterClient.Get(ctx, *resourceGroup.Name, customerClusterName, nil)
				if err != nil {
					return fmt.Errorf("failed to get cluster: %w", err)
				}

				if clusterResp.Properties == nil || clusterResp.Properties.API == nil || clusterResp.Properties.API.URL == nil {
					return fmt.Errorf("cluster API URL not yet available")
				}

				apiServerURL := clusterResp.Properties.API.URL
				actualAPICerts, err := tlsCertsFromURL(ctx, *apiServerURL)
				if err != nil {
					return fmt.Errorf("failed to fetch TLS certificate from %s: %w", *apiServerURL, err)
				}

				err = verifyCertChain(actualAPICerts, trustedCAs)
				if err != nil {
					return fmt.Errorf("certificate verification failed for %s (issuer: %v): %w",
						*apiServerURL, actualAPICerts[0].Issuer, err)
				}
				GinkgoLogr.Info("API certificate issuer", "issuer", actualAPICerts[0].Issuer)
				return nil
			}).WithContext(ctx).WithTimeout(10*time.Minute).WithPolling(10*time.Second).Should(Succeed(),
				"expect API certificate to be signed by a trusted Azure CA")

			By("creating the node pool")
			nodePoolParams := framework.NewDefaultNodePoolParams()
			nodePoolParams.ClusterName = customerClusterName
			nodePoolParams.NodePoolName = customerNodePoolName

			err = tc.CreateNodePoolFromParam(ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				managedResourceGroupName,
				customerClusterName,
				nodePoolParams,
				45*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create node pool %s for TLS endpoint cluster", customerNodePoolName)

			By("ensuring the ingress TLS certificate is signed by a trusted Azure CA")
			hcpOpenShiftClustersClient := tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient()

			By("waiting for the console URL to become available")
			var consoleURL string
			Eventually(func() bool {
				resp, err := hcpOpenShiftClustersClient.Get(ctx, *resourceGroup.Name, customerClusterName, nil)
				if err != nil || resp.Properties == nil || resp.Properties.Console == nil || resp.Properties.Console.URL == nil {
					return false
				}
				Expect(resp.Properties.Console.URL).NotTo(BeNil(), "cluster Properties.Console.URL was nil")
				consoleURL = *resp.Properties.Console.URL
				GinkgoLogr.Info("Console URL found", "url", consoleURL)
				return true
			}).WithTimeout(15 * time.Minute).WithPolling(10 * time.Second).Should(BeTrue())

			By("examining the server certificate returned by the default ingress when routing the console URL")
			// Wait for the certificate to be loaded after console starts
			consoleUrlWithPort := fmt.Sprintf("%s:%d", consoleURL, 443)

			Eventually(func() error {
				certs, err := tlsCertsFromURL(ctx, consoleUrlWithPort)
				if err != nil {
					GinkgoLogr.Info("Ingress certificate check", "status", "failed", "error", err.Error())
					return err
				}
				GinkgoLogr.Info("Ingress certificate issuer", "issuer", certs[0].Issuer.String())
				return verifyCertChain(certs, trustedCAs)
			}).WithTimeout(10*time.Minute).WithPolling(10*time.Second).Should(Succeed(),
				"expect ingress certificate to be signed by a trusted Azure CA")
		})
})

func tlsCertsFromURL(ctx context.Context, u string) ([]*x509.Certificate, error) {
	parsedURL, err := url.Parse(u)
	if err != nil {
		return nil, err
	}
	dialer := &tls.Dialer{
		NetDialer: &net.Dialer{Timeout: 10 * time.Second},
		Config:    &tls.Config{InsecureSkipVerify: true},
	}
	conn, err := dialer.DialContext(ctx, "tcp", parsedURL.Host)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	state := conn.(*tls.Conn).ConnectionState()
	if len(state.PeerCertificates) == 0 {
		return nil, fmt.Errorf("no certificates served")
	}
	return state.PeerCertificates, nil
}

func loadAzureCAs(directory string) (*x509.CertPool, error) {
	pool := x509.NewCertPool()
	entries, err := azureCAs.ReadDir(directory)
	if err != nil {
		return nil, fmt.Errorf("reading embedded %s directory: %w", directory, err)
	}
	for _, entry := range entries {
		data, err := azureCAs.ReadFile(directory + "/" + entry.Name())
		if err != nil {
			return nil, fmt.Errorf("reading embedded CA %s: %w", entry.Name(), err)
		}
		cert, err := x509.ParseCertificate(data)
		if err != nil {
			return nil, fmt.Errorf("parsing CA certificate %s: %w", entry.Name(), err)
		}
		pool.AddCert(cert)
	}
	return pool, nil
}

func verifyCertChain(certs []*x509.Certificate, roots *x509.CertPool) error {
	if len(certs) == 0 {
		return fmt.Errorf("no certificates provided for verification")
	}

	intermediates := x509.NewCertPool()
	for _, c := range certs[1:] {
		intermediates.AddCert(c)
	}
	_, err := certs[0].Verify(x509.VerifyOptions{
		Roots:         roots,
		Intermediates: intermediates,
	})
	return err
}
