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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Azure/ARO-HCP/test/util/framework"

	hcpsdk "github.com/Azure/ARO-HCP/test/sdk/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/integration"
	"github.com/Azure/ARO-HCP/test/util/labels"
)

var _ = Describe("List HCPOpenShiftCluster", func() {
	defer GinkgoRecover()

	var (
		customerEnv *integration.CustomerEnv
		clusterName string
	)

	BeforeEach(func() {
		By("Preparing customer environment values")
		customerEnv = &e2eSetup.CustomerEnv
		clusterName = e2eSetup.Cluster.Name
	})

	Context("Positive", func() {
		It("Successfully lists clusters filtered by subscription ID", labels.RequireHappyPathInfra, labels.Medium, labels.Positive, func(ctx context.Context) {
			tc := framework.NewTestContext()

			By("Preparing pager to list clusters")
			listOptions := &hcpsdk.HcpOpenShiftClustersClientListBySubscriptionOptions{}
			pager := tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient().NewListBySubscriptionPager(listOptions)
			By("Accessing IDs of all fetched clusters")
			foundPreCreated := false
			clusterCount := 0
			for pager.More() {
				clusterList, err := pager.NextPage(ctx)
				Expect(err).To(BeNil())
				clusterCount += len(clusterList.Value)
				for _, val := range clusterList.Value {
					Expect(*val.ID).ToNot(BeEmpty())
					if val.Name != nil && *val.Name == clusterName {
						foundPreCreated = true
						break
					}
				}
			}
			Expect(clusterCount).To(BeNumerically(">", 0), "Expected at least one cluster to be listed")
			Expect(foundPreCreated).To(BeTrue(), "Expected to find pre-created cluster name %s in the list", clusterName)
		})

		It("Successfully lists clusters filtered by resource group name", labels.RequireHappyPathInfra, labels.Medium, labels.Positive, func(ctx context.Context) {
			tc := framework.NewTestContext()

			By("Preparing pager to list clusters")
			pager := tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient().NewListByResourceGroupPager(customerEnv.CustomerRGName, nil)
			By("Accessing IDs of all fetched clusters")
			foundPreCreated := false
			clusterCount := 0
			for pager.More() {
				clusterList, err := pager.NextPage(ctx)
				Expect(err).To(BeNil())
				clusterCount += len(clusterList.Value)
				for _, val := range clusterList.Value {
					Expect(*val.ID).ToNot(BeEmpty())
					if val.Name != nil && *val.Name == clusterName {
						foundPreCreated = true
						break
					}
				}
			}
			Expect(clusterCount).To(BeNumerically(">", 0), "Expected at least one cluster to be listed")
			Expect(foundPreCreated).To(BeTrue(), "Expected to find pre-created cluster name %s in the list", clusterName)
		})
	})
})
