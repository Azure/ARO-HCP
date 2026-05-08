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
	"os"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/msi/armmsi"

	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

// newWITestPodWithWebhook creates a pod that relies on the workload identity
// webhook to inject env vars, volumes, and volume mounts via the
// "azure.workload.identity/use" label.
func newWITestPodWithWebhook(namespace string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "wi-test-pod",
			Namespace: namespace,
			Labels: map[string]string{
				"azure.workload.identity/use": "true",
			},
		},
		Spec: corev1.PodSpec{
			ServiceAccountName: "wi-test-sa",
			RestartPolicy:      corev1.RestartPolicyOnFailure,
			DNSPolicy:          corev1.DNSNone,
			DNSConfig: &corev1.PodDNSConfig{
				Nameservers: []string{"8.8.8.8", "1.1.1.1"},
			},
			Containers: []corev1.Container{
				{
					Name:  "azure-cli",
					Image: "mcr.microsoft.com/azure-cli:latest",
					SecurityContext: &corev1.SecurityContext{
						RunAsUser:                to.Ptr(int64(0)),
						AllowPrivilegeEscalation: to.Ptr(false),
						SeccompProfile: &corev1.SeccompProfile{
							Type: corev1.SeccompProfileTypeRuntimeDefault,
						},
						Capabilities: &corev1.Capabilities{
							Drop: []corev1.Capability{"ALL"},
						},
					},
					Command: []string{"/bin/sh", "-c", `
						az login --federated-token "$(cat $AZURE_FEDERATED_TOKEN_FILE)" \
							--service-principal \
							-u "$AZURE_CLIENT_ID" \
							-t "$AZURE_TENANT_ID" \
							--allow-no-subscriptions
					`},
				},
			},
		},
	}
}

// newWITestPodWithManualInjection creates a pod that manually injects the
// env vars, volumes, and volume mounts that the workload identity webhook
// would normally provide. Use this in environments where the webhook is
// not yet deployed.
func newWITestPodWithManualInjection(namespace, clientID, tenantID string) *corev1.Pod {
	tokenPath := "/var/run/secrets/azure/tokens/azure-identity-token"
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "wi-test-pod",
			Namespace: namespace,
		},
		Spec: corev1.PodSpec{
			ServiceAccountName: "wi-test-sa",
			RestartPolicy:      corev1.RestartPolicyOnFailure,
			DNSPolicy:          corev1.DNSNone,
			DNSConfig: &corev1.PodDNSConfig{
				Nameservers: []string{"8.8.8.8", "1.1.1.1"},
			},
			Volumes: []corev1.Volume{
				{
					Name: "azure-identity-token",
					VolumeSource: corev1.VolumeSource{
						Projected: &corev1.ProjectedVolumeSource{
							Sources: []corev1.VolumeProjection{
								{
									ServiceAccountToken: &corev1.ServiceAccountTokenProjection{
										Audience:          "api://AzureADTokenExchange",
										ExpirationSeconds: to.Ptr(int64(3600)),
										Path:              "azure-identity-token",
									},
								},
							},
						},
					},
				},
			},
			Containers: []corev1.Container{
				{
					Name:  "azure-cli",
					Image: "mcr.microsoft.com/azure-cli:latest",
					SecurityContext: &corev1.SecurityContext{
						RunAsUser:                to.Ptr(int64(0)),
						AllowPrivilegeEscalation: to.Ptr(false),
						SeccompProfile: &corev1.SeccompProfile{
							Type: corev1.SeccompProfileTypeRuntimeDefault,
						},
						Capabilities: &corev1.Capabilities{
							Drop: []corev1.Capability{"ALL"},
						},
					},
					Command: []string{"/bin/sh", "-c", `
						az login --federated-token "$(cat $AZURE_FEDERATED_TOKEN_FILE)" \
							--service-principal \
							-u "$AZURE_CLIENT_ID" \
							-t "$AZURE_TENANT_ID" \
							--allow-no-subscriptions
					`},
					Env: []corev1.EnvVar{
						{
							Name:  "AZURE_CLIENT_ID",
							Value: clientID,
						},
						{
							Name:  "AZURE_TENANT_ID",
							Value: tenantID,
						},
						{
							Name:  "AZURE_FEDERATED_TOKEN_FILE",
							Value: tokenPath,
						},
					},
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      "azure-identity-token",
							MountPath: "/var/run/secrets/azure/tokens",
							ReadOnly:  true,
						},
					},
				},
			},
		},
	}
}

