package e2e

import (
	"context"
	"fmt"
	"net/http"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/client-go/tools/clientcmd"

	operatorv1 "github.com/openshift/api/operator/v1"

	clusterversion "github.com/Azure/ARO-HCP/backend/pkg/controllers/cluster/version"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

var _ = Describe("ARO-HCP", func() {
	DescribeTable("should serve the Key Vault OneCert default ingress certificate with OCP "+framework.DefaultOpenshiftChannelGroup()+" channel",
		labels.MIContainers(1),
		func(ctx context.Context, version string) {
			clusterParams := framework.NewDefaultClusterParams20251223()
			clusterParams.DisableSwift = false
			channel := clusterParams.ChannelGroup
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
				Expect(tc.AssignIdentityContainers(ctx, 1, framework.IdentityContainerAssignmentRetryInterval)).To(Succeed(), "failed to assign pooled identity container")
			}
			By("creating cluster customer resources")
			resourceGroup, err := tc.NewResourceGroup(ctx, "ingress-cert", tc.Location())
			Expect(err).NotTo(HaveOccurred(), "failed to create resource group")
			clusterParams.ClusterName = "ingress-cert-" + rand.String(6)
			clusterParams.ManagedResourceGroupName = framework.SuffixName(*resourceGroup.Name, "-managed", 64)
			clusterParams, err = tc.CreateClusterCustomerResources20251223(ctx, resourceGroup, clusterParams, map[string]interface{}{}, TestArtifactsFS, framework.RBACScopeResourceGroup)
			Expect(err).NotTo(HaveOccurred(), "failed to create cluster customer resources")

			By("provisioning the HCP cluster and node pool through ARM")
			Expect(tc.CreateHCPClusterFromParam20251223(ctx, GinkgoLogr, *resourceGroup.Name, clusterParams, nil, framework.ClusterCreationTimeout)).To(Succeed(), "failed to provision ingress certificate cluster")
			nodePoolParams := framework.NewDefaultNodePoolParams20251223()
			nodePoolParams.ClusterName = clusterParams.ClusterName
			nodePoolParams.NodePoolName = "np-1"
			nodePoolParams.ChannelGroup = channel
			nodePoolParams.OpenshiftVersionId = clusterParams.OpenshiftVersionId
			Expect(tc.CreateNodePoolFromParam20251223(ctx, GinkgoLogr, *resourceGroup.Name, clusterParams.ManagedResourceGroupName, clusterParams.ClusterName, nodePoolParams, framework.NodePoolCreationTimeout)).To(Succeed(), "failed to provision ingress worker nodes")
			adminConfig, err := tc.GetAdminRESTConfigForHCPCluster20260901(ctx, tc.Get20260901ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(), *resourceGroup.Name, clusterParams.ClusterName, framework.GetAdminRESTConfigTimeout)
			Expect(err).NotTo(HaveOccurred(), "failed to obtain guest cluster credentials")
			Expect(verifiers.VerifyNodePoolReadyAndSchedulableNodeCount(nodePoolParams.NodePoolName, int(nodePoolParams.Replicas)).Verify(ctx, adminConfig)).To(Succeed(), "expected ready schedulable ingress worker nodes")

			By("verifying public ingress scope and the served OneCert certificate")
			Expect(verifiers.VerifyIngressControllerScope(operatorv1.ExternalLoadBalancer).Verify(ctx, adminConfig)).To(Succeed(), "default IngressController spec.endpointPublishingStrategy.loadBalancer.scope must be External")
			managementConfig, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(clientcmd.NewDefaultClientConfigLoadingRules(), &clientcmd.ConfigOverrides{}).ClientConfig()
			Expect(err).NotTo(HaveOccurred(), "failed to load provisioning management cluster kubeconfig")
			credential, err := tc.AzureCredential()
			Expect(err).NotTo(HaveOccurred(), "failed to obtain Key Vault certificate read credentials")
			subscriptionID, err := tc.SubscriptionID(ctx)
			Expect(err).NotTo(HaveOccurred(), "failed to determine cluster subscription")
			resourceID := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/%s", subscriptionID, *resourceGroup.Name, clusterParams.ClusterName)
			Expect(verifiers.VerifyIngressDefaultCertificate(managementConfig, credential, resourceID, 15*time.Minute).Verify(ctx, adminConfig)).To(Succeed(), "ingress must serve the intended Key Vault OneCert leaf with a trusted chain and matching hostname")
		},
		// Earlier release lines wait for a CPO override for hypershift #9132 across all previously-released versions; start with 5.1 for early feedback.
		Entry("for 5.1", labels.RequireNothing, labels.High, labels.Positive, labels.CreateCluster, labels.DevelopmentOnly, "5.1"),
	)
})
