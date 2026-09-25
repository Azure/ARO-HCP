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
	"regexp"
	"slices"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	armauthorization "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v3"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v5"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerregistry/armcontainerregistry"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/msi/armmsi"

	configv1client "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"

	hcpsdk20261001preview "github.com/Azure/ARO-HCP/test/sdk/v20261001preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

const (
	managedIdentityOperatorRoleID = "f1a07417-d97a-45cb-824c-7a7467783830"
	acrPullRoleID                 = "7f951dda-4ed3-4680-a7ca-43fe172d538d"
	// Azure Container Registry names must be 5-50 alphanumeric characters.
	acrNameMaxLength = 50

	acrPullSourceRegistry = "registry.access.redhat.com"
	acrPullSourceImage    = "ubi9/ubi-minimal:latest"
	acrPullTestImageName  = "ubi-minimal"
	acrPullTestImageTag   = acrPullTestImageName + ":latest"

	acrPullImagePullTimeout = 3 * time.Minute
)

var nonAlphanumeric = regexp.MustCompile(`[^a-zA-Z0-9]`)

func createACRPullMI(
	ctx context.Context,
	msiClient *armmsi.UserAssignedIdentitiesClient,
	resourceGroupName string,
	miName string,
	location *string,
) (string, error) {
	resp, err := msiClient.CreateOrUpdate(ctx, resourceGroupName, miName, armmsi.Identity{
		Location: location,
	}, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create MI %s: %w", miName, err)
	}
	if resp.ID == nil {
		return "", fmt.Errorf("MI %s resource ID was nil", miName)
	}
	return *resp.ID, nil
}

func lookupCAPZPrincipalID(
	ctx context.Context,
	msiClient *armmsi.UserAssignedIdentitiesClient,
	clusterParams framework.ClusterParams20261001,
) (string, error) {
	if clusterParams.UserAssignedIdentitiesProfile == nil || clusterParams.UserAssignedIdentitiesProfile.ControlPlaneOperators == nil {
		return "", fmt.Errorf("CAPZ identity profile not found in cluster params")
	}
	capzMIResourceIDStr, ok := clusterParams.UserAssignedIdentitiesProfile.ControlPlaneOperators[framework.ClusterApiAzureMiName]
	if !ok || capzMIResourceIDStr == nil {
		return "", fmt.Errorf("CAPZ identity not found in cluster params control plane operators")
	}
	capzMIResourceID, err := azcorearm.ParseResourceID(*capzMIResourceIDStr)
	if err != nil {
		return "", fmt.Errorf("failed to parse CAPZ MI resource ID %q: %w", *capzMIResourceIDStr, err)
	}
	capzMI, err := msiClient.Get(ctx, capzMIResourceID.ResourceGroupName, capzMIResourceID.Name, nil)
	if err != nil {
		return "", fmt.Errorf("failed to get CAPZ MI: %w", err)
	}
	if capzMI.Properties == nil || capzMI.Properties.PrincipalID == nil {
		return "", fmt.Errorf("CAPZ MI has no principal ID")
	}
	return *capzMI.Properties.PrincipalID, nil
}

// lookupMIPrincipalID reads a managed identity's principal ID, retrying because these reads
// follow CreateClusterCustomerResources and so land in a burst of ARM traffic against a
// subscription whose request budget is shared with every other spec in the suite. Throttling
// against that budget has been seen in CI, so a one-shot read is an avoidable flake.
func lookupMIPrincipalID(
	ctx context.Context,
	msiClient *armmsi.UserAssignedIdentitiesClient,
	resourceGroupName, miName string,
	timeout time.Duration,
) string {
	var principalID string
	Eventually(func() error {
		mi, err := msiClient.Get(ctx, resourceGroupName, miName, nil)
		if err != nil {
			return fmt.Errorf("failed to get MI %s details: %w", miName, err)
		}
		if mi.Properties == nil || mi.Properties.PrincipalID == nil {
			return fmt.Errorf("MI %s has no principal ID", miName)
		}
		principalID = *mi.Properties.PrincipalID
		return nil
	}).WithContext(ctx).WithTimeout(timeout).WithPolling(15*time.Second).Should(Succeed(),
		"failed to look up principal ID for MI %s", miName)
	return principalID
}

