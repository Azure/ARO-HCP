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

	operatorv1 "github.com/openshift/api/operator/v1"

	clusterversion "github.com/Azure/ARO-HCP/backend/pkg/controllers/cluster/version"
	hcpsdk20260901preview "github.com/Azure/ARO-HCP/test/sdk/v20260901preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

var _ = Describe("ARO-HCP", func() {
	DescribeTable("should serve a valid default ingress certificate through public ingress with OCP "+framework.DefaultOpenshiftChannelGroup()+" channel",
		labels.MIContainers(1),
		func(ctx context.Context, version string) {
			const (
				customerClusterName  = "public-ingress-cert"
				customerNodePoolName = "np-1"
			)

			clusterParams := framework.NewDefaultClusterParams20260901()
			clusterParams.DisableSwift = false
			channel := clusterParams.ChannelGroup
			// Default ingress certificate support requires the CPO change linked below.
			// Skip when this release line is unavailable in the configured channel rather
			// than falling back to an older release that lacks that support.
			if channel == "nightly" {
				resolved, err := framework.GetLatestNightlyInstallVersion(ctx, channel, version)
				if framework.IsVersionNotFoundError(err) {
					Skip(fmt.Sprintf("no %s version available in configured channel %s: %v", version, channel, err))
				}
				Expect(err).NotTo(HaveOccurred(), "failed to resolve configured nightly channel")
				clusterParams.OpenshiftVersionId = resolved
			} else {
				resolved, err := framework.SelectControlPlaneVersion(ctx, http.DefaultTransport.RoundTrip, nil, channel+"-"+version, clusterversion.GetZStreamOffset(channel))
				if framework.IsVersionNotFoundError(err) || (err == nil && resolved == nil) {
					Skip(fmt.Sprintf("no %s version available in configured channel %s", version, channel))
				}
				Expect(err).NotTo(HaveOccurred(), "failed to resolve configured channel %s-%s", channel, version)
				clusterParams.OpenshiftVersionId = resolved.Version
			}

			tc := framework.NewTestContext()

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, framework.IdentityContainerAssignmentRetryInterval)
				Expect(err).NotTo(HaveOccurred(), "failed to assign pooled identity containers")
			}

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "public-ingress-cert", tc.Location())
			Expect(err).NotTo(HaveOccurred(), "failed to create resource group for public ingress test")

			By("creating cluster parameters with public ingress")
			clusterParams.ClusterName = customerClusterName
			clusterParams.ManagedResourceGroupName = framework.SuffixName(*resourceGroup.Name, "-managed", 64)
			clusterParams.IngressType = "Public"

			By("creating customer resources (infrastructure and managed identities)")
			clusterParams, err = tc.CreateClusterCustomerResources20260901(ctx,
				resourceGroup,
				clusterParams,
				map[string]interface{}{},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create customer resources for public ingress cluster")

			By("creating the HCP cluster with public ingress via v20260901preview")
			err = tc.CreateHCPClusterFromParam20260901(ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
				nil,
				framework.ClusterCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create HCP cluster %q with public ingress", customerClusterName)

			By("verifying cluster was created with public ingress type via ARM GET")
			clientFactory := tc.Get20260901ClientFactoryOrDie(ctx)
			cluster, err := clientFactory.NewHcpOpenShiftClustersClient().Get(
				ctx,
				*resourceGroup.Name,
				customerClusterName,
				nil,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to get cluster %q to verify public ingress", customerClusterName)
			Expect(cluster.Properties).ToNot(BeNil(), "cluster %q Properties was nil", customerClusterName)
			Expect(cluster.Properties.Ingress).ToNot(BeNil(), "cluster %q Properties.Ingress was nil", customerClusterName)
			Expect(cluster.Properties.Ingress.Type).ToNot(BeNil(), "cluster %q Properties.Ingress.Type was nil", customerClusterName)
			Expect(*cluster.Properties.Ingress.Type).To(Equal(hcpsdk20260901preview.IngressTypePublic),
				"cluster %q ingress type should be Public", customerClusterName)
			GinkgoLogr.Info("Cluster created with public ingress", "clusterName", customerClusterName)

			By("creating the node pool")
			nodePoolParams := framework.NewDefaultNodePoolParams20260901()
			nodePoolParams.ClusterName = customerClusterName
			nodePoolParams.NodePoolName = customerNodePoolName
			nodePoolParams.Replicas = int32(2)
			nodePoolParams.ChannelGroup = channel
			nodePoolParams.OpenshiftVersionId = clusterParams.OpenshiftVersionId

			err = tc.CreateNodePoolFromParam20260901(ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams.ManagedResourceGroupName,
				customerClusterName,
				nodePoolParams,
				framework.NodePoolCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create node pool %q for public ingress cluster %q",
				customerNodePoolName, customerClusterName)

			By("getting admin credentials for the cluster")
			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster20260901(
				ctx,
				tc.Get20260901ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
				*resourceGroup.Name,
				customerClusterName,
				framework.GetAdminRESTConfigTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to get admin REST config for public ingress cluster %q", customerClusterName)

			By("ensuring the cluster is viable (basic v2026 API verification)")
			err = verifiers.VerifyHCPCluster(ctx, adminRESTConfig,
				verifiers.VerifyNodePoolReadyAndSchedulableNodeCount(customerNodePoolName, int(nodePoolParams.Replicas)),
				verifiers.VerifyIngressControllerScope(operatorv1.ExternalLoadBalancer),
			)
			Expect(err).NotTo(HaveOccurred(), "failed to verify cluster health or IngressController scope for cluster %q", customerClusterName)
			GinkgoLogr.Info("Cluster health and IngressController scope verified")

			By("deploying a sample web app to verify ingress connectivity")
			sampleApp, err := framework.DeploySampleApp(ctx, adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to deploy sample web app for ingress connectivity test")

			appURL := "https://" + sampleApp.RouteHost
			GinkgoLogr.Info("Sample app deployed", "url", appURL)

			By("verifying sample app HTTPS reachability with a valid, publicly trusted default ingress certificate")
			probe := func(ctx context.Context) error {
				return verifiers.ProbeIngressCertificate(ctx, sampleApp.RouteHost)
			}
			err = verifiers.VerifyIngressDefaultCertificate(probe, framework.IngressCertificateVerificationTimeout).Verify(ctx, adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "sample app must return HTTP 200 over trusted HTTPS through public ingress")

		},
		// Add earlier release lines only once a CPO override for https://github.com/openshift/hypershift/pull/9132
		// exists across all previously-released versions; start with 5.1 for early feedback.
		Entry("for 5.1", labels.RequireNothing, labels.High, labels.Positive, labels.CreateCluster, "5.1"),
	)
})
