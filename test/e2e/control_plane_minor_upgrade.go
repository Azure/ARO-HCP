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
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/blang/semver/v4"
	"github.com/google/uuid"

	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/client-go/kubernetes"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"

	cvocincinnati "github.com/openshift/cluster-version-operator/pkg/cincinnati"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/upgradecontrollers"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/cincinnati"
	hcpsdk20240610preview "github.com/Azure/ARO-HCP/test/sdk/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

var _ = Describe("Customer", func() {
	DescribeTable("should be able to successfully upgrade control plane minor version",
		func(ctx context.Context, targetMinor string) {
			channelGroup := framework.DefaultOpenshiftChannelGroup()
			targetVer := api.Must(semver.ParseTolerant(targetMinor))
			targetPlusOneVer := semver.Version{Major: targetVer.Major, Minor: targetVer.Minor + 1}
			if targetMinor == "4.22" {
				targetPlusOneVer = semver.Version{Major: 5, Minor: 0}
			}

			var previousMinor semver.Version
			if targetMinor == "5.0" {
				previousMinor = semver.Version{Major: 4, Minor: 22}
			} else {
				previousMinor = semver.Version{Major: targetVer.Major, Minor: targetVer.Minor - 1}
			}

			var installVersion *semver.Version
			cincinnatiClient := cvocincinnati.NewClient(uuid.NameSpaceDNS, &http.Transport{}, "ARO-HCP", cincinnati.NewAlwaysConditionRegistry())

			possibleInstallVersions, err := framework.GetAllVersionsInMinorStartingWith(ctx, channelGroup, previousMinor.String())
			if cincinnati.IsCincinnatiVersionNotFoundError(err) {
				Skip(fmt.Sprintf("Cincinnati returned version not found for previous minor %s on channel %s: %v",
					previousMinor.String(),
					channelGroup, err))
			}
			Expect(err).NotTo(HaveOccurred())

			for _, possibleInstallVersion := range possibleInstallVersions {
				possibleUpgradeVersions, err := upgradecontrollers.FindAllUpgradeTargetVersionsInMinor(ctx, cincinnatiClient, channelGroup, targetVer, []semver.Version{possibleInstallVersion})
				if cincinnati.IsCincinnatiVersionNotFoundError(err) {
					Skip(fmt.Sprintf("Cincinnati returned version not found for target minor %s on channel %s: %v",
						targetVer.String(),
						channelGroup, err))
				}
				Expect(err).NotTo(HaveOccurred())

				for _, possibleUpgradeVersion := range possibleUpgradeVersions {
					possibleNextUpgradeVersions, err := upgradecontrollers.FindAllUpgradeTargetVersionsInMinor(ctx, cincinnatiClient, channelGroup, targetPlusOneVer, []semver.Version{possibleUpgradeVersion})
					if cincinnati.IsCincinnatiVersionNotFoundError(err) {
						// in this case we allow it because without a 4.y+2, we allow any 4.y+1
						installVersion = &possibleInstallVersion
						break
					}
					Expect(err).NotTo(HaveOccurred())
					if len(possibleNextUpgradeVersions) > 0 {
						// in this case we allow it because the possibleInstallVersion has a possibleUpgradeVersion that can upgrade to 4.y+2
						installVersion = &possibleInstallVersion
						break
					}
				}
				if installVersion != nil {
					break
				}
			}

			tc := framework.NewTestContext()
			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, 60*time.Second)
				Expect(err).NotTo(HaveOccurred())
			}

			versionLabel := strings.ReplaceAll(targetMinor, ".", "-")
			suffix := rand.String(6)
			clusterName := "cp-ystream-upgrade-" + versionLabel + "-" + suffix

			By("creating resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "rg-cp-ystream-upgrade-"+versionLabel, tc.Location())
			Expect(err).NotTo(HaveOccurred())

			By("creating cluster parameters at install (previous minor) version")
			clusterParams := framework.NewDefaultClusterParams()
			clusterParams.ClusterName = clusterName
			clusterParams.OpenshiftVersionId = fmt.Sprintf("%d.%d", installVersion.Major, installVersion.Minor)
			clusterParams.ChannelGroup = channelGroup
			clusterParams.ManagedResourceGroupName = framework.SuffixName(*resourceGroup.Name+"-cp-ystream-"+suffix, "-managed", 64)

			By("creating customer resources")
			clusterParams, err = tc.CreateClusterCustomerResources(ctx,
				resourceGroup,
				clusterParams,
				map[string]interface{}{
					"customerNsgName":        "customer-nsg-cp-ystream-" + suffix,
					"customerVnetName":       "customer-vnet-cp-ystream-" + suffix,
					"customerVnetSubnetName": "customer-vnet-subnet-cp-ystream-" + suffix,
				},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred())

			By(fmt.Sprintf("creating the HCP cluster with install version %s (previous minor %s)", installVersion,
				previousMinor.String()))
			err = tc.CreateHCPClusterFromParam(
				ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
				45*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			By("getting admin credentials")
			hcpClient := tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient()
			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster(
				ctx,
				hcpClient,
				*resourceGroup.Name,
				clusterName,
				10*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			By("verifying the cluster is viable before upgrade")
			err = verifiers.VerifyHCPCluster(ctx, adminRESTConfig)
			Expect(err).NotTo(HaveOccurred())

			Expect(ctx.Err()).NotTo(HaveOccurred())
			kubeClient, err := kubernetes.NewForConfig(adminRESTConfig)
			Expect(err).NotTo(HaveOccurred())
			preUpgradeKubeAPIServerVersion, err := kubeClient.Discovery().ServerVersion()
			Expect(err).NotTo(HaveOccurred())

			By(fmt.Sprintf("triggering control plane y-stream upgrade to %s (target minor %s)", targetMinor,
				targetVer.String()))
			update := hcpsdk20240610preview.HcpOpenShiftClusterUpdate{
				Properties: &hcpsdk20240610preview.HcpOpenShiftClusterPropertiesUpdate{
					Version: &hcpsdk20240610preview.VersionProfile{
						ID:           to.Ptr(targetMinor),
						ChannelGroup: to.Ptr(channelGroup),
					},
				},
			}
			_, err = framework.UpdateHCPCluster(ctx, hcpClient, *resourceGroup.Name, clusterName, update, 45*time.Minute)
			Expect(err).NotTo(HaveOccurred())

			By("verifying control plane reached desired version and cluster remains viable")
			Eventually(func() error {
				return verifiers.VerifyHCPCluster(ctx, adminRESTConfig,
					verifiers.VerifyKubeAPIServerServerVersionUpgraded(preUpgradeKubeAPIServerVersion),
					verifiers.VerifyHostedControlPlaneYStreamUpgrade(
						previousMinor.String(),
						targetVer.String()))
			}, 45*time.Minute, 2*time.Minute).Should(Succeed())
		},
		Entry("from 4.20 minor to 4.21 minor", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, "4.21"),
		Entry("from 4.21 minor to 4.22 minor", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, "4.22"),
		Entry("from 4.22 minor to 4.23 minor", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, "4.23"),
		Entry("from 4.22 minor to 5.0 minor", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, "5.0"),
		Entry("from 5.0 minor to 5.1 minor", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, "5.1"),
		Entry("from 5.1 minor to 5.2 minor", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, "5.2"),
		Entry("from 5.2 minor to 5.3 minor", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, "5.3"),
		Entry("from 5.3 minor to 5.4 minor", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, "5.4"),
		Entry("from 5.4 minor to 5.5 minor", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, "5.5"),
		Entry("from 5.5 minor to 5.6 minor", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, "5.6"),
		Entry("from 5.6 minor to 5.7 minor", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, "5.7"),
		Entry("from 5.7 minor to 5.8 minor", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, "5.8"),
		Entry("from 5.8 minor to 5.9 minor", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, "5.9"),
		Entry("from 5.9 minor to 5.10 minor", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, "5.10"),
		Entry("from 5.10 minor to 5.11 minor", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, "5.11"),
		Entry("from 5.11 minor to 5.12 minor", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, "5.12"),
	)
})