var _ = Describe("Customer", func() {
	It("should be able to use workload identity via the cluster OIDC issuer URL",
		labels.RequireNothing,
		labels.Medium,
		labels.Positive,
		labels.AroRpApiCompatible,
		func(ctx context.Context) {
			const (
				customerClusterName  = "oidc-wi-cluster"
				customerNodePoolName = "oidc-wi-np"
				testNamespace        = "e2e-oidc-wi"
				testServiceAccount   = "wi-test-sa"
				testManagedIdentity  = "e2e-oidc-wi-test"
			)
			tc := framework.NewTestContext()

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, 60*time.Second)
				Expect(err).NotTo(HaveOccurred())
			}

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "oidc-wi", tc.Location())
			Expect(err).NotTo(HaveOccurred())

			By("creating cluster parameters")
			clusterParams := framework.NewDefaultClusterParams()
			clusterParams.ClusterName = customerClusterName
			managedResourceGroupName := framework.SuffixName(*resourceGroup.Name, "-managed", 64)
			clusterParams.ManagedResourceGroupName = managedResourceGroupName

			By("creating customer resources (infrastructure and managed identities) for cluster")
			clusterParams, err = tc.CreateClusterCustomerResources(ctx,
				resourceGroup,
				clusterParams,
				map[string]any{},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred())

			By("creating the HCP cluster")
			err = tc.CreateHCPClusterFromParam(ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
				45*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			By("getting the cluster's OIDC issuer URL")
			hcpClient := tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient()
			clusterResp, err := hcpClient.Get(ctx, *resourceGroup.Name, customerClusterName, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(clusterResp.Properties).NotTo(BeNil())
			Expect(clusterResp.Properties.Platform).NotTo(BeNil())

			Expect(clusterResp.Properties.Platform.IssuerURL).NotTo(BeNil(), "OIDC issuer URL should be populated on the cluster response")
			Expect(*clusterResp.Properties.Platform.IssuerURL).NotTo(BeEmpty(), "OIDC issuer URL should not be empty")

			oidcIssuerURL := *clusterResp.Properties.Platform.IssuerURL
			GinkgoWriter.Printf("Cluster OIDC issuer URL: %s\n", oidcIssuerURL)

			By("getting admin credentials")
			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster(
				ctx,
				hcpClient,
				*resourceGroup.Name,
				customerClusterName,
				10*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			By("ensuring the cluster is viable")
			err = verifiers.VerifyHCPCluster(ctx, adminRESTConfig)
			Expect(err).NotTo(HaveOccurred())

			By("creating the node pool")
			nodePoolParams := framework.NewDefaultNodePoolParams()
			nodePoolParams.ClusterName = customerClusterName
			nodePoolParams.NodePoolName = customerNodePoolName
			nodePoolParams.Replicas = int32(2)

			err = tc.CreateNodePoolFromParam(ctx,
				*resourceGroup.Name,
				customerClusterName,
				nodePoolParams,
				45*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			By("verifying nodes count and ready status")
			Expect(verifiers.VerifyNodeCount(customerClusterName, int(nodePoolParams.Replicas)).Verify(ctx, adminRESTConfig)).To(Succeed())
			Expect(verifiers.VerifyNodesReady().Verify(ctx, adminRESTConfig)).To(Succeed())

			By("creating a user-assigned managed identity for the test")
			subscriptionID, err := tc.SubscriptionID(ctx)
			Expect(err).NotTo(HaveOccurred())

			cred, err := tc.AzureCredential()
			Expect(err).NotTo(HaveOccurred())

			msiClient, err := armmsi.NewUserAssignedIdentitiesClient(subscriptionID, cred, nil)
			Expect(err).NotTo(HaveOccurred())

			msiResp, err := msiClient.CreateOrUpdate(ctx, *resourceGroup.Name, testManagedIdentity, armmsi.Identity{
				Location: resourceGroup.Location,
			}, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(msiResp.Properties).NotTo(BeNil())
			Expect(msiResp.Properties.ClientID).NotTo(BeNil())
			clientID := *msiResp.Properties.ClientID
			GinkgoWriter.Printf("Created managed identity with client ID: %s\n", clientID)

			By("creating a federated identity credential for the service account")
			ficClient, err := armmsi.NewFederatedIdentityCredentialsClient(subscriptionID, cred, nil)
			Expect(err).NotTo(HaveOccurred())

			subject := fmt.Sprintf("system:serviceaccount:%s:%s", testNamespace, testServiceAccount)
			_, err = ficClient.CreateOrUpdate(ctx, *resourceGroup.Name, testManagedIdentity, "e2e-wi-fic", armmsi.FederatedIdentityCredential{
				Properties: &armmsi.FederatedIdentityCredentialProperties{
					Issuer:    to.Ptr(oidcIssuerURL),
					Subject:   to.Ptr(subject),
					Audiences: []*string{to.Ptr("api://AzureADTokenExchange")},
				},
			}, nil)
			Expect(err).NotTo(HaveOccurred())
			GinkgoWriter.Printf("Created federated identity credential with issuer: %s, subject: %s\n", oidcIssuerURL, subject)

			By("creating the test namespace and service account in the cluster")
			adminClient, err := kubernetes.NewForConfig(adminRESTConfig)
			Expect(err).NotTo(HaveOccurred())

			ns := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: testNamespace,
				},
			}
			_, err = adminClient.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())

			sa := &corev1.ServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      testServiceAccount,
					Namespace: testNamespace,
					Annotations: map[string]string{
						"azure.workload.identity/client-id": clientID,
					},
				},
			}
			_, err = adminClient.CoreV1().ServiceAccounts(testNamespace).Create(ctx, sa, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())

			By("waiting for namespace UID range annotation to be set by the cluster-policy-controller")
			Eventually(func() bool {
				updatedNS, err := adminClient.CoreV1().Namespaces().Get(ctx, testNamespace, metav1.GetOptions{})
				if err != nil {
					return false
				}
				_, ok := updatedNS.Annotations["openshift.io/sa.scc.uid-range"]
				return ok
			}, 5*time.Minute, 5*time.Second).Should(BeTrue(), "namespace %s was never annotated with openshift.io/sa.scc.uid-range", testNamespace)

			By("creating a pod that authenticates to Azure using federated workload identity credentials")
			// Dev and INT environments have the workload identity webhook deployed.
			// Higher environments (stg, prod) may not have it yet, so we manually
			// inject the fields as a fallback.
			// After April 1st 2026, the webhook should be available everywhere.
			timebombWebhook := time.Date(2026, time.April, 1, 0, 0, 0, 0, time.UTC)
			useWebhook := time.Now().After(timebombWebhook) ||
				framework.IsDevelopmentEnvironment() ||
				os.Getenv("ARO_HCP_SUITE_NAME") == "integration/parallel"

			var pod *corev1.Pod
			if useWebhook {
				pod = newWITestPodWithWebhook(testNamespace)
			} else {
				pod = newWITestPodWithManualInjection(testNamespace, clientID, tc.TenantID())
			}
			_, err = adminClient.CoreV1().Pods(testNamespace).Create(ctx, pod, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())

			By("waiting for the pod to complete successfully")
			var prevPodState corev1.PodPhase
			Eventually(func() (corev1.PodPhase, error) {
				p, err := adminClient.CoreV1().Pods(testNamespace).Get(ctx, pod.Name, metav1.GetOptions{})
				if err != nil {
					return "", err
				}
				if prevPodState == "" {
					prevPodState = p.Status.Phase
				}

				// track pod state transitions for better visibility in test output
				if prevPodState != p.Status.Phase {
					GinkgoWriter.Printf("Pod state transitioned from '%s' to '%s'\n", prevPodState, p.Status.Phase)
					prevPodState = p.Status.Phase
				}

				if p.Status.Phase == corev1.PodFailed {
					// Log container status for debugging
					for _, cs := range p.Status.ContainerStatuses {
						if cs.State.Terminated != nil {
							GinkgoWriter.Printf("Container %s terminated with exit code %d, reason: %s, message: %s\n",
								cs.Name, cs.State.Terminated.ExitCode, cs.State.Terminated.Reason, cs.State.Terminated.Message)
						}
					}
					// Fetch pod logs for diagnostics
					logs, logErr := adminClient.CoreV1().Pods(testNamespace).GetLogs(pod.Name, &corev1.PodLogOptions{}).Do(ctx).Raw()
					if logErr == nil {
						GinkgoWriter.Printf("Pod logs:\n%s\n", string(logs))
					}
				}

				return p.Status.Phase, nil
			}, 5*time.Minute, 10*time.Second).Should(Equal(corev1.PodSucceeded))

			GinkgoWriter.Printf("Workload identity authentication succeeded. Pod completed successfully.\n")
		})
})
