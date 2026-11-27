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

	"k8s.io/apimachinery/pkg/util/rand"

	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

// DO NOT MERGE: one control plane, no worker pool or workload/upgrade tests.
var _ = Describe("Stage canary", func() {
	It("should provision one retained control plane",
		labels.RequireNothing,
		labels.Low,
		labels.Positive,
		labels.StageAndProdOnly,
		labels.MIContainers(0), // Dedicated identities, not leased MI containers.
		func(ctx context.Context) {
			if !framework.StageCanaryEnabled() {
				Skip("retained canaries are only provisioned by the explicitly selected Stage suite")
			}
			tc := framework.NewTestContext()
			Expect(tc.UsePooledIdentities()).To(BeFalse(), "retained canary must not depend on pooled identities")

			By("creating one resource group and dedicated customer resources")
			group, err := tc.NewResourceGroup(ctx, framework.StageCanaryResourceGroupPrefix, tc.Location())
			Expect(err).NotTo(HaveOccurred(), "failed to create the canary resource group")
			params := framework.NewDefaultClusterParams20240610()
			params.ClusterName = "stage-canary-" + rand.String(6)
			params.ManagedResourceGroupName = framework.SuffixName(*group.Name, "-managed", 64)
			params.ChannelGroup = "stable"
			params, err = tc.CreateClusterCustomerResources20240610(ctx, group, params,
				map[string]interface{}{}, TestArtifactsFS, framework.RBACScopeResourceGroup)
			Expect(err).NotTo(HaveOccurred(), "failed to create dedicated canary customer resources")

			By("provisioning a control plane without a node pool")
			err = tc.CreateHCPClusterFromParam20240610(ctx, GinkgoLogr, *group.Name, params, framework.ClusterCreationTimeout)
			Expect(err).NotTo(HaveOccurred(), "failed to provision canary cluster %q", params.ClusterName)
			adminConfig, err := tc.GetAdminRESTConfigForHCPCluster20240610(ctx,
				tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
				*group.Name, params.ClusterName, framework.GetAdminRESTConfigTimeout)
			Expect(err).NotTo(HaveOccurred(), "failed to retrieve canary admin credentials")
			Expect(verifiers.VerifyHCPCluster(ctx, adminConfig)).To(Succeed(), "canary control plane is not viable")

			By("retaining the verified canary for later testing with a bounded expiry")
			expires, err := tc.RetainStageCanaryResourceGroup(ctx, *group.Name)
			Expect(err).NotTo(HaveOccurred(), "failed to retain the canary safely")
			GinkgoLogr.Info("retained Stage canary; delete after testing or let expiry cleanup remove it",
				"resourceGroup", *group.Name, "cluster", params.ClusterName,
				"location", tc.Location(), "deleteAfter", expires.Format(time.RFC3339))
		})
})
