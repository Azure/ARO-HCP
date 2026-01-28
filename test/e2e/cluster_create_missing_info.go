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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
)

var _ = Describe("Customer", func() {
	BeforeEach(func() {
		// do nothing.  per test initialization usually ages better than shared.
	})

	for _, version := range []string{
		"4.18",
		// TODO add other disabled versions here.
	} {
		It("should not be able to create a "+version+" HCP cluster",
			labels.RequireNothing,
			labels.Critical,
			labels.Negative,
			labels.AroRpApiCompatible,
			func(ctx context.Context) {
				const (
					customerClusterName = "illegal-hcp-cluster"
				)
				tc := framework.NewTestContext()

				if tc.UsePooledIdentities() {
					err := tc.AssignIdentityContainers(ctx, 1, 60*time.Second)
					Expect(err).NotTo(HaveOccurred())
				}

				By("creating a resource group")
				resourceGroup, err := tc.NewResourceGroup(ctx, "illegal-ocp-version", tc.Location())
				Expect(err).NotTo(HaveOccurred())

				By("creating cluster parameters with invalid version")
				clusterParams := framework.NewDefaultClusterParams()
				clusterParams.ClusterName = customerClusterName
				clusterParams.OpenshiftVersionId = version
				managedResourceGroupName := framework.SuffixName(*resourceGroup.Name, "managed", 64)
				clusterParams.ManagedResourceGroupName = managedResourceGroupName

				By("creating customer resources")
				clusterParams, err = tc.CreateClusterCustomerResources(ctx,
					resourceGroup,
					clusterParams,
					map[string]interface{}{},
					TestArtifactsFS,
					framework.RBACScopeResourceGroup,
				)
				Expect(err).NotTo(HaveOccurred())

				By("attempting to create the hcp cluster with invalid version")
				err = tc.CreateHCPClusterFromParam(ctx,
					GinkgoLogr,
					*resourceGroup.Name,
					clusterParams,
					45*time.Minute,
				)
				Expect(err).To(HaveOccurred())
				Expect(err).To(MatchError(MatchRegexp("Version .* (doesn't exist|is disabled)")))
			},
		)
	}
})
