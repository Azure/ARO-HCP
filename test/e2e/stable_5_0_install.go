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
	"net/http"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/blang/semver/v4"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/rand"

	configv1client "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"

	clusterversion "github.com/Azure/ARO-HCP/backend/pkg/controllers/cluster/version"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

var _ = Describe("ARO-HCP", func() {
	It("should install a cluster from the stable-5.0 channel",
		labels.RequireNothing,
		labels.Critical,
		labels.Positive,
		labels.CreateCluster,
		labels.AroRpApiCompatible,
		labels.MIContainers(1),
		func(ctx context.Context) {
			const (
				channelGroup = "stable"
				versionLine  = "5.0"
				channel      = channelGroup + "-" + versionLine
			)

			// The stable channel group requests one z-stream back. stable-5.0 can
			// have fewer releases than that offset, and the install must still
			// resolve. Keep the request on the bare 5.0 line so the backend
			// performs that selection instead of honoring an exact-version pin.
			release, err := framework.SelectControlPlaneVersion(ctx, http.DefaultTransport.RoundTrip, nil, channel, clusterversion.GetZStreamOffset(channelGroup))
			Expect(err).NotTo(HaveOccurred(), "failed to resolve a control plane version for %s", channel)
			Expect(release).NotTo(BeNil(), "no control plane release resolved for %s", channel)
			resolved, err := semver.Parse(release.Version)
			Expect(err).NotTo(HaveOccurred(), "failed to parse resolved control plane version %q", release.Version)
			Expect(resolved.Major).To(Equal(uint64(5)), "resolved control plane version %s is not in the 5.0 line", release.Version)
			Expect(resolved.Minor).To(Equal(uint64(0)), "resolved control plane version %s is not in the 5.0 line", release.Version)

			clusterParams := framework.NewDefaultClusterParams20251223()
			clusterParams.DisableSwift = false
			clusterParams.ChannelGroup = channelGroup
			clusterParams.OpenshiftVersionId = versionLine

			suffix := rand.String(6)
			clusterName := "cluster-stable-5-0-" + suffix
			clusterParams.ClusterName = clusterName

			tc := framework.NewTestContext()
			if tc.UsePooledIdentities() {
				err = tc.AssignIdentityContainers(ctx, 1, framework.IdentityContainerAssignmentRetryInterval)
				Expect(err).NotTo(HaveOccurred(), "failed to assign pooled identity containers")
			}

			built, err := framework.BuildHCPClusterFromParams20251223(clusterParams, tc.Location(), nil)
			Expect(err).NotTo(HaveOccurred(), "failed to build the stable-5.0 cluster request")
			Expect(built.Properties).NotTo(BeNil(), "built cluster Properties was nil")
			Expect(built.Properties.Version).NotTo(BeNil(), "built cluster Properties.Version was nil")
			Expect(*built.Properties.Version.ID).To(Equal(versionLine), "stable-5.0 install must request the bare %s line", versionLine)
			Expect(*built.Properties.Version.ChannelGroup).To(Equal(channelGroup), "stable-5.0 install must request the stable channel group")
			Expect(built.Tags).NotTo(HaveKey(metadataapi.TagClusterControlPlaneExactVersion), "stable-5.0 install must not pin an exact control plane version")

			By("creating resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "rg-stable-5-0-", tc.Location())
			Expect(err).NotTo(HaveOccurred(), "failed to create resource group for stable-5.0 cluster")

			managedResourceGroupName := framework.SuffixName(*resourceGroup.Name+"-"+suffix, "-managed", 64)
			clusterParams.ManagedResourceGroupName = managedResourceGroupName

			By("creating customer resources")
			clusterParams, err = tc.CreateClusterCustomerResources20251223(ctx,
				resourceGroup,
				clusterParams,
				map[string]interface{}{
					"customerNsgName":        "customer-nsg-stable-5-0-" + suffix,
					"customerVnetName":       "customer-vnet-stable-5-0-" + suffix,
					"customerVnetSubnetName": "customer-vnet-subnet-stable-5-0-" + suffix,
				},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create customer resources for cluster %q", clusterName)

			By(fmt.Sprintf("creating the HCP cluster at version %s on the %s channel", versionLine, channelGroup))
			err = tc.CreateHCPClusterFromParam20251223(
				ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
				nil,
				framework.ClusterCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "HCP cluster %s/%s should provision on %s", *resourceGroup.Name, clusterName, channel)

			By("verifying the cluster is viable and running 5.0")
			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster20240610(
				ctx,
				tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
				*resourceGroup.Name,
				clusterName,
				framework.GetAdminRESTConfigTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to get admin REST config for cluster %q", clusterName)
			err = verifiers.VerifyHCPCluster(ctx, adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to verify HCP cluster %q is viable", clusterName)

			configClient, err := configv1client.NewForConfig(adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to create OpenShift config client for cluster %q", clusterName)
			clusterVersion, err := configClient.ClusterVersions().Get(ctx, "version", metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred(), "failed to get ClusterVersion for cluster %q", clusterName)
			Expect(clusterVersion.Status.Desired.Version).NotTo(BeEmpty(), "ClusterVersion desired version was empty for cluster %q", clusterName)
			installed, err := semver.ParseTolerant(clusterVersion.Status.Desired.Version)
			Expect(err).NotTo(HaveOccurred(), "failed to parse installed control plane version %q", clusterVersion.Status.Desired.Version)
			Expect(installed.Major).To(Equal(uint64(5)), "installed control plane version %s is not in the 5.0 line", clusterVersion.Status.Desired.Version)
			Expect(installed.Minor).To(Equal(uint64(0)), "installed control plane version %s is not in the 5.0 line", clusterVersion.Status.Desired.Version)

			nodePoolName := "np-" + suffix
			nodePoolParams := framework.NewDefaultNodePoolParams20251223()
			nodePoolParams.ClusterName = clusterName
			nodePoolParams.NodePoolName = nodePoolName
			nodePoolParams.ChannelGroup = channelGroup
			nodePoolParams.OpenshiftVersionId = release.Version

			By(fmt.Sprintf("creating node pool %q at version %s on the %s channel", nodePoolName, release.Version, channelGroup))
			err = tc.CreateNodePoolFromParam20251223(
				ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				managedResourceGroupName,
				clusterName,
				nodePoolParams,
				framework.NodePoolCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create node pool %q for cluster %q", nodePoolName, clusterName)

			By("verifying worker nodes are Ready")
			err = verifiers.VerifyNodesReady().Verify(ctx, adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "all nodes should be Ready in cluster %q", clusterName)
		})
})
