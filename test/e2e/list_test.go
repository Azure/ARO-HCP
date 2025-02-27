package e2e

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	api "github.com/Azure/ARO-HCP/internal/api/v20240610preview/generated"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/log"
)

var _ = Describe("List operations", func() {
	defer GinkgoRecover()

	var (
		clustersClient *api.HcpOpenShiftClustersClient
	)

	BeforeEach(func() {
		By("Prepare HCP clusters client")
		clustersClient = clients.NewHcpOpenShiftClustersClient()
	})

	Context("List clusters", func() {
		It("List clusters by subscription", labels.Medium, func(ctx context.Context) {
			By("Prepare pager to list clusters")
			listOptions := &api.HcpOpenShiftClustersClientListBySubscriptionOptions{}
			pager := clustersClient.NewListBySubscriptionPager(listOptions)
			By("Access IDs of all fetched clusters")
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

		It("List clusters by resource group", labels.Medium, func(ctx context.Context) {
			By("Prepare pager to list clusters")
			pager := clustersClient.NewListByResourceGroupPager(customerRGName, nil)
			By("Access IDs of all fetched clusters")
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
