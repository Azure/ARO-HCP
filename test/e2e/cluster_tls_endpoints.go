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
				err := tc.AssignIdentityContainers(ctx, 1, framework.IdentityContainerAssignmentRetryInterval)
				Expect(err).NotTo(HaveOccurred(), "failed to assign pooled identity containers")
			}

			// Load CAs early to fail fast if there's an issue with the test setup, rather than waiting until after cluster creation
			trustedCAs, err := loadAzureCAs("azure-cas")
			Expect(err).NotTo(HaveOccurred(), "loading trusted Azure CAs from embedded directory")

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "tls-endpoint-cluster", tc.Location())
			Expect(err).NotTo(HaveOccurred(), "failed to create resource group for TLS endpoint test")

			// creating cluster parameters
			clusterParams := framework.NewDefaultClusterParams20240610()
			clusterParams.ClusterName = customerClusterName
			managedResourceGroupName := framework.SuffixName(*resourceGroup.Name, "-managed", 64)
			clusterParams.ManagedResourceGroupName = managedResourceGroupName

			By("creating customer resources (infrastructure and managed identities) for cluster")
			clusterParams, err = tc.CreateClusterCustomerResources20240610(ctx,
				resourceGroup,
				clusterParams,
				map[string]interface{}{},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create customer resources for TLS endpoint cluster")

			By("creating a standard hcp cluster")
			err = tc.CreateHCPClusterFromParam20240610(ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
				framework.ClusterCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create HCP cluster for TLS endpoint test")

			By("ensuring the API TLS certificate is signed by a trusted Azure CA")
			clusterClient := tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient()
			var lastAPIErr string
			defer func() {
				if lastAPIErr != "" {
					GinkgoLogr.Info("API certificate final state", "error", lastAPIErr)
				}
			}()
			Eventually(func(ctx context.Context) error {
				err := func() error {
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
				}()
				if err != nil {
					if err.Error() != lastAPIErr {
						GinkgoLogr.Info("API certificate check", "status", "failed", "error", err.Error())
						lastAPIErr = err.Error()
					}
					return err
				}
				lastAPIErr = ""
				return nil
			}).WithContext(ctx).WithTimeout(10*time.Minute).WithPolling(10*time.Second).Should(Succeed(),
				"expect API certificate to be signed by a trusted Azure CA")

			By("creating the node pool")
			nodePoolParams := framework.NewDefaultNodePoolParams20240610()
			nodePoolParams.ClusterName = customerClusterName
			nodePoolParams.NodePoolName = customerNodePoolName

			err = tc.CreateNodePoolFromParam20240610(ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				managedResourceGroupName,
				customerClusterName,
				nodePoolParams,
				framework.NodePoolCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create node pool %s for TLS endpoint cluster", customerNodePoolName)

			By("ensuring the ingress TLS certificate is signed by a trusted Azure CA")
			hcpOpenShiftClustersClient := tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient()

			By("waiting for the console URL to become available")
			var consoleURL string
			var lastConsoleErr string
			defer func() {
				if lastConsoleErr != "" {
					GinkgoLogr.Info("Console URL final state", "error", lastConsoleErr)
				}
			}()
			Eventually(func(ctx context.Context) error {
				err := func() error {
					resp, err := hcpOpenShiftClustersClient.Get(ctx, *resourceGroup.Name, customerClusterName, nil)
					if err != nil {
						return fmt.Errorf("failed to get cluster: %w", err)
					}
					if resp.Properties == nil || resp.Properties.Console == nil || resp.Properties.Console.URL == nil {
						return fmt.Errorf("cluster console URL not yet available")
					}
					consoleURL = *resp.Properties.Console.URL
					GinkgoLogr.Info("Console URL found", "url", consoleURL)
					return nil
				}()
				if err != nil {
					if err.Error() != lastConsoleErr {
						GinkgoLogr.Info("Console URL check", "status", "failed", "error", err.Error())
						lastConsoleErr = err.Error()
					}
					return err
				}
				lastConsoleErr = ""
				return nil
			}).WithContext(ctx).WithTimeout(15*time.Minute).WithPolling(10*time.Second).Should(Succeed(),
				"expect cluster console URL to become available")

			By("waiting for console hostname DNS to resolve")
			consoleHostname, err := framework.HostnameFromURL(consoleURL)
			Expect(err).NotTo(HaveOccurred(), "failed to parse console URL %s", consoleURL)
			err = framework.WaitForDNSResolution(ctx, consoleHostname, framework.DNSResolutionTimeout)
			Expect(err).NotTo(HaveOccurred(), "DNS for console host %s did not resolve within timeout", consoleHostname)

			By("examining the server certificate returned by the default ingress when routing the console URL")
			// Wait for the certificate to be loaded after console starts
			consoleUrlWithPort := fmt.Sprintf("%s:%d", consoleURL, 443)

			var lastIngressIssuer, lastIngressErr string
			defer func() {
				kv := []any{"issuer", lastIngressIssuer}
				if lastIngressErr != "" {
					kv = append(kv, "error", lastIngressErr)
				}
				GinkgoLogr.Info("Ingress certificate final state", kv...)
			}()
			Eventually(func(ctx context.Context) error {
				certs, err := tlsCertsFromURL(ctx, consoleUrlWithPort)
				if err != nil {
					if err.Error() != lastIngressErr {
						GinkgoLogr.Info("Ingress certificate check", "status", "failed", "error", err.Error())
						lastIngressErr = err.Error()
					}
					return err
				}
				issuer := certs[0].Issuer.String()
				if issuer != lastIngressIssuer {
					GinkgoLogr.Info("Ingress certificate issuer", "issuer", issuer)
					lastIngressIssuer = issuer
				}
				verifyErr := verifyCertChain(certs, trustedCAs)
				if verifyErr != nil {
					if verifyErr.Error() != lastIngressErr {
						GinkgoLogr.Info("Ingress certificate check", "status", "failed", "error", verifyErr.Error())
						lastIngressErr = verifyErr.Error()
					}
					return verifyErr
				}
				lastIngressErr = ""
				return nil
			}).WithContext(ctx).WithTimeout(10*time.Minute).WithPolling(10*time.Second).Should(Succeed(),
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
