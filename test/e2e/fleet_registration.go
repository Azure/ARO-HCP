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
	"time"

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
		labels.MIContainers(0),
		func(ctx context.Context) {
			tc := framework.NewTestContext()

			By("resolving current Azure identity")
			currentIdentity, err := tc.GetCurrentAzureIdentityDetails(ctx)
			Expect(err).NotTo(HaveOccurred(), "failed to get current Azure identity details")

			By("waiting for stamps to be registered with ready management clusters")
			Eventually(func(g Gomega) {
				stamps, err := tc.ListStamps(ctx, currentIdentity)
				g.Expect(err).NotTo(HaveOccurred(), "failed to list stamps")
				g.Expect(stamps).NotTo(BeEmpty(), "no stamps found — registration may not have run")

				for _, s := range stamps {
					g.Expect(s.ResourceID).NotTo(BeEmpty(), "stamp resourceId must not be empty")

					approvedCondition := apimeta.FindStatusCondition(s.Status.Conditions, string(fleet.StampConditionApproved))
					g.Expect(approvedCondition).NotTo(BeNil(), "stamp %s must have Approved condition", s.ResourceID)
					g.Expect(approvedCondition.Status).To(Equal(metav1.ConditionTrue), "stamp %s must be approved", s.ResourceID)

					stampResourceID, err := azcorearm.ParseResourceID(s.ResourceID)
					g.Expect(err).NotTo(HaveOccurred(), "failed to parse stamp resource ID %q", s.ResourceID)
					stampIdentifier := stampResourceID.Name

					managementCluster, err := tc.GetManagementCluster(ctx, stampIdentifier, fleet.ManagementClusterResourceName, currentIdentity)
					g.Expect(err).NotTo(HaveOccurred(), "failed to get management cluster for stamp %s", stampIdentifier)

					g.Expect(managementCluster.ResourceID).NotTo(BeEmpty(), "management cluster resourceId must not be empty")

					readyCondition := apimeta.FindStatusCondition(managementCluster.Status.Conditions, string(fleet.ManagementClusterConditionReady))
					g.Expect(readyCondition).NotTo(BeNil(), "management cluster %s must have Ready condition", stampIdentifier)
					g.Expect(readyCondition.Status).To(Equal(metav1.ConditionTrue), "management cluster %s must be ready", stampIdentifier)
				}
			}, 15*time.Minute, 30*time.Second).Should(Succeed(), "fleet registration did not complete in time")
		},
	)
})
