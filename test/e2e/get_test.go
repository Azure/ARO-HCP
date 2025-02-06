package e2e

import (
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	api "github.com/Azure/ARO-HCP/internal/api/v20240610preview/generated"
	"github.com/Azure/ARO-HCP/test/util/labels"
)

var _ = Describe("Get operation", func() {
	var (
		clustersClient *api.HcpOpenShiftClustersClient
	)

	BeforeEach(func() {
		By("Prepare HCP clusters client")
		clustersClient = clients.NewHcpOpenShiftClustersClient()
	})

	It("Get non existing cluster", labels.Medium, labels.Negative, func(ctx context.Context) {
		clusterName := "non-existing-cluster"
		By("Send get request for cluster")
		_, err := clustersClient.Get(ctx, customerRGName, clusterName, nil)
		Expect(err).ToNot(BeNil())
		errMessage := fmt.Sprintf("The resource 'hcpOpenShiftClusters/%s' under resource group '%s' was not found.", clusterName, customerRGName)
		Expect(err.Error()).To(ContainSubstring(errMessage))
	})
})
