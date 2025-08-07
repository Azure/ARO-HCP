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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"

	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
)

type testContext interface {
	NewResourceGroup(ctx context.Context, resourceGroupPrefix, location string) (*armresources.ResourceGroup, error)
	GetARMResourcesClientFactoryOrDie(ctx context.Context) *armresources.ClientFactory
	Location() string
}

var _ = Describe("Endpoint TLS", func() {
	BeforeEach(func() {
		// do nothing.  per test initialization usually ages better than shared.
	})

	const (
		customerNetworkSecurityGroupName = "customer-nsg-name"
		customerVnetName                 = "customer-vnet-name"
		customerVnetSubnetName           = "customer-vnet-subnet1"
		customerClusterName              = "basic-hcp-cluster"
		customerNodePoolName             = "np-1"
	)

	var (
		managedResourceGroupName string
	)

	createResourceGroup := func(ctx context.Context, testCtx testContext) (*armresources.ResourceGroup, error) {
		return testCtx.NewResourceGroup(ctx, "e2e-no-openshift-ca", "uksouth")
	}

	createInfra := func(ctx context.Context, testCtx testContext, resourceGroupName string) error {
		_, err := framework.CreateBicepTemplateAndWait(ctx,
			testCtx.GetARMResourcesClientFactoryOrDie(ctx).NewDeploymentsClient(),
			resourceGroupName,
			"infra",
			framework.Must(TestArtifactsFS.ReadFile("test-artifacts/generated-test-artifacts/standard-cluster-create/customer-infra.json")),
			map[string]interface{}{
				"customerNsgName":        customerNetworkSecurityGroupName,
				"customerVnetName":       customerVnetName,
				"customerVnetSubnetName": customerVnetSubnetName,
			},
			45*time.Minute,
		)
		return err
	}

	createCluster := func(ctx context.Context, testCtx testContext, resourceGroupName string) error {
		managedResourceGroupName = framework.SuffixName(resourceGroupName, "-managed", 64)
		_, err := framework.CreateBicepTemplateAndWait(ctx,
			testCtx.GetARMResourcesClientFactoryOrDie(ctx).NewDeploymentsClient(),
			resourceGroupName,
			"hcp-cluster",
			framework.Must(TestArtifactsFS.ReadFile("test-artifacts/generated-test-artifacts/standard-cluster-create/cluster.json")),
			map[string]interface{}{
				"nsgName":                  customerNetworkSecurityGroupName,
				"vnetName":                 customerVnetName,
				"subnetName":               customerVnetSubnetName,
				"clusterName":              customerClusterName,
				"managedResourceGroupName": managedResourceGroupName,
			},
			45*time.Minute,
		)
		return err
	}

	Context("for the Kubernetes API server", func() {
		It("should not serve a TLS certificate issued by an OpenShift root CA", labels.RequireNothing, labels.Critical, labels.Positive, func(ctx context.Context) {
			testCtx := framework.NewTestContext()

			By("creating a resource group")
			resourceGroup, err := createResourceGroup(ctx, testCtx)
			Expect(err).NotTo(HaveOccurred())

			By("creating a prereqs in the resource group")
			err = createInfra(ctx, testCtx, *resourceGroup.Name)
			Expect(err).NotTo(HaveOccurred())

			By("creating the hcp cluster")
			err = createCluster(ctx, testCtx, *resourceGroup.Name)
			Expect(err).NotTo(HaveOccurred())

			By("examining the server certificate returned by the Kube API server")
			clusterResp, err := testCtx.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient().Get(ctx, *resourceGroup.Name, customerClusterName, nil)
			Expect(err).NotTo(HaveOccurred())
			apiServerURL := clusterResp.Properties.API.URL
			actualCert, err := tlsCertFromURL(ctx, *apiServerURL)
			Expect(err).NotTo(HaveOccurred())

			fmt.Print(GinkgoWriter, "Issuer: %s", actualCert.Issuer)
			Expect(actualCert.Issuer).NotTo(SatisfyAll(
				HaveField("CommonName", "root-ca"),
				HaveField("OrganizationalUnit", ContainElements("openshift")),
			), "expected certificate not issued by an OpenShift root CA")
		})
	})

	Context("for the default Ingress", func() {
		It("should not serve a TLS certificate issued by an OpenShift root CA", labels.RequireNothing, labels.Critical, labels.Positive, func(ctx context.Context) {
			testCtx := framework.NewTestContext()

			By("creating a resource group")
			resourceGroup, err := createResourceGroup(ctx, testCtx)
			Expect(err).NotTo(HaveOccurred())

			By("creating a prereqs in the resource group")
			err = createInfra(ctx, testCtx, *resourceGroup.Name)
			Expect(err).NotTo(HaveOccurred())

			By("creating the hcp cluster")
			err = createCluster(ctx, testCtx, *resourceGroup.Name)
			Expect(err).NotTo(HaveOccurred())

			By("creating the node pool")
			_, err = framework.CreateBicepTemplateAndWait(ctx,
				testCtx.GetARMResourcesClientFactoryOrDie(ctx).NewDeploymentsClient(),
				*resourceGroup.Name,
				"node-pool",
				framework.Must(TestArtifactsFS.ReadFile("test-artifacts/generated-test-artifacts/standard-cluster-create/nodepool.json")),
				map[string]interface{}{
					"clusterName":  customerClusterName,
					"nodePoolName": customerNodePoolName,
				},
				45*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			hcpOpenShiftClustersClient := testCtx.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient()

			By("waiting for the console URL to become available")
			ingressURL := func(g Gomega) *string {
				resp, err := hcpOpenShiftClustersClient.Get(ctx, *resourceGroup.Name, customerClusterName, nil)
				g.Expect(err).NotTo(HaveOccurred())
				return resp.Properties.Console.URL
			}
			Eventually(ingressURL, ctx).WithTimeout(10 * time.Minute).ShouldNot(BeNil())

			By("examining the server certificate returned by the default ingress when routing the console URL")
			actualCert, err := tlsCertFromURL(ctx, *ingressURL(Default))
			Expect(err).NotTo(HaveOccurred())
			fmt.Print(GinkgoWriter, "Issuer: %s", actualCert.Issuer)
			Expect(actualCert.Issuer).NotTo(SatisfyAll(
				HaveField("CommonName", "root-ca"),
				HaveField("OrganizationalUnit", ContainElements("openshift")),
			), "expected certificate not issued by an OpenShift root CA")
		})
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
