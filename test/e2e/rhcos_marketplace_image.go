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
	"net/http"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"golang.org/x/sync/errgroup"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"

	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

// isUnrecognizedExperimentalTagError reports whether the frontend rejected the
// request because it does not recognize the marketplace-image experimental tag.
// An RP that predates the tag fails admission with this error regardless of
// whether the ExperimentalReleaseFeatures AFEC is registered, so the test cannot
// run until the frontend change has rolled out to the target environment.
func isUnrecognizedExperimentalTagError(err error) bool {
	if err == nil {
		return false
	}
	var respErr *azcore.ResponseError
	if !errors.As(err, &respErr) || respErr.StatusCode != http.StatusBadRequest {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "unrecognized experimental tag")
}

var _ = Describe("Customer", func() {
	// The marketplace-image experimental tag is rejected by any RP that predates
	// it. Skip until the frontend change has had time to reach every environment
	// including prod, then start failing so the skip cannot go stale unnoticed.
	timeBombDeadline := framework.Must(time.Parse(time.RFC3339, "2026-09-24T00:00:00Z"))

	It("should be able to create a 5.0 cluster with RHCOS marketplace image nodepools",
		labels.RequireNothing,
		labels.Critical,
		labels.Positive,
		labels.AroRpApiCompatible,
		labels.MIContainers(1),
		func(ctx context.Context) {
			const (
				customerClusterName = "rhcos-mp-cluster"
				rhel9X64URN         = "azureopenshift:aro4:aro_5-0_x64_gen2:9.8.20260428"
				rhel10X64URN        = "azureopenshift:aro4:aro_5-0_x64_gen2:10.2.20260423"
				rhel9ARM64URN       = "azureopenshift:aro4:aro_5-0_arm_gen2:9.8.20260428"
				rhel10ARM64URN      = "azureopenshift:aro4:aro_5-0_arm_gen2:10.2.20260423"
			)

			tc := framework.NewTestContext()

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, framework.IdentityContainerAssignmentRetryInterval)
				Expect(err).NotTo(HaveOccurred(), "failed to assign identity containers")
			}

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "rhcos-mp", tc.Location())
			Expect(err).NotTo(HaveOccurred(), "failed to create resource group")

			By("resolving the latest 5.0.x install version")
			clusterParams := framework.NewDefaultClusterParams20260901()
			clusterParams.ClusterName = customerClusterName
			nodePoolVersion, err := resolveNodePoolTestVersion(ctx, clusterParams.ChannelGroup, "5.0", 0)
			Expect(err).NotTo(HaveOccurred(), "failed to get latest 5.0 install version")
			if nodePoolVersion == "" {
				Skip(fmt.Sprintf("no 5.0 install version available in the %s channel group", clusterParams.ChannelGroup))
			}
			GinkgoLogr.Info(fmt.Sprintf("resolved 5.0 install version: %s", nodePoolVersion))
			clusterParams.OpenshiftVersionId = nodePoolVersion

			managedResourceGroupName := framework.SuffixName(*resourceGroup.Name, "-managed", 64)
			clusterParams.ManagedResourceGroupName = managedResourceGroupName

			By("creating customer resources")
			clusterParams, err = tc.CreateClusterCustomerResources20260901(ctx,
				resourceGroup,
				clusterParams,
				map[string]interface{}{},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create customer resources")

			By(fmt.Sprintf("creating the HCP cluster with version %s", nodePoolVersion))
			err = tc.CreateHCPClusterFromParam20260901(
				ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
				nil,
				framework.ClusterCreationTimeout,
			)
			if isAPINotDeployedError(err) {
				Skip("v20260901preview API not deployed yet, skipping")
			}
			Expect(err).NotTo(HaveOccurred(), "failed to create HCP cluster %q", customerClusterName)

			By("discovering x64 and ARM64 VM sizes")
			x64VMSize, err := tc.SelectVMSize(ctx, framework.DefaultWorkerVMSizeSelector())
			Expect(err).NotTo(HaveOccurred(), "failed to find x64 VM size")

			arm64VMSize, err := tc.SelectVMSize(ctx, framework.ARM64NodePoolVMSizeSelector())
			Expect(err).NotTo(HaveOccurred(), "failed to find ARM64 VM size")

			type npSpec struct {
				name              string
				vmSize            string
				urn               string
				expectedRHELMajor string
			}
			nodePoolSpecs := []npSpec{
				{name: "rhel9-x64", vmSize: x64VMSize, urn: rhel9X64URN, expectedRHELMajor: "9"},
				{name: "rhel10-x64", vmSize: x64VMSize, urn: rhel10X64URN, expectedRHELMajor: "10"},
				{name: "rhel9-arm64", vmSize: arm64VMSize, urn: rhel9ARM64URN, expectedRHELMajor: "9"},
				{name: "rhel10-arm64", vmSize: arm64VMSize, urn: rhel10ARM64URN, expectedRHELMajor: "10"},
			}

			allSpecs := append([]npSpec{{name: "default-np", vmSize: "", urn: "", expectedRHELMajor: ""}}, nodePoolSpecs...)

			By(fmt.Sprintf("creating all %d nodepools in parallel with version %s", len(allSpecs), nodePoolVersion))
			var wg errgroup.Group
			for _, spec := range allSpecs {
				spec := spec
				wg.Go(func() error {
					params := framework.NewDefaultNodePoolParams20260901()
					params.ClusterName = customerClusterName
					params.NodePoolName = spec.name
					params.Replicas = int32(1)
					params.OpenshiftVersionId = nodePoolVersion
					if spec.vmSize != "" {
						params.VMSize = spec.vmSize
					}
					if spec.urn != "" {
						params.Tags[metadataapi.TagNodePoolMarketplaceImage] = to.Ptr(spec.urn)
					}

					if err := tc.CreateNodePoolFromParam20260901(ctx,
						GinkgoLogr,
						*resourceGroup.Name,
						managedResourceGroupName,
						customerClusterName,
						params,
						framework.NodePoolCreationTimeout,
					); err != nil {
						return fmt.Errorf("nodepool %q: %w", spec.name, err)
					}
					return nil
				})
			}
			err = wg.Wait()
			if isUnrecognizedExperimentalTagError(err) {
				if time.Now().Before(timeBombDeadline) {
					Skip(fmt.Sprintf("frontend does not yet recognize the %s tag; skipping until %s",
						metadataapi.TagNodePoolMarketplaceImage, timeBombDeadline.Format(time.RFC3339)))
				}
				Fail(fmt.Sprintf("frontend still does not recognize the %s tag as of the %s deadline",
					metadataapi.TagNodePoolMarketplaceImage, timeBombDeadline.Format(time.RFC3339)))
			}
			Expect(err).NotTo(HaveOccurred(), "nodepool creation failed")

			By("verifying node OS images match the expected RHEL version")
			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster20240610(
				ctx,
				tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
				*resourceGroup.Name,
				customerClusterName,
				framework.GetAdminRESTConfigTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to get admin REST config for cluster %q", customerClusterName)

			var osVerifiers []verifiers.HostedClusterVerifier
			for _, spec := range nodePoolSpecs {
				osVerifiers = append(osVerifiers, verifiers.VerifyNodePoolOSImage(spec.name, spec.expectedRHELMajor))
			}
			err = verifiers.VerifyHCPCluster(ctx, adminRESTConfig, osVerifiers...)
			Expect(err).NotTo(HaveOccurred(), "node OS image verification failed")
		})
})
