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

	api "github.com/Azure/ARO-HCP/internal/api/v20240610preview/generated"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/log"
)

var _ = Describe("List HCPOpenShiftCluster", func() {
	defer GinkgoRecover()

	var (
		clustersClient *api.HcpOpenShiftClustersClient
	)

	BeforeEach(func() {
		By("Preparing HCP clusters client")
		clustersClient = clients.NewHcpOpenShiftClustersClient()
	})

	Context("Positive", func() {
		It("Successfully lists clusters filtered by subscription ID", labels.Medium, func(ctx context.Context) {
			By("Preparing pager to list clusters")
			listOptions := &api.HcpOpenShiftClustersClientListBySubscriptionOptions{}
			pager := clustersClient.NewListBySubscriptionPager(listOptions)
			By("Accessing IDs of all fetched clusters")
			for pager.More() {
				clusterList, err := pager.NextPage(ctx)
				Expect(err).To(BeNil())
				log.Logger.Infoln("Number of clusters:", len(clusterList.Value))
				for _, val := range clusterList.Value {
					Expect(*val.ID).ToNot(BeEmpty())
					log.Logger.Infoln(*val.ID)
				}
			}
		})

		It("Successfully lists clusters filtered by resource group name", labels.Medium, func(ctx context.Context) {
			By("Preparing pager to list clusters")
			pager := clustersClient.NewListByResourceGroupPager(customerRGName, nil)
			By("Accessing IDs of all fetched clusters")
			for pager.More() {
				clusterList, err := pager.NextPage(ctx)
				Expect(err).To(BeNil())
				log.Logger.Infoln("Number of clusters:", len(clusterList.Value))
				for _, val := range clusterList.Value {
					Expect(*val.ID).ToNot(BeEmpty())
					log.Logger.Infoln(*val.ID)
				}
			}
		})
	})
})
