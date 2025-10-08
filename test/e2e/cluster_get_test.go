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
	"fmt"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/integration"
	"github.com/Azure/ARO-HCP/test/util/labels"
)

var _ = Describe("Get HCPOpenShiftCluster", func() {
	var (
		customerEnv *integration.CustomerEnv
		clusterInfo *integration.Cluster
	)

	BeforeEach(func() {
		By("Preparing customer environment values")
		customerEnv = &e2eSetup.CustomerEnv
		clusterInfo = &e2eSetup.Cluster
	})

	Context("Positive", func() {
		It("Confirms cluster has been created successfully", labels.RequireHappyPathInfra, labels.Medium, labels.Positive, labels.SetupValidation, func(ctx context.Context) {
			tc := framework.NewTestContext()

			By("Checking Provisioning state with RP")
			out, err := tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient().Get(ctx, customerEnv.CustomerRGName, clusterInfo.Name, nil)
			Expect(err).To(BeNil())
			Expect(string(*out.Properties.ProvisioningState)).To(Equal(("Succeeded")))
		})
	})

	Context("Negative", func() {
		It("Fails to get a nonexistent cluster with a Not Found error", labels.RequireHappyPathInfra, labels.Medium, labels.Negative, func(ctx context.Context) {
			tc := framework.NewTestContext()

			clusterName := "non-existing-cluster"
			By("Sending a GET request for the nonexistent cluster")
			_, err := tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient().Get(ctx, customerEnv.CustomerRGName, clusterName, nil)
			Expect(err).ToNot(BeNil())
			errMessage := fmt.Sprintf("hcpOpenShiftClusters/%s' under resource group '%s' was not found.", clusterName, customerEnv.CustomerRGName)
			Expect(strings.ToLower(err.Error())).To(ContainSubstring(strings.ToLower(errMessage)))
		})
	})
})
