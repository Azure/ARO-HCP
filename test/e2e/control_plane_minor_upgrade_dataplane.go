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
	cryptorand "crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/blang/semver/v4"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/client-go/kubernetes"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"

	clusterversion "github.com/Azure/ARO-HCP/backend/pkg/controllers/cluster/version"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	hcpsdk20240610preview "github.com/Azure/ARO-HCP/test/sdk/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

const (
	dataplaneScenarioCordon  = "cordon"
	dataplaneScenarioWebhook = "pod-deny-webhook"
	dataplaneExpectedNodes   = 2
	dataplaneNodePoolName    = "np-dataplane"
	dataplaneDenyWebhookName = "deny-pods-e2e-aro-hcp"
	dataplaneDenyWebhookFQDN = "deny-pods.e2e.aro-hcp.test"
)

var _ = Describe("Customer", func() {
	DescribeTable("should upgrade control plane minor version when dataplane is degraded",
		labels.MIContainers(1),
		func(ctx context.Context, targetMinor, scenario string) {
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
			scenarioLabel := scenario
			if scenario == dataplaneScenarioWebhook {
				scenarioLabel = "webhook"
			}
			suffix := rand.String(6)
			clusterName := "cp-ystream-dp-" + scenarioLabel + "-" + versionLabel + "-" + suffix

			By("creating resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "rg-cp-ystream-dp-"+scenarioLabel+"-"+versionLabel, tc.Location())
			Expect(err).NotTo(HaveOccurred(), "failed to create resource group for y-stream dataplane upgrade to %s (%s)", targetMinor, scenario)

			By("creating cluster parameters at install (previous minor) version")
			clusterParams := framework.NewDefaultClusterParams20240610()
			clusterParams.ClusterName = clusterName
			clusterParams.OpenshiftVersionId = installVersionId
			clusterParams.ChannelGroup = channelGroup
			managedResourceGroupName := framework.SuffixName(*resourceGroup.Name+"-cp-ystream-dp-"+suffix, "-managed", 64)
			clusterParams.ManagedResourceGroupName = managedResourceGroupName

			By("creating customer resources")
			clusterParams, err = tc.CreateClusterCustomerResources20240610(ctx,
				resourceGroup,
				clusterParams,
				map[string]interface{}{
					"customerNsgName":        "customer-nsg-cp-ystream-dp-" + suffix,
					"customerVnetName":       "customer-vnet-cp-ystream-dp-" + suffix,
					"customerVnetSubnetName": "customer-vnet-subnet-cp-ystream-dp-" + suffix,
				},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create customer resources for y-stream dataplane upgrade cluster %q", clusterName)

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
			nodePoolParams.NodePoolName = dataplaneNodePoolName
			nodePoolParams.Replicas = int32(dataplaneExpectedNodes)
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
			Expect(err).NotTo(HaveOccurred(), "failed to create node pool %q for cluster %q", dataplaneNodePoolName, clusterName)

			By("waiting for guest nodes to be ready")
			Eventually(func() error {
				if err := verifiers.VerifyNodeCount(clusterName, dataplaneExpectedNodes).Verify(ctx, adminRESTConfig); err != nil {
					return err
				}
				return verifiers.VerifyNodesReady().Verify(ctx, adminRESTConfig)
			}).WithContext(ctx).WithTimeout(10*time.Minute).WithPolling(framework.StandardPollInterval).Should(Succeed(), "guest nodes were not ready after creating node pool %q on cluster %q", dataplaneNodePoolName, clusterName)

			By(fmt.Sprintf("degrading dataplane via %s", scenario))
			switch scenario {
			case dataplaneScenarioCordon:
				err = cordonAllGuestNodes(ctx, kubeClient)
				Expect(err).NotTo(HaveOccurred(), "failed to cordon guest nodes on cluster %q", clusterName)
				err = assertAllGuestNodesUnschedulable(ctx, kubeClient)
				Expect(err).NotTo(HaveOccurred(), "guest nodes were not unschedulable after cordon on cluster %q", clusterName)
			case dataplaneScenarioWebhook:
				err = applyDenyPodWebhook(ctx, kubeClient)
				Expect(err).NotTo(HaveOccurred(), "failed to apply deny-pod webhook on cluster %q", clusterName)
				Eventually(func() error {
					return assertProbePodCreateDenied(ctx, kubeClient)
				}).WithContext(ctx).WithTimeout(2*time.Minute).WithPolling(framework.StandardPollInterval).Should(Succeed(), "probe pod CREATE was not denied by webhook on cluster %q", clusterName)
			default:
				Fail(fmt.Sprintf("unknown dataplane scenario %q", scenario))
			}

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

			By("verifying control plane reached the target minor with dataplane still degraded")
			Eventually(func() error {
				return verifiers.VerifyHCPCluster(ctx, adminRESTConfig,
					verifiers.VerifyKubeAPIServerServerVersionUpgraded(preUpgradeKubeAPIServerVersion),
					verifiers.VerifyHostedControlPlaneYStreamUpgrade(
						installVersionId,
						upgradeVersionId))
			}).WithContext(ctx).WithTimeout(framework.HCPClusterVersionUpgradeTimeout).WithPolling(2*time.Minute).Should(Succeed(), "control plane did not reach %s on cluster %q with dataplane scenario %s", upgradeVersionId, clusterName, scenario)

			By("asserting dataplane is still degraded after control-plane upgrade")
			switch scenario {
			case dataplaneScenarioCordon:
				err = assertAllGuestNodesUnschedulable(ctx, kubeClient)
				Expect(err).NotTo(HaveOccurred(), "guest nodes were no longer unschedulable after upgrade on cluster %q", clusterName)
			case dataplaneScenarioWebhook:
				err = assertProbePodCreateDenied(ctx, kubeClient)
				Expect(err).NotTo(HaveOccurred(), "probe pod CREATE was no longer denied after upgrade on cluster %q", clusterName)
			}
		},
		Entry("from 4.20 minor to 4.21 minor with cordoned nodes", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, labels.Slow, "4.21", dataplaneScenarioCordon),
		Entry("from 4.21 minor to 4.22 minor with cordoned nodes", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, labels.Slow, "4.22", dataplaneScenarioCordon),
		Entry("from 4.22 minor to 4.23 minor with cordoned nodes", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, labels.Slow, "4.23", dataplaneScenarioCordon),
		Entry("from 4.22 minor to 5.0 minor with cordoned nodes", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, labels.Slow, "5.0", dataplaneScenarioCordon),
		Entry("from 5.0 minor to 5.1 minor with cordoned nodes", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, labels.Slow, "5.1", dataplaneScenarioCordon),
		Entry("from 5.1 minor to 5.2 minor with cordoned nodes", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, labels.Slow, "5.2", dataplaneScenarioCordon),
		Entry("from 5.2 minor to 5.3 minor with cordoned nodes", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, labels.Slow, "5.3", dataplaneScenarioCordon),
		Entry("from 5.3 minor to 5.4 minor with cordoned nodes", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, labels.Slow, "5.4", dataplaneScenarioCordon),
		Entry("from 5.4 minor to 5.5 minor with cordoned nodes", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, labels.Slow, "5.5", dataplaneScenarioCordon),
		Entry("from 5.5 minor to 5.6 minor with cordoned nodes", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, labels.Slow, "5.6", dataplaneScenarioCordon),
		Entry("from 5.6 minor to 5.7 minor with cordoned nodes", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, labels.Slow, "5.7", dataplaneScenarioCordon),
		Entry("from 5.7 minor to 5.8 minor with cordoned nodes", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, labels.Slow, "5.8", dataplaneScenarioCordon),
		Entry("from 5.8 minor to 5.9 minor with cordoned nodes", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, labels.Slow, "5.9", dataplaneScenarioCordon),
		Entry("from 5.9 minor to 5.10 minor with cordoned nodes", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, labels.Slow, "5.10", dataplaneScenarioCordon),
		Entry("from 5.10 minor to 5.11 minor with cordoned nodes", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, labels.Slow, "5.11", dataplaneScenarioCordon),
		Entry("from 5.11 minor to 5.12 minor with cordoned nodes", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, labels.Slow, "5.12", dataplaneScenarioCordon),
		Entry("from 4.20 minor to 4.21 minor with pod-deny webhook", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, labels.Slow, "4.21", dataplaneScenarioWebhook),
		Entry("from 4.21 minor to 4.22 minor with pod-deny webhook", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, labels.Slow, "4.22", dataplaneScenarioWebhook),
		Entry("from 4.22 minor to 4.23 minor with pod-deny webhook", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, labels.Slow, "4.23", dataplaneScenarioWebhook),
		Entry("from 4.22 minor to 5.0 minor with pod-deny webhook", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, labels.Slow, "5.0", dataplaneScenarioWebhook),
		Entry("from 5.0 minor to 5.1 minor with pod-deny webhook", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, labels.Slow, "5.1", dataplaneScenarioWebhook),
		Entry("from 5.1 minor to 5.2 minor with pod-deny webhook", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, labels.Slow, "5.2", dataplaneScenarioWebhook),
		Entry("from 5.2 minor to 5.3 minor with pod-deny webhook", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, labels.Slow, "5.3", dataplaneScenarioWebhook),
		Entry("from 5.3 minor to 5.4 minor with pod-deny webhook", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, labels.Slow, "5.4", dataplaneScenarioWebhook),
		Entry("from 5.4 minor to 5.5 minor with pod-deny webhook", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, labels.Slow, "5.5", dataplaneScenarioWebhook),
		Entry("from 5.5 minor to 5.6 minor with pod-deny webhook", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, labels.Slow, "5.6", dataplaneScenarioWebhook),
		Entry("from 5.6 minor to 5.7 minor with pod-deny webhook", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, labels.Slow, "5.7", dataplaneScenarioWebhook),
		Entry("from 5.7 minor to 5.8 minor with pod-deny webhook", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, labels.Slow, "5.8", dataplaneScenarioWebhook),
		Entry("from 5.8 minor to 5.9 minor with pod-deny webhook", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, labels.Slow, "5.9", dataplaneScenarioWebhook),
		Entry("from 5.9 minor to 5.10 minor with pod-deny webhook", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, labels.Slow, "5.10", dataplaneScenarioWebhook),
		Entry("from 5.10 minor to 5.11 minor with pod-deny webhook", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, labels.Slow, "5.11", dataplaneScenarioWebhook),
		Entry("from 5.11 minor to 5.12 minor with pod-deny webhook", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, labels.Slow, "5.12", dataplaneScenarioWebhook),
	)
})

func cordonAllGuestNodes(ctx context.Context, kubeClient kubernetes.Interface) error {
	nodes, err := kubeClient.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list guest nodes: %w", err)
	}
	if len(nodes.Items) == 0 {
		return fmt.Errorf("no guest nodes found to cordon")
	}
	patch := []byte(`{"spec":{"unschedulable":true}}`)
	for _, node := range nodes.Items {
		if _, err := kubeClient.CoreV1().Nodes().Patch(ctx, node.Name, types.MergePatchType, patch, metav1.PatchOptions{}); err != nil {
			return fmt.Errorf("failed to cordon node %s: %w", node.Name, err)
		}
	}
	return nil
}

func assertAllGuestNodesUnschedulable(ctx context.Context, kubeClient kubernetes.Interface) error {
	nodes, err := kubeClient.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list guest nodes: %w", err)
	}
	if len(nodes.Items) == 0 {
		return fmt.Errorf("no guest nodes found")
	}
	var schedulable []string
	for _, node := range nodes.Items {
		if !node.Spec.Unschedulable {
			schedulable = append(schedulable, node.Name)
		}
	}
	if len(schedulable) > 0 {
		return fmt.Errorf("expected all guest nodes to be unschedulable, but these were still schedulable: %s", strings.Join(schedulable, ", "))
	}
	return nil
}

