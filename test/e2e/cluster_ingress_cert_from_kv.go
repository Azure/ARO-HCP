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
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/discovery"
	memory "k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	armauthorization "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v3"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/msi/armmsi"

	"github.com/google/uuid"

	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

// Pinned upstream ESO release. Bump deliberately; do not float to :latest.
const externalSecretsManifestURL = "https://github.com/external-secrets/external-secrets/releases/download/v0.16.0/external-secrets.yaml"

var _ = Describe("Customer", func() {
	It("should be able to source the default ingress serving certificate from an Azure Key Vault and have rotations propagate automatically",
		labels.RequireNothing,
		labels.High,
		labels.Positive,
		labels.CreateCluster,
		func(ctx context.Context) {
			const (
				customerClusterName  = "ingress-cert-kv"
				customerNodePoolName = "np-1"
				kvCertName           = "ingress-default"
				esoNamespace         = "external-secrets"
				esoServiceAccount    = "eso-azure-kv"
				ingressSecretName    = "ingress-tls"
				ingressNamespace     = "openshift-ingress"
				ingressOperatorNS    = "openshift-ingress-operator"
				refreshInterval      = "30s"
			)

			tc := framework.NewTestContext()

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, 60*time.Second)
				Expect(err).NotTo(HaveOccurred())
			}

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "ingress-cert-kv", tc.Location())
			Expect(err).NotTo(HaveOccurred())

			By("creating cluster parameters")
			clusterParams := framework.NewDefaultClusterParams()
			clusterParams.ClusterName = customerClusterName
			managedResourceGroupName := framework.SuffixName(*resourceGroup.Name, "-managed", 64)
			clusterParams.ManagedResourceGroupName = managedResourceGroupName

			By("creating customer resources (incl. the customer Key Vault)")
			clusterParams, err = tc.CreateClusterCustomerResources(ctx,
				resourceGroup,
				clusterParams,
				map[string]interface{}{},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(clusterParams.KeyVaultName).NotTo(BeEmpty(), "customer KeyVaultName should be populated by customer-infra")

			By("creating the HCP cluster")
			err = tc.CreateHCPClusterFromParam(ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
				45*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			By("getting the cluster's OIDC issuer URL and apps base domain")
			hcpClient := tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient()
			clusterResp, err := hcpClient.Get(ctx, *resourceGroup.Name, customerClusterName, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(clusterResp.Properties).NotTo(BeNil(), "cluster Properties was nil")
			Expect(clusterResp.Properties.Platform).NotTo(BeNil(), "cluster Properties.Platform was nil")
			Expect(clusterResp.Properties.Platform.IssuerURL).NotTo(BeNil(), "cluster OIDC IssuerURL was nil")
			oidcIssuerURL := *clusterResp.Properties.Platform.IssuerURL

			Expect(clusterResp.Properties.Console).NotTo(BeNil(), "cluster Properties.Console was nil")
			Expect(clusterResp.Properties.Console.URL).NotTo(BeNil(), "cluster Properties.Console.URL was nil")
			consoleURL := *clusterResp.Properties.Console.URL
			appsBaseDomain := appsBaseDomainFromConsoleURL(consoleURL)
			Expect(appsBaseDomain).NotTo(BeEmpty(), "could not derive apps base domain from console URL %s", consoleURL)
			GinkgoLogr.Info("cluster identity", "oidcIssuer", oidcIssuerURL, "appsBaseDomain", appsBaseDomain)

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
			Expect(verifiers.VerifyHCPCluster(ctx, adminRESTConfig)).To(Succeed())

			By("creating the node pool")
			nodePoolParams := framework.NewDefaultNodePoolParams()
			nodePoolParams.ClusterName = customerClusterName
			nodePoolParams.NodePoolName = customerNodePoolName
			nodePoolParams.Replicas = int32(2)
			Expect(tc.CreateNodePoolFromParam(ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				managedResourceGroupName,
				customerClusterName,
				nodePoolParams,
				45*time.Minute,
			)).To(Succeed())

			By("creating a UAMI for ESO and federating it to the in-cluster ServiceAccount")
			subscriptionID, err := tc.SubscriptionID(ctx)
			Expect(err).NotTo(HaveOccurred())
			cred, err := tc.AzureCredential()
			Expect(err).NotTo(HaveOccurred())

			msiClient, err := armmsi.NewUserAssignedIdentitiesClient(subscriptionID, cred, nil)
			Expect(err).NotTo(HaveOccurred())
			msiResp, err := msiClient.CreateOrUpdate(ctx, *resourceGroup.Name, "eso-akv", armmsi.Identity{
				Location: resourceGroup.Location,
			}, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(msiResp.Properties).NotTo(BeNil(), "UAMI properties was nil")
			Expect(msiResp.Properties.ClientID).NotTo(BeNil(), "UAMI ClientID was nil")
			uamiClientID := *msiResp.Properties.ClientID
			uamiPrincipalID := *msiResp.Properties.PrincipalID

			ficClient, err := armmsi.NewFederatedIdentityCredentialsClient(subscriptionID, cred, nil)
			Expect(err).NotTo(HaveOccurred())
			ficSubject := fmt.Sprintf("system:serviceaccount:%s:%s", esoNamespace, esoServiceAccount)
			_, err = ficClient.CreateOrUpdate(ctx, *resourceGroup.Name, "eso-akv", "eso-akv-fic", armmsi.FederatedIdentityCredential{
				Properties: &armmsi.FederatedIdentityCredentialProperties{
					Issuer:    to.Ptr(oidcIssuerURL),
					Subject:   to.Ptr(ficSubject),
					Audiences: []*string{to.Ptr("api://AzureADTokenExchange")},
				},
			}, nil)
			Expect(err).NotTo(HaveOccurred())

			By("granting the UAMI Key Vault Certificate User + Key Vault Secrets User on the customer Key Vault")
			kvScope := fmt.Sprintf(
				"/subscriptions/%s/resourceGroups/%s/providers/Microsoft.KeyVault/vaults/%s",
				subscriptionID, *resourceGroup.Name, clusterParams.KeyVaultName,
			)
			roleAssignmentsClient, err := armauthorization.NewRoleAssignmentsClient(subscriptionID, cred, nil)
			Expect(err).NotTo(HaveOccurred())
			// Built-in role definition IDs:
			//   Key Vault Certificate User: db79e9a7-68ee-4b58-9aeb-b90e7c24fcba
			//   Key Vault Secrets User:     4633458b-17de-408a-b874-0445c86b69e6
			for _, roleDefID := range []string{
				"db79e9a7-68ee-4b58-9aeb-b90e7c24fcba",
				"4633458b-17de-408a-b874-0445c86b69e6",
			} {
				Expect(assignBuiltInRoleAtScope(ctx, roleAssignmentsClient, subscriptionID, kvScope, uamiPrincipalID, roleDefID)).To(Succeed())
			}

			By("creating cert v1 in the customer Key Vault with a rotation policy")
			vaultURL := fmt.Sprintf("https://%s.vault.azure.net", clusterParams.KeyVaultName)
			v1, err := framework.CreateOrRotateSelfSignedKVCert(ctx, cred, vaultURL, kvCertName, appsBaseDomain, 12, 80)
			Expect(err).NotTo(HaveOccurred())
			GinkgoLogr.Info("issued cert v1", "version", v1.Version, "sha256", v1.SHA256)

			By("installing External Secrets Operator from the pinned upstream release manifest")
			kubeClient, err := kubernetes.NewForConfig(adminRESTConfig)
			Expect(err).NotTo(HaveOccurred())
			dynClient, err := dynamic.NewForConfig(adminRESTConfig)
			Expect(err).NotTo(HaveOccurred())
			mapper := newDiscoveryMapper(adminRESTConfig)

			Expect(applyManifestFromURL(ctx, dynClient, mapper, externalSecretsManifestURL)).To(Succeed())

			By("waiting for the ESO controller deployment to become Available")
			Eventually(func() error {
				dep, err := kubeClient.AppsV1().Deployments(esoNamespace).Get(ctx, "external-secrets", metav1.GetOptions{})
				if err != nil {
					return err
				}
				if dep.Status.ReadyReplicas < 1 {
					return fmt.Errorf("external-secrets deployment not ready: ready=%d desired=%d", dep.Status.ReadyReplicas, dep.Status.Replicas)
				}
				return nil
			}).WithContext(ctx).WithTimeout(10 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())

			By("creating ServiceAccount, ClusterSecretStore, and ExternalSecret for the ingress cert")
			esoSA := &corev1.ServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      esoServiceAccount,
					Namespace: esoNamespace,
					Annotations: map[string]string{
						"azure.workload.identity/client-id": uamiClientID,
						"azure.workload.identity/tenant-id": tc.TenantID(),
					},
				},
			}
			_, err = kubeClient.CoreV1().ServiceAccounts(esoNamespace).Create(ctx, esoSA, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())

			css := unstructuredClusterSecretStore("customer-akv", vaultURL, esoNamespace, esoServiceAccount)
			ess := unstructuredExternalSecretTLS(
				ingressSecretName, ingressNamespace,
				"customer-akv", kvCertName, refreshInterval,
			)

			cssGVR := schema.GroupVersionResource{Group: "external-secrets.io", Version: "v1", Resource: "clustersecretstores"}
			essGVR := schema.GroupVersionResource{Group: "external-secrets.io", Version: "v1", Resource: "externalsecrets"}

			Eventually(func() error {
				_, err := dynClient.Resource(cssGVR).Create(ctx, css, metav1.CreateOptions{})
				if err != nil && !apierrors.IsAlreadyExists(err) {
					return err
				}
				return nil
			}).WithContext(ctx).WithTimeout(2*time.Minute).WithPolling(5*time.Second).Should(Succeed(),
				"ClusterSecretStore CRD should be installed and the resource creatable")

			_, err = dynClient.Resource(essGVR).Namespace(ingressNamespace).Create(ctx, ess, metav1.CreateOptions{})
			if err != nil && !apierrors.IsAlreadyExists(err) {
				Fail(fmt.Sprintf("creating ExternalSecret: %v", err))
			}

			By("waiting for the ingress-tls Secret to materialize and match cert v1")
			Eventually(func() error {
				return expectTLSSecretSHA256(ctx, kubeClient, ingressNamespace, ingressSecretName, v1.SHA256)
			}).WithContext(ctx).WithTimeout(5 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())

			By("pointing IngressController/default at the new TLS Secret")
			patch := []byte(fmt.Sprintf(`{"spec":{"defaultCertificate":{"name":%q}}}`, ingressSecretName))
			icGVR := schema.GroupVersionResource{Group: "operator.openshift.io", Version: "v1", Resource: "ingresscontrollers"}
			_, err = dynClient.Resource(icGVR).Namespace(ingressOperatorNS).Patch(ctx, "default", types.MergePatchType, patch, metav1.PatchOptions{})
			Expect(err).NotTo(HaveOccurred())

			By("waiting for the router to serve cert v1 on the apps wildcard")
			consoleHostPort := mustHostPortFromURL(consoleURL, 443)
			Eventually(func() error {
				return expectServedCertSHA256(ctx, consoleHostPort, v1.SHA256)
			}).WithContext(ctx).WithTimeout(10 * time.Minute).WithPolling(15 * time.Second).Should(Succeed())

			By("rotating: creating cert v2 in the customer Key Vault")
			v2, err := framework.CreateOrRotateSelfSignedKVCert(ctx, cred, vaultURL, kvCertName, appsBaseDomain, 12, 80)
			Expect(err).NotTo(HaveOccurred())
			Expect(v2.SHA256).NotTo(Equal(v1.SHA256), "v2 SHA should differ from v1")
			GinkgoLogr.Info("issued cert v2", "version", v2.Version, "sha256", v2.SHA256)

			By("waiting for the ingress-tls Secret to pick up cert v2")
			// 4 * refreshInterval gives ESO room to land the change; pad for safety.
			Eventually(func() error {
				return expectTLSSecretSHA256(ctx, kubeClient, ingressNamespace, ingressSecretName, v2.SHA256)
			}).WithContext(ctx).WithTimeout(5 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())

			By("waiting for the router to serve cert v2")
			Eventually(func() error {
				return expectServedCertSHA256(ctx, consoleHostPort, v2.SHA256)
			}).WithContext(ctx).WithTimeout(10 * time.Minute).WithPolling(15 * time.Second).Should(Succeed())
		})
})

