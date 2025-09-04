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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Azure/ARO-HCP/test/util/framework"

	"github.com/Azure/ARO-HCP/test/util/labels"
)

var _ = Describe("ARO-HCP Cluster Deletion", func() {
	var clusterName string
	var resourceGroup string

	BeforeEach(func() {
		clusterName = e2eSetup.Cluster.Name
		resourceGroup = e2eSetup.CustomerEnv.CustomerRGName
	})

	It("should confirm the HCP cluster is deleted (not found)", labels.TeardownValidation, labels.Critical, func(ctx context.Context) {
		tc := framework.NewTestContext()
		hcpClient := tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient()
		By("checking that the HCP cluster is not present")
		_, err := hcpClient.Get(ctx, resourceGroup, clusterName, nil)
		Expect(err).ToNot(BeNil())
		errMessage := fmt.Sprintf("The Resource 'Microsoft.RedHatOpenShift/HCPOpenShiftClusters/%s' under resource group '%s' was not found.", clusterName, resourceGroup)
		Expect(err.Error()).To(ContainSubstring(errMessage))
	})
})
