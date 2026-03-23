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
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	hcpsdk20240610preview "github.com/Azure/ARO-HCP/test/sdk/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/integration"
	"github.com/Azure/ARO-HCP/test/util/labels"
)

var _ = Describe("Put HCPOpenShiftCluster Nodepool", func() {
	var (
		customerEnv *integration.CustomerEnv
	)

	BeforeEach(func() {
		By("Preparing customer environment values")
		customerEnv = &e2eSetup.CustomerEnv
	})

	It("Attempts to create a nodepool for a non-existent HCPOpenshiftCluster", labels.RequireHappyPathInfra, labels.Medium, labels.Negative, func(ctx context.Context) {
		tc := framework.NewTestContext()

		var (
			nodePoolName     = "mynodepool"
			clusterName      = "non-existing_cluster"
			nodePoolResource hcpsdk20240610preview.NodePool
			nodePoolOptions  *hcpsdk20240610preview.NodePoolsClientBeginCreateOrUpdateOptions
		)

		By("Sending a  put request to create nodepool for non-existing HCPOpenshiftCluster and cluster resource as nil")
		_, err := tc.Get20240610ClientFactoryOrDie(ctx).NewNodePoolsClient().BeginCreateOrUpdate(ctx, customerEnv.CustomerRGName, clusterName, nodePoolName, nodePoolResource, nodePoolOptions)
		Expect(err).ToNot(BeNil())
		errMessage := "The location property is required"
		Expect(strings.ToLower(err.Error())).To(ContainSubstring(strings.ToLower(errMessage)))
	})
})
