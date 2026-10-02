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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
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

	acrPullImagePullTimeout       = 2 * time.Minute  // observed ~15s (3 runs), 8x safety
	acrPullGetAdminConfigTimeout  = 2 * time.Minute  // observed ~1m, 2x safety
	acrPullNodePoolScalingTimeout = 5 * time.Minute  // observed <1m, 5x safety
	day2ConfigPatchTimeout        = 5 * time.Minute  // observed ~20-71s (3 runs), 4x safety
	day2RolloutTimeout            = 15 * time.Minute // observed ~8.5-10m (3 runs), 1.5x safety
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

// lookupMIPrincipalID retries because these reads land in a burst of ARM traffic right after
// CreateClusterCustomerResources, and throttling on the shared subscription budget has been seen in CI.
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

// isARMThrottlingError matches on status code, not error code, since ARM returns several
// distinct throttling codes for the shared-subscription 429s that are routine in CI.
func isARMThrottlingError(err error) bool {
	var respErr *azcore.ResponseError
	return errors.As(err, &respErr) && respErr.StatusCode == http.StatusTooManyRequests
}

// grantBuiltInRoleWithRetry retries while the principal is still propagating to the Authorization
// RP (PrincipalNotFound) or ARM is throttling; any other error stops the retry immediately.
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

// vmUserAssignedIdentityIDs lowercases IDs so they can be compared against an ARM resource ID whose casing ARM does not preserve.
func vmUserAssignedIdentityIDs(vm *armcompute.VirtualMachine) ([]string, error) {
	if vm == nil {
		return nil, errors.New("virtual machine was nil")
	}
	if vm.Identity == nil {
		return nil, errors.New("virtual machine Identity block was nil")
	}
	ids := make([]string, 0, len(vm.Identity.UserAssignedIdentities))
	for id := range vm.Identity.UserAssignedIdentities {
		ids = append(ids, strings.ToLower(id))
	}
	return ids, nil
}

// verifyACRPullFromNodes uses no imagePullSecrets: a successful pull proves the node's CAPZ-attached
// identity authenticated via the kubelet credential provider. Each call needs a fresh namespace since
// VerifyImagePulled succeeds on *any* pod in it, and reuse would let day 1's cached image satisfy day 2.
func verifyACRPullFromNodes(ctx context.Context, adminRESTConfig *rest.Config, namespace, acrLoginServer, phase string) {
	By(fmt.Sprintf("[%s] creating namespace %s for the ACR image pull check", phase, namespace))
	kubeClient, err := kubernetes.NewForConfig(adminRESTConfig)
	Expect(err).NotTo(HaveOccurred(), "failed to create kubernetes client for the %s ACR pull check", phase)

	// Register cleanup before namespace creation so it runs regardless of success/failure.
	DeferCleanup(func(ctx context.Context) {
		err := kubeClient.CoreV1().Namespaces().Delete(ctx, namespace, metav1.DeleteOptions{})
		if err != nil {
			GinkgoLogr.Info("failed to delete namespace during cleanup", "namespace", namespace, "error", err)
		}
	})

	Eventually(func() error {
		_, err := kubeClient.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: namespace},
		}, metav1.CreateOptions{})
		if err != nil && apierrors.IsAlreadyExists(err) {
			_, err = kubeClient.CoreV1().Namespaces().Get(ctx, namespace, metav1.GetOptions{})
		}
		return err
	}).WithContext(ctx).WithTimeout(30*time.Second).WithPolling(1*time.Second).Should(Succeed(),
		"failed to create namespace %s", namespace)

	By(fmt.Sprintf("[%s] creating a service account for the image pull verification pod", phase))
	var sa *corev1.ServiceAccount
	Eventually(func() error {
		var err error
		sa, err = kubeClient.CoreV1().ServiceAccounts(namespace).Create(ctx, &corev1.ServiceAccount{
			ObjectMeta: metav1.ObjectMeta{Name: "acr-pull-test"},
		}, metav1.CreateOptions{})
		if err != nil && apierrors.IsAlreadyExists(err) {
			sa, err = kubeClient.CoreV1().ServiceAccounts(namespace).Get(ctx, "acr-pull-test", metav1.GetOptions{})
		}
		return err
	}).WithContext(ctx).WithTimeout(30*time.Second).WithPolling(1*time.Second).Should(Succeed(),
		"failed to create service account in %s", namespace)

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

