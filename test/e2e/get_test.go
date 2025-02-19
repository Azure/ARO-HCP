package e2e

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	api "github.com/Azure/ARO-HCP/internal/api/v20240610preview/generated"
	"github.com/Azure/ARO-HCP/test/util/log"
)

var _ = Describe("Get operation", func() {
	var (
		clustersClient *api.HcpOpenShiftClustersClient
	)

	BeforeEach(func() {
		By("Prepare HCP clusters client")
		clustersClient = clients.NewHcpOpenShiftClustersClient()
	})

	It("Get cluster", func(ctx context.Context) {
		rgName := "psuba-net-rg"
		clusterName := "psuba-hcp-test"
		By("Send get request for cluster")
		resp, err := clustersClient.Get(ctx, rgName, clusterName, nil)
		Expect(err).To(BeNil())
		By("Make sure cluster ID is not empty")
		Expect(*resp.ID).ToNot(BeEmpty())
		log.Logger.Infoln(*resp.ID)
		By("Make sure cluster Name is not empty")
		Expect(*resp.Name).ToNot(BeEmpty())
		log.Logger.Infoln(*resp.Name)
	})
})
