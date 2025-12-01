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

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"

	hcpsdk20240610preview "github.com/Azure/ARO-HCP/test/sdk/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
)

var _ = Describe("Customer", func() {

	It("should be able to list HCP clusters without node pools at both subscription and resource group levels",
		labels.RequireNothing,
		labels.Positive,
		labels.Medium,
		func(ctx context.Context) {
			tc := framework.NewTestContext()

			var resourceGroups []*armresources.ResourceGroup
			var clusterNames []string
			const createClustersCount = 2

			for range createClustersCount {
				By("creating resource group for cluster listing test")
				resourceGroup, err := tc.NewResourceGroup(ctx, "cluster-listing", tc.Location())
				Expect(err).NotTo(HaveOccurred())
				resourceGroups = append(resourceGroups, resourceGroup)

				clusterName := "list-test-cluster-" + rand.String(6)
				clusterNames = append(clusterNames, clusterName)

				By("creating cluster without node pool using cluster-only template: " + clusterName)
				_, err = tc.CreateBicepTemplateAndWait(ctx,
					*resourceGroup.Name,
					"cluster-only",
					framework.Must(TestArtifactsFS.ReadFile("test-artifacts/generated-test-artifacts/cluster-only.json")),
					map[string]any{
						"clusterName":     clusterName,
						"persistTagValue": false,
					},
					45*time.Minute,
				)
				Expect(err).NotTo(HaveOccurred())
			}

			By("testing subscription-level cluster listing")
			listOptions := &hcpsdk20240610preview.HcpOpenShiftClustersClientListBySubscriptionOptions{}
			pager := tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient().NewListBySubscriptionPager(listOptions)

			foundClusters := make(map[string]bool)
			clusterCount := 0
			for pager.More() {
				clusterList, err := pager.NextPage(ctx)
				Expect(err).To(BeNil())
				clusterCount += len(clusterList.Value)
				for _, val := range clusterList.Value {
					Expect(*val.ID).ToNot(BeEmpty())
					if val.Name != nil {
						for _, expectedCluster := range clusterNames {
							if *val.Name == expectedCluster {
								foundClusters[expectedCluster] = true
							}
						}
					}
				}
			}
			Expect(clusterCount).To(BeNumerically(">", 0), "Expected at least one cluster to be listed")
			Expect(len(foundClusters)).To(Equal(createClustersCount), "Expected to find all created clusters in subscription listing")

			By("testing resource group-level cluster listing")
			for i, resourceGroup := range resourceGroups {
				pager := tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient().NewListByResourceGroupPager(*resourceGroup.Name, nil)

				foundInRG := false
				rgClusterCount := 0
				for pager.More() {
					clusterList, err := pager.NextPage(ctx)
					Expect(err).To(BeNil())
					rgClusterCount += len(clusterList.Value)
					for _, val := range clusterList.Value {
						Expect(*val.ID).ToNot(BeEmpty())
						if val.Name != nil && *val.Name == clusterNames[i] {
							foundInRG = true
						}
					}
				}
				Expect(rgClusterCount).To(Equal(1), "Expected exactly one cluster in resource group %s", *resourceGroup.Name)
				Expect(foundInRG).To(BeTrue(), "Expected to find cluster %s in resource group %s", clusterNames[i], *resourceGroup.Name)
			}
		})
})
