// Copyright 2026 Microsoft Corporation
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

	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
)

var _ = Describe("Customer", func() {

	It("should be able to list available HCP Operator Identity Role Sets and validate response content",
		labels.RequireNothing,
		labels.Medium,
		labels.Positive,
		labels.AroRpApiCompatible,
		labels.MIContainers(0),
		func(ctx context.Context) {
			tc := framework.NewTestContext()

			By("listing HCP Operator Identity Role Sets")
			roleSetsClient := tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOperatorIdentityRoleSetsClient()
			roleSetsPager := roleSetsClient.NewListPager(tc.Location(), nil)

			roleSets, err := roleSetsPager.NextPage(ctx)
			Expect(err).NotTo(HaveOccurred(), "failed to list HCP Operator Identity Role Sets")
			Expect(roleSets.Value).NotTo(BeEmpty(), "should return at least one Operator Identity Role Set")

			By("validating role set response structure and content")
			for _, roleSet := range roleSets.Value {
				Expect(roleSet.ID).NotTo(BeNil(), "role set ID was nil")
				Expect(roleSet.Name).NotTo(BeNil(), "role set Name was nil")
				Expect(roleSet.Type).NotTo(BeNil(), "role set Type was nil")
				Expect(roleSet.Properties).NotTo(BeNil(), "role set Properties was nil")

				// Validate name format (should be semantic version minor version like "4.19")
				Expect(*roleSet.Name).To(MatchRegexp(`^\d+\.\d+$`), "role set name should be a minor version (e.g., 4.19)")

				// Validate ID contains role set-related path
				Expect(*roleSet.ID).To(ContainSubstring("/hcpOperatorIdentityRoleSets/"), "role set ID should contain the hcpOperatorIdentityRoleSets resource path")
				Expect(*roleSet.ID).To(ContainSubstring(*roleSet.Name), "role set ID should contain the role set name %s", *roleSet.Name)

				// Validate Type
				Expect(*roleSet.Type).To(Equal("Microsoft.RedHatOpenShift/locations/hcpOperatorIdentityRoleSets"), "role set Type should match expected value")

				// Validate Properties contain operator arrays
				Expect(roleSet.Properties.ControlPlaneOperators).NotTo(BeNil(), "controlPlaneOperators should not be nil")
				Expect(roleSet.Properties.DataPlaneOperators).NotTo(BeNil(), "dataPlaneOperators should not be nil")

				// Validate at least one operator in each array
				Expect(roleSet.Properties.ControlPlaneOperators).NotTo(BeEmpty(), "should have at least one control plane operator")
				Expect(roleSet.Properties.DataPlaneOperators).NotTo(BeEmpty(), "should have at least one data plane operator")

				// Validate operator structure for the first control plane operator
				if len(roleSet.Properties.ControlPlaneOperators) > 0 {
					operator := roleSet.Properties.ControlPlaneOperators[0]
					Expect(operator.Name).NotTo(BeNil(), "operator name should not be nil")
					Expect(operator.Required).NotTo(BeNil(), "operator required field should not be nil")
					Expect(operator.RoleDefinitions).NotTo(BeEmpty(), "operator should have at least one role definition")

					// Validate role definition structure
					if len(operator.RoleDefinitions) > 0 {
						roleDef := operator.RoleDefinitions[0]
						Expect(roleDef.Name).NotTo(BeNil(), "role definition name should not be nil")
						Expect(roleDef.ResourceID).NotTo(BeNil(), "role definition resource ID should not be nil")
						Expect(*roleDef.ResourceID).To(ContainSubstring("/providers/Microsoft.Authorization/roleDefinitions/"), "role definition resource ID should be an Azure role definition")
					}
				}
			}

			By("verifying at least one role set is available")
			Expect(len(roleSets.Value)).To(BeNumerically(">=", 1), "at least one operator identity role set should be available")
		})

	It("should be able to get a specific HCP Operator Identity Role Set by version",
		labels.RequireNothing,
		labels.Medium,
		labels.Positive,
		labels.AroRpApiCompatible,
		labels.MIContainers(0),
		func(ctx context.Context) {
			tc := framework.NewTestContext()

			By("listing HCP Operator Identity Role Sets to get a valid version")
			roleSetsClient := tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOperatorIdentityRoleSetsClient()
			roleSetsPager := roleSetsClient.NewListPager(tc.Location(), nil)

			roleSets, err := roleSetsPager.NextPage(ctx)
			Expect(err).NotTo(HaveOccurred(), "failed to list HCP Operator Identity Role Sets")
			Expect(roleSets.Value).NotTo(BeEmpty(), "should return at least one Operator Identity Role Set")

			// Get the first role set name to use for the GET request
			firstRoleSetName := *roleSets.Value[0].Name

			By("getting the specific role set by name: " + firstRoleSetName)
			roleSet, err := roleSetsClient.Get(ctx, tc.Location(), firstRoleSetName, nil)
			Expect(err).NotTo(HaveOccurred(), "failed to get HCP Operator Identity Role Set %s", firstRoleSetName)

			By("validating the retrieved role set matches the expected structure")
			Expect(roleSet.ID).NotTo(BeNil(), "role set ID was nil")
			Expect(roleSet.Name).NotTo(BeNil(), "role set Name was nil")
			Expect(roleSet.Type).NotTo(BeNil(), "role set Type was nil")
			Expect(roleSet.Properties).NotTo(BeNil(), "role set Properties was nil")

			// Validate the name matches what we requested
			Expect(*roleSet.Name).To(Equal(firstRoleSetName), "retrieved role set name should match requested name")

			// Validate ID contains role set-related path
			Expect(*roleSet.ID).To(ContainSubstring("/hcpOperatorIdentityRoleSets/"), "role set ID should contain the hcpOperatorIdentityRoleSets resource path")
			Expect(*roleSet.ID).To(ContainSubstring(firstRoleSetName), "role set ID should contain the role set name")

			// Validate Type
			Expect(*roleSet.Type).To(Equal("Microsoft.RedHatOpenShift/locations/hcpOperatorIdentityRoleSets"), "role set Type should match expected value")

			// Validate Properties contain operator arrays
			Expect(roleSet.Properties.ControlPlaneOperators).NotTo(BeNil(), "controlPlaneOperators should not be nil")
			Expect(roleSet.Properties.DataPlaneOperators).NotTo(BeNil(), "dataPlaneOperators should not be nil")
			Expect(roleSet.Properties.ControlPlaneOperators).NotTo(BeEmpty(), "should have at least one control plane operator")
			Expect(roleSet.Properties.DataPlaneOperators).NotTo(BeEmpty(), "should have at least one data plane operator")
		})
})