// isARMThrottlingError reports whether an ARM call was rejected for rate limiting. The
// subscription-level read/write budget is shared with everything else running in the CI
// subscription, so bursts of 429 (SubscriptionRequestsThrottled and friends) are routine
// and always transient. Matching on the status code rather than the error code covers the
// several distinct throttling codes ARM returns.
func isARMThrottlingError(err error) bool {
	var respErr *azcore.ResponseError
	return errors.As(err, &respErr) && respErr.StatusCode == http.StatusTooManyRequests
}

// grantBuiltInRoleWithRetry assigns a built-in role at a scope, retrying while the principal
// is still propagating to the Authorization RP. A freshly created managed identity's principal
// is not immediately visible (PrincipalNotFound), and role-assignment writes are also subject
// to tenant-level throttling. Any other error stops the retry immediately.
func grantBuiltInRoleWithRetry(
	ctx context.Context,
	client *armauthorization.RoleAssignmentsClient,
	subscriptionID, scope, principalID, roleID, description string,
	timeout time.Duration,
) {
	Eventually(func() error {
		err := assignBuiltInRoleAtScope(ctx, client, subscriptionID, scope, principalID, roleID)
		if err != nil && !isPrincipalNotFoundError(err) && !isARMThrottlingError(err) {
			return StopTrying(err.Error()).Wrap(err)
		}
		return err
	}).WithContext(ctx).WithTimeout(timeout).WithPolling(15*time.Second).Should(Succeed(),
		"%s should succeed once the principal propagates", description)
}

// vmUserAssignedIdentityIDs returns the user-assigned identity resource IDs attached
// to a VM, lowercased so they can be compared to an ARM resource ID whose casing ARM
// does not preserve.
func vmUserAssignedIdentityIDs(vm *armcompute.VirtualMachine) []string {
	if vm == nil || vm.Identity == nil {
		return nil
	}
	ids := make([]string, 0, len(vm.Identity.UserAssignedIdentities))
	for id := range vm.Identity.UserAssignedIdentities {
		ids = append(ids, strings.ToLower(id))
	}
	return ids
}

// verifyACRPullFromNodes deploys a pod that pulls the test image from the private ACR with no
// imagePullSecrets, and asserts the pull succeeds. A successful pull is only possible if
// kubelet's credential provider authenticated with an identity attached to the node.
//
// Each call uses its own namespace on purpose: verifiers.VerifyImagePulled succeeds if *any*
// pod in the namespace has pulled the image, so reusing one namespace would let the day 1
// pod's already-pulled image satisfy the day 2 check vacuously.
func verifyACRPullFromNodes(ctx context.Context, adminRESTConfig *rest.Config, namespace, acrLoginServer, phase string) {
	By(fmt.Sprintf("[%s] creating namespace %s for the ACR image pull check", phase, namespace))
	kubeClient, err := kubernetes.NewForConfig(adminRESTConfig)
	Expect(err).NotTo(HaveOccurred(), "failed to create kubernetes client for the %s ACR pull check", phase)

	_, err = kubeClient.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: namespace},
	}, metav1.CreateOptions{})
	Expect(err).NotTo(HaveOccurred(), "failed to create namespace %s", namespace)
	DeferCleanup(func(ctx context.Context) {
		_ = kubeClient.CoreV1().Namespaces().Delete(ctx, namespace, metav1.DeleteOptions{})
	})

	By(fmt.Sprintf("[%s] creating a service account for the image pull verification pod", phase))
	sa, err := kubeClient.CoreV1().ServiceAccounts(namespace).Create(ctx, &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{Name: "acr-pull-test"},
	}, metav1.CreateOptions{})
	Expect(err).NotTo(HaveOccurred(), "failed to create service account in %s", namespace)

	By(fmt.Sprintf("[%s] deploying a pod that pulls from private ACR %s via kubelet credential provider", phase, acrLoginServer))
	_, err = kubeClient.CoreV1().Pods(namespace).Create(ctx, &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "acr-pull-test",
			Namespace: namespace,
		},
		Spec: corev1.PodSpec{
			ServiceAccountName:           sa.Name,
			AutomountServiceAccountToken: to.Ptr(false),
			Containers: []corev1.Container{
				{
					Name:            "acr-pull-test",
					Image:           fmt.Sprintf("%s/%s", acrLoginServer, acrPullTestImageTag),
					Command:         []string{"true"},
					ImagePullPolicy: corev1.PullAlways,
					SecurityContext: &corev1.SecurityContext{
						AllowPrivilegeEscalation: to.Ptr(false),
						RunAsNonRoot:             to.Ptr(true),
						SeccompProfile: &corev1.SeccompProfile{
							Type: corev1.SeccompProfileTypeRuntimeDefault,
						},
						Capabilities: &corev1.Capabilities{
							Drop: []corev1.Capability{"ALL"},
						},
					},
				},
			},
			RestartPolicy: corev1.RestartPolicyNever,
		},
	}, metav1.CreateOptions{})
	Expect(err).NotTo(HaveOccurred(), "failed to create ACR pull test pod in %s", namespace)

	By(fmt.Sprintf("[%s] verifying the image was pulled from the private ACR", phase))
	err = verifiers.VerifyImagePulled(namespace, acrLoginServer, acrPullTestImageName, acrPullImagePullTimeout).
		Verify(ctx, adminRESTConfig)
	Expect(err).NotTo(HaveOccurred(), "[%s] pod in namespace %s never pulled image %s from private ACR %s",
		phase, namespace, acrPullTestImageName, acrLoginServer)
}