func applyDenyPodWebhook(ctx context.Context, kubeClient kubernetes.Interface) error {
	caBundle, err := dummyWebhookCABundle()
	if err != nil {
		return err
	}
	webhook := &admissionregistrationv1.ValidatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Name: dataplaneDenyWebhookName,
		},
		Webhooks: []admissionregistrationv1.ValidatingWebhook{
			{
				Name: dataplaneDenyWebhookFQDN,
				ClientConfig: admissionregistrationv1.WebhookClientConfig{
					URL:      to.Ptr("https://127.0.0.1:1/deny-pods"),
					CABundle: caBundle,
				},
				Rules: []admissionregistrationv1.RuleWithOperations{
					{
						Operations: []admissionregistrationv1.OperationType{admissionregistrationv1.Create},
						Rule: admissionregistrationv1.Rule{
							APIGroups:   []string{""},
							APIVersions: []string{"v1"},
							Resources:   []string{"pods"},
						},
					},
				},
				FailurePolicy:           to.Ptr(admissionregistrationv1.Fail),
				SideEffects:             to.Ptr(admissionregistrationv1.SideEffectClassNone),
				AdmissionReviewVersions: []string{"v1"},
				TimeoutSeconds:          to.Ptr(int32(1)),
				NamespaceSelector: &metav1.LabelSelector{
					MatchExpressions: []metav1.LabelSelectorRequirement{
						{
							Key:      "kubernetes.io/metadata.name",
							Operator: metav1.LabelSelectorOpNotIn,
							Values:   []string{"kube-system"},
						},
					},
				},
			},
		},
	}
	_, err = kubeClient.AdmissionregistrationV1().ValidatingWebhookConfigurations().Create(ctx, webhook, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create ValidatingWebhookConfiguration %s: %w", dataplaneDenyWebhookName, err)
	}
	return nil
}

func assertProbePodCreateDenied(ctx context.Context, kubeClient kubernetes.Interface) error {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "dataplane-probe-",
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "probe",
					Image: "does-not-exist.invalid/probe:e2e",
				},
			},
			RestartPolicy: corev1.RestartPolicyNever,
		},
	}
	created, err := kubeClient.CoreV1().Pods("default").Create(ctx, pod, metav1.CreateOptions{})
	if err == nil {
		_ = kubeClient.CoreV1().Pods("default").Delete(ctx, created.Name, metav1.DeleteOptions{})
		return fmt.Errorf("probe pod CREATE succeeded; expected deny-pod webhook to reject it")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "webhook") {
		return fmt.Errorf("probe pod CREATE failed with an unexpected error (wanted webhook denial): %w", err)
	}
	return nil
}

func dummyWebhookCABundle() ([]byte, error) {
	key, err := rsa.GenerateKey(cryptorand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("failed to generate webhook CA key: %w", err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "e2e-dataplane-webhook"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(cryptorand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return nil, fmt.Errorf("failed to create webhook CA certificate: %w", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), nil
}
