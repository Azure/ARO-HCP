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
	"sort"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/blang/semver/v4"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/rand"

	configv1 "github.com/openshift/api/config/v1"
	configv1client "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"

	clusterversion "github.com/Azure/ARO-HCP/backend/pkg/controllers/cluster/version"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

const eusSeededMinVersionMinor uint64 = 20

var _ = Describe("ARO-HCP", func() {

	It("should perform a control plane and node pool install on the OCP eus channel",
		labels.RequireNothing,
		labels.High,
		labels.Positive,
		labels.AroRpApiCompatible,
		labels.MIContainers(1),
		labels.StartPriority(70),
		func(ctx context.Context) {
			const channelGroup = metadataapi.ChannelGroupEus

			// EUS channels are only published for selected OpenShift minor lines, so resolve the
			// default control plane minor and verify an eus-<minor> release exists before burning
			// resources. If the channel has no release, skip rather than fail.
			defaultVersion := framework.DefaultOpenshiftControlPlaneVersionId()
			parsedDefault, err := semver.ParseTolerant(defaultVersion)
			Expect(err).NotTo(HaveOccurred(), "failed to parse default control-plane version %q", defaultVersion)

			offset := clusterversion.GetZStreamOffset(channelGroup)
			startMinor := parsedDefault.Minor
			if startMinor%2 != 0 {
				startMinor--
			}

			var desiredVersion *configv1.Release
			var minorLine string
			var resolveErrs []string
			for minor := startMinor; minor >= eusSeededMinVersionMinor; minor -= 2 {
				candidateMinor := fmt.Sprintf("%d.%d", parsedDefault.Major, minor)
				channel := fmt.Sprintf("%s-%s", channelGroup, candidateMinor)
				release, selErr := framework.SelectControlPlaneVersion(ctx, http.DefaultTransport.RoundTrip, nil, channel, offset)
				if selErr != nil {
					resolveErrs = append(resolveErrs, fmt.Sprintf("%s: %v", channel, selErr))
					continue
				}
				if release == nil {
					resolveErrs = append(resolveErrs, fmt.Sprintf("%s: no version resolved", channel))
					continue
				}
				desiredVersion = release
				minorLine = candidateMinor
				break
			}
			if desiredVersion == nil {
				Skip(fmt.Sprintf("no eus channel resolved for minors %d.%d down to %d.%d: %s", parsedDefault.Major, startMinor, parsedDefault.Major, eusSeededMinVersionMinor, strings.Join(resolveErrs, "; ")))
			}

			clusterParams := framework.NewDefaultClusterParams20251223()
			clusterParams.DisableSwift = false
			clusterParams.ChannelGroup = channelGroup
			// Install with the bare major.minor line and let the RP resolve it from the seeded
			// eus profiles, mirroring the stable/fast install path.
			clusterParams.OpenshiftVersionId = minorLine

			suffix := rand.String(6)
			versionLabel := strings.ReplaceAll(minorLine, ".", "-") // e.g. "4.20" -> "4-20"
			clusterName := "cluster-eus-" + versionLabel + "-" + suffix
			clusterParams.ClusterName = clusterName

			tc := framework.NewTestContext()
			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, framework.IdentityContainerAssignmentRetryInterval)
				Expect(err).NotTo(HaveOccurred(), "failed to assign pooled identity containers")
			}

			By("creating resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "rg-eus-"+versionLabel, tc.Location())
			Expect(err).NotTo(HaveOccurred(), "failed to create resource group for eus channel install")

			managedResourceGroupName := framework.SuffixName(*resourceGroup.Name+"-eus-"+suffix, "-managed", 64)
			clusterParams.ManagedResourceGroupName = managedResourceGroupName

			By("creating customer resources")
			clusterParams, err = tc.CreateClusterCustomerResources20251223(ctx,
				resourceGroup,
				clusterParams,
				map[string]interface{}{
					"customerNsgName":        "customer-nsg-eus-" + suffix,
					"customerVnetName":       "customer-vnet-eus-" + suffix,
					"customerVnetSubnetName": "customer-vnet-subnet-eus-" + suffix,
				},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create customer resources for cluster %q", clusterName)

			By(fmt.Sprintf("creating the HCP cluster with version '%s' on %s channel", clusterParams.OpenshiftVersionId, clusterParams.ChannelGroup))
			err = tc.CreateHCPClusterFromParam20251223(
				ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
				nil,
				framework.ClusterCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "HCP cluster %s/%s should provision", *resourceGroup.Name, clusterName)

			hcpClient := tc.Get20260901ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient()

			By("verifying the cluster is viable")
			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster20260901(
				ctx,
				hcpClient,
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

			By("confirming the RP recorded the cluster on the eus channel group")
			clusterResp, err := hcpClient.Get(ctx, *resourceGroup.Name, clusterName, nil)
			Expect(err).NotTo(HaveOccurred(), "failed to get HCP cluster %q", clusterName)
			Expect(clusterResp.Properties).NotTo(BeNil(), "cluster %q Properties was nil", clusterName)
			Expect(clusterResp.Properties.Version).NotTo(BeNil(), "cluster %q Properties.Version was nil", clusterName)
			Expect(clusterResp.Properties.Version.ChannelGroup).NotTo(BeNil(), "cluster %q Version.ChannelGroup was nil", clusterName)
			Expect(*clusterResp.Properties.Version.ChannelGroup).To(Equal(channelGroup), "cluster %q should be recorded on the eus channel group", clusterName)

			nodePoolName := "np-" + suffix
			nodePoolParams := framework.NewDefaultNodePoolParams20240610()
			nodePoolParams.ClusterName = clusterName
			nodePoolParams.NodePoolName = nodePoolName
			nodePoolParams.ChannelGroup = channelGroup
			// Calculate the node pool version from the control plane's resolved version so the node
			// pool never exceeds the control plane.
			var parseableVersions []string
			for _, h := range clusterVersion.Status.History {
				if _, err := semver.ParseTolerant(h.Version); err != nil {
					continue
				}
				parseableVersions = append(parseableVersions, h.Version)
				if h.State == configv1.CompletedUpdate {
					break
				}
			}
			sort.Slice(parseableVersions, func(i, j int) bool {
				vi, _ := semver.ParseTolerant(parseableVersions[i])
				vj, _ := semver.ParseTolerant(parseableVersions[j])
				return vi.LT(vj)
			})
			if len(parseableVersions) == 0 {
				Skip(fmt.Sprintf("No node pool install version found for %s in %s channel", minorLine, channelGroup))
			}
			nodePoolParams.OpenshiftVersionId = parseableVersions[0]

			By(fmt.Sprintf("creating node pool %q with version '%s' on %s channel", nodePoolName, nodePoolParams.OpenshiftVersionId, nodePoolParams.ChannelGroup))
			err = tc.CreateNodePoolFromParam20240610(
				ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				managedResourceGroupName,
				clusterName,
				nodePoolParams,
				framework.NodePoolCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create node pool %q for cluster %q", nodePoolName, clusterName)

			By("verifying worker nodes are Ready (proves the eus node image boots)")
			err = verifiers.VerifyNodesReady().Verify(ctx, adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "all nodes should be Ready in cluster %q", clusterName)
		})
})
