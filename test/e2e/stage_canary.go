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
	"os"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/rand"

	configv1client "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"

	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

// DO NOT MERGE: one control plane per version, no worker pool or workload/upgrade tests.
var _ = Describe("Stage canary", func() {
	DescribeTable("should provision a retained control plane",
		labels.RequireNothing,
		labels.Low,
		labels.Positive,
		labels.StageAndProdOnly,
		labels.MIContainers(0), // Dedicated identities, not leased MI containers.
		func(ctx context.Context, minor string) {
			if !framework.StageCanaryEnabled() {
				Skip("retained canaries are only provisioned by the explicitly selected Stage suite")
			}
			tc := framework.NewTestContext()
			Expect(tc.UsePooledIdentities()).To(BeFalse(), "retained canary must not depend on pooled identities")
			Expect(os.Getenv("CUSTOMER_SUBSCRIPTION")).To(Equal(framework.StageCanarySubscription), "canaries must use the subscription covered by Stage expiry cleanup")
			Expect(tc.Location()).To(Equal("westus3"), "all canaries must provision in the same Stage region")

			By("creating one resource group and dedicated customer resources")
			group, err := tc.NewResourceGroup(ctx, framework.StageCanaryResourceGroupPrefix, tc.Location())
			Expect(err).NotTo(HaveOccurred(), "failed to create the canary resource group")
			params := framework.NewDefaultClusterParams20240610()
			params.ClusterName = "stage-canary-" + rand.String(6)
			params.ManagedResourceGroupName = framework.SuffixName(*group.Name, "-managed", 64)
			params.ChannelGroup = "stable"
			params.OpenshiftVersionId = minor
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
			configClient, err := configv1client.NewForConfig(adminConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to create the OpenShift configuration client")
			var resolvedVersion string
			Eventually(ctx, func(g Gomega) {
				version, err := configClient.ClusterVersions().Get(ctx, "version", metav1.GetOptions{})
				g.Expect(err).NotTo(HaveOccurred(), "failed to read the canary ClusterVersion")
				resolvedVersion = version.Status.Desired.Version
				g.Expect(resolvedVersion).To(HavePrefix(minor+"."), "canary must resolve to the requested OpenShift minor")
			}, 5*time.Minute, framework.StandardPollInterval).Should(Succeed(), "canary did not report the requested OpenShift minor %s", minor)

			By("retaining the verified canary for later testing with a bounded expiry")
			expires, err := tc.RetainStageCanaryResourceGroup(ctx, *group.Name)
			Expect(err).NotTo(HaveOccurred(), "failed to retain the canary safely")
			GinkgoLogr.Info("retained Stage canary; delete after testing or let expiry cleanup remove it",
				"resourceGroup", *group.Name, "cluster", params.ClusterName,
				"location", tc.Location(), "version", resolvedVersion, "deleteAfter", expires.Format(time.RFC3339))
		},
		Entry("for OpenShift 4.20", "4.20"),
		Entry("for OpenShift 4.21", "4.21"),
		Entry("for OpenShift 4.22", "4.22"),
	)
})
