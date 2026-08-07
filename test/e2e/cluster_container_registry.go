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
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v2"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerregistry/armcontainerregistry"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/msi/armmsi"

	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

const (
	managedIdentityOperatorRoleID = "f1a07417-d97a-45cb-824c-7a7467783830"
	acrPullRoleID                 = "7f951dda-4ed3-4680-a7ca-43fe172d538d"
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
	clusterParams framework.ClusterParams20260630,
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

var _ = Describe("Container Registry Pull Credentials", func() {
	Context("with v2026-06-30 API", func() {
		It("should create a cluster with containerRegistry.managedIdentity and pull an image from a private ACR",
			labels.RequireNothing,
			labels.Medium,
			labels.Positive,
			labels.AroRpApiCompatible,
			labels.CreateCluster,
			labels.MIContainers(1),
			func(ctx context.Context) {
				const (
					customerClusterName = "acr-pull-create"
					apiVersion          = "2026-06-30-preview"
					imagePullTimeout    = 10 * time.Minute
				)

				tc := framework.NewTestContext()

				By("checking API version availability")
				if !framework.IsDevelopmentEnvironment() {
					resourcesFactory, err := tc.GetARMResourcesClientFactory(ctx)
					Expect(err).NotTo(HaveOccurred(), "failed to get ARM resources client factory")
					providersClient := resourcesFactory.NewProvidersClient()
					provider, err := providersClient.Get(ctx, "Microsoft.RedHatOpenShift", nil)
					Expect(err).NotTo(HaveOccurred(), "failed to get Microsoft.RedHatOpenShift resource provider")
					available := false
					for _, rt := range provider.ResourceTypes {
						if rt.ResourceType == nil || !strings.EqualFold(*rt.ResourceType, "hcpOpenShiftClusters") {
							continue
						}
						for _, v := range rt.APIVersions {
							if v != nil && strings.EqualFold(*v, apiVersion) {
								available = true
								break
							}
						}
					}
					if !available {
						if time.Now().After(framework.V20260630PreviewDeploymentDeadline) {
							Fail(fmt.Sprintf("API version %s should be available by %s", apiVersion, framework.V20260630PreviewDeploymentDeadline.Format(time.RFC3339)))
						}
						Skip(fmt.Sprintf("API version %s not available in this environment", apiVersion))
					}
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
				if len(acrName) > 50 {
					acrName = acrName[:50]
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
				acrLoginServer := *acrResp.Properties.LoginServer
				GinkgoWriter.Printf("Created ACR: %s (login: %s)\n", acrName, acrLoginServer)

				By("importing a test image into the ACR")
				importPoller, err := registriesClient.BeginImportImage(ctx, *resourceGroup.Name, acrName, armcontainerregistry.ImportImageParameters{
					Source: &armcontainerregistry.ImportSource{
						RegistryURI: to.Ptr("mcr.microsoft.com"),
						SourceImage: to.Ptr("azurelinux/distroless/debug:3.0"),
					},
					TargetTags: []*string{to.Ptr("debug:3.0")},
					Mode:       to.Ptr(armcontainerregistry.ImportModeForce),
				}, nil)
				Expect(err).NotTo(HaveOccurred(), "failed to start image import into ACR")
				_, err = importPoller.PollUntilDone(ctx, nil)
				Expect(err).NotTo(HaveOccurred(), "failed to import image into ACR %s", acrName)
				GinkgoWriter.Printf("Imported mcr.microsoft.com/azurelinux/distroless/debug:3.0 into %s\n", acrLoginServer)

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
				clusterParams := framework.NewDefaultClusterParams20260630()
				clusterParams.ClusterName = customerClusterName
				clusterParams.OpenshiftVersionId = "4.22"
				clusterParams.ManagedResourceGroupName = framework.SuffixName(*resourceGroup.Name, "-managed", 64)

				By("creating customer resources (infrastructure and managed identities)")
				clusterParams, err = tc.CreateClusterCustomerResources20260630(ctx,
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
				clusterResource, err := framework.BuildHCPClusterFromParams20260630(clusterParams, tc.Location(), nil)
				Expect(err).NotTo(HaveOccurred(), "failed to build HCP cluster from params")

				hcpClient := tc.Get20260630ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient()
				_, err = framework.CreateHCPClusterAndWait20260630(ctx, GinkgoLogr, hcpClient,
					*resourceGroup.Name, customerClusterName, clusterResource, framework.ClusterCreationTimeout)
				Expect(err).NotTo(HaveOccurred(), "failed to create HCP cluster with containerRegistry set")

				By("creating a node pool")
				nodePoolParams := framework.NewDefaultNodePoolParams20260630()
				nodePoolParams.ClusterName = customerClusterName
				nodePoolParams.NodePoolName = "np-1"
				nodePoolParams.Replicas = int32(2)
				err = tc.CreateNodePoolFromParam20260630(ctx, GinkgoLogr,
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

				By(fmt.Sprintf("deploying a pod that pulls from private ACR %s — no imagePullSecrets", acrLoginServer))
				testImage := fmt.Sprintf("%s/debug:3.0", acrLoginServer)
				pod := &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "acr-pull-test",
						Namespace: pullTestNamespace,
					},
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{
								Name:            "acr-pull-test",
								Image:           testImage,
								Command:         []string{"/bin/sh", "-c", "exit 0"},
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
				_, err = kubeClient.CoreV1().Pods(pullTestNamespace).Create(ctx, pod, metav1.CreateOptions{})
				Expect(err).NotTo(HaveOccurred(), "failed to create ACR pull test pod")

				By("verifying the image was pulled from the private ACR")
				err = verifiers.VerifyImagePulled(pullTestNamespace, acrLoginServer, "debug", imagePullTimeout).
					Verify(ctx, adminRESTConfig)
				Expect(err).NotTo(HaveOccurred(), "failed to pull image from private ACR %s — the credential provider did not authenticate the pull", acrLoginServer)
			})

		// TODO: Day 2 test (update/clear containerRegistry on existing cluster) is disabled.
		// The update path triggers node pool rolling replacements via CS, and the operation
		// convergence time needs investigation before this can be reliably tested in CI.
		// See ARO-24037 for tracking.
	})
})
