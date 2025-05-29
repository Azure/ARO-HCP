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
)

var _ = Describe("Put HCPOpenShiftCluster Nodepool", func() {
	var (
		NodePoolsClient *api.NodePoolsClient
	)

	BeforeEach(func() {
		By("Preparing HCP nodepools client")
		NodePoolsClient = clients.NewNodePoolsClient()
	})

	It("Attempts to create a nodepool for a non-existant HCPOpenshiftCluster", labels.Medium, labels.Negative, func(ctx context.Context) {
		var (
			nodePoolName     = "mynodepool"
			clusterName      = "non-existing_cluster"
			nodePoolResource api.NodePool
			nodePoolOptions  *api.NodePoolsClientBeginCreateOrUpdateOptions
		)

		By("Sending a  put request to create nodepool for non-existing HCPOpenshiftCluster")
		_, err := NodePoolsClient.BeginCreateOrUpdate(ctx, customerRGName, clusterName, nodePoolName, nodePoolResource, nodePoolOptions)
		Expect(err).ToNot(BeNil())
		errMessage := "RESPONSE 500: 500 Internal Server Error"
		Expect(err.Error()).To(ContainSubstring(errMessage))
	})
})
