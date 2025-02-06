package e2e

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	api "github.com/Azure/ARO-HCP/internal/api/v20240610preview/generated"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
)

var _ = Describe("List operations", func() {
	defer GinkgoRecover()

	var (
		clients    *api.ClientFactory
		creds      azcore.TokenCredential
		armOptions *arm.ClientOptions
		ctx        context.Context
	)

	const (
		subscriptionID = "00000000-0000-0000-0000-000000000000"
	)

	BeforeEach(func() {
		var err error
		creds, err = azidentity.NewDefaultAzureCredential(nil)
		Expect(err).To(BeNil())
		armOptions = &arm.ClientOptions{}
		clients, err = api.NewClientFactory(subscriptionID, creds, armOptions)
		Expect(err).To(BeNil())
		Expect(clients).ToNot(BeNil())
		ctx = context.Background()
	})

	Context("List clusters", Label("List"), func() {
		It("List clusters by subscription", Label("first-test"), func() {
			By("Prepare HCP clusters client")
			clustersClient := clients.NewHcpOpenShiftClustersClient()
			By("List clusters")
			listOptions := &api.HcpOpenShiftClustersClientListBySubscriptionOptions{}
			pager := clustersClient.NewListBySubscriptionPager(listOptions)
			By("Access IDs of all fetched clusters")
			for {
				clusterList, err := pager.NextPage(ctx)
				Expect(err).To(BeNil())
				for _, val := range clusterList.Value {
					Expect(val.ID).ToNot(BeEmpty())
				}
				if !pager.More() {
					break
				}
			}
		})

		It("List clusters by resource group", Label("second-test"), func() {
			rgName := "test-resource-group"
			By("Prepare HCP clusters client")
			clustersClient := clients.NewHcpOpenShiftClustersClient()
			By("List clusters")
			pager := clustersClient.NewListByResourceGroupPager(rgName, nil)
			By("Access IDs of all fetched clusters")
			for {
				clusterList, err := pager.NextPage(ctx)
				Expect(err).To(BeNil())
				for _, val := range clusterList.Value {
					Expect(val.ID).ToNot(BeEmpty())
				}
				if !pager.More() {
					break
				}
			}
		})
	})
})
