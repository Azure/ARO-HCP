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
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	armauthorization "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v3"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/dns/armdns"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/msi/armmsi"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azcertificates"

	graphutil "github.com/Azure/ARO-HCP/internal/graph/util"
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
		labels.AroRpApiCompatible,
		labels.MIContainers(1),
		func(ctx context.Context) {
			const (
				customerClusterName  = "ingress-cert-kv"
				customerNodePoolName = "np-1"
				kvCertName           = "ingress-default"
				kvIssuerName         = "digicert"
				esoNamespace         = "external-secrets"
				esoServiceAccount    = "eso-azure-kv"
				ingressSecretName    = "ingress-tls"
				ingressNamespace     = "openshift-ingress"
				//ingressOperatorNS    = "openshift-ingress-operator"
				refreshInterval            = "30s"
				certificateIssuanceTimeout = 15 * time.Minute
			)

			tc := framework.NewTestContext()

			By("reading DigiCert credentials from mounted files")
			readCredential := func(field string) string {
				path := filepath.Join(tc.DigiCertCredentialsPath(), field)
				data, err := os.ReadFile(path)
				if errors.Is(err, os.ErrNotExist) {
					Skip(fmt.Sprintf("DigiCert credential file not found at %s, skipping test", path))
				}
				Expect(err).NotTo(HaveOccurred(), "failed to read DigiCert credential file %s", path)
				value := strings.TrimSpace(string(data))
				Expect(value).NotTo(BeEmpty(), "DigiCert credential file %s must not be empty", path)
				return value
			}
			digiCertAccountID := readCredential("account-id")
			digiCertAPIKey := readCredential("api-key")
			digiCertOrganizationID := readCredential("organization-id")
			organizationID, err := strconv.ParseInt(digiCertOrganizationID, 10, 64)
			// Avoid logging the file contents through strconv's error message.
			Expect(err == nil && organizationID > 0).To(BeTrue(),
				"DigiCert organization-id file must contain a positive integer")

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, framework.IdentityContainerAssignmentRetryInterval)
				Expect(err).NotTo(HaveOccurred(), "failed to assign pooled identity containers")
			}

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "ingress-cert-kv", tc.Location())
			Expect(err).NotTo(HaveOccurred(), "failed to create resource group")

			By("creating cluster parameters")
			clusterParams := framework.NewDefaultClusterParams20240610()
			clusterParams.ClusterName = customerClusterName
			managedResourceGroupName := framework.SuffixName(*resourceGroup.Name, "-managed", 64)
			clusterParams.ManagedResourceGroupName = managedResourceGroupName

			By("creating customer resources (incl. the customer Key Vault)")
			clusterParams, err = tc.CreateClusterCustomerResources20240610(ctx,
				resourceGroup,
				clusterParams,
				map[string]interface{}{},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create customer resources")
			Expect(clusterParams.KeyVaultName).NotTo(BeEmpty(), "customer KeyVaultName should be populated by customer-infra")

			By("creating the HCP cluster")
			err = tc.CreateHCPClusterFromParam20240610(ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
				framework.ClusterCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create HCP cluster")

			By("creating the node pool")
			nodePoolParams := framework.NewDefaultNodePoolParams20240610()
			nodePoolParams.ClusterName = customerClusterName
			nodePoolParams.NodePoolName = customerNodePoolName
			nodePoolParams.Replicas = int32(2)
			Expect(tc.CreateNodePoolFromParam20240610(ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				managedResourceGroupName,
				customerClusterName,
				nodePoolParams,
				framework.NodePoolCreationTimeout,
			)).To(Succeed(), "failed to create node pool")

			hcpClient := tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient()
			clusterResp, err := hcpClient.Get(ctx, *resourceGroup.Name, customerClusterName, nil)
			Expect(err).NotTo(HaveOccurred(), "failed to get HCP cluster")

			By("getting admin credentials")
			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster20240610(
				ctx,
				hcpClient,
				*resourceGroup.Name,
				customerClusterName,
				framework.GetAdminRESTConfigTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to get admin REST config")

			By("ensuring the cluster is viable")
			Expect(verifiers.VerifyHCPCluster(ctx, adminRESTConfig)).To(Succeed(), "cluster viability check failed")

			By("getting the cluster's OIDC issuer URL and apps base domain")
			Expect(clusterResp.Properties).NotTo(BeNil(), "cluster Properties was nil")
			Expect(clusterResp.Properties.Platform).NotTo(BeNil(), "cluster Properties.Platform was nil")
			Expect(clusterResp.Properties.Platform.IssuerURL).NotTo(BeNil(), "cluster OIDC IssuerURL was nil")
			oidcIssuerURL := *clusterResp.Properties.Platform.IssuerURL

			Expect(clusterResp.Properties.Console).NotTo(BeNil(), "cluster Properties.Console was nil")
			Expect(clusterResp.Properties.Console.URL).NotTo(BeNil(), "cluster Properties.Console.URL was nil")
			consoleURL := *clusterResp.Properties.Console.URL
			appsBaseDomain := strings.TrimSuffix(strings.Replace(consoleURL, "https://console-openshift-console.", "", 1), "/")
			Expect(appsBaseDomain).NotTo(BeEmpty(), "could not derive apps base domain from console URL %s", consoleURL)
			Expect(clusterResp.Properties.DNS).NotTo(BeNil(), "cluster Properties.DNS was nil")
			Expect(clusterResp.Properties.DNS.BaseDomain).NotTo(BeNil(), "cluster DNS.BaseDomain was nil")
			Expect(clusterResp.Properties.DNS.BaseDomainPrefix).NotTo(BeNil(), "cluster DNS.BaseDomainPrefix was nil")
			delegatedDomain := fmt.Sprintf("aro.%s.%s", *clusterResp.Properties.DNS.BaseDomainPrefix, *clusterResp.Properties.DNS.BaseDomain)
			Expect(appsBaseDomain).To(Equal("apps."+delegatedDomain), "console apps domain should belong to the cluster's delegated DNS zone")

			GinkgoLogr.Info("cluster identity", "oidcIssuer", oidcIssuerURL, "appsBaseDomain", appsBaseDomain)

			By("creating a UAMI for ESO and federating it to the in-cluster ServiceAccount")
			subscriptionID, err := tc.SubscriptionID(ctx)
			Expect(err).NotTo(HaveOccurred(), "failed to get subscription ID")
			cred, err := tc.AzureCredential()
			Expect(err).NotTo(HaveOccurred(), "failed to get Azure credential")

			msiClient, err := armmsi.NewUserAssignedIdentitiesClient(subscriptionID, cred, nil)
			Expect(err).NotTo(HaveOccurred(), "failed to create UAMI client")
			msiResp, err := msiClient.CreateOrUpdate(ctx, *resourceGroup.Name, "eso-akv", armmsi.Identity{
				Location: resourceGroup.Location,
			}, nil)
			Expect(err).NotTo(HaveOccurred(), "failed to create UAMI")
			Expect(msiResp.Properties).NotTo(BeNil(), "UAMI properties was nil")
			Expect(msiResp.Properties.ClientID).NotTo(BeNil(), "UAMI ClientID was nil")
			Expect(msiResp.Properties.PrincipalID).NotTo(BeNil(), "UAMI PrincipalID was nil")
			uamiClientID := *msiResp.Properties.ClientID
			uamiPrincipalID := *msiResp.Properties.PrincipalID

			// Safeguard for local dev if AZURE_TENANT_ID not set
			tenantID := tc.TenantID()
			if len(tenantID) == 0 {
				Expect(msiResp.Properties.TenantID).NotTo(BeNil(), "Tenant ID was nil")
				tenantID = *msiResp.Properties.TenantID
			}

			ficClient, err := armmsi.NewFederatedIdentityCredentialsClient(subscriptionID, cred, nil)
			Expect(err).NotTo(HaveOccurred(), "failed to create FIC client")
			ficSubject := fmt.Sprintf("system:serviceaccount:%s:%s", esoNamespace, esoServiceAccount)
			_, err = ficClient.CreateOrUpdate(ctx, *resourceGroup.Name, "eso-akv", "eso-akv-fic", armmsi.FederatedIdentityCredential{
				Properties: &armmsi.FederatedIdentityCredentialProperties{
					Issuer:    to.Ptr(oidcIssuerURL),
					Subject:   to.Ptr(ficSubject),
					Audiences: []*string{to.Ptr("api://AzureADTokenExchange")},
				},
			}, nil)
			Expect(err).NotTo(HaveOccurred(), "failed to create federated identity credential")

			By("granting the UAMI Key Vault Certificate User + Key Vault Secrets User on the customer Key Vault")
			kvScope := fmt.Sprintf(
				"/subscriptions/%s/resourceGroups/%s/providers/Microsoft.KeyVault/vaults/%s",
				subscriptionID, *resourceGroup.Name, clusterParams.KeyVaultName,
			)
			roleAssignmentsClient, err := armauthorization.NewRoleAssignmentsClient(subscriptionID, cred, nil)
			Expect(err).NotTo(HaveOccurred(), "failed to create role assignments client")
			// Built-in role definition IDs:
			//   Key Vault Certificate User:    db79e9a7-68ee-4b58-9aeb-b90e7c24fcba (read for the UAMI)
			//   Key Vault Secrets User:        4633458b-17de-408a-b874-0445c86b69e6 (read for the UAMI)
			//   Key Vault Certificates Officer: a4417e6f-fecd-4de8-b567-7b0420556985 (write for the test caller)
			for _, roleDefID := range []string{
				"db79e9a7-68ee-4b58-9aeb-b90e7c24fcba",
				"4633458b-17de-408a-b874-0445c86b69e6",
			} {
				Eventually(func() error {
					err := assignBuiltInRoleAtScope(ctx, roleAssignmentsClient, subscriptionID, kvScope, uamiPrincipalID, roleDefID)
					if err != nil && !isPrincipalNotFoundError(err) {
						return StopTrying(err.Error()).Wrap(err)
					}
					return err
				}).WithContext(ctx).WithTimeout(5*time.Minute).WithPolling(15*time.Second).Should(Succeed(),
					"role assignment should succeed once principal propagates")
			}

			By("granting the test caller Key Vault Certificates Officer on the customer KV so it can issue and rotate the cert")
			_, callerOID, err := graphutil.IdentifyCallerFromToken(ctx, cred)
			Expect(err).NotTo(HaveOccurred(), "looking up the e2e caller's OID from its ARM token")
			Expect(assignBuiltInRoleAtScope(ctx, roleAssignmentsClient, subscriptionID, kvScope, callerOID, "a4417e6f-fecd-4de8-b567-7b0420556985")).To(Succeed(), "failed to assign KV Certificates Officer to test caller")

			By("configuring the DigiCert certificate issuer in the customer Key Vault")
			vaultURL := fmt.Sprintf("https://%s.vault.azure.net", clusterParams.KeyVaultName)
			certificatesClient, err := azcertificates.NewClient(vaultURL, cred, nil)
			Expect(err).NotTo(HaveOccurred(), "failed to create Key Vault certificates client")
			issuerParams := azcertificates.SetIssuerParameters{
				Provider: to.Ptr("DigiCert"),
				Attributes: &azcertificates.IssuerAttributes{
					Enabled: to.Ptr(true),
				},
				Credentials: &azcertificates.IssuerCredentials{
					AccountID: to.Ptr(digiCertAccountID),
					Password:  to.Ptr(digiCertAPIKey),
				},
				OrganizationDetails: &azcertificates.OrganizationDetails{
					ID: to.Ptr(digiCertOrganizationID),
				},
			}
			// Azure RBAC propagation on a KV can lag by 30-60s after the
			// role assignment lands, so we retry issuer configuration until
			// the 403 clears.
			Eventually(func() error {
				_, err := certificatesClient.SetIssuer(ctx, kvIssuerName, issuerParams, nil)
				return err
			}).WithContext(ctx).WithTimeout(3*time.Minute).WithPolling(15*time.Second).Should(Succeed(),
				"configuring the DigiCert issuer should succeed once RBAC propagates")

			By("registering the apps domain for OV validation in DigiCert CertCentral")
			// Use the issuer's credentials so DCV and Key Vault issuance share the
			// same account and organization. The organization must already be OV validated.
			digiCert, err := framework.NewDigiCertClient(*issuerParams.Credentials.Password)
			Expect(err).NotTo(HaveOccurred(), "failed to create CertCentral client")
			domain, created, err := digiCert.EnsureDomain(ctx, appsBaseDomain, organizationID)
			if created {
				createdDomainID := domain.ID
				DeferCleanup(func(ctx context.Context) {
					cleanupCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
					defer cancel()
					Expect(digiCert.DeleteDomain(cleanupCtx, createdDomainID)).To(Succeed(), "failed to remove test-owned DigiCert domain %d (%s)", createdDomainID, appsBaseDomain)
				})
			}
			Expect(err).NotTo(HaveOccurred(), "failed to register or find DigiCert domain %s", appsBaseDomain)
			// Capture the ID before a revalidation response replaces domain.
			domainID := domain.ID
			GinkgoLogr.Info("DigiCert domain", "domain", appsBaseDomain, "domainID", domainID, "created", created)
			if !domain.OVValidated() {
				if !created {
					domain, err = digiCert.SubmitDNSValidation(ctx, domainID)
					Expect(err).NotTo(HaveOccurred(), "failed to submit DigiCert domain %d for DNS TXT validation", domainID)
				}
				Expect(domain.DCVToken.Token).NotTo(BeEmpty(), "DigiCert domain %d returned no DNS TXT validation token", domainID)
				if domain.DCVToken.ExpirationDate != "" {
					expires, err := time.Parse(time.RFC3339, domain.DCVToken.ExpirationDate)
					Expect(err).NotTo(HaveOccurred(), "invalid DigiCert token expiration for domain %d", domainID)
					Expect(expires.After(time.Now())).To(BeTrue(), "DigiCert DNS token for domain %d has expired", domainID)
				}

				By("publishing the DigiCert validation token in the cluster's Azure DNS zone")
				recordSetsClient, err := armdns.NewRecordSetsClient(subscriptionID, cred, nil)
				Expect(err).NotTo(HaveOccurred(), "failed to create Azure DNS record sets client")
				cleanupTXT, err := framework.PresentAzureDNSTXT(ctx, recordSetsClient, managedResourceGroupName, delegatedDomain, "apps", domain.DCVToken.Token)
				if cleanupTXT != nil {
					DeferCleanup(func(ctx context.Context) {
						cleanupCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
						defer cancel()
						Expect(cleanupTXT(cleanupCtx)).To(Succeed(), "failed to remove DigiCert validation TXT value for %s", appsBaseDomain)
					})
				}
				Expect(err).NotTo(HaveOccurred(), "failed to publish DigiCert DNS TXT for %s in resource group %s", appsBaseDomain, managedResourceGroupName)

				By("waiting for DigiCert's TXT token to appear in public DNS")
				zonesClient, err := armdns.NewZonesClient(subscriptionID, cred, nil)
				Expect(err).NotTo(HaveOccurred(), "failed to create Azure DNS zones client")
				zone, err := zonesClient.Get(ctx, managedResourceGroupName, delegatedDomain, nil)
				Expect(err).NotTo(HaveOccurred(), "failed to get authoritative nameservers for %s", delegatedDomain)
				Expect(zone.Properties).NotTo(BeNil(), "DNS zone %s properties were nil", delegatedDomain)
				Expect(zone.Properties.NameServers).NotTo(BeEmpty(), "DNS zone %s has no authoritative nameservers", delegatedDomain)
				var nameservers []string
				for _, nameserver := range zone.Properties.NameServers {
					Expect(nameserver).NotTo(BeNil(), "DNS zone %s nameserver was nil", delegatedDomain)
					nameservers = append(nameservers, strings.ToLower(strings.TrimSuffix(*nameserver, "."))+".")
				}
				slices.Sort(nameservers)
				nameserverAddresses := map[string]string{}
				var lastDNSState string
				Eventually(func(ctx context.Context) error {
					err := readbackIngressDNS(ctx, delegatedDomain, nameservers, nameserverAddresses, appsBaseDomain+".", domain.DCVToken.Token)
					if err != nil && err.Error() != lastDNSState {
						lastDNSState = err.Error()
						GinkgoLogr.Info("waiting for DigiCert TXT propagation", "domainID", domainID, "domain", appsBaseDomain, "error", lastDNSState)
					}
					return err
				}).WithContext(ctx).WithTimeout(5*time.Minute).WithPolling(15*time.Second).Should(Succeed(),
					"DigiCert validation TXT for %s (domain ID %d) should be visible on all public authoritative nameservers", appsBaseDomain, domainID)

				By("asking DigiCert to validate the DNS TXT token and waiting for OV domain approval")
				Expect(digiCert.CheckAndWaitForDNSValidation(ctx, domainID, 10*time.Minute)).To(Succeed(),
					"DigiCert domain %d (%s) should pass DNS validation before Key Vault certificate issuance", domainID, appsBaseDomain)
			} else {
				GinkgoLogr.Info("reusing current DigiCert OV domain validation", "domainID", domainID, "domain", appsBaseDomain)
			}

			By("issuing cert v1 through the customer Key Vault's DigiCert issuer with a rotation policy")
			v1, err := framework.CreateOrRotateDigiCertKVCert(ctx, cred, vaultURL, kvCertName, kvIssuerName, appsBaseDomain, 6, 80, certificateIssuanceTimeout)
			Expect(err).NotTo(HaveOccurred(), "failed to issue cert v1 through the Key Vault DigiCert integration")
			GinkgoLogr.Info("issued cert v1", "version", v1.Version, "sha256", v1.SHA256)

			By("installing External Secrets Operator from the pinned upstream release manifest")
			kubeClient, err := kubernetes.NewForConfig(adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to create kube client")
			dynClient, err := dynamic.NewForConfig(adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to create dynamic client")
			mapper := newDiscoveryMapper(adminRESTConfig)

			Expect(applyManifestFromURL(ctx, dynClient, mapper, externalSecretsManifestURL)).To(Succeed(), "failed to install ESO manifests")

			By("waiting for the ESO controller deployment to become Available")
			Eventually(func() error {
				dep, err := kubeClient.AppsV1().Deployments("default").Get(ctx, "external-secrets", metav1.GetOptions{})
				if err != nil {
					return err
				}
				if dep.Status.ReadyReplicas < 1 {
					return fmt.Errorf("external-secrets deployment not ready: ready=%d desired=%d", dep.Status.ReadyReplicas, dep.Status.Replicas)
				}
				return nil
			}).WithContext(ctx).WithTimeout(10*time.Minute).WithPolling(10*time.Second).Should(Succeed(), "external-secrets deployment should become ready")

			By("creating the external secrets namespace")
			esoNS := corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: esoNamespace,
				},
			}
			_, err = kubeClient.CoreV1().Namespaces().Create(ctx, &esoNS, metav1.CreateOptions{})
			if err != nil && !apierrors.IsAlreadyExists(err) {
				Expect(err).NotTo(HaveOccurred(), "failed to create ESO namespace")
			}

			By("creating ServiceAccount, ClusterSecretStore, and ExternalSecret for the ingress cert")
			esoSA := &corev1.ServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      esoServiceAccount,
					Namespace: esoNamespace,
					Annotations: map[string]string{
						"azure.workload.identity/client-id": uamiClientID,
						"azure.workload.identity/tenant-id": tenantID,
					},
				},
			}
			_, err = kubeClient.CoreV1().ServiceAccounts(esoNamespace).Create(ctx, esoSA, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred(), "failed to create ESO service account")

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
			var lastErr string
			Eventually(func() error {
				err := expectTLSSecretSHA256(ctx, kubeClient, ingressNamespace, ingressSecretName, v1.SHA256)
				if err != nil {
					if msg := err.Error(); msg != lastErr {
						GinkgoLogr.Info("waiting for Secret to match cert v1", "error", msg)
						lastErr = msg
					}
					lastErr = err.Error()
				}
				return err
			}).WithContext(ctx).WithTimeout(5*time.Minute).WithPolling(10*time.Second).Should(Succeed(),
				"ingress-tls Secret should match cert v1 SHA256")

			//By("pointing IngressController/default at the new TLS Secret")
			//patch := []byte(fmt.Sprintf(`{"spec":{"defaultCertificate":{"name":%q}}}`, ingressSecretName))
			//icGVR := schema.GroupVersionResource{Group: "operator.openshift.io", Version: "v1", Resource: "ingresscontrollers"}
			//_, err = dynClient.Resource(icGVR).Namespace(ingressOperatorNS).Patch(ctx, "default", types.MergePatchType, patch, metav1.PatchOptions{})
			//Expect(err).NotTo(HaveOccurred(), "failed to patch IngressController default certificate")

			//By("waiting for the router to serve cert v1 on the apps wildcard")
			//consoleHostPort := mustHostPortFromURL(consoleURL, 443)
			//Eventually(func() error {
			//	return expectServedCertSHA256(ctx, consoleHostPort, v1.SHA256)
			//}).WithContext(ctx).WithTimeout(10 * time.Minute).WithPolling(15 * time.Second).Should(Succeed())

			By("rotating: issuing cert v2 through the customer Key Vault's DigiCert issuer")
			v2, err := framework.CreateOrRotateDigiCertKVCert(ctx, cred, vaultURL, kvCertName, kvIssuerName, appsBaseDomain, 6, 80, certificateIssuanceTimeout)
			Expect(err).NotTo(HaveOccurred(), "failed to rotate the cert through the Key Vault DigiCert integration")
			Expect(v2.SHA256).NotTo(Equal(v1.SHA256), "rotation should produce a different certificate")
			GinkgoLogr.Info("issued cert v2", "version", v2.Version, "sha256", v2.SHA256)

			By("waiting for the ingress-tls Secret to pick up cert v2")
			lastErr = ""
			Eventually(func() error {
				err := expectTLSSecretSHA256(ctx, kubeClient, ingressNamespace, ingressSecretName, v2.SHA256)
				if err != nil {
					if msg := err.Error(); msg != lastErr {
						GinkgoLogr.Info("waiting for Secret to match cert v2", "error", msg)
						lastErr = msg
					}
				}
				return err
			}).WithContext(ctx).WithTimeout(5*time.Minute).WithPolling(10*time.Second).Should(Succeed(),
				"ingress-tls Secret should match cert v2 SHA256")

			//By("waiting for the router to serve cert v2")
			//Eventually(func() error {
			//	return expectServedCertSHA256(ctx, consoleHostPort, v2.SHA256)
			//}).WithContext(ctx).WithTimeout(10 * time.Minute).WithPolling(15 * time.Second).Should(Succeed())
		})
})

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
