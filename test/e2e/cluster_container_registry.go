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
	"regexp"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/google/uuid"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v3"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerregistry/armcontainerregistry"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/msi/armmsi"

	hcpsdk20260901preview "github.com/Azure/ARO-HCP/test/sdk/v20260901preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

const (
	managedIdentityOperatorRoleID = "f1a07417-d97a-45cb-824c-7a7467783830"
	acrPullRoleID                 = "7f951dda-4ed3-4680-a7ca-43fe172d538d"

	// Azure Container Registry names must be 5-50 alphanumeric characters.
	acrNameMaxLength = 50
)

var nonAlphanumeric = regexp.MustCompile(`[^a-zA-Z0-9]`)

func createACRPullMI(
	ctx context.Context,
	msiClient *armmsi.UserAssignedIdentitiesClient,
	rgName string,
	miName string,
	location *string,
) (string, error) {
	resp, err := msiClient.CreateOrUpdate(ctx, rgName, miName, armmsi.Identity{
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

func grantRole(
	ctx context.Context,
	roleAssignmentsClient *armauthorization.RoleAssignmentsClient,
	subscriptionID string,
	scope string,
	principalID string,
	roleID string,
) error {
	roleDefID := fmt.Sprintf("/subscriptions/%s/providers/Microsoft.Authorization/roleDefinitions/%s", subscriptionID, roleID)
	assignmentName := uuid.NewSHA1(uuid.NameSpaceDNS, []byte(scope+"|"+principalID+"|"+roleDefID)).String()
	_, err := roleAssignmentsClient.Create(ctx, scope, assignmentName, armauthorization.RoleAssignmentCreateParameters{
		Properties: &armauthorization.RoleAssignmentProperties{
			PrincipalID:      to.Ptr(principalID),
			RoleDefinitionID: to.Ptr(roleDefID),
			PrincipalType:    to.Ptr(armauthorization.PrincipalTypeServicePrincipal),
		},
	}, nil)
	return err
}

func lookupCAPZPrincipalID(
	ctx context.Context,
	msiClient *armmsi.UserAssignedIdentitiesClient,
	clusterParams framework.ClusterParams20260901,
) (string, error) {
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
				customerClusterName = "acr-pull-create"
				apiVersion          = "2026-09-01-preview"
				imagePullTimeout    = 3 * time.Minute
			)

			tc := framework.NewTestContext()

			By("checking API version availability")
			apiAvailable, err := tc.IsHCPAPIVersionAvailable(ctx, apiVersion)
			Expect(err).NotTo(HaveOccurred(), "failed to check API version availability")
			if !apiAvailable {
				if time.Now().After(framework.V20260901PreviewDeploymentDeadline) {
					Fail(fmt.Sprintf("API version %s should be fully available by %s", apiVersion, framework.V20260901PreviewDeploymentDeadline.Format(time.RFC3339)))
				}
				Skip(fmt.Sprintf("API version %s not available in this environment", apiVersion))
			}

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, framework.IdentityContainerAssignmentRetryInterval)
				Expect(err).NotTo(HaveOccurred(), "failed to assign pooled identity containers")
			}

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "acr-pull-create", tc.Location())
			Expect(err).NotTo(HaveOccurred(), "failed to create resource group")

			subscriptionID, err := tc.SubscriptionID(ctx)
			Expect(err).NotTo(HaveOccurred(), "failed to get subscription ID")
			cred, err := tc.AzureCredential()
			Expect(err).NotTo(HaveOccurred(), "failed to get Azure credential")

			By("creating a private Azure Container Registry")
			acrName := nonAlphanumeric.ReplaceAllString(*resourceGroup.Name, "")
			if len(acrName) > acrNameMaxLength {
				acrName = acrName[:acrNameMaxLength]
			}
			registriesClient, err := armcontainerregistry.NewRegistriesClient(subscriptionID, cred, nil)
			Expect(err).NotTo(HaveOccurred(), "failed to create ACR registries client")

			acrPoller, err := registriesClient.BeginCreate(ctx, *resourceGroup.Name, acrName, armcontainerregistry.Registry{
				Location: resourceGroup.Location,
				SKU:      &armcontainerregistry.SKU{Name: to.Ptr(armcontainerregistry.SKUNameBasic)},
			}, nil)
			Expect(err).NotTo(HaveOccurred(), "failed to start ACR creation")
			acrResp, err := acrPoller.PollUntilDone(ctx, nil)
			Expect(err).NotTo(HaveOccurred(), "failed to create ACR %s", acrName)
			Expect(acrResp.ID).NotTo(BeNil(), "ACR resource ID was nil")
			acrResourceID := *acrResp.ID
			Expect(acrResp.Properties).NotTo(BeNil(), "ACR response Properties was nil")
			Expect(acrResp.Properties.LoginServer).NotTo(BeNil(), "ACR LoginServer was nil")
			acrLoginServer := *acrResp.Properties.LoginServer
			GinkgoWriter.Printf("Created ACR: %s (login: %s)\n", acrName, acrLoginServer)

			By("importing a test image into the ACR")
			importPoller, err := registriesClient.BeginImportImage(ctx, *resourceGroup.Name, acrName, armcontainerregistry.ImportImageParameters{
				Source: &armcontainerregistry.ImportSource{
					RegistryURI: to.Ptr("registry.access.redhat.com"),
					SourceImage: to.Ptr("ubi9/ubi-minimal:latest"),
				},
				TargetTags: []*string{to.Ptr("ubi-minimal:latest")},
				Mode:       to.Ptr(armcontainerregistry.ImportModeForce),
			}, nil)
			Expect(err).NotTo(HaveOccurred(), "failed to start image import into ACR")
			_, err = importPoller.PollUntilDone(ctx, nil)
			Expect(err).NotTo(HaveOccurred(), "failed to import image into ACR %s", acrName)
			GinkgoWriter.Printf("Imported registry.access.redhat.com/ubi9/ubi-minimal:latest into %s\n", acrLoginServer)

			By("creating ACR pull managed identity")
			msiClient, err := armmsi.NewUserAssignedIdentitiesClient(subscriptionID, cred, nil)
			Expect(err).NotTo(HaveOccurred(), "failed to create MSI client")
			acrPullMIResourceID, err := createACRPullMI(ctx, msiClient, *resourceGroup.Name, "acr-pull-mi", resourceGroup.Location)
			Expect(err).NotTo(HaveOccurred(), "failed to create ACR pull MI")

			acrPullMI, err := msiClient.Get(ctx, *resourceGroup.Name, "acr-pull-mi", nil)
			Expect(err).NotTo(HaveOccurred(), "failed to get ACR pull MI details")
			Expect(acrPullMI.Properties).NotTo(BeNil(), "ACR pull MI properties was nil")
			Expect(acrPullMI.Properties.PrincipalID).NotTo(BeNil(), "ACR pull MI principal ID was nil")
			acrPullMIPrincipalID := *acrPullMI.Properties.PrincipalID

			roleAssignmentsClient, err := armauthorization.NewRoleAssignmentsClient(subscriptionID, cred, nil)
			Expect(err).NotTo(HaveOccurred(), "failed to create role assignments client")

			By("granting AcrPull to the ACR pull MI on the ACR")
			err = grantRole(ctx, roleAssignmentsClient, subscriptionID, acrResourceID, acrPullMIPrincipalID, acrPullRoleID)
			Expect(err).NotTo(HaveOccurred(), "failed to grant AcrPull to MI on ACR")

			By("granting CAPZ 'Managed Identity Operator' on the ACR pull MI")
			clusterParams := framework.NewDefaultClusterParams20260901()
			clusterParams.ClusterName = customerClusterName
			clusterParams.OpenshiftVersionId = "4.22"
			clusterParams.ManagedResourceGroupName = framework.SuffixName(*resourceGroup.Name, "-managed", 64)

			By("creating customer resources (infrastructure and managed identities)")
			clusterParams, err = tc.CreateClusterCustomerResources20260901(ctx,
				resourceGroup, clusterParams, map[string]any{},
				TestArtifactsFS, framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create cluster customer resources")

			capzPrincipalID, err := lookupCAPZPrincipalID(ctx, msiClient, clusterParams)
			Expect(err).NotTo(HaveOccurred(), "failed to look up CAPZ principal ID")
			err = grantRole(ctx, roleAssignmentsClient, subscriptionID, acrPullMIResourceID, capzPrincipalID, managedIdentityOperatorRoleID)
			Expect(err).NotTo(HaveOccurred(), "failed to grant CAPZ MIO on ACR pull MI")

			By("building and creating the cluster with containerRegistry set")
			clusterParams.ContainerRegistryManagedIdentity = to.Ptr(acrPullMIResourceID)
			clusterResource, err := framework.BuildHCPClusterFromParams20260901(clusterParams, tc.Location(), nil)
			Expect(err).NotTo(HaveOccurred(), "failed to build HCP cluster from params")

			hcpClient := tc.Get20260901ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient()
			_, err = framework.CreateHCPClusterAndWait20260901(ctx, GinkgoLogr, hcpClient,
				*resourceGroup.Name, customerClusterName, clusterResource, framework.ClusterCreationTimeout)
			Expect(err).NotTo(HaveOccurred(), "failed to create HCP cluster with containerRegistry set")

			By("creating a node pool")
			nodePoolParams := framework.NewDefaultNodePoolParams20260901()
			nodePoolParams.ClusterName = customerClusterName
			nodePoolParams.NodePoolName = "np-1"
			nodePoolParams.Replicas = int32(2)
			err = tc.CreateNodePoolFromParam20260901(ctx, GinkgoLogr,
				*resourceGroup.Name, clusterParams.ManagedResourceGroupName,
				customerClusterName, nodePoolParams, framework.NodePoolCreationTimeout)
			Expect(err).NotTo(HaveOccurred(), "failed to create node pool")

			By("verifying containerRegistry.managedIdentity via GET")
			actualCluster, err := hcpClient.Get(ctx, *resourceGroup.Name, customerClusterName, nil)
			Expect(err).NotTo(HaveOccurred(), "failed to GET cluster")
			Expect(actualCluster.Properties).NotTo(BeNil(), "cluster properties was nil")
			Expect(actualCluster.Properties.Platform).NotTo(BeNil(), "cluster platform was nil")
			Expect(actualCluster.Properties.Platform.ContainerRegistry).NotTo(BeNil(), "containerRegistry should be set")
			Expect(actualCluster.Properties.Platform.ContainerRegistry.ManagedIdentity).NotTo(BeNil(), "containerRegistry.managedIdentity should be set")
			Expect(strings.EqualFold(*actualCluster.Properties.Platform.ContainerRegistry.ManagedIdentity, acrPullMIResourceID)).To(BeTrue(),
				"containerRegistry.managedIdentity mismatch: got %q want %q",
				*actualCluster.Properties.Platform.ContainerRegistry.ManagedIdentity, acrPullMIResourceID)

			By("getting admin credentials")
			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster20240610(
				ctx,
				tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
				*resourceGroup.Name,
				customerClusterName,
				framework.GetAdminRESTConfigTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to get admin REST config")

			By("verifying cluster health")
			err = verifiers.VerifyHCPCluster(ctx, adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "cluster health check failed")

			By("creating test namespace for ACR image pull verification")
			kubeClient, err := kubernetes.NewForConfig(adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to create kubernetes client")

			pullTestNamespace := "acr-pull-test"
			_, err = kubeClient.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{Name: pullTestNamespace},
			}, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred(), "failed to create namespace %s", pullTestNamespace)
			DeferCleanup(func(ctx context.Context) {
				_ = kubeClient.CoreV1().Namespaces().Delete(ctx, pullTestNamespace, metav1.DeleteOptions{})
			})

			By("creating a service account for the image pull verification pod")
			sa, err := kubeClient.CoreV1().ServiceAccounts(pullTestNamespace).Create(ctx, &corev1.ServiceAccount{
				ObjectMeta: metav1.ObjectMeta{Name: "acr-pull-test"},
			}, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred(), "failed to create service account in %s", pullTestNamespace)

			createACRPullTestPod := func(podName string) {
				testImage := fmt.Sprintf("%s/ubi-minimal:latest", acrLoginServer)
				pod := &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      podName,
						Namespace: pullTestNamespace,
					},
					Spec: corev1.PodSpec{
						ServiceAccountName:           sa.Name,
						AutomountServiceAccountToken: to.Ptr(false),
						Containers: []corev1.Container{
							{
								Name:            "acr-pull-test",
								Image:           testImage,
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
				}
				_, podErr := kubeClient.CoreV1().Pods(pullTestNamespace).Create(ctx, pod, metav1.CreateOptions{})
				Expect(podErr).NotTo(HaveOccurred(), "failed to create ACR pull test pod %s", podName)
			}

			By(fmt.Sprintf("deploying a pod that pulls from private ACR %s — no imagePullSecrets", acrLoginServer))
			createACRPullTestPod("acr-pull-test")

			By("verifying the image was pulled from the private ACR")
			err = verifiers.VerifyImagePulled(pullTestNamespace, acrLoginServer, "ubi-minimal", imagePullTimeout).
				Verify(ctx, adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to pull image from private ACR %s — the credential provider did not authenticate the pull", acrLoginServer)

			By("creating a second ACR pull MI for day-2 update")
			acrPullMI2ResourceID, err := createACRPullMI(ctx, msiClient, *resourceGroup.Name, "acr-pull-mi-2", resourceGroup.Location)
			Expect(err).NotTo(HaveOccurred(), "failed to create second ACR pull MI")

			acrPullMI2, err := msiClient.Get(ctx, *resourceGroup.Name, "acr-pull-mi-2", nil)
			Expect(err).NotTo(HaveOccurred(), "failed to get second ACR pull MI details")
			Expect(acrPullMI2.Properties).NotTo(BeNil(), "second ACR pull MI properties was nil")
			Expect(acrPullMI2.Properties.PrincipalID).NotTo(BeNil(), "second ACR pull MI principal ID was nil")
			acrPullMI2PrincipalID := *acrPullMI2.Properties.PrincipalID

			By("granting AcrPull and MIO for the second MI")
			err = grantRole(ctx, roleAssignmentsClient, subscriptionID, acrResourceID, acrPullMI2PrincipalID, acrPullRoleID)
			Expect(err).NotTo(HaveOccurred(), "failed to grant AcrPull to second MI on ACR")
			err = grantRole(ctx, roleAssignmentsClient, subscriptionID, acrPullMI2ResourceID, capzPrincipalID, managedIdentityOperatorRoleID)
			Expect(err).NotTo(HaveOccurred(), "failed to grant CAPZ MIO on second ACR pull MI")

			By("updating containerRegistry to the second MI via PATCH (day-2 update)")
			updateResp, err := framework.UpdateHCPCluster20260901(
				ctx,
				hcpClient,
				*resourceGroup.Name,
				customerClusterName,
				hcpsdk20260901preview.HcpOpenShiftClusterUpdate{
					Properties: &hcpsdk20260901preview.HcpOpenShiftClusterPropertiesUpdate{
						Platform: &hcpsdk20260901preview.PlatformProfileUpdate{
							ContainerRegistry: &hcpsdk20260901preview.ContainerRegistryProfile{
								ManagedIdentity: to.Ptr(acrPullMI2ResourceID),
							},
						},
					},
				},
				framework.UpdateHCPClusterTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to update containerRegistry via PATCH")
			Expect(updateResp).NotTo(BeNil(), "update response was nil")
			Expect(updateResp.Properties).NotTo(BeNil(), "update response Properties was nil")
			Expect(updateResp.Properties.ProvisioningState).NotTo(BeNil(), "update response ProvisioningState was nil")
			Expect(*updateResp.Properties.ProvisioningState).To(
				Equal(hcpsdk20260901preview.ProvisioningStateSucceeded),
				"cluster provisioning state should be Succeeded after updating containerRegistry",
			)

			By("verifying containerRegistry points to the second MI via GET")
			clusterAfterUpdate, err := hcpClient.Get(ctx, *resourceGroup.Name, customerClusterName, nil)
			Expect(err).NotTo(HaveOccurred(), "failed to GET cluster after updating containerRegistry")
			Expect(clusterAfterUpdate.Properties).NotTo(BeNil(), "cluster properties was nil after update")
			Expect(clusterAfterUpdate.Properties.Platform).NotTo(BeNil(), "cluster platform was nil after update")
			Expect(clusterAfterUpdate.Properties.Platform.ContainerRegistry).NotTo(BeNil(), "containerRegistry should be set after update")
			Expect(clusterAfterUpdate.Properties.Platform.ContainerRegistry.ManagedIdentity).NotTo(BeNil(), "containerRegistry.managedIdentity should be set after update")
			Expect(strings.EqualFold(*clusterAfterUpdate.Properties.Platform.ContainerRegistry.ManagedIdentity, acrPullMI2ResourceID)).To(BeTrue(),
				"containerRegistry.managedIdentity mismatch after update: got %q want %q",
				*clusterAfterUpdate.Properties.Platform.ContainerRegistry.ManagedIdentity, acrPullMI2ResourceID)

			By("verifying the updated MI can pull from the private ACR")
			createACRPullTestPod("acr-pull-test-after-update")
			err = verifiers.VerifyImagePulled(pullTestNamespace, acrLoginServer, "ubi-minimal", imagePullTimeout).
				Verify(ctx, adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to pull image after updating containerRegistry to second MI")
		})
})
