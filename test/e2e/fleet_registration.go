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

	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
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

					approvedCondition := apimeta.FindStatusCondition(s.Status.Conditions, string(fleetapi.StampConditionApproved))
					g.Expect(approvedCondition).NotTo(BeNil(), "stamp %s must have Approved condition", s.ResourceID)
					g.Expect(approvedCondition.Status).To(Equal(metav1.ConditionTrue), "stamp %s must be approved", s.ResourceID)

					stampResourceID, err := azcorearm.ParseResourceID(s.ResourceID)
					g.Expect(err).NotTo(HaveOccurred(), "failed to parse stamp resource ID %q", s.ResourceID)
					stampIdentifier := stampResourceID.Name

					managementCluster, err := tc.GetManagementCluster(ctx, stampIdentifier, fleetapi.ManagementClusterResourceName, currentIdentity)
					g.Expect(err).NotTo(HaveOccurred(), "failed to get management cluster for stamp %s", stampIdentifier)

					g.Expect(managementCluster.ResourceID).NotTo(BeEmpty(), "management cluster resourceId must not be empty")

					readyCondition := apimeta.FindStatusCondition(managementCluster.Status.Conditions, string(fleetapi.ManagementClusterConditionReady))
					g.Expect(readyCondition).NotTo(BeNil(), "management cluster %s must have Ready condition", stampIdentifier)
					g.Expect(readyCondition.Status).To(Equal(metav1.ConditionTrue), "management cluster %s must be ready", stampIdentifier)
				}
			}, 15*time.Minute, 30*time.Second).Should(Succeed(), "fleet registration did not complete in time")
		},
	)

	It("should have scheduling data for ready management clusters",
		labels.RequireNothing,
		labels.Medium,
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

			By("waiting for scheduling documents to be populated")
			Eventually(func(g Gomega) {
				stamps, err := tc.ListStamps(ctx, currentIdentity)
				g.Expect(err).NotTo(HaveOccurred(), "failed to list stamps")
				g.Expect(stamps).NotTo(BeEmpty(), "no stamps found")

				for _, s := range stamps {
					stampResourceID, err := azcorearm.ParseResourceID(s.ResourceID)
					g.Expect(err).NotTo(HaveOccurred(), "failed to parse stamp resource ID %q", s.ResourceID)
					stampIdentifier := stampResourceID.Name

					scheduling, err := tc.GetManagementClusterScheduling(ctx, stampIdentifier, fleetapi.ManagementClusterResourceName, currentIdentity)
					g.Expect(err).NotTo(HaveOccurred(), "failed to get scheduling for stamp %s", stampIdentifier)

					g.Expect(scheduling.Conditions).NotTo(BeEmpty(), "stamp %s scheduling must have conditions", stampIdentifier)
					g.Expect(apimeta.IsStatusConditionTrue(scheduling.Conditions, fleetapi.ConditionTypeCapacityDataCurrent)).To(BeTrue(), "stamp %s CapacityDataCurrent condition must be True", stampIdentifier)
					g.Expect(apimeta.IsStatusConditionTrue(scheduling.Conditions, fleetapi.ConditionTypeScalingDataCurrent)).To(BeTrue(), "stamp %s ScalingDataCurrent condition must be True", stampIdentifier)

					g.Expect(scheduling.Capacity.Current).NotTo(BeEmpty(), "stamp %s scheduling must have current capacity", stampIdentifier)
					g.Expect(scheduling.Capacity.Current.Cpu().IsZero()).To(BeFalse(), "stamp %s scheduling CPU capacity must be positive", stampIdentifier)

					g.Expect(scheduling.Capacity.Requests).NotTo(BeEmpty(), "stamp %s scheduling must have requested resources", stampIdentifier)
					g.Expect(scheduling.Capacity.Requests.Cpu().IsZero()).To(BeFalse(), "stamp %s scheduling requested CPU must be positive", stampIdentifier)

					g.Expect(scheduling.Scaling.Max).NotTo(BeEmpty(), "stamp %s scheduling must have max scaling capacity", stampIdentifier)
					g.Expect(scheduling.Scaling.Max.Cpu().IsZero()).To(BeFalse(), "stamp %s scheduling max CPU must be positive", stampIdentifier)
					g.Expect(scheduling.Scaling.LastReportedAt).NotTo(BeNil(), "stamp %s scheduling must have scaling lastReportedAt", stampIdentifier)
				}
			}, 15*time.Minute, 30*time.Second).Should(Succeed(), "scheduling data was not populated in time")
		},
	)
})