// Full lifecycle of ACR pull via managed identity (ARO-24037): day 1 cluster creation and pull,
// day 2 MI swap and pull. Self-contained per test/AGENTS.md (sharing a cluster would duplicate
// expensive infrastructure setup). Observed wall-clock time ~28-34m across 3 runs, well under the
// 150m suite TestTimeout — but the per-phase timeouts below are stuck-operation backstops, not an
// additive budget, and their worst-case sum exceeds 150m (dominated by shared framework defaults
// in test/util/framework/constants.go and CreateClusterCustomerResources20261001, which this test
// does not control).
var _ = Describe("Customer", func() {
	timeBombDeadline := framework.V20261001PreviewDeploymentDeadline

	It("should be able to create a cluster with ACR pull via managed identity and pull from a private ACR",
		labels.RequireNothing,
		labels.Medium,
		labels.Positive,
		labels.AroRpApiCompatible,
		labels.CreateCluster,
		labels.Slow,
		labels.MIContainers(2),
		func(ctx context.Context) {
			const (
				customerClusterName = "acr-pull"
				nodePoolName        = "np-1"
				day1MIName          = "acr-pull-mi-1"
				day2MIName          = "acr-pull-mi-2"

				// ACR pull requires OCP >= 4.22 (kubelet credential provider only from 4.22).
				minOpenshiftVersion = "4.22"

				miLookupTimeout         = 1 * time.Minute // observed <20s, 3x safety
				roleAssignmentTimeout   = 2 * time.Minute // observed <20s, 6x safety
				vmIdentityAttachTimeout = 3 * time.Minute // observed <30s, 6x safety; brief because a miss here is a real error, not rollout lag

				day1PullNamespace = "acr-pull-test-day1"
				day2PullNamespace = "acr-pull-test-day2"
			)

			// Setup: infrastructure, ACR, and managed identities for day 1 and day 2.
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

			tc := framework.NewTestContext()

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 2, framework.IdentityContainerAssignmentRetryInterval)
				Expect(err).NotTo(HaveOccurred(), "failed to assign pooled identity containers")
			}

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "acr-pull", tc.Location())
			Expect(err).NotTo(HaveOccurred(), "failed to create resource group")

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

			msiClient, err := armmsi.NewUserAssignedIdentitiesClient(subscriptionID, cred, nil)
			Expect(err).NotTo(HaveOccurred(), "failed to create MSI client")

			By("creating the day 1 and day 2 ACR pull managed identities")
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

			// Day 1: create with the first identity and pull.
			By("building and creating the cluster with containerRegistry set to the day 1 MI")
			clusterParams.ContainerRegistryManagedIdentity = to.Ptr(day1MIResourceID)
			clusterResource, err := framework.BuildHCPClusterFromParams20261001(clusterParams, tc.Location(), nil)
			Expect(err).NotTo(HaveOccurred(), "failed to build HCP cluster from params")

			hcpClient := tc.Get20261001ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient()
			_, err = framework.CreateHCPClusterAndWait20261001(ctx, GinkgoLogr, hcpClient,
				*resourceGroup.Name, customerClusterName, clusterResource, framework.ClusterCreationTimeout)
			if framework.IsAPINotDeployedError(err) {
				if time.Now().Before(timeBombDeadline) {
					Skip(fmt.Sprintf("v20261001preview API not yet deployed; skipping until %s", timeBombDeadline.Format(time.RFC3339)))
				}
				Fail(fmt.Sprintf("v20261001preview API still not deployed as of %s deadline", timeBombDeadline.Format(time.RFC3339)))
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
				acrPullGetAdminConfigTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to get admin REST config")

			By("verifying cluster health")
			err = verifiers.VerifyHCPCluster(ctx, adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "cluster health check failed")

			// Node pools need a concrete Major.Minor.Patch (unlike the control plane's bare
			// major.minor) and must not outrun the control plane, so the desired version is taken
			// from ClusterVersion.status.desired rather than waiting on a Completed history entry,
			// which can still be empty once the control plane is ready.
			By("deriving the node pool version from the installed control plane")
			configClient, err := configv1client.NewForConfig(adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to create OpenShift config client")
			var nodePoolVersion string
			Eventually(func() error {
				clusterVersion, err := configClient.ClusterVersions().Get(ctx, "version", metav1.GetOptions{})
				if err != nil {
					return fmt.Errorf("failed to get ClusterVersion: %w", err)
				}
				if clusterVersion.Status.Desired.Version == "" {
					return fmt.Errorf("ClusterVersion desired version not yet available")
				}
				nodePoolVersion = clusterVersion.Status.Desired.Version
				return nil
			}).WithContext(ctx).WithTimeout(acrPullGetAdminConfigTimeout).WithPolling(5*time.Second).Should(Succeed(),
				"failed to retrieve ClusterVersion desired version")

			By("creating a node pool")
			nodePoolParams := framework.NewDefaultNodePoolParams20261001()
			nodePoolParams.ChannelGroup = clusterParams.ChannelGroup
			nodePoolParams.OpenshiftVersionId = nodePoolVersion
			nodePoolParams.ClusterName = customerClusterName
			nodePoolParams.NodePoolName = nodePoolName
			// One node proves the credential provider works and limits day 2 to a single VM replacement.
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

			// workerVMIdentities requires exactly the expected VM count so the day 2 poll below waits
			// out the rollout's transient surge VM instead of reading it as the final state.
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
					ids, err := vmUserAssignedIdentityIDs(vm)
					if err != nil {
						return nil, fmt.Errorf("failed to get identities for VM %s: %w", *vm.Name, err)
					}
					identities[*vm.Name] = ids
				}
				return identities, nil
			}

			// Asserts attachment separately from the pull so a missing CAPZ attachment (the MIO
			// grant didn't take effect) isn't indistinguishable from a rejected credential provider
			// — both otherwise surface as "unauthorized". Retried because the VM list read is a
			// subscription-scoped call subject to shared 429s, and because node pool LRO settling
			// doesn't guarantee the VM's identity block is observable yet.
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

			// Gated on readiness first so a slow join surfaces as "node not ready" rather than a
			// confusing image-pull timeout.
			By("[day 1] verifying the node pool's node joined and is ready")
			Eventually(func(g Gomega) {
				g.Expect(verifiers.VerifyNodeCount(customerClusterName, expectedWorkerCount).Verify(ctx, adminRESTConfig)).
					To(Succeed(), "node count should reach %d replicas after node pool creation", expectedWorkerCount)
				g.Expect(verifiers.VerifyNodesReady().Verify(ctx, adminRESTConfig)).
					To(Succeed(), "all nodes should be ready after node pool creation")
			}).WithContext(ctx).WithTimeout(acrPullNodePoolScalingTimeout).WithPolling(30*time.Second).Should(Succeed(),
				"node pool %s never reached %d ready node(s) after creation", nodePoolName, expectedWorkerCount)

			verifyACRPullFromNodes(ctx, adminRESTConfig, day1PullNamespace, acrLoginServer, "day 1")

			// Day 2: swap the managed identity and verify nodes roll over. The PATCH wait and the
			// rollout that follows are budgeted separately (day2ConfigPatchTimeout vs day2RolloutTimeout);
			// other day-2 features may differ.
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
				day2ConfigPatchTimeout)
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

			// ARM PATCH Succeeded only means CS/HC .Spec accepted the change — containerRegistry is
			// +rollout, so the worker VMs must still roll, and Azure attaches identities
			// non-atomically (day 2 can appear before day 1 is removed), so both conditions are
			// polled together.
			By("[day 2] waiting for node rollout to swap MI (day 2 attach + day 1 detach)")
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
			}).WithContext(ctx).WithTimeout(day2RolloutTimeout).WithPolling(30*time.Second).Should(Succeed(),
				"node pool %s never completed MI swap (day 2 attach + day 1 detach) for ACR pull", nodePoolName)
			// Logged after the poll converges, not inside it, so a VM that already satisfies the
			// check isn't re-logged every iteration while the rest of the pool rolls.
			for vmName, attached := range day2Identities {
				GinkgoLogr.Info("worker VM identities after rollout", "vm", vmName, "userAssignedIdentities", attached)
			}

			// Retried because guest node objects lag Azure VMs in both directions: the replacement
			// can exist before its kubelet registers, and the old node survives until reaped.
			By("[day 2] verifying the replacement node joined and is ready")
			Eventually(func(g Gomega) {
				g.Expect(verifiers.VerifyNodeCount(customerClusterName, expectedWorkerCount).Verify(ctx, adminRESTConfig)).
					To(Succeed(), "node count should settle back to %d replicas after the rollout", expectedWorkerCount)
				g.Expect(verifiers.VerifyNodesReady().Verify(ctx, adminRESTConfig)).
					To(Succeed(), "all nodes should be ready after the rollout")
			}).WithContext(ctx).WithTimeout(acrPullNodePoolScalingTimeout).WithPolling(30*time.Second).Should(Succeed(),
				"node pool %s never settled back to %d ready node(s) after the day 2 rollout", nodePoolName, expectedWorkerCount)

			verifyACRPullFromNodes(ctx, adminRESTConfig, day2PullNamespace, acrLoginServer, "day 2")
		})
})
