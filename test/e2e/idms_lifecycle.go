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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"

	configv1 "github.com/openshift/api/config/v1"

	hcpsdk20251223preview "github.com/Azure/ARO-HCP/test/sdk/v20251223preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

var _ = Describe("Customer", func() {

	It("should be able to create an HCP cluster and manage ImageDigestMirrors",
		labels.RequireNothing,
		labels.Critical,
		labels.Positive,
		labels.AroRpApiCompatible,
		func(ctx context.Context) {
			const (
				customerNetworkSecurityGroupName = "customer-nsg-name"
				customerVnetName                 = "customer-vnet-name"
				customerVnetSubnetName           = "customer-vnet-subnet1"
				customerClusterName              = "idms-e2e-hcp-cluster"

				idmsSource = "fake-source.example.com/fake"
				idmsMirror = "fake-mirror.example.com/fake"

				idmsSource2 = "fake-source2.example.com/fake"
				idmsMirror2 = "fake-mirror2.example.com/fake"
			)

			tc := framework.NewTestContext()

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, 60*time.Second)
				Expect(err).NotTo(HaveOccurred())
			}

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "idms", tc.Location())
			Expect(err).NotTo(HaveOccurred())

			By("creating cluster parameters")
			clusterParams := framework.NewDefaultClusterParams()
			clusterParams.ClusterName = customerClusterName
			managedResourceGroupName := framework.SuffixName(*resourceGroup.Name, "managed", 64)
			clusterParams.ManagedResourceGroupName = managedResourceGroupName

			By("creating customer resources")
			clusterParams, err = tc.CreateClusterCustomerResources(ctx,
				resourceGroup,
				clusterParams,
				map[string]interface{}{
					"customerNsgName":        customerNetworkSecurityGroupName,
					"customerVnetName":       customerVnetName,
					"customerVnetSubnetName": customerVnetSubnetName,
				},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred())

			By("creating the HCP cluster with ImageDigestMirrors via v20251223preview")
			imageDigestMirrors := []*hcpsdk20251223preview.ImageDigestMirror{
				{
					Source:  to.Ptr(idmsSource),
					Mirrors: []*string{to.Ptr(idmsMirror)},
				},
			}

			createErr := tc.CreateHCPCluster20251223FromParam(
				ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
				imageDigestMirrors,
				45*time.Minute,
			)

			var respErr *azcore.ResponseError
			if createErr != nil && errors.As(createErr, &respErr) && respErr.ErrorCode == "NoRegisteredProviderFound" {
				Fail(fmt.Sprintf("v20251223preview should be available but cluster creation failed: %v", createErr))
			}
			Expect(createErr).NotTo(HaveOccurred())

			By("verifying the cluster returns ImageDigestMirrors via GET")
			hcpClient := tc.Get20251223ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient()
			actualCluster, err := hcpClient.Get(ctx, *resourceGroup.Name, customerClusterName, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(actualCluster.Properties).NotTo(BeNil())
			Expect(actualCluster.Properties.ImageDigestMirrors).NotTo(BeEmpty())
			Expect(ptr.Deref(actualCluster.Properties.ImageDigestMirrors[0].Source, "")).To(Equal(idmsSource))
			Expect(actualCluster.Properties.ImageDigestMirrors[0].Mirrors).NotTo(BeEmpty())
			Expect(ptr.Deref(actualCluster.Properties.ImageDigestMirrors[0].Mirrors[0], "")).To(Equal(idmsMirror))

			By("getting admin credentials")
			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster(
				ctx,
				tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
				*resourceGroup.Name,
				customerClusterName,
				10*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			By("verifying basic cluster health")
			err = verifiers.VerifyHCPCluster(ctx, adminRESTConfig)
			Expect(err).NotTo(HaveOccurred())

			By("verifying customer-specified mirrors are present in the cluster ImageDigestMirrorSet")
			expectedMirrors := []verifiers.ImageDigestMirrorExpectation{
				{
					Source:             idmsSource,
					Mirrors:            []configv1.ImageMirror{idmsMirror},
					MirrorSourcePolicy: configv1.AllowContactingSource,
				},
			}
			verifier := verifiers.VerifyImageDigestMirrorSets(expectedMirrors)
			Eventually(func() error {
				err := verifier.Verify(ctx, adminRESTConfig)
				if err != nil {
					GinkgoLogr.Info("Verifier check", "name", verifier.Name(), "status", "failed", "error", err.Error())
				}
				return err
			}, 1*time.Minute, 15*time.Second).Should(Succeed(), "ImageDigestMirrorSet CRDs should exist on the hosted cluster")

			By("updating the cluster to add a second ImageDigestMirror set")
			updateAdd := hcpsdk20251223preview.HcpOpenShiftClusterUpdate{
				Properties: &hcpsdk20251223preview.HcpOpenShiftClusterPropertiesUpdate{
					ImageDigestMirrors: []*hcpsdk20251223preview.ImageDigestMirror{
						{
							Source:  to.Ptr(idmsSource),
							Mirrors: []*string{to.Ptr(idmsMirror)},
						},
						{
							Source:  to.Ptr(idmsSource2),
							Mirrors: []*string{to.Ptr(idmsMirror2)},
						},
					},
				},
			}

			updateAddResp, err := framework.UpdateHCPCluster20251223(
				ctx, hcpClient, *resourceGroup.Name, customerClusterName, updateAdd, 10*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			By("verifying the update response contains both ImageDigestMirror sets")
			Expect(updateAddResp.Properties).NotTo(BeNil())
			Expect(updateAddResp.Properties.ImageDigestMirrors).To(HaveLen(2))

			By("verifying both ImageDigestMirror sets are returned via GET")
			getAfterAdd, err := hcpClient.Get(ctx, *resourceGroup.Name, customerClusterName, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(getAfterAdd.Properties).NotTo(BeNil())
			Expect(getAfterAdd.Properties.ImageDigestMirrors).To(HaveLen(2))

			By("verifying both mirror sets are present in the cluster ImageDigestMirrorSet")
			expectedMirrorsAfterAdd := []verifiers.ImageDigestMirrorExpectation{
				{
					Source:             idmsSource,
					Mirrors:            []configv1.ImageMirror{idmsMirror},
					MirrorSourcePolicy: configv1.AllowContactingSource,
				},
				{
					Source:             idmsSource2,
					Mirrors:            []configv1.ImageMirror{idmsMirror2},
					MirrorSourcePolicy: configv1.AllowContactingSource,
				},
			}
			verifierAfterAdd := verifiers.VerifyImageDigestMirrorSets(expectedMirrorsAfterAdd)
			Eventually(func() error {
				err := verifierAfterAdd.Verify(ctx, adminRESTConfig)
				if err != nil {
					GinkgoLogr.Info("Verifier check", "name", verifierAfterAdd.Name(), "status", "failed", "error", err.Error())
				}
				return err
			}, 10*time.Minute, 15*time.Second).Should(Succeed(), "both ImageDigestMirrorSet entries should exist on the hosted cluster")

			By("updating the cluster to remove the second ImageDigestMirror set")
			updateRemove := hcpsdk20251223preview.HcpOpenShiftClusterUpdate{
				Properties: &hcpsdk20251223preview.HcpOpenShiftClusterPropertiesUpdate{
					ImageDigestMirrors: []*hcpsdk20251223preview.ImageDigestMirror{
						{
							Source:  to.Ptr(idmsSource),
							Mirrors: []*string{to.Ptr(idmsMirror)},
						},
					},
				},
			}

			updateRemoveResp, err := framework.UpdateHCPCluster20251223(
				ctx, hcpClient, *resourceGroup.Name, customerClusterName, updateRemove, 10*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			By("verifying the update response contains only the original ImageDigestMirror set")
			Expect(updateRemoveResp.Properties).NotTo(BeNil())
			Expect(updateRemoveResp.Properties.ImageDigestMirrors).To(HaveLen(1))
			Expect(ptr.Deref(updateRemoveResp.Properties.ImageDigestMirrors[0].Source, "")).To(Equal(idmsSource))

			By("verifying only the original ImageDigestMirror set is returned via GET")
			getAfterRemove, err := hcpClient.Get(ctx, *resourceGroup.Name, customerClusterName, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(getAfterRemove.Properties).NotTo(BeNil())
			Expect(getAfterRemove.Properties.ImageDigestMirrors).To(HaveLen(1))
			Expect(ptr.Deref(getAfterRemove.Properties.ImageDigestMirrors[0].Source, "")).To(Equal(idmsSource))

			By("verifying only the original mirror set remains in the cluster ImageDigestMirrorSet")
			expectedMirrorsAfterRemove := []verifiers.ImageDigestMirrorExpectation{
				{
					Source:             idmsSource,
					Mirrors:            []configv1.ImageMirror{idmsMirror},
					MirrorSourcePolicy: configv1.AllowContactingSource,
					AbsentSources:      []string{idmsSource2},
				},
			}
			verifierAfterRemove := verifiers.VerifyImageDigestMirrorSets(expectedMirrorsAfterRemove)
			Eventually(func() error {
				err := verifierAfterRemove.Verify(ctx, adminRESTConfig)
				if err != nil {
					GinkgoLogr.Info("Verifier check", "name", verifierAfterRemove.Name(), "status", "failed", "error", err.Error())
				}
				return err
			}, 10*time.Minute, 15*time.Second).Should(Succeed(), "only the original ImageDigestMirrorSet entry should remain on the hosted cluster")
		})
})
