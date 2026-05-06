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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v3"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/msi/armmsi"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azcertificates"

	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

var _ = Describe("Customer", func() {
	It("should be able to create a custom ingress with a self-managed TLS certificate from Azure Key Vault",
		labels.RequireNothing,
		labels.Critical,
		labels.Positive,
		labels.Slow,
		labels.AroRpApiCompatible,
		func(ctx context.Context) {
			const (
				customerClusterName  = "cmi-cluster"
				customerNodePoolName = "cmi-np"
				testManagedIdentity  = "e2e-cmi-kv-cert"
				kvCertName           = "custom-ingress-tls"
				ingressName          = "custom"
				ingressNamespace     = "openshift-ingress"
				ingressOperatorNS    = "openshift-ingress-operator"
			)

			tc := framework.NewTestContext()

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, 60*time.Second)
				Expect(err).NotTo(HaveOccurred())
			}

			// ── 1. Cluster setup ──

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "cmi", tc.Location())
			Expect(err).NotTo(HaveOccurred())

			By("creating cluster parameters")
			clusterParams := framework.NewDefaultClusterParams()
			clusterParams.ClusterName = customerClusterName
			clusterParams.ManagedResourceGroupName = framework.SuffixName(*resourceGroup.Name, "-managed", 64)

			By("creating customer resources with static public IP")
			clusterParams, err = tc.CreateClusterCustomerResources(ctx,
				resourceGroup,
				clusterParams,
				map[string]any{
					"createIngressPublicIP": true,
				},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(clusterParams.IngressPublicIPAddress).NotTo(BeEmpty(), "static public IP should be allocated")
			staticIP := clusterParams.IngressPublicIPAddress
			GinkgoWriter.Printf("Static public IP for custom ingress: %s\n", staticIP)

			By("creating the HCP cluster")
			err = tc.CreateHCPClusterFromParam(ctx, GinkgoLogr, *resourceGroup.Name, clusterParams, 45*time.Minute)
			Expect(err).NotTo(HaveOccurred())

			By("getting admin credentials")
			hcpClient := tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient()
			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster(ctx, hcpClient, *resourceGroup.Name, customerClusterName, 10*time.Minute)
			Expect(err).NotTo(HaveOccurred())

			By("ensuring the cluster is viable")
			Expect(verifiers.VerifyHCPCluster(ctx, adminRESTConfig)).To(Succeed())

			By("creating the node pool")
			nodePoolParams := framework.NewDefaultNodePoolParams()
			nodePoolParams.ClusterName = customerClusterName
			nodePoolParams.NodePoolName = customerNodePoolName
			nodePoolParams.Replicas = int32(2)
			err = tc.CreateNodePoolFromParam(ctx, *resourceGroup.Name, customerClusterName, nodePoolParams, 45*time.Minute)
			Expect(err).NotTo(HaveOccurred())

			By("verifying nodes are ready")
			Expect(verifiers.VerifyNodeCount(customerClusterName, int(nodePoolParams.Replicas)).Verify(ctx, adminRESTConfig)).To(Succeed())
			Expect(verifiers.VerifyNodesReady().Verify(ctx, adminRESTConfig)).To(Succeed())

			// ── 2. Workload identity for Key Vault access ──

			By("getting the cluster OIDC issuer URL")
			clusterResp, err := hcpClient.Get(ctx, *resourceGroup.Name, customerClusterName, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(clusterResp.Properties.Platform.IssuerURL).NotTo(BeNil(), "OIDC issuer URL should be populated")
			oidcIssuerURL := *clusterResp.Properties.Platform.IssuerURL
			GinkgoWriter.Printf("OIDC issuer URL: %s\n", oidcIssuerURL)

			subscriptionID, err := tc.SubscriptionID(ctx)
			Expect(err).NotTo(HaveOccurred())
			cred, err := tc.AzureCredential()
			Expect(err).NotTo(HaveOccurred())

			By("creating a managed identity for Key Vault certificate access")
			msiClient, err := armmsi.NewUserAssignedIdentitiesClient(subscriptionID, cred, nil)
			Expect(err).NotTo(HaveOccurred())
			msiResp, err := msiClient.CreateOrUpdate(ctx, *resourceGroup.Name, testManagedIdentity, armmsi.Identity{
				Location: resourceGroup.Location,
			}, nil)
			Expect(err).NotTo(HaveOccurred())
			clientID := *msiResp.Properties.ClientID
			principalID := *msiResp.Properties.PrincipalID
			GinkgoWriter.Printf("Created managed identity: clientID=%s principalID=%s\n", clientID, principalID)

			By("granting Key Vault Secrets User role to the managed identity")
			kvSecretsUserRoleID := fmt.Sprintf("/subscriptions/%s/providers/Microsoft.Authorization/roleDefinitions/4633458b-17de-408a-b874-0445c86b69e6", subscriptionID)
			kvScope := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.KeyVault/vaults/%s", subscriptionID, *resourceGroup.Name, clusterParams.KeyVaultName)
			raClient, err := armauthorization.NewRoleAssignmentsClient(subscriptionID, cred, nil)
			Expect(err).NotTo(HaveOccurred())

			assignmentName := fmt.Sprintf("e2e-cmi-kv-%s", clusterParams.KeyVaultName)
			_, err = raClient.Create(ctx, kvScope, assignmentName, armauthorization.RoleAssignmentCreateParameters{
				Properties: &armauthorization.RoleAssignmentProperties{
					PrincipalID:      to.Ptr(principalID),
					RoleDefinitionID: to.Ptr(kvSecretsUserRoleID),
					PrincipalType:    to.Ptr(armauthorization.PrincipalTypeServicePrincipal),
				},
			}, nil)
			Expect(err).NotTo(HaveOccurred())

			By("creating a federated identity credential for the CSI driver service account")
			ficClient, err := armmsi.NewFederatedIdentityCredentialsClient(subscriptionID, cred, nil)
			Expect(err).NotTo(HaveOccurred())
			subject := fmt.Sprintf("system:serviceaccount:%s:secrets-store-csi-driver-provider-azure", ingressNamespace)
			_, err = ficClient.CreateOrUpdate(ctx, *resourceGroup.Name, testManagedIdentity, "cmi-csi-fic", armmsi.FederatedIdentityCredential{
				Properties: &armmsi.FederatedIdentityCredentialProperties{
					Issuer:    to.Ptr(oidcIssuerURL),
					Subject:   to.Ptr(subject),
					Audiences: []*string{to.Ptr("api://AzureADTokenExchange")},
				},
			}, nil)
			Expect(err).NotTo(HaveOccurred())

			// ── 3. Self-signed certificate in Key Vault ──

			By("creating a self-signed certificate in Key Vault")
			kvURL := fmt.Sprintf("https://%s.vault.azure.net", clusterParams.KeyVaultName)
			certClient, err := azcertificates.NewClient(kvURL, cred, nil)
			Expect(err).NotTo(HaveOccurred())

			san := fmt.Sprintf("*.%s.nip.io", staticIP)
			createResp, err := certClient.CreateCertificate(ctx, kvCertName, azcertificates.CreateCertificateParameters{
				CertificatePolicy: &azcertificates.CertificatePolicy{
					IssuerParameters: &azcertificates.IssuerParameters{
						Name: to.Ptr("Self"),
					},
					SecretProperties: &azcertificates.SecretProperties{
						ContentType: to.Ptr("application/x-pem-file"),
					},
					X509CertificateProperties: &azcertificates.X509CertificateProperties{
						Subject: to.Ptr(fmt.Sprintf("CN=%s", san)),
						SubjectAlternativeNames: &azcertificates.SubjectAlternativeNames{
							DNSNames: []*string{to.Ptr(san)},
						},
						ValidityInMonths: to.Ptr(int32(12)),
					},
				},
			}, nil)
			Expect(err).NotTo(HaveOccurred())
			GinkgoWriter.Printf("Certificate creation initiated: %s\n", *createResp.ID)

			By("waiting for the certificate to be ready")
			err = wait.PollUntilContextTimeout(ctx, 5*time.Second, 5*time.Minute, true, func(ctx context.Context) (bool, error) {
				cert, err := certClient.GetCertificate(ctx, kvCertName, "", nil)
				if err != nil {
					return false, nil
				}
				if cert.ID == nil {
					return false, nil
				}
				return true, nil
			})
			Expect(err).NotTo(HaveOccurred())

			// ── 4. CSI Secrets Store Driver ──

			By("installing CSI Secrets Store Driver and Azure Key Vault Provider")
			csiInstaller := verifiers.CSISecretsStoreInstaller{AzureProviderVersion: "1.5.4"}
			err = csiInstaller.Install(ctx, adminRESTConfig)
			Expect(err).NotTo(HaveOccurred())

			adminClient, err := kubernetes.NewForConfig(adminRESTConfig)
			Expect(err).NotTo(HaveOccurred())
			dynamicClient, err := dynamic.NewForConfig(adminRESTConfig)
			Expect(err).NotTo(HaveOccurred())

			// ── 5. Custom IngressController ──

			By("creating a SecretProviderClass to sync KV certificate to a TLS Secret")
			spcGVR := schema.GroupVersionResource{Group: "secrets-store.csi.x-k8s.io", Version: "v1", Resource: "secretproviderclasses"}
			spc := &unstructured.Unstructured{Object: map[string]any{
				"apiVersion": "secrets-store.csi.x-k8s.io/v1",
				"kind":       "SecretProviderClass",
				"metadata": map[string]any{
					"name":      "custom-ingress-cert-spc",
					"namespace": ingressNamespace,
				},
				"spec": map[string]any{
					"provider": "azure",
					"parameters": map[string]any{
						"usePodIdentity":       "false",
						"useVMManagedIdentity": "false",
						"clientID":             clientID,
						"keyvaultName":         clusterParams.KeyVaultName,
						"tenantId":             tc.TenantID(),
						"objects": fmt.Sprintf(`array:
  - |
    objectName: %s
    objectType: secret`, kvCertName),
					},
					"secretObjects": []any{
						map[string]any{
							"secretName": "custom-ingress-cert",
							"type":       "kubernetes.io/tls",
							"data": []any{
								map[string]any{
									"objectName": kvCertName,
									"key":        "tls.key",
								},
								map[string]any{
									"objectName": kvCertName,
									"key":        "tls.crt",
								},
							},
						},
					},
				},
			}}
			_, err = dynamicClient.Resource(spcGVR).Namespace(ingressNamespace).Create(ctx, spc, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())

			By("creating a syncer pod to trigger SecretProviderClass sync")
			syncerPod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "csi-cert-syncer",
					Namespace: ingressNamespace,
					Labels: map[string]string{
						"azure.workload.identity/use": "true",
					},
				},
				Spec: corev1.PodSpec{
					ServiceAccountName: "secrets-store-csi-driver-provider-azure",
					RestartPolicy:      corev1.RestartPolicyNever,
					Containers: []corev1.Container{
						{
							Name:    "syncer",
							Image:   "registry.access.redhat.com/ubi9/ubi-minimal:latest",
							Command: []string{"sleep", "3600"},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "secrets-store",
									MountPath: "/mnt/secrets-store",
									ReadOnly:  true,
								},
							},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: "secrets-store",
							VolumeSource: corev1.VolumeSource{
								CSI: &corev1.CSIVolumeSource{
									Driver:   "secrets-store.csi.k8s.io",
									ReadOnly: to.Ptr(true),
									VolumeAttributes: map[string]string{
										"secretProviderClass": "custom-ingress-cert-spc",
									},
								},
							},
						},
					},
				},
			}
			_, err = adminClient.CoreV1().Pods(ingressNamespace).Create(ctx, syncerPod, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())

			By("waiting for the TLS Secret to be synced")
			err = wait.PollUntilContextTimeout(ctx, 10*time.Second, 10*time.Minute, true, func(ctx context.Context) (bool, error) {
				secret, err := adminClient.CoreV1().Secrets(ingressNamespace).Get(ctx, "custom-ingress-cert", metav1.GetOptions{})
				if err != nil {
					return false, nil
				}
				return len(secret.Data["tls.crt"]) > 0 && len(secret.Data["tls.key"]) > 0, nil
			})
			Expect(err).NotTo(HaveOccurred())

			By("creating a custom IngressController")
			ingressControllerGVR := schema.GroupVersionResource{Group: "operator.openshift.io", Version: "v1", Resource: "ingresscontrollers"}
			ingressDomain := fmt.Sprintf("apps.%s.nip.io", staticIP)
			ic := &unstructured.Unstructured{Object: map[string]any{
				"apiVersion": "operator.openshift.io/v1",
				"kind":       "IngressController",
				"metadata": map[string]any{
					"name":      ingressName,
					"namespace": ingressOperatorNS,
				},
				"spec": map[string]any{
					"domain": ingressDomain,
					"defaultCertificate": map[string]any{
						"name": "custom-ingress-cert",
					},
					"endpointPublishingStrategy": map[string]any{
						"type": "LoadBalancerService",
						"loadBalancer": map[string]any{
							"scope": "External",
						},
					},
					"routeSelector": map[string]any{
						"matchLabels": map[string]any{
							"ingress": "custom",
						},
					},
				},
			}}
			_, err = dynamicClient.Resource(ingressControllerGVR).Namespace(ingressOperatorNS).Create(ctx, ic, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())

			By("patching the router Service with the static IP")
			routerServiceName := fmt.Sprintf("router-%s", ingressName)
			err = wait.PollUntilContextTimeout(ctx, 10*time.Second, 5*time.Minute, true, func(ctx context.Context) (bool, error) {
				_, err := adminClient.CoreV1().Services(ingressNamespace).Get(ctx, routerServiceName, metav1.GetOptions{})
				if err != nil {
					return false, nil
				}
				return true, nil
			})
			Expect(err).NotTo(HaveOccurred())

			svcGVR := schema.GroupVersionResource{Group: "", Version: "v1", Resource: "services"}
			patch := fmt.Sprintf(`{"metadata":{"annotations":{"service.beta.kubernetes.io/azure-load-balancer-resource-group":"%s"}},"spec":{"loadBalancerIP":"%s"}}`,
				*resourceGroup.Name, staticIP)
			_, err = dynamicClient.Resource(svcGVR).Namespace(ingressNamespace).Patch(ctx, routerServiceName, "application/merge-patch+json", []byte(patch), metav1.PatchOptions{})
			Expect(err).NotTo(HaveOccurred())

			By("waiting for the IngressController to become available with the correct IP")
			var lastStatus string
			err = wait.PollUntilContextTimeout(ctx, 15*time.Second, 15*time.Minute, true, func(ctx context.Context) (bool, error) {
				icObj, err := dynamicClient.Resource(ingressControllerGVR).Namespace(ingressOperatorNS).Get(ctx, ingressName, metav1.GetOptions{})
				if err != nil {
					return false, nil
				}

				conditions, found, _ := unstructured.NestedSlice(icObj.Object, "status", "conditions")
				if !found {
					return false, nil
				}
				available := false
				for _, c := range conditions {
					cond, ok := c.(map[string]any)
					if !ok {
						continue
					}
					condType, _, _ := unstructured.NestedString(cond, "type")
					condStatus, _, _ := unstructured.NestedString(cond, "status")
					if condType == "Available" && condStatus == "True" {
						available = true
						break
					}
				}
				if !available {
					status := fmt.Sprintf("IngressController %s not yet Available", ingressName)
					if status != lastStatus {
						GinkgoWriter.Println(status)
						lastStatus = status
					}
					return false, nil
				}

				svc, err := adminClient.CoreV1().Services(ingressNamespace).Get(ctx, routerServiceName, metav1.GetOptions{})
				if err != nil {
					return false, nil
				}
				for _, ingress := range svc.Status.LoadBalancer.Ingress {
					if ingress.IP == staticIP {
						GinkgoWriter.Printf("IngressController %s is Available with IP %s\n", ingressName, staticIP)
						return true, nil
					}
				}

				status := fmt.Sprintf("IngressController %s Available but waiting for IP %s", ingressName, staticIP)
				if status != lastStatus {
					GinkgoWriter.Println(status)
					lastStatus = status
				}
				return false, nil
			})
			Expect(err).NotTo(HaveOccurred())

			// ── 6. Verification ──

			By("verifying a simple web app is reachable through the custom ingress")
			appHost := fmt.Sprintf("app.%s.nip.io", staticIP)
			err = verifiers.VerifySimpleWebApp(
				verifiers.WithRouteLabels(map[string]string{"ingress": "custom"}),
				verifiers.WithRouteHost(appHost),
			).Verify(ctx, adminRESTConfig)
			Expect(err).NotTo(HaveOccurred())
		},
	)
})
