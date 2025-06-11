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
	"github.com/Azure/ARO-HCP/test/util/integration"
	"github.com/Azure/ARO-HCP/test/util/labels"
)

var _ = Describe("Confirm nodepools are present on HCPCluster", func() {
	var (
		NodePoolsClient *api.NodePoolsClient
		clusterEnv      *integration.Cluster
		nodePoolOptions *api.NodePoolsClientGetOptions
		customerEnv     *integration.CustomerEnv
		nodePools       *[]integration.Nodepool
	)

	BeforeEach(func() {
		By("Prepare HCPnodepool client")
		NodePoolsClient = clients.NewNodePoolsClient()
		By("Preparing customer environment values")
		customerEnv = &e2eSetup.CustomerEnv
		nodePools = &e2eSetup.Nodepools
		clusterEnv = &e2eSetup.Cluster
	})

	Context("Positive", func() {
		It("Get each nodepool from cluster", labels.Medium, labels.Positive, labels.SetupValidation, func(ctx context.Context) {
			if nodePools != nil {
				nps := *nodePools
				for np := range nps {
					By("Send get request for nodepool")
					clusterNodePool, err := NodePoolsClient.Get(ctx, customerEnv.CustomerRGName, clusterEnv.Name, nps[np].Name, nodePoolOptions)
					Expect(err).To(BeNil())
					Expect(clusterNodePool).ToNot(BeNil())
					By("Check to see nodepool exists and is successfully provisioned")
					Expect(string(*clusterNodePool.Name)).To(Equal(nps[np].Name))
					Expect(string(*clusterNodePool.Properties.ProvisioningState)).To(Equal("Succeeded"))
				}
			}
		})
	})
})
