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

	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
)

var _ = Describe("Customer", func() {

	It("should be able to list available HCP OpenShift versions and validate response content",
		labels.RequireNothing,
		labels.Medium,
		labels.Positive,
		labels.AroRpApiCompatible,
		func(ctx context.Context) {
			tc := framework.NewTestContext()

			By("listing HCP OpenShift versions")
			versionsClient := tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftVersionsClient()
			versionsPager := versionsClient.NewListPager(tc.Location(), nil)

			versions, err := versionsPager.NextPage(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(versions.Value).NotTo(BeEmpty(), "Should return at least one OpenShift version")

			By("validating version response structure and content")
			for _, version := range versions.Value {
				Expect(version.ID).NotTo(BeNil())
				Expect(version.Name).NotTo(BeNil())
				Expect(version.Properties).NotTo(BeNil())

				// Validate version name format (should be semantic version)
				Expect(*version.Name).To(MatchRegexp(`^\d+\.\d+\.\d+`), "Version should follow semantic versioning")

				// Validate ID contains version-related path (works for both ARM and direct RP access)
				Expect(*version.ID).To(ContainSubstring("/hcpOpenShiftVersions/"))
				Expect(*version.ID).To(ContainSubstring(*version.Name))
			}

			By("verifying at least one version is available for cluster creation")
			Expect(len(versions.Value)).To(BeNumerically(">=", 1))
		})
})
