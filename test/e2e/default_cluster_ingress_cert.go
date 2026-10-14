// Copyright 2025 Microsoft Corporation
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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	operatorv1 "github.com/openshift/api/operator/v1"

	clusterversion "github.com/Azure/ARO-HCP/backend/pkg/controllers/cluster/version"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

var _ = Describe("ARO-HCP", func() {
	DescribeTable("should serve the default ingress certificate",
		labels.RequireNothing,
		labels.High,
		labels.Positive,
		labels.CreateCluster,
		labels.AroRpApiCompatible,
		labels.MIContainers(1),
		func(ctx context.Context, version string) {
			const (
				customerClusterName  = "default-ingress-cert"
				customerNodePoolName = "np-1"
				ingressNamespace     = "openshift-ingress"
			)
			tc := framework.NewTestContext()
			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, framework.IdentityContainerAssignmentRetryInterval)
				Expect(err).NotTo(HaveOccurred(), "failed to assign pooled identity containers")
			}

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "ingress-cert-le", tc.Location())
			Expect(err).NotTo(HaveOccurred(), "failed to create resource group")

			By("creating cluster parameters")
			clusterParams := framework.NewDefaultClusterParams20260901()
			channel := clusterParams.ChannelGroup
			if channel == "nightly" {
				resolved, err := framework.GetLatestNightlyInstallVersion(ctx, channel, version)
				if framework.IsVersionNotFoundError(err) || framework.IsIncompatibleNightlyVersionError(err) {
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
			clusterParams.ClusterName = customerClusterName
			clusterParams.IngressType = "Public"
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

			By("creating the HCP cluster")
			err = tc.CreateHCPClusterFromParam20260901(ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
				nil,
				framework.ClusterCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create HCP cluster")

			By("creating the node pool")
			nodePoolParams := framework.NewDefaultNodePoolParams20260901()
			nodePoolParams.OpenshiftVersionId = clusterParams.OpenshiftVersionId
			nodePoolParams.ChannelGroup = clusterParams.ChannelGroup
			nodePoolParams.ClusterName = customerClusterName
			nodePoolParams.NodePoolName = customerNodePoolName
			nodePoolParams.Replicas = int32(2)
			Expect(tc.CreateNodePoolFromParam20260901(ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				managedResourceGroupName,
				customerClusterName,
				nodePoolParams,
				framework.NodePoolCreationTimeout,
			)).To(Succeed(), "failed to create node pool")

			hcpClient := tc.Get20260901ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient()
			clusterResp, err := hcpClient.Get(ctx, *resourceGroup.Name, customerClusterName, nil)
			Expect(err).NotTo(HaveOccurred(), "failed to get HCP cluster")

			By("getting admin credentials")
			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster20260901(
				ctx,
				hcpClient,
				*resourceGroup.Name,
				customerClusterName,
				framework.GetAdminRESTConfigTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to get admin REST config")

			By("ensuring the cluster is viable")
			Expect(verifiers.VerifyHCPCluster(ctx, adminRESTConfig)).To(Succeed(), "cluster viability check failed")
			Expect(verifiers.VerifyIngressControllerScope(operatorv1.ExternalLoadBalancer).Verify(ctx, adminRESTConfig)).To(Succeed(), "default ingress certificate scenario requires public ingress")

			By("getting the cluster's OIDC issuer URL and DNS domain")
			Expect(clusterResp.Properties).NotTo(BeNil(), "cluster Properties was nil")
			Expect(clusterResp.Properties.Platform).NotTo(BeNil(), "cluster Properties.Platform was nil")
			Expect(clusterResp.Properties.Platform.IssuerURL).NotTo(BeNil(), "cluster OIDC IssuerURL was nil")

			Expect(clusterResp.Properties.DNS).NotTo(BeNil(), "cluster Properties.DNS was nil")
			Expect(clusterResp.Properties.DNS.BaseDomain).NotTo(BeNil(), "cluster DNS.BaseDomain was nil")
			Expect(clusterResp.Properties.DNS.BaseDomainPrefix).NotTo(BeNil(), "cluster DNS.BaseDomainPrefix was nil")
			baseDomain := *clusterResp.Properties.DNS.BaseDomain
			baseDomainPrefix := *clusterResp.Properties.DNS.BaseDomainPrefix
			delegatedDomain := fmt.Sprintf("aro.%s.%s", baseDomainPrefix, baseDomain)
			appsWildcard := fmt.Sprintf("*.apps.%s", delegatedDomain)

			By("deploying a sample application before verifying the default ingress certificate")
			kubeClient, err := kubernetes.NewForConfig(adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to create kube client")
			sampleApp, err := framework.DeploySampleApp(ctx, adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to deploy sample application")
			DeferCleanup(func(ctx context.Context) {
				err := kubeClient.CoreV1().Namespaces().Delete(ctx, sampleApp.Namespace, metav1.DeleteOptions{})
				if !apierrors.IsNotFound(err) {
					Expect(err).NotTo(HaveOccurred(), "failed to delete sample application namespace %q", sampleApp.Namespace)
				}
			})

			By("verifying the environment wired HyperShift's default ingress secret and suppressed ACM's secret")
			Expect(verifiers.VerifyHyperShiftIngressCertificate(10*time.Minute).Verify(ctx, adminRESTConfig)).To(Succeed(), "HyperShift's ingress certificate secret must exist and be referenced by IngressController/default")
			Expect(verifiers.VerifyACMIngressCertificateAbsent().Verify(ctx, adminRESTConfig)).To(Succeed(), "ACM must not create its ingress certificate secret")

			By("verifying the served default ingress certificate covers the cluster's ingress wildcard")
			Expect(verifiers.VerifyDefaultIngressCertificate(sampleApp.RouteHost, appsWildcard, 35*time.Minute).Verify(ctx, adminRESTConfig)).To(Succeed(), "sample application must serve the delivered default certificate")
		},
		// Add earlier release lines only once a CPO override for https://github.com/openshift/hypershift/pull/9132
		// exists across all previously-released versions; start with 5.1 for early feedback.
		Entry("for 5.1", "5.1"),
	)
})
