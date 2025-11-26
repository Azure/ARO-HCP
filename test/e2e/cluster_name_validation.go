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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"k8s.io/apimachinery/pkg/util/rand"

	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
)

var _ = Describe("Customer", func() {

	It("should not be able to deploy 2 identically named clusters within the same resource group",
		labels.RequireNothing,
		labels.Negative,
		labels.Medium,
		labels.AroRpApiCompatible,
		func(ctx context.Context) {
			tc := framework.NewTestContext()

			By("creating resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "rg-cluster-name-duplicate", tc.Location())
			Expect(err).NotTo(HaveOccurred())

			clusterName := "name-duplicate" + rand.String(6)

			clusterDeployments := []struct {
				deploymentName string
				description    string
				shouldFail     bool
			}{
				{
					deploymentName: "first-cluster",
					description:    "deploying first cluster",
					shouldFail:     false,
				},
				{
					deploymentName: "second-cluster",
					description:    "attempting to deploy second cluster with same name",
					shouldFail:     true,
				},
			}

			for _, deployment := range clusterDeployments {
				By(deployment.description)

				clusterParams := framework.NewDefaultClusterParams()
				clusterParams.ClusterName = clusterName
				managedResourceGroupName := framework.SuffixName(*resourceGroup.Name, "-managed-"+deployment.deploymentName, 64)
				clusterParams.ManagedResourceGroupName = managedResourceGroupName

				clusterParams, err = framework.CreateClusterCustomerResources(ctx,
					tc.GetARMResourcesClientFactoryOrDie(ctx).NewDeploymentsClient(),
					resourceGroup,
					clusterParams,
					map[string]any{
						"persistTagValue": false,
					},
					TestArtifactsFS,
				)

				Expect(err).NotTo(HaveOccurred())

				err = framework.CreateHCPClusterFromParam(ctx,
					tc,
					*resourceGroup.Name,
					clusterParams,
					45*time.Minute,
				)

				if deployment.shouldFail {
					By("verifying second cluster failed to deploy with the proper error message")
					Expect(err).To(HaveOccurred())
					GinkgoLogr.Error(err, "cluster deployment error")
					Expect(err.Error()).To(ContainSubstring("Forbidden: field is immutable"))
				} else {
					Expect(err).NotTo(HaveOccurred())
				}
			}

		})
	It("should not be able to deploy cluster with invalid name",
		labels.RequireNothing,
		labels.Negative,
		labels.Medium,
		labels.AroRpApiCompatible,
		func(ctx context.Context) {
			tc := framework.NewTestContext()

			By("creating resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "rg-cluster-name-validation", tc.Location())
			Expect(err).NotTo(HaveOccurred())

			By("creating customer resources")
			baseClusterParams := framework.NewDefaultClusterParams()
			baseClusterParams.ClusterName = "temp-cluster-for-setup"
			managedResourceGroupName := framework.SuffixName(*resourceGroup.Name, "-managed", 64)
			baseClusterParams.ManagedResourceGroupName = managedResourceGroupName

			baseClusterParams, err = framework.CreateClusterCustomerResources(ctx,
				tc.GetARMResourcesClientFactoryOrDie(ctx).NewDeploymentsClient(),
				resourceGroup,
				baseClusterParams,
				map[string]any{
					"persistTagValue": false,
				},
				TestArtifactsFS,
			)
			Expect(err).NotTo(HaveOccurred())

			invalidNameCases := map[string]struct {
				clusterName string
				description string
			}{
				"long-name": {
					clusterName: "cluster-name-that-is-definitely-longer-than-fifty-four-characters-and-should-fail-validation",
					description: "attempting to deploy cluster with name longer than 54 characters",
				},
				"starts-with-non-letter": {
					clusterName: "1cluster",
					description: "attempting to deploy cluster with name starting with non-letter character",
				},
				"special-chars": {
					clusterName: "clu$ter",
					description: "attempting to deploy cluster with non-allowed special characters",
				},
			}

			for _, nameCase := range invalidNameCases {
				By(nameCase.description)

				testClusterParams := baseClusterParams
				testClusterParams.ClusterName = nameCase.clusterName

				err = framework.CreateHCPClusterFromParam(ctx,
					tc,
					*resourceGroup.Name,
					testClusterParams,
					45*time.Minute,
				)
				Expect(err).To(HaveOccurred())
				GinkgoLogr.Error(err, "cluster deployment error")
				Expect(err.Error()).To(Or(
					MatchRegexp("The Resource .* does not conform to the naming restriction"),
					MatchRegexp("Resource name .* is invalid for user assigned identity"),
					ContainSubstring("InvalidResourceName"),
				))
			}

		})
})
