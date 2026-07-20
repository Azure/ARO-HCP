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
			clusterParams := framework.NewDefaultClusterParams20251223()

			customerNetworkSecurityGroupName := "customer-nsg-" + clusterParams.ChannelGroup + "-"
			customerVnetName := "customer-vnet-" + clusterParams.ChannelGroup + "-"
			customerVnetSubnetName := "customer-vnet-subnet-" + clusterParams.ChannelGroup + "-"
			customerClusterNamePrefix := "cluster-" + clusterParams.ChannelGroup + "-"

			versionLabel := strings.ReplaceAll(version, ".", "-") // e.g. "4.20" -> "4-20"
			suffix := rand.String(6)
			clusterName := customerClusterNamePrefix + versionLabel + "-" + suffix
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
			clusterParams.OpenshiftVersionId = openShiftControlPlaneVersion

			// OCPBUGS-98571: HyperShift HCCO forces InternalLoadBalancer scope for
			// PublicAndPrivate topology on ARO HCP, causing *.apps routes to point
			// to an unreachable internal LB IP. The fix (PR #8992) merged to main
			// on 2026-07-14 but 5.0.0-ec.4 (built 2026-07-03) does not include it.
			// Skip 5.0 until an EC build with the fix is available.
			if version == "5.0" {
				timeBombDeadline := time.Date(2026, time.August, 10, 0, 0, 0, 0, time.UTC)
				if time.Now().Before(timeBombDeadline) {
					Skip(fmt.Sprintf("5.0 candidate releases do not yet include the HCCO ingress LB scope fix (https://issues.redhat.com/browse/OCPBUGS-98571); skipping until %s", timeBombDeadline.Format(time.RFC3339)))
				}
				Fail("5.0 HCCO ingress LB scope fix (OCPBUGS-98571) still not available; remove this skip or update the deadline")
			}

			tc := framework.NewTestContext()
			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, framework.IdentityContainerAssignmentRetryInterval)
				Expect(err).NotTo(HaveOccurred(), "failed to assign pooled identity containers")
			}

			By("creating resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "rg-"+clusterParams.ChannelGroup+"-"+versionLabel, tc.Location())
			Expect(err).NotTo(HaveOccurred(), "failed to create resource group for version %s", version)

			managedResourceGroupName := framework.SuffixName(*resourceGroup.Name+"-"+clusterParams.ChannelGroup+"-"+suffix, "-managed", 64)
			clusterParams.ManagedResourceGroupName = managedResourceGroupName

			By("creating customer resources")
			clusterParams, err = tc.CreateClusterCustomerResources20251223(ctx,
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

			By("verifying the cluster is viable")
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

			nodePoolName := "np-" + suffix
			nodePoolParams := framework.NewDefaultNodePoolParams20240610()
			nodePoolParams.ClusterName = clusterName
			nodePoolParams.NodePoolName = nodePoolName
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
				Skip(fmt.Sprintf("No node pool install version found for %s in %s channel", version, nodePoolParams.ChannelGroup))
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

			By("verifying nodepool DiskStorageAccountType matches framework default")
			err = framework.ValidateNodePoolDiskStorageAccountType20240610(ctx,
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
