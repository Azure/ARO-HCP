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
	"errors"
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/blang/semver/v4"

	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/apimachinery/pkg/util/wait"

	azcore "github.com/Azure/azure-sdk-for-go/sdk/azcore"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/cincinatti"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

var _ = Describe("Service Provider", func() {
	DescribeTable("should upgrade the control plane z-stream automatically on behalf of the customer",
		func(ctx context.Context, minorVersion string, baseInstallVersion string) {
			const (
				customerNetworkSecurityGroupName = "customer-nsg-zstream-"
				customerVnetName                 = "customer-vnet-zstream-"
				customerVnetSubnetName           = "customer-vnet-subnet-zstream-"
				customerClusterNamePrefix        = "cluster-zstream-"
			)

			tc := framework.NewTestContext()

			if len(baseInstallVersion) == 0 {
				baseInstallVersion = minorVersion // set it to minor so that we defaul to .0 as the patch version
			}
			configuredVersionID := api.Must(semver.ParseTolerant(baseInstallVersion))
			installVersion, hasUpgradePath, err := framework.GetInstallVersionForZStreamUpgrade(ctx, "candidate", configuredVersionID.String())
			if err != nil {
				if cincinatti.IsCincinnatiVersionNotFoundError(err) {
					Skip(fmt.Sprintf("Cincinnati returned version not found for configured id %s (minor %s)", configuredVersionID, minorVersion))
				}
				Expect(err).NotTo(HaveOccurred())
			}
			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, 60*time.Second)
				Expect(err).NotTo(HaveOccurred())
			}

			versionLabel := strings.ReplaceAll(minorVersion, ".", "-") // e.g. "4.20" -> "4-20"
			suffix := rand.String(6)
			clusterName := customerClusterNamePrefix + versionLabel + "-" + suffix
			clusterParams := framework.NewDefaultClusterParams()
			clusterParams.ClusterName = clusterName
			clusterParams.OpenshiftVersionId = installVersion

			// We use the candidate channel to potentially catch early z-stream upgrades.
			// issues before they reach stable.
			By("using the candidate channel")
			clusterParams.ChannelGroup = "candidate"

			By("creating resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "rg-zstream-upgrade-"+versionLabel, tc.Location())
			Expect(err).NotTo(HaveOccurred())

			managedResourceGroupName := framework.SuffixName(*resourceGroup.Name+"-zstream-"+suffix, "-managed", 64)
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

			By(fmt.Sprintf("creating the HCP cluster with version '%s' on candidate channel", installVersion))
			// Cincinnati can advertise a z-stream build before Cluster Service has registered that version, so
			// create fails with InvalidRequestContent until the worker in CS catches up—rare but flaky. We retry with backoff
			// for up to 5m instead of failing the whole test. See https://github.com/Azure/ARO-HCP/pull/4621#discussion_r2986322194
			// Drop this retry once cluster creation runs in the backend (https://github.com/Azure/ARO-HCP/pull/4477).
			stopRetryingAfter := time.Now().Add(5 * time.Minute)
			backoffErr := wait.ExponentialBackoffWithContext(ctx, wait.Backoff{
				Duration: 5 * time.Second,
				Factor:   2,
				Jitter:   0.1,
				Steps:    25,
				Cap:      45 * time.Second,
			}, func(_ context.Context) (done bool, err error) {
				createErr := tc.CreateHCPClusterFromParam(
					ctx,
					GinkgoLogr,
					*resourceGroup.Name,
					clusterParams,
					framework.ClusterCreationTimeout,
				)
				if createErr == nil {
					return true, nil
				}
				var azureErr *azcore.ResponseError
				// Example ARM body: { "error": { "code": "InvalidRequestContent", "message": "Version 'openshift-v4.y.z-candidate' doesn't exist" } }
				shouldRetryMissingVersionInCS := errors.As(createErr, &azureErr) &&
					azureErr.ErrorCode == "InvalidRequestContent" &&
					strings.Contains(azureErr.Error(), "Version") &&
					strings.Contains(azureErr.Error(), "openshift-v") &&
					strings.Contains(azureErr.Error(), "doesn't exist")
				if shouldRetryMissingVersionInCS {
					if time.Now().After(stopRetryingAfter) {
						return false, fmt.Errorf("giving up after %v waiting for OpenShift version in Cluster Service: %w", 5*time.Minute, createErr)
					}
					GinkgoLogr.Info("OpenShift version not yet in Cluster Service; retrying cluster create", "error", createErr)
					return false, nil
				}
				return false, createErr
			})
			Expect(backoffErr).NotTo(HaveOccurred())

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

			if !hasUpgradePath {
				By("skipping z-stream upgrade verification: no upgrade path (cluster installed at latest)")
				return
			}

			By("verifying that only a z-stream upgrade was performed")
			Eventually(func() error {
				return verifiers.VerifyHCPCluster(ctx, adminRESTConfig, verifiers.VerifyHostedControlPlaneZStreamUpgradeOnly(installVersion))
			}, 40*time.Minute, 2*time.Minute).Should(Succeed())
			GinkgoLogr.Info("z-stream upgrade verification passed", "installVersion", installVersion)
		},

		// for 4.19, if we start with 4.19.0, the version that has an upgrade path to 4.19.latest is
		// 4.19.3 but cluster install on this version fails with KMS authentication problem.
		// For all the other minor versions, we can start with 4.y.0 and install the latest version in the candidate channel.
		Entry("for 4.19", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, "4.19", "4.19.25"),
		Entry("for 4.20", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, "4.20", ""),
		Entry("for 4.21", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, "4.21", ""),
		Entry("for 4.22", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, "4.22", ""),
		Entry("for 4.23", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, "4.23", ""),
	)
})
