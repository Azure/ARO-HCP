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

	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
)

var _ = Describe("Customer", func() {

	It("should be able to list available HCP OpenShift versions and validate response content",
		labels.RequireNothing,
		labels.Medium,
		labels.Positive,
		labels.AroRpApiCompatible,
		labels.MIContainers(0),
		func(ctx context.Context) {
			tc := framework.NewTestContext()

			By("listing HCP OpenShift versions")
			versionsClient := tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftVersionsClient()
			versionsPager := versionsClient.NewListPager(tc.Location(), nil)

			versions, err := versionsPager.NextPage(ctx)
			Expect(err).NotTo(HaveOccurred(), "failed to list HCP OpenShift versions")
			Expect(versions.Value).NotTo(BeEmpty(), "Should return at least one OpenShift version")

			By("validating version response structure and content")
			for _, version := range versions.Value {
				Expect(version.ID).NotTo(BeNil(), "version ID was nil")
				Expect(version.Name).NotTo(BeNil(), "version Name was nil")
				Expect(version.Properties).NotTo(BeNil(), "version Properties was nil")

				Expect(version.Properties.ChannelGroup).NotTo(BeNil(), "version %s channel group was nil", *version.Name)
				group := *version.Properties.ChannelGroup
				Expect(metadataapi.AllowedChannelGroupsWithExperimentalFlag.Has(group)).To(BeTrue(), "version %s should use a supported channel group, got %s", *version.Name, group)
				minor := *version.Name
				if group != metadataapi.ChannelGroupStable {
					Expect(minor).To(HaveSuffix("-"+group), "version %s should have its channel group suffix", *version.Name)
					minor = strings.TrimSuffix(minor, "-"+group)
				}
				Expect(minor).To(MatchRegexp(`^\d+\.\d+$`), "version %s should identify a minor version", *version.Name)
				Expect(version.Properties.Enabled).To(HaveValue(BeTrue()), "advertised version %s should be enabled", *version.Name)
				Expect(version.Properties.EndOfLifeTimestamp).To(BeNil(), "version %s should omit an unknown end-of-life timestamp", *version.Name)

				// Validate ID contains version-related path (works for both ARM and direct RP access)
				Expect(*version.ID).To(ContainSubstring("/hcpOpenShiftVersions/"), "version ID should contain the hcpOpenShiftVersions resource path")
				Expect(*version.ID).To(ContainSubstring(*version.Name), "version ID should contain the version name %s", *version.Name)

				By("getting advertised HCP OpenShift version " + *version.Name)
				got, err := versionsClient.Get(ctx, tc.Location(), *version.Name, nil)
				Expect(err).NotTo(HaveOccurred(), "failed to get advertised OpenShift version %s", *version.Name)
				Expect(got.HcpOpenShiftVersion).To(BeComparableTo(*version), "get and list should agree for OpenShift version %s", *version.Name)
			}

			By("verifying at least one version is available for cluster creation")
			Expect(len(versions.Value)).To(BeNumerically(">=", 1), "at least one cluster version should be available")
		})
})
