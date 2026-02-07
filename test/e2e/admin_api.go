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
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"k8s.io/client-go/rest"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

var _ = Describe("Engineering", func() {
	BeforeEach(func() {
		// do nothing.  per test initialization usually ages better than shared.
	})

	It("should be able to log into a cluster via a breakglass session",
		labels.RequireNothing,
		labels.High,
		labels.Positive,
		labels.CoreInfraService,
		labels.DevelopmentOnly,
		labels.AroRpApiCompatible,
		func(ctx context.Context) {
			const (
				engineeringNetworkSecurityGroupName = "engineering-nsg-name"
				engineeringVnetName                 = "engineering-vnet-name"
				engineeringVnetSubnetName           = "engineering-vnet-subnet1"
				engineeringClusterName              = "engineering-hcp-cluster"
			)
			tc := framework.NewTestContext()

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, 60*time.Second)
				Expect(err).NotTo(HaveOccurred())
			}

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "admin-api-breakglass", tc.Location())
			Expect(err).NotTo(HaveOccurred())

			By("creating cluster parameters")
			clusterParams := framework.NewDefaultClusterParams()
			clusterParams.ClusterName = engineeringClusterName
			managedResourceGroupName := framework.SuffixName(*resourceGroup.Name, "-managed", 64)
			clusterParams.ManagedResourceGroupName = managedResourceGroupName

			By("creating customer resources")
			clusterParams, err = tc.CreateClusterCustomerResources(ctx,
				resourceGroup,
				clusterParams,
				map[string]interface{}{
					"customerNsgName":        engineeringNetworkSecurityGroupName,
					"customerVnetName":       engineeringVnetName,
					"customerVnetSubnetName": engineeringVnetSubnetName,
				},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred())

			By("creating the HCP cluster")
			err = tc.CreateHCPClusterFromParam(
				ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
				45*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			hcpResourceID := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.RedHatOpenshift/hcpOpenShiftClusters/%s", api.Must(tc.SubscriptionID(ctx)), *resourceGroup.Name, engineeringClusterName)

			commonVerifiers := []verifiers.HostedClusterVerifier{
				verifiers.VerifyCanReadNamespaced("kube-system", "pods", "configmaps"),
				verifiers.VerifyCanRead("nodes", "namespaces"),
			}

			By("creating SRE breakglass credentials with aro-sre permissions")
			aroSreRestConfig, err := tc.SREBreakglassCredentials(ctx, hcpResourceID, 2*time.Minute, "aro-sre")
			Expect(err).NotTo(HaveOccurred())
			err = runSREBreakglassCredentialsVerifier(ctx, "aro-sre", aroSreRestConfig, append(commonVerifiers,
				verifiers.VerifyCannotReadNamespaced("kube-system", "secrets"),
			))
			Expect(err).NotTo(HaveOccurred())

			By("creating SRE breakglass credentials with aro-sre permissions")
			aroSreAdminRestConfig, err := tc.SREBreakglassCredentials(ctx, hcpResourceID, 2*time.Minute, "aro-sre-cluster-admin")
			Expect(err).NotTo(HaveOccurred())
			err = runSREBreakglassCredentialsVerifier(ctx, "aro-sre-cluster-admin", aroSreAdminRestConfig, append(commonVerifiers,
				verifiers.VerifyCanReadNamespaced("kube-system", "secrets"),
			))
			Expect(err).NotTo(HaveOccurred())

			// TODO: cover more capabilities per access level
			// TODO: cover expiry
		})
})

func runSREBreakglassCredentialsVerifier(ctx context.Context, expectedGroup string, restConfig *rest.Config, tests []verifiers.HostedClusterVerifier) error {
	By(fmt.Sprintf("verifying %s group membership", expectedGroup))
	Eventually(func() error {
		return verifiers.VerifyWhoAmI(expectedGroup).Verify(ctx, restConfig)
	}, 30*time.Second, 2*time.Second).Should(Succeed())
	for _, verifier := range tests {
		By(fmt.Sprintf("verifying %s", verifier.Name()))
		Eventually(func() error {
			return verifier.Verify(ctx, restConfig)
		}, 30*time.Second, 2*time.Second).Should(Succeed())
	}
	return nil
}
