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

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 2, 60*time.Second)
				Expect(err).NotTo(HaveOccurred())
			}

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

				clusterParams, err = tc.CreateClusterCustomerResources(ctx,
					resourceGroup,
					clusterParams,
					map[string]any{
						"persistTagValue": false,
					},
					TestArtifactsFS,
				)

				Expect(err).NotTo(HaveOccurred())

				err = tc.CreateHCPClusterFromParam(
					ctx,
					GinkgoLogr,
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
})