// This spec covers the full lifecycle of ACR pull via managed identity (ARO-24037) on a single
// cluster, because test/AGENTS.md requires specs to be self-contained and a second spec would
// have to build its own ~45 minute cluster to reach the same starting point. Day 2 is appended
// to day 1 the way cluster_autoscaling.go, idms_lifecycle.go and nodepool_labels_taints.go
// append theirs.
var _ = Describe("Customer", func() {
	It("should be able to create a cluster with ACR pull via managed identity and pull from a private ACR",
		labels.RequireNothing,
		labels.Medium,
		labels.Positive,
		labels.AroRpApiCompatible,
		labels.CreateCluster,
		labels.MIContainers(1),
		func(ctx context.Context) {
			const (
				customerClusterName = "acr-pull"
				nodePoolName        = "np-1"
				day1MIName          = "acr-pull-mi-1"
				day2MIName          = "acr-pull-mi-2"

				// ACR pull via managed identity requires OCP >= 4.22: RP admission rejects
				// lower versions, and the kubelet credential provider that authenticates the
				// pull only exists from 4.22 on.
				minOpenshiftVersion = "4.22"

				miLookupTimeout       = 2 * time.Minute
				roleAssignmentTimeout = 5 * time.Minute
				// Covers ARM read throttling plus any lag between the node pool LRO settling
				// and the VM's identity block being observable. Short on purpose: this is not
				// the day 2 rollout, so a real missing identity should still fail fast.
				vmIdentityAttachTimeout = 5 * time.Minute

				day1PullNamespace = "acr-pull-test-day1"
				day2PullNamespace = "acr-pull-test-day2"
			)

			// Changing containerRegistry.managedIdentity rolls every existing node pool (the
			// field is marked +rollout in the HyperShift API), so the day 2 wait is a full
			// machine replacement. NodePoolVersionUpgradeTimeout is this repo's budget for
			// exactly that — node pools use the Replace strategy, so a version upgrade is
			// also a machine replacement.
			nodeRolloutTimeout := framework.NodePoolVersionUpgradeTimeout

			tc := framework.NewTestContext()

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, framework.IdentityContainerAssignmentRetryInterval)
				Expect(err).NotTo(HaveOccurred(), "failed to assign pooled identity containers")
			}

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "acr-pull", tc.Location())
			Expect(err).NotTo(HaveOccurred(), "failed to create resource group")

			By("creating cluster parameters")
			clusterParams := framework.NewDefaultClusterParams20261001()
			clusterParams.ClusterName = customerClusterName

			openshiftVersionID, err := framework.PickAtLeastOpenshiftVersionId(clusterParams.OpenshiftVersionId, minOpenshiftVersion)
			if framework.IsIncompatibleNightlyVersionError(err) {
				skipMsg := fmt.Sprintf("ACR pull via managed identity requires OCP >= %s, but default version %q does not satisfy it: %v", minOpenshiftVersion, clusterParams.OpenshiftVersionId, err)
				GinkgoLogr.Info(skipMsg)
				Skip(skipMsg)
			}
			Expect(err).NotTo(HaveOccurred(), "failed to select OpenShift version >= %s for ACR pull test (default version: %q)", minOpenshiftVersion, clusterParams.OpenshiftVersionId)
			clusterParams.OpenshiftVersionId = openshiftVersionID

			clusterParams.ManagedResourceGroupName = framework.SuffixName(*resourceGroup.Name, "-managed", 64)

			GinkgoLogr.Info("selected control plane OpenShift version",
				"controlPlane", clusterParams.OpenshiftVersionId, "minimum", minOpenshiftVersion)

			By("creating customer resources (infrastructure and managed identities)")
			clusterParams, err = tc.CreateClusterCustomerResources20261001(ctx,
				resourceGroup, clusterParams, map[string]any{},
				TestArtifactsFS, framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create cluster customer resources")

			subscriptionID, err := tc.SubscriptionID(ctx)
			Expect(err).NotTo(HaveOccurred(), "failed to get subscription ID")
			cred, err := tc.AzureCredential()
			Expect(err).NotTo(HaveOccurred(), "failed to get Azure credential")

			acrName := strings.ToLower(nonAlphanumeric.ReplaceAllString(*resourceGroup.Name, ""))
			if len(acrName) > acrNameMaxLength {
				acrName = acrName[:acrNameMaxLength]
			}

			registriesClient, err := armcontainerregistry.NewRegistriesClient(subscriptionID, cred, nil)
			Expect(err).NotTo(HaveOccurred(), "failed to create ACR registries client")

			By("creating a private Azure Container Registry")
			acrPoller, err := registriesClient.BeginCreate(ctx, *resourceGroup.Name, acrName, armcontainerregistry.Registry{
				Location: resourceGroup.Location,
				SKU:      &armcontainerregistry.SKU{Name: to.Ptr(armcontainerregistry.SKUNameBasic)},
				Properties: &armcontainerregistry.RegistryProperties{
					AdminUserEnabled: to.Ptr(false),
				},
			}, nil)
			Expect(err).NotTo(HaveOccurred(), "failed to start ACR creation")
			acrResp, err := acrPoller.PollUntilDone(ctx, nil)
			Expect(err).NotTo(HaveOccurred(), "failed to create ACR %s", acrName)
			Expect(acrResp.ID).NotTo(BeNil(), "ACR resource ID was nil")
			Expect(acrResp.Properties).NotTo(BeNil(), "ACR response Properties was nil")
			Expect(acrResp.Properties.LoginServer).NotTo(BeNil(), "ACR LoginServer was nil")
			acrResourceID := *acrResp.ID
			acrLoginServer := *acrResp.Properties.LoginServer
			GinkgoLogr.Info("created acr", "name", acrName, "login", acrLoginServer)

			By("importing a test image into the ACR")
			importPoller, err := registriesClient.BeginImportImage(ctx, *resourceGroup.Name, acrName, armcontainerregistry.ImportImageParameters{
				Source: &armcontainerregistry.ImportSource{
					RegistryURI: to.Ptr(acrPullSourceRegistry),
					SourceImage: to.Ptr(acrPullSourceImage),
				},
				TargetTags: []*string{to.Ptr(acrPullTestImageTag)},
				Mode:       to.Ptr(armcontainerregistry.ImportModeForce),
			}, nil)
			Expect(err).NotTo(HaveOccurred(), "failed to start image import into ACR")
			_, err = importPoller.PollUntilDone(ctx, nil)
			Expect(err).NotTo(HaveOccurred(), "failed to import image into ACR %s", acrName)
			GinkgoLogr.Info("imported source registry image to acr", "source", acrPullSourceRegistry, "image", acrPullSourceImage, "login", acrLoginServer)

			// Both identities are created and fully granted before the cluster exists. Every
			// ARM identity and role-assignment write therefore happens in one burst up front,
			// so the day 2 phase adds no ARM setup latency after the cluster is running and
			// cannot be delayed by role propagation at that point.
			By("creating the day 1 and day 2 ACR pull managed identities")
			msiClient, err := armmsi.NewUserAssignedIdentitiesClient(subscriptionID, cred, nil)
			Expect(err).NotTo(HaveOccurred(), "failed to create MSI client")

			day1MIResourceID, err := createACRPullMI(ctx, msiClient, *resourceGroup.Name, day1MIName, resourceGroup.Location)
			Expect(err).NotTo(HaveOccurred(), "failed to create the day 1 ACR pull MI")
			day2MIResourceID, err := createACRPullMI(ctx, msiClient, *resourceGroup.Name, day2MIName, resourceGroup.Location)
			Expect(err).NotTo(HaveOccurred(), "failed to create the day 2 ACR pull MI")

			day1MIPrincipalID := lookupMIPrincipalID(ctx, msiClient, *resourceGroup.Name, day1MIName, miLookupTimeout)
			day2MIPrincipalID := lookupMIPrincipalID(ctx, msiClient, *resourceGroup.Name, day2MIName, miLookupTimeout)

			var capzPrincipalID string
			Eventually(func() error {
				var lookupErr error
				capzPrincipalID, lookupErr = lookupCAPZPrincipalID(ctx, msiClient, clusterParams)
				return lookupErr
			}).WithContext(ctx).WithTimeout(miLookupTimeout).WithPolling(15*time.Second).Should(Succeed(),
				"failed to look up CAPZ principal ID")

			roleAssignmentsClient := newRoleAssignmentsClient(subscriptionID, cred)

			By("granting AcrPull on the ACR to both managed identities")
			grantBuiltInRoleWithRetry(ctx, roleAssignmentsClient, subscriptionID,
				acrResourceID, day1MIPrincipalID, acrPullRoleID,
				"AcrPull role assignment on the ACR for the day 1 MI", roleAssignmentTimeout)
			grantBuiltInRoleWithRetry(ctx, roleAssignmentsClient, subscriptionID,
				acrResourceID, day2MIPrincipalID, acrPullRoleID,
				"AcrPull role assignment on the ACR for the day 2 MI", roleAssignmentTimeout)

			By("granting Managed Identity Operator on both managed identities to CAPZ")
			grantBuiltInRoleWithRetry(ctx, roleAssignmentsClient, subscriptionID,
				day1MIResourceID, capzPrincipalID, managedIdentityOperatorRoleID,
				"Managed Identity Operator role assignment on the day 1 MI", roleAssignmentTimeout)
			grantBuiltInRoleWithRetry(ctx, roleAssignmentsClient, subscriptionID,
				day2MIResourceID, capzPrincipalID, managedIdentityOperatorRoleID,
				"Managed Identity Operator role assignment on the day 2 MI", roleAssignmentTimeout)

			//
			// Day 1: create with the first identity and pull.
			//

			By("building and creating the cluster with containerRegistry set to the day 1 MI")
			clusterParams.ContainerRegistryManagedIdentity = to.Ptr(day1MIResourceID)
			clusterResource, err := framework.BuildHCPClusterFromParams20261001(clusterParams, tc.Location(), nil)
			Expect(err).NotTo(HaveOccurred(), "failed to build HCP cluster from params")

			hcpClient := tc.Get20261001ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient()
			_, err = framework.CreateHCPClusterAndWait20261001(ctx, GinkgoLogr, hcpClient,
				*resourceGroup.Name, customerClusterName, clusterResource, framework.ClusterCreationTimeout)
			if framework.IsAPINotDeployedError(err) {
				if time.Now().Before(framework.V20261001PreviewDeploymentDeadline) {
					Skip(fmt.Sprintf("v20261001preview API not yet deployed; skipping until %s", framework.V20261001PreviewDeploymentDeadline.Format(time.RFC3339)))
				}
				Fail(fmt.Sprintf("v20261001preview API still not deployed as of %s deadline", framework.V20261001PreviewDeploymentDeadline.Format(time.RFC3339)))
			}
			Expect(err).NotTo(HaveOccurred(), "failed to create HCP cluster with containerRegistry set")

			By("[day 1] verifying containerRegistry.managedIdentity via GET")
			actualCluster, err := hcpClient.Get(ctx, *resourceGroup.Name, customerClusterName, nil)
			Expect(err).NotTo(HaveOccurred(), "failed to GET cluster after create")
			Expect(actualCluster.Properties).NotTo(BeNil(), "cluster properties was nil after create")
			Expect(actualCluster.Properties.Platform).NotTo(BeNil(), "cluster platform was nil after create")
			Expect(actualCluster.Properties.Platform.ContainerRegistry).NotTo(BeNil(), "containerRegistry should be set after create")
			Expect(actualCluster.Properties.Platform.ContainerRegistry.ManagedIdentity).NotTo(BeNil(), "containerRegistry.managedIdentity should be set after create")
			Expect(strings.EqualFold(*actualCluster.Properties.Platform.ContainerRegistry.ManagedIdentity, day1MIResourceID)).To(BeTrue(),
				"containerRegistry.managedIdentity after create: got %q want %q",
				*actualCluster.Properties.Platform.ContainerRegistry.ManagedIdentity, day1MIResourceID)

			By("getting admin credentials")
			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster20261001(
				ctx,
				tc.Get20261001ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
				*resourceGroup.Name,
				customerClusterName,
				framework.GetAdminRESTConfigTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to get admin REST config")

			By("verifying cluster health")
			err = verifiers.VerifyHCPCluster(ctx, adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "cluster health check failed")

			// The node pool must satisfy the same minimum as the control plane: the kubelet
			// credential provider that authenticates the pull ships with the worker payload,
			// so a 4.22 control plane over an older worker cannot satisfy this test. The
			// minimum cannot be applied as a floor the way it is for the control plane — the
			// RP resolves a bare major.minor for the control plane but requires a concrete
			// Major.Minor.Patch for node pools. Taking the installed version both guarantees
			// the minimum and keeps the node pool from outrunning the control plane.
			By("deriving the node pool version from the installed control plane")
			configClient, err := configv1client.NewForConfig(adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to create OpenShift config client")
			clusterVersion, err := configClient.ClusterVersions().Get(ctx, "version", metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred(), "failed to get ClusterVersion")
			Expect(clusterVersion.Status.Desired.Version).NotTo(BeEmpty(),
				"ClusterVersion reported no installed version to derive the node pool version from")

			By("creating a node pool")
			nodePoolParams := framework.NewDefaultNodePoolParams20261001()
			nodePoolParams.ChannelGroup = clusterParams.ChannelGroup
			nodePoolParams.OpenshiftVersionId = clusterVersion.Status.Desired.Version
			nodePoolParams.ClusterName = customerClusterName
			nodePoolParams.NodePoolName = nodePoolName
			// One node is sufficient to prove the kubelet credential provider can pull from
			// the private ACR, and it keeps the day 2 rollout to a single machine
			// replacement. More replicas only widen the surface for unrelated
			// machine-provisioning flakes and lengthen the rollout.
			nodePoolParams.Replicas = int32(1)
			GinkgoLogr.Info("resolved OpenShift versions",
				"controlPlaneRequested", clusterParams.OpenshiftVersionId,
				"nodePool", nodePoolParams.OpenshiftVersionId,
				"minimum", minOpenshiftVersion)
			err = tc.CreateNodePoolFromParam20261001(ctx, GinkgoLogr,
				*resourceGroup.Name, clusterParams.ManagedResourceGroupName,
				customerClusterName, nodePoolParams, framework.NodePoolCreationTimeout)
			Expect(err).NotTo(HaveOccurred(), "failed to create node pool")

			computeFactory := tc.GetARMComputeClientFactoryOrDie(ctx)
			expectedWorkerCount := int(nodePoolParams.Replicas)

			// workerVMIdentities returns the attached user-assigned identity IDs per node
			// pool VM, and errors unless exactly the expected number of node pool VMs exist.
			// Insisting on the exact count is what lets the day 2 poll below wait out the
			// transient surge VM that the rollout creates before deleting the old machine.
			workerVMIdentities := func(ctx context.Context) (map[string][]string, error) {
				vms, err := framework.GetVirtualMachinesInResourceGroup(ctx, computeFactory, clusterParams.ManagedResourceGroupName, expectedWorkerCount)
				if err != nil {
					return nil, err
				}
				workerVMs := filterNodePoolVMs(vms, nodePoolName)
				if len(workerVMs) != expectedWorkerCount {
					names := make([]string, 0, len(workerVMs))
					for _, vm := range workerVMs {
						names = append(names, *vm.Name)
					}
					return nil, fmt.Errorf("expected exactly %d VM(s) for node pool %s, got %d (%v)",
						expectedWorkerCount, nodePoolName, len(workerVMs), names)
				}
				identities := map[string][]string{}
				for _, vm := range workerVMs {
					identities[*vm.Name] = vmUserAssignedIdentityIDs(vm)
				}
				return identities, nil
			}

			// The ARM GET above only proves the RP persisted the field. The pull can still
			// fail anonymously if CAPZ never attached the identity to the worker VMs — for
			// example when the Managed Identity Operator grant did not take effect.
			// Asserting attachment here separates that failure from a credential provider
			// that ran and was rejected; both surface as "unauthorized" at the pull.
			//
			// Both the read and the attachment are retried. The read because the VM list is a
			// subscription-scoped ARM call and this subscription's budget is shared, so a 429
			// would otherwise fail day 1 for no product reason. The attachment because the
			// node pool LRO settling does not prove the VM's identity block is already
			// observable: this asserts on the ARM representation, not on what CAPZ intended,
			// and the two can differ briefly. A genuinely missing identity still fails, just
			// after the bound rather than on the first read.
			By("[day 1] verifying the day 1 MI is attached to the worker VMs")
			var day1Identities map[string][]string
			Eventually(func() error {
				identities, err := workerVMIdentities(ctx)
				if err != nil {
					return err
				}
				for vmName, attached := range identities {
					if !slices.Contains(attached, strings.ToLower(day1MIResourceID)) {
						return fmt.Errorf("worker VM %s does not yet have the day 1 ACR pull MI %s attached, has %v",
							vmName, day1MIResourceID, attached)
					}
				}
				day1Identities = identities
				return nil
			}).WithContext(ctx).WithTimeout(vmIdentityAttachTimeout).WithPolling(15*time.Second).Should(Succeed(),
				"worker VMs in managed resource group %s never showed the day 1 ACR pull MI %s attached",
				clusterParams.ManagedResourceGroupName, day1MIResourceID)
			for vmName, attached := range day1Identities {
				GinkgoLogr.Info("worker VM identities after create", "vm", vmName, "userAssignedIdentities", attached)
			}

			// The pull pod has a 3 minute budget and only one node to land on. Gate on node
			// readiness first so a slow join surfaces as "node not ready" rather than as an
			// image pull timeout that looks like a credential failure. The node pool LRO
			// settling only means Azure accepted the pool, not that the kubelet has registered
			// and joined — poll with the same shape as day 2.
			By("[day 1] verifying the node pool's node joined and is ready")
			Eventually(func(g Gomega) {
				g.Expect(verifiers.VerifyNodeCount(customerClusterName, expectedWorkerCount).Verify(ctx, adminRESTConfig)).
					To(Succeed(), "node count should reach %d replicas after node pool creation", expectedWorkerCount)
				g.Expect(verifiers.VerifyNodesReady().Verify(ctx, adminRESTConfig)).
					To(Succeed(), "all nodes should be ready after node pool creation")
			}).WithContext(ctx).WithTimeout(framework.NodePoolScalingTimeout).WithPolling(30*time.Second).Should(Succeed(),
				"node pool %s never reached %d ready node(s) after creation", nodePoolName, expectedWorkerCount)

			verifyACRPullFromNodes(ctx, adminRESTConfig, day1PullNamespace, acrLoginServer, "day 1")

			//
			// Day 2: change the identity on the running cluster and prove the change reaches
			// the nodes that already exist.
			//

			By("[day 2] updating containerRegistry.managedIdentity to the day 2 MI via PATCH")
			updateResp, err := framework.UpdateHCPCluster20261001(ctx, hcpClient,
				*resourceGroup.Name, customerClusterName,
				hcpsdk20261001preview.HcpOpenShiftCluster{
					Properties: &hcpsdk20261001preview.HcpOpenShiftClusterProperties{
						Platform: &hcpsdk20261001preview.PlatformProfile{
							ContainerRegistry: &hcpsdk20261001preview.ContainerRegistryProfile{
								ManagedIdentity: to.Ptr(day2MIResourceID),
							},
						},
					},
				},
				framework.UpdateHCPClusterTimeout)
			Expect(err).NotTo(HaveOccurred(), "failed to update containerRegistry.managedIdentity via PATCH")
			Expect(updateResp).NotTo(BeNil(), "containerRegistry update response was nil")
			Expect(updateResp.Properties).NotTo(BeNil(), "containerRegistry update response Properties was nil")
			Expect(updateResp.Properties.ProvisioningState).NotTo(BeNil(), "containerRegistry update response ProvisioningState was nil")
			Expect(*updateResp.Properties.ProvisioningState).To(Equal(hcpsdk20261001preview.ProvisioningStateSucceeded),
				"cluster provisioning state should be Succeeded after the containerRegistry update")

			By("[day 2] verifying the cluster reports the day 2 MI via GET")
			clusterAfterUpdate, err := hcpClient.Get(ctx, *resourceGroup.Name, customerClusterName, nil)
			Expect(err).NotTo(HaveOccurred(), "failed to GET cluster after the containerRegistry update")
			Expect(clusterAfterUpdate.Properties).NotTo(BeNil(), "cluster properties was nil after the update")
			Expect(clusterAfterUpdate.Properties.Platform).NotTo(BeNil(), "cluster platform was nil after the update")
			Expect(clusterAfterUpdate.Properties.Platform.ContainerRegistry).NotTo(BeNil(), "containerRegistry should still be set after the update")
			Expect(clusterAfterUpdate.Properties.Platform.ContainerRegistry.ManagedIdentity).NotTo(BeNil(), "containerRegistry.managedIdentity should be set after the update")
			Expect(strings.EqualFold(*clusterAfterUpdate.Properties.Platform.ContainerRegistry.ManagedIdentity, day2MIResourceID)).To(BeTrue(),
				"containerRegistry.managedIdentity after the update: got %q want %q",
				*clusterAfterUpdate.Properties.Platform.ContainerRegistry.ManagedIdentity, day2MIResourceID)

			// The ARM PATCH reporting Succeeded proves the new identity reached the
			// HostedCluster spec, but not that it reached the nodes. The RP gates that state
			// on Cluster Service and on the observed HostedCluster .Spec
			// (clusterServiceClusterContainerRegistryPullMISpecMatchesDesired and
			// hypershiftHostedClusterContainerRegistrySpecMatchesDesired in
			// backend/pkg/controllers/cluster/operations/operation_cluster_update_state_calculation.go);
			// neither looks at .Status, NodePools or machines. Since this field is +rollout,
			// at Succeeded the workers can still be the old VMs carrying the old identity, so
			// observed Azure state is the only sound signal here. Poll until the node pool is
			// back to its steady-state
			// size with the day 2 MI attached and the day 1 MI gone. Requiring the day 1 MI
			// to be absent is what makes the pull below attributable to the day 2 MI: kubelet
			// cannot authenticate as an identity that is not attached to its VM.
			By("[day 2] waiting for the node rollout to re-identify the worker VMs")
			var day2Identities map[string][]string
			Eventually(func() error {
				identities, err := workerVMIdentities(ctx)
				if err != nil {
					return err
				}
				for vmName, attached := range identities {
					if !slices.Contains(attached, strings.ToLower(day2MIResourceID)) {
						return fmt.Errorf("worker VM %s does not yet have the day 2 MI %s attached, has %v", vmName, day2MIResourceID, attached)
					}
					if slices.Contains(attached, strings.ToLower(day1MIResourceID)) {
						return fmt.Errorf("worker VM %s still has the day 1 MI %s attached, has %v", vmName, day1MIResourceID, attached)
					}
				}
				day2Identities = identities
				return nil
			}).WithContext(ctx).WithTimeout(nodeRolloutTimeout).WithPolling(30*time.Second).Should(Succeed(),
				"node pool %s never rolled onto the day 2 ACR pull MI %s", nodePoolName, day2MIResourceID)
			// Logged once the poll has converged rather than inside it: a VM that already
			// satisfies the check would otherwise re-log identical state on every poll while
			// the rest of the node pool is still rolling.
			for vmName, attached := range day2Identities {
				GinkgoLogr.Info("worker VM identities after rollout", "vm", vmName, "userAssignedIdentities", attached)
			}

			// VerifyNodeCount and VerifyNodesReady are one-shot List calls. The guest node
			// objects lag Azure VMs in both directions: the replacement VM can exist before
			// its kubelet registers, and the old node object survives until the cloud node
			// manager reaps it. Retrying the pair is the same shape as day 1 and
			// nodepool_labels_taints.go.
			By("[day 2] verifying the replacement node joined and is ready")
			Eventually(func(g Gomega) {
				g.Expect(verifiers.VerifyNodeCount(customerClusterName, expectedWorkerCount).Verify(ctx, adminRESTConfig)).
					To(Succeed(), "node count should settle back to %d replicas after the rollout", expectedWorkerCount)
				g.Expect(verifiers.VerifyNodesReady().Verify(ctx, adminRESTConfig)).
					To(Succeed(), "all nodes should be ready after the rollout")
			}).WithContext(ctx).WithTimeout(framework.NodePoolScalingTimeout).WithPolling(30*time.Second).Should(Succeed(),
				"node pool %s never settled back to %d ready node(s) after the day 2 rollout", nodePoolName, expectedWorkerCount)

			verifyACRPullFromNodes(ctx, adminRESTConfig, day2PullNamespace, acrLoginServer, "day 2")
		})
})
