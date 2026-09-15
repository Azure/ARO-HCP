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

	"github.com/blang/semver/v4"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/client-go/kubernetes"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"

	clusterversion "github.com/Azure/ARO-HCP/backend/pkg/controllers/cluster/version"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	hcpsdk20240610preview "github.com/Azure/ARO-HCP/test/sdk/v20240610preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

const (
	ystreamDenyExpectedNodes = 2
	ystreamDenyNodePoolName  = "np-deny-pods"
	ystreamDenyPolicyName    = "deny-pods-e2e-ystream"
	ystreamDenyPolicyMessage = "e2e: pod CREATE denied while control plane y-stream upgrade is in progress"
)

var _ = Describe("Customer", func() {
	DescribeTable("should upgrade control plane minor version when pods cannot be created",
		labels.MIContainers(1),
		func(ctx context.Context, targetMinor string) {
			const (
				customerNetworkSecurityGroupName = "customer-nsg-ystream-deny-pods-"
				customerVnetName                 = "customer-vnet-ystream-deny-pods-"
				customerVnetSubnetName           = "customer-vnet-subnet-ystream-deny-pods-"
				customerClusterNamePrefix        = "cp-ystream-deny-pods-"
			)

			channelGroup := framework.DefaultOpenshiftChannelGroup()
			upgradeVersion := metadataapi.Must(semver.ParseTolerant(targetMinor))

			var installVersion semver.Version
			if targetMinor == "5.0" {
				installVersion = semver.Version{Major: 4, Minor: 22}
			} else {
				installVersion = semver.Version{Major: upgradeVersion.Major, Minor: upgradeVersion.Minor - 1}
			}

			// Resolve the install (previous minor) and upgrade (target minor) version strings up front
			// so a missing version skips before we burn resources on cluster creation. For nightly,
			// each resolves to its exact build tag (the RP cannot resolve major.minor to a nightly
			// build). For other channel groups, each is the bare major.minor string, verified
			// resolvable via the OpenShift update service at the channel's z-stream offset.
			installVersionId := fmt.Sprintf("%d.%d", installVersion.Major, installVersion.Minor)
			upgradeVersionId := fmt.Sprintf("%d.%d", upgradeVersion.Major, upgradeVersion.Minor)
			// Node pools require a concrete Major.Minor.Patch (the RP does not resolve a
			// bare major.minor for version.id the way it does for the control plane).
			nodePoolVersionId, err := resolveNodePoolTestVersion(ctx, channelGroup, installVersionId, clusterversion.GetZStreamOffset(channelGroup))
			if err != nil {
				Skip(fmt.Sprintf("failed to resolve node pool version for %s-%s: %v", channelGroup, installVersionId, err))
			}
			if nodePoolVersionId == "" {
				Skip(fmt.Sprintf("no node pool version resolved for %s-%s", channelGroup, installVersionId))
			}
			if channelGroup == "nightly" {
				resolvedInstall, err := framework.GetLatestNightlyInstallVersion(ctx, channelGroup, installVersionId)
				if framework.IsVersionNotFoundError(err) {
					Skip(fmt.Sprintf("no nightly version for %s: %v", installVersionId, err))
				}
				Expect(err).NotTo(HaveOccurred(), "failed to resolve nightly install version for %s", installVersionId)
				installVersionId = resolvedInstall

				resolvedUpgrade, err := framework.GetLatestNightlyInstallVersion(ctx, channelGroup, upgradeVersionId)
				if framework.IsVersionNotFoundError(err) {
					Skip(fmt.Sprintf("no nightly version for %s: %v", upgradeVersionId, err))
				}
				Expect(err).NotTo(HaveOccurred(), "failed to resolve nightly upgrade version for %s", upgradeVersionId)
				upgradeVersionId = resolvedUpgrade
			} else {
				for _, minorLine := range []string{installVersionId, upgradeVersionId} {
					desiredVersion, err := framework.SelectControlPlaneVersion(ctx, http.DefaultTransport.RoundTrip, nil, fmt.Sprintf("%s-%s", channelGroup, minorLine), clusterversion.GetZStreamOffset(channelGroup))
					if err != nil {
						Skip(fmt.Sprintf("failed to resolve a version for channel %s-%s: %v", channelGroup, minorLine, err))
					}
					if desiredVersion == nil {
						Skip(fmt.Sprintf("no version resolved for channel %s-%s; skipping y-stream upgrade %s -> %s",
							channelGroup, minorLine, installVersionId, upgradeVersionId))
					}
				}
			}

			tc := framework.NewTestContext()
			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, framework.IdentityContainerAssignmentRetryInterval)
				Expect(err).NotTo(HaveOccurred(), "failed to assign pooled identity containers")
			}

			versionLabel := strings.ReplaceAll(targetMinor, ".", "-")
			suffix := rand.String(6)
			clusterName := customerClusterNamePrefix + versionLabel + "-" + suffix

			By("creating resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "rg-cp-ystream-deny-pods-"+versionLabel, tc.Location())
			Expect(err).NotTo(HaveOccurred(), "failed to create resource group for y-stream deny-pods upgrade to %s", targetMinor)

			By("creating cluster parameters at install (previous minor) version")
			clusterParams := framework.NewDefaultClusterParams20240610()
			clusterParams.ClusterName = clusterName
			clusterParams.OpenshiftVersionId = installVersionId
			clusterParams.ChannelGroup = channelGroup
			managedResourceGroupName := framework.SuffixName(*resourceGroup.Name+"-cp-ystream-deny-pods-"+suffix, "-managed", 64)
			clusterParams.ManagedResourceGroupName = managedResourceGroupName

			By("creating customer resources")
			clusterParams, err = tc.CreateClusterCustomerResources20240610(ctx,
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
			Expect(err).NotTo(HaveOccurred(), "failed to create customer resources for y-stream deny-pods upgrade cluster %q", clusterName)

			By(fmt.Sprintf("creating the HCP cluster at install version %s (previous minor)", installVersionId))
			err = tc.CreateHCPClusterFromParam20240610(
				ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
				framework.ClusterCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create HCP cluster %q at install version %s", clusterName, installVersionId)

			By("getting admin credentials")
			hcpClient := tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient()
			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster20240610(
				ctx,
				hcpClient,
				*resourceGroup.Name,
				clusterName,
				framework.GetAdminRESTConfigTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to get admin REST config for cluster %q", clusterName)

			By("verifying the cluster is viable before upgrade")
			err = verifiers.VerifyHCPCluster(ctx, adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to verify cluster %q is viable before upgrade", clusterName)

			kubeClient, err := kubernetes.NewForConfig(adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to create Kubernetes client for cluster %q", clusterName)

			By(fmt.Sprintf("creating a 2-replica nodepool at version %s", nodePoolVersionId))
			nodePoolParams := framework.NewDefaultNodePoolParams20240610()
			nodePoolParams.ClusterName = clusterName
			nodePoolParams.NodePoolName = ystreamDenyNodePoolName
			nodePoolParams.Replicas = int32(ystreamDenyExpectedNodes)
			nodePoolParams.OpenshiftVersionId = nodePoolVersionId
			nodePoolParams.ChannelGroup = channelGroup
			smallVMSize, err := tc.SelectVMSize(ctx, framework.SmallWorkerVMSizeSelector())
			Expect(err).NotTo(HaveOccurred(), "failed to resolve a small worker VM size; check VM SKU restrictions/quota for the test subscription in %s", tc.Location())
			nodePoolParams.VMSize = smallVMSize
			err = tc.CreateNodePoolFromParam20240610(
				ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				managedResourceGroupName,
				clusterName,
				nodePoolParams,
				framework.NodePoolCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create node pool %q for cluster %q", ystreamDenyNodePoolName, clusterName)

			By("waiting for guest nodes to be ready")
			Eventually(func() error {
				if err := verifiers.VerifyNodeCount(clusterName, ystreamDenyExpectedNodes).Verify(ctx, adminRESTConfig); err != nil {
					return err
				}
				return verifiers.VerifyNodesReady().Verify(ctx, adminRESTConfig)
			}).WithContext(ctx).WithTimeout(10*time.Minute).WithPolling(framework.StandardPollInterval).Should(Succeed(), "guest nodes were not ready after creating node pool %q on cluster %q", ystreamDenyNodePoolName, clusterName)

			By("applying a deny-pod ValidatingAdmissionPolicy")
			Expect(applyPodDenyAdmissionPolicy(ctx, kubeClient)).To(Succeed(), "failed to apply deny-pod ValidatingAdmissionPolicy on cluster %q", clusterName)
			Eventually(func() error {
				return assertProbePodDeniedByAdmissionPolicy(ctx, kubeClient)
			}).WithContext(ctx).WithTimeout(2*time.Minute).WithPolling(framework.StandardPollInterval).Should(Succeed(), "probe pod CREATE was not denied by ValidatingAdmissionPolicy on cluster %q", clusterName)

			Expect(ctx.Err()).NotTo(HaveOccurred(), "test context expired before triggering upgrade for cluster %q", clusterName)
			preUpgradeKubeAPIServerVersion, err := kubeClient.Discovery().ServerVersion()
			Expect(err).NotTo(HaveOccurred(), "failed to get pre-upgrade kube-apiserver version for cluster %q", clusterName)

			By(fmt.Sprintf("triggering control plane y-stream upgrade to %s (target minor %s)", upgradeVersionId,
				upgradeVersion.String()))
			update := hcpsdk20240610preview.HcpOpenShiftClusterUpdate{
				Properties: &hcpsdk20240610preview.HcpOpenShiftClusterPropertiesUpdate{
					Version: &hcpsdk20240610preview.VersionProfile{
						ID:           to.Ptr(upgradeVersionId),
						ChannelGroup: to.Ptr(channelGroup),
					},
				},
			}
			_, err = framework.UpdateHCPCluster20240610(ctx, hcpClient, *resourceGroup.Name, clusterName, update, framework.HCPClusterVersionUpgradeTimeout)
			Expect(err).NotTo(HaveOccurred(), "failed to trigger y-stream upgrade of cluster %q to %s", clusterName, upgradeVersionId)

			By("verifying control plane reached the target minor")
			Eventually(func() error {
				return errors.Join(
					verifiers.VerifyKubeAPIServerServerVersionUpgraded(preUpgradeKubeAPIServerVersion).Verify(ctx, adminRESTConfig),
					verifiers.VerifyHostedControlPlaneYStreamUpgrade(installVersionId, upgradeVersionId).Verify(ctx, adminRESTConfig),
				)
			}).WithContext(ctx).WithTimeout(framework.HCPClusterVersionUpgradeTimeout).WithPolling(2*time.Minute).Should(Succeed(), "control plane did not reach %s on cluster %q with deny-pod ValidatingAdmissionPolicy", upgradeVersionId, clusterName)

			By("verifying cluster API remains reachable after control plane upgrade")
			err = verifiers.VerifyHCPCluster(ctx, adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to verify cluster %q API after y-stream upgrade to %s", clusterName, upgradeVersionId)

			By("asserting probe pod CREATE is still denied by the ValidatingAdmissionPolicy")
			Expect(assertProbePodDeniedByAdmissionPolicy(ctx, kubeClient)).To(Succeed(), "probe pod CREATE was no longer denied after upgrade on cluster %q", clusterName)
		},
		Entry("from 4.20 minor to 4.21 minor", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, labels.Slow, "4.21"),
		Entry("from 4.21 minor to 4.22 minor", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, labels.Slow, "4.22"),
		Entry("from 4.22 minor to 4.23 minor", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, labels.Slow, "4.23"),
		Entry("from 4.22 minor to 5.0 minor", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, labels.Slow, "5.0"),
		Entry("from 5.0 minor to 5.1 minor", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, labels.Slow, "5.1"),
		Entry("from 5.1 minor to 5.2 minor", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, labels.Slow, "5.2"),
		Entry("from 5.2 minor to 5.3 minor", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, labels.Slow, "5.3"),
		Entry("from 5.3 minor to 5.4 minor", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, labels.Slow, "5.4"),
		Entry("from 5.4 minor to 5.5 minor", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, labels.Slow, "5.5"),
		Entry("from 5.5 minor to 5.6 minor", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, labels.Slow, "5.6"),
		Entry("from 5.6 minor to 5.7 minor", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, labels.Slow, "5.7"),
		Entry("from 5.7 minor to 5.8 minor", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, labels.Slow, "5.8"),
		Entry("from 5.8 minor to 5.9 minor", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, labels.Slow, "5.9"),
		Entry("from 5.9 minor to 5.10 minor", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, labels.Slow, "5.10"),
		Entry("from 5.10 minor to 5.11 minor", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, labels.Slow, "5.11"),
		Entry("from 5.11 minor to 5.12 minor", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, labels.Slow, "5.12"),
	)
})

func applyPodDenyAdmissionPolicy(ctx context.Context, kubeClient kubernetes.Interface) error {
	policy := &admissionregistrationv1.ValidatingAdmissionPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: ystreamDenyPolicyName},
		Spec: admissionregistrationv1.ValidatingAdmissionPolicySpec{
			FailurePolicy: to.Ptr(admissionregistrationv1.Fail),
			MatchConstraints: &admissionregistrationv1.MatchResources{
				NamespaceSelector: &metav1.LabelSelector{
					MatchExpressions: []metav1.LabelSelectorRequirement{
						{
							Key:      "kubernetes.io/metadata.name",
							Operator: metav1.LabelSelectorOpNotIn,
							Values:   []string{"kube-system"},
						},
					},
				},
				ResourceRules: []admissionregistrationv1.NamedRuleWithOperations{
					{
						RuleWithOperations: admissionregistrationv1.RuleWithOperations{
							Operations: []admissionregistrationv1.OperationType{admissionregistrationv1.Create},
							Rule: admissionregistrationv1.Rule{
								APIGroups:   []string{""},
								APIVersions: []string{"v1"},
								Resources:   []string{"pods"},
							},
						},
					},
				},
			},
			Validations: []admissionregistrationv1.Validation{
				{
					Expression: "false",
					Message:    ystreamDenyPolicyMessage,
					Reason:     to.Ptr(metav1.StatusReasonForbidden),
				},
			},
		},
	}
	if _, err := kubeClient.AdmissionregistrationV1().ValidatingAdmissionPolicies().Create(ctx, policy, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("failed to create ValidatingAdmissionPolicy %s: %w", ystreamDenyPolicyName, err)
	}

	binding := &admissionregistrationv1.ValidatingAdmissionPolicyBinding{
		ObjectMeta: metav1.ObjectMeta{Name: ystreamDenyPolicyName},
		Spec: admissionregistrationv1.ValidatingAdmissionPolicyBindingSpec{
			PolicyName:        ystreamDenyPolicyName,
			ValidationActions: []admissionregistrationv1.ValidationAction{admissionregistrationv1.Deny},
		},
	}
	if _, err := kubeClient.AdmissionregistrationV1().ValidatingAdmissionPolicyBindings().Create(ctx, binding, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("failed to create ValidatingAdmissionPolicyBinding %s: %w", ystreamDenyPolicyName, err)
	}
	return nil
}

func assertProbePodDeniedByAdmissionPolicy(ctx context.Context, kubeClient kubernetes.Interface) error {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{GenerateName: "dataplane-probe-"},
		Spec: corev1.PodSpec{
			Containers:    []corev1.Container{{Name: "probe", Image: "does-not-exist.invalid/probe:e2e"}},
			RestartPolicy: corev1.RestartPolicyNever,
		},
	}
	created, err := kubeClient.CoreV1().Pods("default").Create(ctx, pod, metav1.CreateOptions{})
	if err == nil {
		_ = kubeClient.CoreV1().Pods("default").Delete(ctx, created.Name, metav1.DeleteOptions{})
		return fmt.Errorf("probe pod CREATE succeeded; expected ValidatingAdmissionPolicy %s to reject it", ystreamDenyPolicyName)
	}
	if !apierrors.IsForbidden(err) {
		return fmt.Errorf("probe pod CREATE failed with an unexpected error (wanted Forbidden from ValidatingAdmissionPolicy %s): %w", ystreamDenyPolicyName, err)
	}
	if !strings.Contains(err.Error(), ystreamDenyPolicyName) && !strings.Contains(err.Error(), ystreamDenyPolicyMessage) {
		return fmt.Errorf("probe pod CREATE was Forbidden but not by ValidatingAdmissionPolicy %s: %w", ystreamDenyPolicyName, err)
	}
	return nil
}
