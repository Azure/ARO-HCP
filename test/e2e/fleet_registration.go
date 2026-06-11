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
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/fleet"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
)

var _ = Describe("Fleet", func() {
	BeforeEach(func() {
		// do nothing.  per test initialization usually ages better than shared.
	})

	It("should have registered stamps with ready management clusters",
		labels.RequireNothing,
		labels.High,
		labels.Positive,
		labels.CoreInfraService,
		labels.DevelopmentOnly,
		labels.AroRpApiCompatible,
		func(ctx context.Context) {
			tc := framework.NewTestContext()

			By("resolving current Azure identity")
			currentIdentity, err := tc.GetCurrentAzureIdentityDetails(ctx)
			Expect(err).NotTo(HaveOccurred(), "failed to get current Azure identity details")

			By("listing all stamps via admin API")
			stamps, err := tc.ListStamps(ctx, currentIdentity)
			Expect(err).NotTo(HaveOccurred(), "failed to list stamps")
			Expect(stamps).NotTo(BeEmpty(), "no stamps found — registration may not have run")

			for _, s := range stamps {
				Expect(s.ResourceID).NotTo(BeEmpty(), "stamp resourceId must not be empty")
				By(fmt.Sprintf("verifying stamp %s", s.ResourceID))

				approvedCondition := apimeta.FindStatusCondition(s.Status.Conditions, string(fleet.StampConditionApproved))
				Expect(approvedCondition).NotTo(BeNil(), "stamp must have Approved condition")
				Expect(approvedCondition.Status).To(Equal(metav1.ConditionTrue), "stamp must be approved")

				stampResourceID, err := azcorearm.ParseResourceID(s.ResourceID)
				Expect(err).NotTo(HaveOccurred(), "failed to parse stamp resource ID %q", s.ResourceID)
				stampIdentifier := stampResourceID.Name

				By(fmt.Sprintf("verifying management cluster for stamp %s", stampIdentifier))
				managementCluster, err := tc.GetManagementCluster(ctx, stampIdentifier, fleet.ManagementClusterResourceName, currentIdentity)
				Expect(err).NotTo(HaveOccurred(), "failed to get management cluster for stamp %s", stampIdentifier)

				Expect(managementCluster.ResourceID).NotTo(BeEmpty(), "management cluster resourceId must not be empty")

				readyCondition := apimeta.FindStatusCondition(managementCluster.Status.Conditions, string(fleet.ManagementClusterConditionReady))
				Expect(readyCondition).NotTo(BeNil(), "management cluster must have Ready condition")
				Expect(readyCondition.Status).To(Equal(metav1.ConditionTrue), "management cluster must be ready")
			}
		},
	)
})
