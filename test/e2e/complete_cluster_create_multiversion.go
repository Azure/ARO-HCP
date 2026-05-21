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
	"embed"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/blang/semver/v4"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/rand"

	configv1 "github.com/openshift/api/config/v1"
	configv1client "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"

	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

//go:embed test-artifacts
var TestArtifactsFS embed.FS

var _ = Describe("ARO-HCP", func() {

	DescribeTable("should be able to perform a control plane and node pool install with OCP "+framework.DefaultOpenshiftChannelGroup()+" channel",
		func(ctx context.Context, version string) {
			// Use channel group from environment variable, or default to stable
			var channelGroup = framework.DefaultOpenshiftChannelGroup()
			var nodePoolChannelGroup = framework.DefaultOpenshiftNodePoolChannelGroup()

			if version == "4.23" || version == "5.0" {
				// Nightly channel picks up the Hypershift fix for Swift NIC scheduling overrides
				// (https://github.com/openshift/hypershift/pull/8552) and test it against the CS bump.
				channelGroup = "nightly"
				nodePoolChannelGroup = "nightly"
			}
			customerNetworkSecurityGroupName := "customer-nsg-" + channelGroup + "-"
			customerVnetName := "customer-vnet-" + channelGroup + "-"
			customerVnetSubnetName := "customer-vnet-subnet-" + channelGroup + "-"
			customerClusterNamePrefix := "cluster-" + channelGroup + "-"

			versionLabel := strings.ReplaceAll(version, ".", "-") // e.g. "4.20" -> "4-20"
			suffix := rand.String(6)
			clusterName := customerClusterNamePrefix + versionLabel + "-" + suffix
			clusterParams := framework.NewDefaultClusterParams()
			clusterParams.ClusterName = clusterName
			clusterParams.OpenshiftVersionId = version
			clusterParams.ChannelGroup = channelGroup
			openShiftControlPlaneVersion, err := framework.GetLatestInstallVersion(ctx, clusterParams.ChannelGroup, version)
			if err != nil {
				if errors.Is(err, framework.ErrNightlyReleaseStreamNotFound) || errors.Is(err, framework.ErrNoAcceptedNightlyTags) || errors.Is(err, framework.ErrVersionNotFound) {
					Skip(fmt.Sprintf("No install version found for %s in %s channel (%s)", version, clusterParams.ChannelGroup, err.Error()))
				} else {
					Fail(fmt.Sprintf("failed to get latest install version for %s channel: %s", clusterParams.ChannelGroup, err.Error()))
				}
			}
			clusterParams.OpenshiftVersionId = openShiftControlPlaneVersion

			tc := framework.NewTestContext()
			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, 60*time.Second)
				Expect(err).NotTo(HaveOccurred(), "failed to assign pooled identity containers")
			}

			By("creating resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "rg-"+channelGroup+"-"+versionLabel, tc.Location())
			Expect(err).NotTo(HaveOccurred(), "failed to create resource group for version %s", version)

			managedResourceGroupName := framework.SuffixName(*resourceGroup.Name+"-"+channelGroup+"-"+suffix, "-managed", 64)
			clusterParams.ManagedResourceGroupName = managedResourceGroupName

			By("creating customer resources")
			clusterParams, err = tc.CreateClusterCustomerResources(ctx,
				resourceGroup,
				clusterParams,
				map[string]interface{}{
					"customerNsgName":        customerNetworkSecurityGroupName + suffix,
					"customerVnetName":       customerVnetName + suffix,
					"customerVnetSubnetName": customerVnetSubnetName + suffix,
				},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create customer resources for cluster %q", clusterName)

			// OCP 4.23 and 5.0 use v20251223preview to create Swift-based clusters so we can validate
			// Swift NIC scheduling end-to-end: Hypershift sets aro.openshift.io/swift-nic limits overrides
			// (https://github.com/openshift/hypershift/pull/8552), and Cluster Service applies the matching
			// NIC resource request (https://redhat.atlassian.net/browse/ARO-27209).
			if version == "4.23" || version == "5.0" {
				By(fmt.Sprintf("creating a Swift cluster on version '%s' and channel group '%s'", clusterParams.OpenshiftVersionId, clusterParams.ChannelGroup))
				clusterResource, err := framework.BuildHCPCluster20251223FromParams(clusterParams, tc.Location(), nil)
				Expect(err).NotTo(HaveOccurred(), "Swift cluster resource for %s should build from params", clusterName)
				clientFactory, err := tc.Get20251223ClientFactory(ctx)
				Expect(err).NotTo(HaveOccurred(), "Get20251223ClientFactory: client factory should be obtained for Swift cluster %s", clusterName)
				_, err = framework.CreateHCPCluster20251223AndWait(
					ctx,
					GinkgoLogr,
					clientFactory.NewHcpOpenShiftClustersClient(),
					*resourceGroup.Name,
					clusterName,
					clusterResource,
					framework.ClusterCreationTimeout,
				)
				Expect(err).NotTo(HaveOccurred(), "Swift cluster %s/%s should provision", *resourceGroup.Name, clusterName)
			} else {
				By(fmt.Sprintf("creating the HCP cluster with version '%s' on %s channel", clusterParams.OpenshiftVersionId, channelGroup))
				err = tc.CreateHCPClusterFromParam(
					ctx,
					GinkgoLogr,
					*resourceGroup.Name,
					clusterParams,
					framework.ClusterCreationTimeout,
				)
				Expect(err).NotTo(HaveOccurred(), "HCP cluster %s/%s should provision", *resourceGroup.Name, clusterName)
			}

			By("verifying the cluster is viable")
			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster(
				ctx,
				tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
				*resourceGroup.Name,
				clusterName,
				10*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to get admin REST config for cluster %q", clusterName)
			err = verifiers.VerifyHCPCluster(ctx, adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to verify HCP cluster %q is viable", clusterName)

			nodePoolName := "np-" + suffix
			nodePoolParams := framework.NewDefaultNodePoolParams()
			nodePoolParams.ClusterName = clusterName
			nodePoolParams.NodePoolName = nodePoolName
			nodePoolParams.ChannelGroup = nodePoolChannelGroup
			// Calculate the node pool version
			configClient, err := configv1client.NewForConfig(adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to create OpenShift config client for cluster %q", clusterName)
			clusterVersion, err := configClient.ClusterVersions().Get(ctx, "version", metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred(), "failed to get ClusterVersion for cluster %q", clusterName)
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
				Skip(fmt.Sprintf("No node pool install version found for %s in %s channel", version, nodePoolChannelGroup))
			}
			nodePoolParams.OpenshiftVersionId = parseableVersions[0]

			By(fmt.Sprintf("creating node pool %q with version '%s' on %s channel", nodePoolName, nodePoolParams.OpenshiftVersionId, nodePoolChannelGroup))
			err = tc.CreateNodePoolFromParam(
				ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				managedResourceGroupName,
				clusterName,
				nodePoolParams,
				framework.NodePoolCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create node pool %q for cluster %q", nodePoolName, clusterName)

			By("verifying nodepool DiskStorageAccountType matches framework default")
			err = framework.ValidateNodePoolDiskStorageAccountType(ctx,
				tc.Get20240610ClientFactoryOrDie(ctx).NewNodePoolsClient(),
				*resourceGroup.Name,
				clusterName,
				nodePoolName,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to validate DiskStorageAccountType for node pool %q in cluster %q", nodePoolName, clusterName)

			By("verifying a simple web app can run")
			err = verifiers.VerifySimpleWebApp().Verify(ctx, adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to verify simple web app runs on cluster %q", clusterName)
		},
		Entry("for 4.20", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, "4.20"),
		Entry("for 4.21", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, "4.21"),
		Entry("for 4.22", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, "4.22"),
		Entry("for 4.23", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, "4.23"),
		Entry("for 5.0", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, "5.0"),
		Entry("for 5.1", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, "5.1"),
		Entry("for 5.2", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, "5.2"),
		Entry("for 5.3", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, "5.3"),
		Entry("for 5.4", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, "5.4"),
		Entry("for 5.5", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, "5.5"),
		Entry("for 5.6", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, "5.6"),
		Entry("for 5.7", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, "5.7"),
		Entry("for 5.8", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, "5.8"),
		Entry("for 5.9", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, "5.9"),
		Entry("for 5.10", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, "5.10"),
		Entry("for 5.11", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, "5.11"),
		Entry("for 5.12", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, "5.12"),
	)
})