// appsBaseDomainFromConsoleURL extracts the `apps.<basedomain>` suffix from a
// console URL such as `https://console-openshift-console.apps.<basedomain>`.
func appsBaseDomainFromConsoleURL(consoleURL string) string {
	u, err := url.Parse(consoleURL)
	if err != nil {
		return ""
	}
	host := u.Hostname()
	idx := strings.Index(host, ".apps.")
	if idx < 0 {
		return ""
	}
	return host[idx+1:]
}

func mustHostPortFromURL(raw string, defaultPort int) string {
	u, err := url.Parse(raw)
	Expect(err).NotTo(HaveOccurred())
	host := u.Host
	if !strings.Contains(host, ":") {
		host = fmt.Sprintf("%s:%d", host, defaultPort)
	}
	return host
}

// expectTLSSecretSHA256 returns nil iff a Secret with type kubernetes.io/tls
// exists at ns/name and the SHA-256 of the leaf cert in tls.crt matches want.
func expectTLSSecretSHA256(ctx context.Context, kubeClient kubernetes.Interface, ns, name, want string) error {
	s, err := kubeClient.CoreV1().Secrets(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return err
	}
	if s.Type != corev1.SecretTypeTLS {
		return fmt.Errorf("secret %s/%s has type %q, want kubernetes.io/tls", ns, name, s.Type)
	}
	crtPEM, ok := s.Data[corev1.TLSCertKey]
	if !ok || len(crtPEM) == 0 {
		return fmt.Errorf("secret %s/%s has no tls.crt", ns, name)
	}
	block, _ := pem.Decode(crtPEM)
	if block == nil {
		return fmt.Errorf("secret %s/%s tls.crt is not valid PEM", ns, name)
	}
	leaf, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return fmt.Errorf("parsing leaf cert in %s/%s: %w", ns, name, err)
	}
	got := hex.EncodeToString(sha256Sum(leaf.Raw))
	if got != want {
		return fmt.Errorf("secret %s/%s leaf SHA256=%s, want %s", ns, name, got, want)
	}
	return nil
}

