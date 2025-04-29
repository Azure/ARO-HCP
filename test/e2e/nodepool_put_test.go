package e2e

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	api "github.com/Azure/ARO-HCP/internal/api/v20240610preview/generated"
	"github.com/Azure/ARO-HCP/test/util/labels"
)

var _ = Describe("Put HCPOpenShiftCluster Nodepool", func() {
	var (
		NodePoolsClient *api.NodePoolsClient
	)

	BeforeEach(func() {
		By("Prepare HCP nodepools client")
		NodePoolsClient = clients.NewNodePoolsClient()
	})

	var (
		nodePoolName     = "mynodepool"
		nodePoolResource api.NodePool
		nodePoolOptions  *api.HcpOpenShiftClustersClientBeginCreateOrUpdateOptions
	)
	It("Puts invalid nodepool request", labels.Medium, labels.Negative, func(ctx context.Context) {
		clusterName := "non-existing_cluster"
		By("Send put request to create nodepool for non-existing HCPOpenshiftCluster")
		_, err := NodePoolsClient.BeginCreateOrUpdate(ctx, customerRGName, clusterName, nodePoolName, nodePoolResource, (*api.NodePoolsClientBeginCreateOrUpdateOptions)(nodePoolOptions))
		Expect(err).ToNot(BeNil())
		errMessage := "RESPONSE 500: 500 Internal Server Error"
		Expect(err.Error()).To(ContainSubstring(errMessage))
	})
})
