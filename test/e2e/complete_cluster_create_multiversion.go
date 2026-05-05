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
	// Use channel group from environment variable, or default to stable
	var channelGroup = framework.DefaultOpenshiftChannelGroup()
	var nodePoolChannelGroup = framework.DefaultOpenshiftNodePoolChannelGroup()

	DescribeTable("should be able to perform a control plane and node pool install with OCP "+framework.DefaultOpenshiftChannelGroup()+" channel",
		func(ctx context.Context, version string) {

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
			openShiftControlPlaneVersion, err := framework.GetLatestInstallVersion(ctx, clusterParams.ChannelGroup, version)
			if err != nil {
				if errors.Is(err, framework.ErrNightlyReleaseStreamNotFound) || errors.Is(err, framework.ErrNoAcceptedNightlyTags) || errors.Is(err, framework.ErrVersionNotFound) {
					Skip(fmt.Sprintf("No install version found for %s in %s channel (%s)", version, clusterParams.ChannelGroup, err.Error()))
				} else {
					Fail(fmt.Sprintf("failed to get latest install version for %s channel: %s", clusterParams.ChannelGroup, err.Error()))
				}
			}
			// TODO: remove this filter when https://redhat.atlassian.net/browse/OCPBUGS-83564 is fixed
			calculatedControlPlaneSemver, err := semver.ParseTolerant(clusterParams.OpenshiftVersionId)
			Expect(err).NotTo(HaveOccurred(), "calculated control plane version was not semver parseable")
			if calculatedControlPlaneSemver.Major == 5 {
				Skip(fmt.Sprintf("Skipping test for control plane version %s: versions >= 5.0 are not yet supported by this test", clusterParams.OpenshiftVersionId))
			}
			clusterParams.OpenshiftVersionId = openShiftControlPlaneVersion

			tc := framework.NewTestContext()
			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, 60*time.Second)
				Expect(err).NotTo(HaveOccurred())
			}

			By("creating resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "rg-"+channelGroup+"-"+versionLabel, tc.Location())
			Expect(err).NotTo(HaveOccurred())

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
			Expect(err).NotTo(HaveOccurred())

			By(fmt.Sprintf("creating the HCP cluster with version '%s' on %s channel", clusterParams.OpenshiftVersionId, channelGroup))
			err = tc.CreateHCPClusterFromParam(
				ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
				framework.ClusterCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred())

			By("verifying the cluster is viable")
			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster(
				ctx,
				tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
				*resourceGroup.Name,
				clusterName,
				10*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())
			err = verifiers.VerifyHCPCluster(ctx, adminRESTConfig)
			Expect(err).NotTo(HaveOccurred())

			nodePoolName := "np-" + suffix
			nodePoolParams := framework.NewDefaultNodePoolParams()
			nodePoolParams.ClusterName = clusterName
			nodePoolParams.NodePoolName = nodePoolName
			// Calculate the node pool version
			configClient, err := configv1client.NewForConfig(adminRESTConfig)
			Expect(err).NotTo(HaveOccurred())
			clusterVersion, err := configClient.ClusterVersions().Get(ctx, "version", metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred())
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
				*resourceGroup.Name,
				clusterName,
				nodePoolParams,
				framework.NodePoolCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred())

			By("verifying nodepool DiskStorageAccountType matches framework default")
			err = framework.ValidateNodePoolDiskStorageAccountType(ctx,
				tc.Get20240610ClientFactoryOrDie(ctx).NewNodePoolsClient(),
				*resourceGroup.Name,
				clusterName,
				nodePoolName,
			)
			Expect(err).NotTo(HaveOccurred())

			By("verifying a simple web app can run")
			err = verifiers.VerifySimpleWebApp().Verify(ctx, adminRESTConfig)
			Expect(err).NotTo(HaveOccurred())
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