func expectServedCertSHA256(ctx context.Context, hostPort, want string) error {
	certs, err := tlsCertsFromURL(ctx, "https://"+hostPort)
	if err != nil {
		return err
	}
	got := hex.EncodeToString(sha256Sum(certs[0].Raw))
	if got != want {
		return fmt.Errorf("served cert SHA256=%s, want %s, issuer=%s", got, want, certs[0].Issuer)
	}
	return nil
}

func sha256Sum(b []byte) []byte {
	sum := sha256.Sum256(b)
	return sum[:]
}

// unstructuredClusterSecretStore builds an external-secrets.io/v1
// ClusterSecretStore that authenticates to AKV via Workload Identity.
func unstructuredClusterSecretStore(name, vaultURL, saNamespace, saName string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "external-secrets.io/v1",
		"kind":       "ClusterSecretStore",
		"metadata":   map[string]interface{}{"name": name},
		"spec": map[string]interface{}{
			"provider": map[string]interface{}{
				"azurekv": map[string]interface{}{
					"authType": "WorkloadIdentity",
					"vaultUrl": vaultURL,
					"serviceAccountRef": map[string]interface{}{
						"name":      saName,
						"namespace": saNamespace,
					},
				},
			},
		},
	}}
}

// unstructuredExternalSecretTLS builds an external-secrets.io/v1 ExternalSecret
// that templates the PFX returned by AKV into a kubernetes.io/tls Secret.
func unstructuredExternalSecretTLS(name, namespace, storeName, akvCertName, refreshInterval string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "external-secrets.io/v1",
		"kind":       "ExternalSecret",
		"metadata": map[string]interface{}{
			"name":      name,
			"namespace": namespace,
		},
		"spec": map[string]interface{}{
			"refreshInterval": refreshInterval,
			"secretStoreRef": map[string]interface{}{
				"name": storeName,
				"kind": "ClusterSecretStore",
			},
			"target": map[string]interface{}{
				"name": name,
				"template": map[string]interface{}{
					"type": "kubernetes.io/tls",
					"data": map[string]interface{}{
						"tls.crt": "{{ .pfx | b64dec | pkcs12cert }}",
						"tls.key": "{{ .pfx | b64dec | pkcs12key }}",
					},
				},
			},
			"data": []interface{}{
				map[string]interface{}{
					"secretKey": "pfx",
					"remoteRef": map[string]interface{}{
						"key": akvCertName,
					},
				},
			},
		},
	}}
}

