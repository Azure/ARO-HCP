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
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"k8s.io/client-go/rest"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"

	hcpsdk20240610preview "github.com/Azure/ARO-HCP/test/sdk/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	hcpsdk20251223preview "github.com/Azure/ARO-HCP/test/sdk/v20251223preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
)

// This test aggregates the following features in one cluster+nodepools scenario:
// - External OIDC provider via ExternalAuth
// - Cilium CNI with kube-proxy replacement
// - ETCD data encryption with customer-managed keys
// - ETCD disk-level encryption with platform-managed keys
// - Internal image registry disabled
// - API IP address access control (authorized CIDRs)
// - KeyVaultVisibility set to Private
var _ = Describe("Customer", func() {
	It("should be able to create a cluster and node pools with aggregated advanced features",
		labels.RequireNothing,
		labels.Critical,
		labels.Positive,
		labels.AroRpApiCompatible,
		labels.Slow,
		labels.CreateCluster,
		func(ctx context.Context) {
			const (
				customerClusterName       = "agg-cluster"
				customerNodePoolNameA     = "agg-np-a"
				customerNodePoolNameB     = "agg-np-b"
				customerExternalAuthName  = "agg-extauth"
				externalAuthSubjectPrefix = "prefix-"
				ciliumNamespace           = "kube-system"
				ciliumVersion             = "1.19.2"
			)

			tc := framework.NewTestContext()

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, framework.IdentityContainerAssignmentRetryInterval)
				Expect(err).NotTo(HaveOccurred(), "failed to assign pooled identity containers")
			}

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "feature-aggregation", tc.Location())
			Expect(err).NotTo(HaveOccurred(), "failed to create resource group for feature aggregation test")

			By("building cluster parameters for aggregated feature coverage")
			clusterParams := framework.NewDefaultClusterParams20251223()
			clusterParams.ClusterName = customerClusterName
			clusterParams.ManagedResourceGroupName = framework.SuffixName(*resourceGroup.Name, "-managed", 64)
			clusterParams.Network.NetworkType = "Other"
			clusterParams.Network.PodCIDR = "10.255.0.0/16"
			clusterParams.Network.ServiceCIDR = "172.30.0.0/16"
			clusterParams.Network.MachineCIDR = "10.0.0.0/16"
			clusterParams.Network.HostPrefix = 23
			clusterParams.EncryptionKeyManagementMode = string(hcpsdk20251223preview.EtcdDataEncryptionKeyManagementModeTypeCustomerManaged)
			clusterParams.EncryptionType = string(hcpsdk20251223preview.CustomerManagedEncryptionTypeKms)
			clusterParams.KeyVaultVisibility = "Private"
			clusterParams.ImageRegistryState = string(hcpsdk20251223preview.ClusterImageRegistryStateDisabled)

			By("creating customer resources with private key vault support")
			clusterParams, err = tc.CreateClusterCustomerResources20251223(
				ctx,
				resourceGroup,
				clusterParams,
				map[string]any{
					"privateKeyVault": true,
				},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create customer resources for aggregated feature cluster")

			By("generating SSH key pair for authorized CIDR VM")
			sshPublicKey, _, err := framework.GenerateSSHKeyPair()
			Expect(err).NotTo(HaveOccurred(), "failed to generate SSH key pair for authorized CIDR VM")

			By("deploying a VM to source the authorized public IP")
			vmName := fmt.Sprintf("%s-test-vm", customerClusterName)
			// Use a restriction-aware VM size selector to reduce SkuNotAvailable flakiness.
			vmSize, err := tc.SelectVMSize(ctx, framework.JumpboxVMSizeSelector())
			Expect(err).NotTo(HaveOccurred(), "failed to resolve a jumpbox VM size; check VM SKU restrictions/quota for the test subscription in %s", tc.Location())
			var vmDeployment *armresources.DeploymentExtended
			var deployErr error
			for attempt := 0; attempt < 3; attempt++ {
				vmDeployment, deployErr = tc.CreateBicepTemplateAndWait(ctx,
					framework.WithTemplateFromFS(TestArtifactsFS, "test-artifacts/generated-test-artifacts/modules/test-vm.json"),
					framework.WithDeploymentName("test-vm"),
					framework.WithScope(framework.BicepDeploymentScopeResourceGroup),
					framework.WithClusterResourceGroup(*resourceGroup.Name),
					framework.WithParameters(map[string]any{
						"vmName":       vmName,
						"vnetName":     clusterParams.VnetName,
						"subnetName":   clusterParams.SubnetName,
						"sshPublicKey": sshPublicKey,
						"vmSize":       vmSize,
					}),
					framework.WithTimeout(30*time.Minute),
				)
				if deployErr == nil || !strings.Contains(deployErr.Error(), "SkuNotAvailable") {
					break
				}
				time.Sleep(20 * time.Second)
			}
			Expect(deployErr).NotTo(HaveOccurred(), "failed to deploy authorized CIDR VM")

			By("extracting VM public IP and assigning it as the only authorized CIDR")
			vmPublicIP, err := framework.GetOutputValueString(vmDeployment, "publicIP")
			Expect(err).NotTo(HaveOccurred(), "failed to extract VM public IP from deployment outputs")
			Expect(vmPublicIP).NotTo(BeEmpty(), "VM public IP should not be empty in deployment outputs")
			authorizedCIDR := fmt.Sprintf("%s/32", vmPublicIP)
			clusterParams.AuthorizedCIDRs = []*string{to.Ptr(authorizedCIDR)}

			By("creating cluster resource payload with private key vault visibility")
			clusterResource, err := framework.BuildHCPClusterFromParams20251223(clusterParams, tc.Location(), nil)
			Expect(err).NotTo(HaveOccurred(), "failed to build v20251223 cluster resource payload")
			if clusterResource.Properties != nil &&
				clusterResource.Properties.Etcd != nil &&
				clusterResource.Properties.Etcd.DataEncryption != nil &&
				clusterResource.Properties.Etcd.DataEncryption.CustomerManaged != nil &&
				clusterResource.Properties.Etcd.DataEncryption.CustomerManaged.Kms != nil {
				clusterResource.Properties.Etcd.DataEncryption.CustomerManaged.Kms.Visibility = to.Ptr(hcpsdk20251223preview.KeyVaultVisibilityPrivate)
			}

			By("creating the HCP cluster with aggregated settings")
			_, err = framework.CreateHCPClusterAndWait20251223(
				ctx,
				GinkgoLogr,
				tc.Get20251223ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
				*resourceGroup.Name,
				customerClusterName,
				clusterResource,
				framework.ClusterCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create HCP cluster %q with aggregated settings", customerClusterName)

			By("verifying cluster properties for key vault visibility, image registry, etcd data encryption and authorized CIDRs")
			cluster, err := tc.Get20251223ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient().Get(ctx, *resourceGroup.Name, customerClusterName, nil)
			Expect(err).NotTo(HaveOccurred(), "failed to get HCP cluster %q", customerClusterName)
			Expect(cluster.Properties).ToNot(BeNil(), "cluster %q Properties was nil", customerClusterName)
			Expect(cluster.Properties.Etcd).ToNot(BeNil(), "cluster %q Properties.Etcd was nil", customerClusterName)
			Expect(cluster.Properties.Etcd.DataEncryption).ToNot(BeNil(), "cluster %q Properties.Etcd.DataEncryption was nil", customerClusterName)
			Expect(cluster.Properties.Etcd.DataEncryption.KeyManagementMode).ToNot(BeNil(), "cluster %q Properties.Etcd.DataEncryption.KeyManagementMode was nil", customerClusterName)
			Expect(*cluster.Properties.Etcd.DataEncryption.KeyManagementMode).To(Equal(hcpsdk20251223preview.EtcdDataEncryptionKeyManagementModeTypeCustomerManaged), "cluster %q etcd data encryption key management mode should be CustomerManaged", customerClusterName)
			Expect(cluster.Properties.Etcd.DataEncryption.CustomerManaged).ToNot(BeNil(), "cluster %q Properties.Etcd.DataEncryption.CustomerManaged was nil", customerClusterName)
			Expect(cluster.Properties.Etcd.DataEncryption.CustomerManaged.Kms).ToNot(BeNil(), "cluster %q Properties.Etcd.DataEncryption.CustomerManaged.Kms was nil", customerClusterName)
			Expect(cluster.Properties.Etcd.DataEncryption.CustomerManaged.Kms.Visibility).ToNot(BeNil(), "cluster %q Properties.Etcd.DataEncryption.CustomerManaged.Kms.Visibility was nil", customerClusterName)
			Expect(*cluster.Properties.Etcd.DataEncryption.CustomerManaged.Kms.Visibility).To(Equal(hcpsdk20251223preview.KeyVaultVisibilityPrivate), "cluster %q key vault visibility should be Private", customerClusterName)
			Expect(cluster.Properties.ClusterImageRegistry).ToNot(BeNil(), "cluster %q Properties.ClusterImageRegistry was nil", customerClusterName)
			Expect(cluster.Properties.ClusterImageRegistry.State).ToNot(BeNil(), "cluster %q Properties.ClusterImageRegistry.State was nil", customerClusterName)
			Expect(*cluster.Properties.ClusterImageRegistry.State).To(Equal(hcpsdk20251223preview.ClusterImageRegistryStateDisabled), "cluster %q image registry state should be Disabled", customerClusterName)
			Expect(cluster.Properties.API).ToNot(BeNil(), "cluster %q Properties.API was nil", customerClusterName)
			Expect(cluster.Properties.API.AuthorizedCIDRs).To(HaveLen(1), "cluster %q should have exactly one authorized CIDR", customerClusterName)
			Expect(*cluster.Properties.API.AuthorizedCIDRs[0]).To(Equal(authorizedCIDR), "cluster %q authorized CIDR should match authorized VM public IP", customerClusterName)

			By("getting admin credentials")
			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster20240610(
				ctx,
				tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
				*resourceGroup.Name,
				customerClusterName,
				framework.GetAdminRESTConfigTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to get admin REST config for cluster %q", customerClusterName)
			adminKubeconfig, err := framework.GenerateKubeconfig(adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to generate admin kubeconfig for authorized VM kubectl operations")
			adminKubeconfigB64 := base64.StdEncoding.EncodeToString([]byte(adminKubeconfig))

			By("disabling kube-proxy in the cluster network operator from the authorized VM")
			disableKubeProxyCmd := fmt.Sprintf(
				`echo '%s' | base64 -d > /tmp/admin-kubeconfig && kubectl --kubeconfig=/tmp/admin-kubeconfig patch networks.operator.openshift.io cluster --type=merge -p '{"spec":{"deployKubeProxy":false}}'`,
				adminKubeconfigB64,
			)
			Eventually(func(g Gomega) {
				_, cmdErr := framework.RunVMCommand(ctx, tc, *resourceGroup.Name, vmName, disableKubeProxyCmd, 2*time.Minute)
				g.Expect(cmdErr).NotTo(HaveOccurred(), "failed to disable kube-proxy from authorized VM")
			}, 5*time.Minute, 10*time.Second).Should(Succeed(), "kube-proxy disable patch should succeed from authorized VM")

			By("installing cilium with kube-proxy replacement enabled from the authorized VM")
			installCiliumCmd := fmt.Sprintf(
				`echo '%s' | base64 -d > /tmp/admin-kubeconfig; `+
					`if ! command -v helm >/dev/null 2>&1; then curl -fsSL https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3 | bash; fi; `+
					`helm repo add cilium https://helm.cilium.io >/dev/null 2>&1 || true; `+
					`helm repo update >/dev/null; `+
					`helm upgrade --install cilium cilium/cilium --version %s --namespace %s --create-namespace --kubeconfig /tmp/admin-kubeconfig `+
					`--set cni.uninstall=false `+
					`--set cni.binPath=/var/lib/cni/bin `+
					`--set cni.confPath=/var/run/multus/cni/net.d `+
					`--set kubeProxyReplacement=true `+
					`--set k8sServiceHost=172.20.0.1 `+
					`--set k8sServicePort=6443 `+
					`--set ipam.mode=cluster-pool `+
					`--set ipam.operator.clusterPoolIPv4PodCIDRList=%s `+
					`--set ipam.operator.clusterPoolIPv4MaskSize=%d `+
					`--set cluster.name=%s `+
					`--set operator.replicas=1 `+
					`--set routingMode=tunnel `+
					`--set tunnelProtocol=vxlan`,
				adminKubeconfigB64,
				ciliumVersion,
				ciliumNamespace,
				clusterParams.Network.PodCIDR,
				clusterParams.Network.HostPrefix,
				customerClusterName,
			)
			_, err = framework.RunVMCommand(ctx, tc, *resourceGroup.Name, vmName, installCiliumCmd, 10*time.Minute)
			Expect(err).NotTo(HaveOccurred(), "failed to install cilium chart from authorized VM")

			By("creating two node pools")
			nodePoolParamsA := framework.NewDefaultNodePoolParams20251223()
			nodePoolParamsA.ClusterName = customerClusterName
			nodePoolParamsA.NodePoolName = customerNodePoolNameA
			nodePoolParamsA.Replicas = int32(2)
			nodePoolParamsA.AutoRepair = true
			nodePoolErrA := tc.CreateNodePoolFromParam20251223(
				ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams.ManagedResourceGroupName,
				customerClusterName,
				nodePoolParamsA,
				framework.NodePoolCreationTimeout,
			)

			nodePoolParamsB := framework.NewDefaultNodePoolParams20251223()
			nodePoolParamsB.ClusterName = customerClusterName
			nodePoolParamsB.NodePoolName = customerNodePoolNameB
			nodePoolParamsB.Replicas = int32(1)
			nodePoolParamsB.AutoRepair = true
			nodePoolErrB := tc.CreateNodePoolFromParam20251223(
				ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams.ManagedResourceGroupName,
				customerClusterName,
				nodePoolParamsB,
				framework.NodePoolCreationTimeout,
			)
			Expect(errors.Join(nodePoolErrA, nodePoolErrB)).NotTo(HaveOccurred(), "failed to create aggregated feature node pools for cluster %q", customerClusterName)

			By("verifying node pools use platform managed disk-level encryption")
			nodePoolClient := tc.Get20251223ClientFactoryOrDie(ctx).NewNodePoolsClient()
			nodePoolA, err := framework.GetNodePool20251223(ctx, nodePoolClient, *resourceGroup.Name, customerClusterName, customerNodePoolNameA)
			Expect(err).NotTo(HaveOccurred(), "failed to get node pool %q", customerNodePoolNameA)
			Expect(nodePoolA.Properties).ToNot(BeNil(), "node pool %q Properties was nil", customerNodePoolNameA)
			Expect(nodePoolA.Properties.Platform).ToNot(BeNil(), "node pool %q Properties.Platform was nil", customerNodePoolNameA)
			Expect(nodePoolA.Properties.Platform.OSDisk).ToNot(BeNil(), "node pool %q Properties.Platform.OSDisk was nil", customerNodePoolNameA)
			Expect(nodePoolA.Properties.Platform.OSDisk.EncryptionSetID).To(BeNil(), "node pool %q should not specify an OSDisk EncryptionSetID when platform-managed disk encryption is expected", customerNodePoolNameA)
			nodePoolB, err := framework.GetNodePool20251223(ctx, nodePoolClient, *resourceGroup.Name, customerClusterName, customerNodePoolNameB)
			Expect(err).NotTo(HaveOccurred(), "failed to get node pool %q", customerNodePoolNameB)
			Expect(nodePoolB.Properties).ToNot(BeNil(), "node pool %q Properties was nil", customerNodePoolNameB)
			Expect(nodePoolB.Properties.Platform).ToNot(BeNil(), "node pool %q Properties.Platform was nil", customerNodePoolNameB)
			Expect(nodePoolB.Properties.Platform.OSDisk).ToNot(BeNil(), "node pool %q Properties.Platform.OSDisk was nil", customerNodePoolNameB)
			Expect(nodePoolB.Properties.Platform.OSDisk.EncryptionSetID).To(BeNil(), "node pool %q should not specify an OSDisk EncryptionSetID when platform-managed disk encryption is expected", customerNodePoolNameB)

			By("creating an external OIDC auth provider and verifying its state")
			app, sp, err := tc.NewAppRegistrationWithServicePrincipal(ctx)
			Expect(err).NotTo(HaveOccurred(), "failed to create app registration for external OIDC configuration")
			graphClient, err := tc.GetGraphClient(ctx)
			Expect(err).NotTo(HaveOccurred(), "failed to get Microsoft Graph client for external OIDC configuration")
			pass, err := graphClient.AddPassword(ctx, app.ID, "agg-ext-auth-pass", time.Now(), time.Now().Add(24*time.Hour))
			Expect(err).NotTo(HaveOccurred(), "failed to add client secret to app registration for external OIDC configuration")
			extAuth := hcpsdk20240610preview.ExternalAuth{
				Properties: &hcpsdk20240610preview.ExternalAuthProperties{
					Issuer: &hcpsdk20240610preview.TokenIssuerProfile{
						URL:       to.Ptr(fmt.Sprintf("https://login.microsoftonline.com/%s/v2.0", tc.TenantID())),
						Audiences: []*string{to.Ptr(app.AppID)},
					},
					Claim: &hcpsdk20240610preview.ExternalAuthClaimProfile{
						Mappings: &hcpsdk20240610preview.TokenClaimMappingsProfile{
							Username: &hcpsdk20240610preview.UsernameClaimProfile{
								Claim:        to.Ptr("sub"),
								PrefixPolicy: to.Ptr(hcpsdk20240610preview.UsernameClaimPrefixPolicyPrefix),
								Prefix:       to.Ptr(externalAuthSubjectPrefix),
							},
							Groups: &hcpsdk20240610preview.GroupClaimProfile{
								Claim: to.Ptr("groups"),
							},
						},
					},
					Clients: []*hcpsdk20240610preview.ExternalAuthClientProfile{
						{
							ClientID: to.Ptr(app.AppID),
							Component: &hcpsdk20240610preview.ExternalAuthClientComponentProfile{
								Name:                to.Ptr("console"),
								AuthClientNamespace: to.Ptr("openshift-console"),
							},
							Type: to.Ptr(hcpsdk20240610preview.ExternalAuthClientTypeConfidential),
						},
						{
							ClientID: to.Ptr(app.AppID),
							Component: &hcpsdk20240610preview.ExternalAuthClientComponentProfile{
								Name:                to.Ptr("cli"),
								AuthClientNamespace: to.Ptr("openshift-console"),
							},
							Type: to.Ptr(hcpsdk20240610preview.ExternalAuthClientTypePublic),
						},
					},
				},
			}
			_, err = framework.CreateOrUpdateExternalAuthAndWait20240610(
				ctx,
				tc.Get20240610ClientFactoryOrDie(ctx).NewExternalAuthsClient(),
				*resourceGroup.Name,
				customerClusterName,
				customerExternalAuthName,
				extAuth,
				framework.ExternalAuthCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create external auth config %q for cluster %q", customerExternalAuthName, customerClusterName)
			extAuthResult, err := framework.GetExternalAuth20240610(
				ctx,
				tc.Get20240610ClientFactoryOrDie(ctx).NewExternalAuthsClient(),
				*resourceGroup.Name,
				customerClusterName,
				customerExternalAuthName,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to get external auth config %q for cluster %q", customerExternalAuthName, customerClusterName)
			Expect(extAuthResult.Properties).ToNot(BeNil(), "external auth %q Properties was nil", customerExternalAuthName)
			Expect(extAuthResult.Properties.ProvisioningState).ToNot(BeNil(), "external auth %q ProvisioningState was nil", customerExternalAuthName)
			Expect(*extAuthResult.Properties.ProvisioningState).To(Equal(hcpsdk20240610preview.ExternalAuthProvisioningStateSucceeded), "external auth %q provisioning state should be Succeeded", customerExternalAuthName)

			By("creating a cluster role binding for the external OIDC subject from the authorized VM")
			clusterRoleBindingName := "agg-external-auth-cluster-admin"
			clusterRoleBindingSubject := externalAuthSubjectPrefix + sp.ID
			createClusterRoleBindingCmd := fmt.Sprintf(
				`echo '%s' | base64 -d > /tmp/admin-kubeconfig && kubectl --kubeconfig=/tmp/admin-kubeconfig create clusterrolebinding %s --clusterrole=cluster-admin --user=%s`,
				adminKubeconfigB64,
				clusterRoleBindingName,
				clusterRoleBindingSubject,
			)
			_, err = framework.RunVMCommand(ctx, tc, *resourceGroup.Name, vmName, createClusterRoleBindingCmd, 2*time.Minute)
			Expect(err).NotTo(HaveOccurred(), "failed to create cluster role binding for external OIDC subject from authorized VM")

			By("requesting an OIDC access token for the external auth client")
			Expect(tc.TenantID()).NotTo(BeEmpty(), "tenant ID must not be empty for OIDC authentication")
			cred, err := azidentity.NewClientSecretCredential(tc.TenantID(), app.AppID, pass.SecretText, nil)
			Expect(err).NotTo(HaveOccurred(), "failed to create client secret credential for OIDC authentication")

			var accessToken azcore.AccessToken
			Eventually(func() error {
				var tokenErr error
				accessToken, tokenErr = cred.GetToken(ctx, policy.TokenRequestOptions{
					Scopes: []string{fmt.Sprintf("%s/.default", app.AppID)},
				})
				if tokenErr != nil {
					GinkgoWriter.Printf("GetToken failed for external OIDC flow: %v\n", tokenErr)
				}
				return tokenErr
			}, 2*time.Minute, 10*time.Second).Should(Succeed(), "failed to acquire OIDC access token for external auth flow")

			By("verifying Kubernetes API access using the external OIDC token from the authorized VM")
			oidcRESTConfig := &rest.Config{
				Host:        adminRESTConfig.Host,
				BearerToken: accessToken.Token,
				TLSClientConfig: rest.TLSClientConfig{
					Insecure: adminRESTConfig.Insecure,
					CAData:   adminRESTConfig.CAData,
					CAFile:   adminRESTConfig.CAFile,
				},
			}
			oidcKubeconfig, err := framework.GenerateKubeconfig(oidcRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to generate kubeconfig for external OIDC token")
			oidcKubeconfigB64 := base64.StdEncoding.EncodeToString([]byte(oidcKubeconfig))
			oidcGetNamespacesCmd := fmt.Sprintf(
				`echo '%s' | base64 -d > /tmp/oidc-kubeconfig && kubectl --kubeconfig=/tmp/oidc-kubeconfig get namespaces -o name`,
				oidcKubeconfigB64,
			)
			Eventually(func(g Gomega) {
				output, cmdErr := framework.RunVMCommand(ctx, tc, *resourceGroup.Name, vmName, oidcGetNamespacesCmd, 2*time.Minute)
				g.Expect(cmdErr).NotTo(HaveOccurred(), "external OIDC kubectl command should succeed from authorized VM")
				g.Expect(strings.TrimSpace(output)).NotTo(BeEmpty(), "external OIDC identity should list namespaces from authorized VM")
			}, 5*time.Minute, 10*time.Second).Should(Succeed(), "external OIDC identity should be able to list namespaces through Kubernetes API from authorized VM")

			By("verifying cluster health from the authorized VM")
			waitNodesReadyCmd := fmt.Sprintf(
				`echo '%s' | base64 -d > /tmp/admin-kubeconfig && kubectl --kubeconfig=/tmp/admin-kubeconfig wait --for=condition=Ready nodes --all --timeout=15m`,
				adminKubeconfigB64,
			)
			_, err = framework.RunVMCommand(ctx, tc, *resourceGroup.Name, vmName, waitNodesReadyCmd, 16*time.Minute)
			Expect(err).NotTo(HaveOccurred(), "all nodes should become Ready from authorized VM checks")
			waitCiliumReadyCmd := fmt.Sprintf(
				`echo '%s' | base64 -d > /tmp/admin-kubeconfig && kubectl --kubeconfig=/tmp/admin-kubeconfig -n %s rollout status daemonset/cilium --timeout=10m`,
				adminKubeconfigB64,
				ciliumNamespace,
			)
			_, err = framework.RunVMCommand(ctx, tc, *resourceGroup.Name, vmName, waitCiliumReadyCmd, 11*time.Minute)
			Expect(err).NotTo(HaveOccurred(), "cilium daemonset should become Ready from authorized VM checks")
		},
	)
})
