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
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/verifiers"

	hcpsdk20240610preview "github.com/Azure/ARO-HCP/test/sdk/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/labels"
)

// Helper to convert ManagedServiceIdentity to AzureResourceManagerCommonTypesManagedServiceIdentityUpdate
func toIdentityUpdate(identity *hcpsdk20240610preview.ManagedServiceIdentity) *hcpsdk20240610preview.AzureResourceManagerCommonTypesManagedServiceIdentityUpdate {
	if identity == nil {
		return nil
	}
	return &hcpsdk20240610preview.AzureResourceManagerCommonTypesManagedServiceIdentityUpdate{
		Type:                   identity.Type,
		UserAssignedIdentities: identity.UserAssignedIdentities,
	}
}

var _ = Describe("Update HCPOpenShiftCluster", func() {
	Context("Negative", func() {
		It("creates a cluster and fails to update its name with a PATCH request",
			labels.RequireNothing, labels.Medium, labels.Negative,
			func(ctx context.Context) {
				const clusterName = "patch-name-cluster"

				tc := framework.NewTestContext()

				By("creating a resource group")
				resourceGroup, err := tc.NewResourceGroup(ctx, "patch-name", tc.Location())
				Expect(err).NotTo(HaveOccurred())

				By("deploying demo template (single-step infra + identities + cluster)")
				_, err = framework.CreateBicepTemplateAndWait(ctx,
					tc.GetARMResourcesClientFactoryOrDie(ctx).NewDeploymentsClient(),
					*resourceGroup.Name,
					"aro-hcp-demo",
					framework.Must(TestArtifactsFS.ReadFile("test-artifacts/generated-test-artifacts/cluster-only.json")),
					map[string]interface{}{
						"clusterName": clusterName,
					},
					45*time.Minute,
				)
				Expect(err).NotTo(HaveOccurred())

				By("getting credentials")
				adminRESTConfig, err := framework.GetAdminRESTConfigForHCPCluster(
					ctx,
					tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
					*resourceGroup.Name,
					clusterName,
					10*time.Minute,
				)
				Expect(err).NotTo(HaveOccurred())

				By("ensuring the cluster is viable")
				err = verifiers.VerifyHCPCluster(ctx, adminRESTConfig)
				Expect(err).NotTo(HaveOccurred())

				By("sending a PATCH request attempting to change the resource name")
				newName := clusterName + "-renamed"
				update := hcpsdk20240610preview.HcpOpenShiftClusterUpdate{
					Name: &newName,
				}
				_, err = framework.UpdateHCPCluster(
					ctx,
					tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
					*resourceGroup.Name,
					clusterName,
					update,
					10*time.Minute,
				)
				Expect(err).To(HaveOccurred())
				Expect(strings.ToLower(err.Error())).To(ContainSubstring("mismatchingresourcename"))
			},
		)
	})

	Context("Positive", func() {
		It("creates a cluster and updates tags with a PATCH request",
			labels.RequireNothing, labels.Medium, labels.Positive,
			func(ctx context.Context) {
				const clusterName = "patch-tags-cluster"

				tc := framework.NewTestContext()

				By("creating a resource group")
				resourceGroup, err := tc.NewResourceGroup(ctx, "patch-tags", tc.Location())
				Expect(err).NotTo(HaveOccurred())

				By("deploying demo template (single-step infra + identities + cluster)")
				_, err = framework.CreateBicepTemplateAndWait(ctx,
					tc.GetARMResourcesClientFactoryOrDie(ctx).NewDeploymentsClient(),
					*resourceGroup.Name,
					"aro-hcp-demo",
					framework.Must(TestArtifactsFS.ReadFile("test-artifacts/generated-test-artifacts/cluster-only.json")),
					map[string]interface{}{
						"clusterName": clusterName,
					},
					45*time.Minute,
				)
				Expect(err).NotTo(HaveOccurred())

				By("getting credentials")
				adminRESTConfig, err := framework.GetAdminRESTConfigForHCPCluster(
					ctx,
					tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
					*resourceGroup.Name,
					clusterName,
					10*time.Minute,
				)
				Expect(err).NotTo(HaveOccurred())

				By("ensuring the cluster is viable")
				err = verifiers.VerifyHCPCluster(ctx, adminRESTConfig)
				Expect(err).NotTo(HaveOccurred())

				By("sending a PATCH request to set a tag")
				val := "should succeed"
				// Fetch the current cluster to get the existing identity
				got, err := framework.GetHCPCluster(
					ctx,
					tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
					*resourceGroup.Name,
					clusterName,
				)
				Expect(err).NotTo(HaveOccurred())

				update := hcpsdk20240610preview.HcpOpenShiftClusterUpdate{
					Identity: toIdentityUpdate(got.Identity),
					Tags: map[string]*string{
						"test": &val,
					},
				}
				resp, err := framework.UpdateHCPCluster(
					ctx,
					tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
					*resourceGroup.Name,
					clusterName,
					update,
					10*time.Minute,
				)
				Expect(err).NotTo(HaveOccurred())

				By("verifying the tag is present in the update response body")
				Expect(resp.Tags).ToNot(BeNil())
				Expect(resp.Tags["test"]).ToNot(BeNil())
				Expect(*resp.Tags["test"]).To(Equal(val))

				By("verifying the tag is present on the cluster")
				got, err = framework.GetHCPCluster(
					ctx,
					tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
					*resourceGroup.Name,
					clusterName,
				)
				Expect(err).NotTo(HaveOccurred())
				Expect(got.Tags).ToNot(BeNil())
				Expect(got.Tags["test"]).ToNot(BeNil())
				Expect(*got.Tags["test"]).To(Equal(val))
			},
		)
	})
})