// applyManifestFromURL fetches a multi-doc YAML manifest, splits it, and
// Creates each object via the dynamic client. IsAlreadyExists is treated as
// success so the helper is re-runnable.
func applyManifestFromURL(ctx context.Context, dyn dynamic.Interface, mapper meta.RESTMapper, manifestURL string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, manifestURL, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("fetching %s: %w", manifestURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: HTTP %d", manifestURL, resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	decoder := utilyaml.NewYAMLOrJSONDecoder(bytes.NewReader(body), 4096)
	for {
		obj := &unstructured.Unstructured{}
		if err := decoder.Decode(obj); err != nil {
			if err == io.EOF {
				break
			}
			return fmt.Errorf("decoding manifest doc: %w", err)
		}
		if len(obj.Object) == 0 {
			continue
		}
		gvk := obj.GroupVersionKind()
		mapping, err := mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
		if err != nil {
			return fmt.Errorf("mapping %v: %w", gvk, err)
		}
		var ri dynamic.ResourceInterface
		if mapping.Scope.Name() == meta.RESTScopeNameNamespace {
			ri = dyn.Resource(mapping.Resource).Namespace(obj.GetNamespace())
		} else {
			ri = dyn.Resource(mapping.Resource)
		}
		if _, err := ri.Create(ctx, obj, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("creating %s %s/%s: %w", gvk.Kind, obj.GetNamespace(), obj.GetName(), err)
		}
	}
	return nil
}

func newDiscoveryMapper(cfg *rest.Config) meta.RESTMapper {
	dc := discovery.NewDiscoveryClientForConfigOrDie(cfg)
	return restmapper.NewDeferredDiscoveryRESTMapper(memory.NewMemCacheClient(dc))
}

// assignBuiltInRoleAtScope assigns an Azure built-in role to principalID at
// the given scope. The assignment name is a deterministic UUID over
// (scope, principal, roleDef) so re-runs hit the idempotent "already exists"
// path rather than creating duplicates.
func assignBuiltInRoleAtScope(
	ctx context.Context,
	client *armauthorization.RoleAssignmentsClient,
	subscriptionID, scope, principalID, roleDefinitionID string,
) error {
	roleDefResourceID := fmt.Sprintf("/subscriptions/%s/providers/Microsoft.Authorization/roleDefinitions/%s", subscriptionID, roleDefinitionID)
	assignmentName := uuid.NewSHA1(uuid.NameSpaceDNS, []byte(strings.Join([]string{scope, principalID, roleDefinitionID}, "|"))).String()

	_, err := client.Create(ctx, scope, assignmentName, armauthorization.RoleAssignmentCreateParameters{
		Properties: &armauthorization.RoleAssignmentProperties{
			PrincipalID:      to.Ptr(principalID),
			RoleDefinitionID: to.Ptr(roleDefResourceID),
			PrincipalType:    to.Ptr(armauthorization.PrincipalTypeServicePrincipal),
		},
	}, nil)
	if err != nil && !strings.Contains(err.Error(), "RoleAssignmentExists") {
		return fmt.Errorf("assigning role %s on %s to %s: %w", roleDefinitionID, scope, principalID, err)
	}
	return nil
}
