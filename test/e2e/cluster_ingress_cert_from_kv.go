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
	"crypto/tls"
	"crypto/x509"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
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
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	armauthorization "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v3"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/dns/armdns"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/msi/armmsi"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azcertificates"

	configv1client "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"

	graphutil "github.com/Azure/ARO-HCP/internal/graph/util"
	"github.com/Azure/ARO-HCP/test/util/dnsreadback"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

// Pinned upstream ESO release. Bump deliberately; do not float to :latest.
const externalSecretsManifestURL = "https://github.com/external-secrets/external-secrets/releases/download/v0.16.0/external-secrets.yaml"

// Pin the DigiCert root used by the Key Vault issuer independently of the runner's
// trust store, just as the Let's Encrypt test pins its staging roots. This legacy
// root is no longer in Mozilla's trust store; this test verifies certificate
// delivery and rotation, rather than public browser trust. Source and fingerprint:
// https://knowledge.digicert.com/general-information/digicert-trusted-root-authority-certificates
//
//go:embed digicert-cas/*.crt
var digiCertCAs embed.FS

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
				customerClusterName         = "ingress-cert-kv"
				customerNodePoolName        = "np-1"
				kvCertName                  = "ingress-default"
				kvIssuerName                = "digicert"
				esoNamespace                = "external-secrets"
				esoServiceAccount           = "eso-azure-kv"
				ingressSecretName           = "ingress-tls"
				ingressNamespace            = "openshift-ingress"
				refreshInterval             = "30s"
				certificateIssuanceTimeout  = 15 * time.Minute
				externalSecretsReadyTimeout = 10 * time.Minute
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

			By("loading the pinned DigiCert root CA")
			roots, err := loadEmbeddedCAs(digiCertCAs, "digicert-cas")
			Expect(err).NotTo(HaveOccurred(), "failed to load the pinned DigiCert root CA")

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
			appsWildcard := "*." + appsBaseDomain

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

				By("waiting for DigiCert's TXT token to appear through the runner's configured DNS resolver")
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
				var lastDNSState string
				Eventually(func(ctx context.Context) error {
					lookupCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
					defer cancel()
					err := dnsreadback.VerifyTXT(lookupCtx, net.DefaultResolver, delegatedDomain, nameservers, appsBaseDomain, domain.DCVToken.Token)
					if err != nil && err.Error() != lastDNSState {
						lastDNSState = err.Error()
						GinkgoLogr.Info("waiting for DigiCert TXT propagation", "domainID", domainID, "domain", appsBaseDomain, "error", lastDNSState)
					}
					return err
				}).WithContext(ctx).WithTimeout(5*time.Minute).WithPolling(15*time.Second).Should(Succeed(),
					"DigiCert validation TXT for %s (domain ID %d) and the expected delegation should be visible through the configured DNS resolver", appsBaseDomain, domainID)

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
			configClient, err := configv1client.NewForConfig(adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to create guest config client")
			DeferCleanup(func(ctx context.Context) {
				if CurrentSpecReport().Failed() {
					for _, resource := range []struct {
						name, namespace, kind, resource string
					}{
						{"customer-akv", "", "ClusterSecretStore", "clustersecretstores"},
						{ingressSecretName, ingressNamespace, "ExternalSecret", "externalsecrets"},
					} {
						gvr := schema.GroupVersionResource{Group: "external-secrets.io", Version: "v1", Resource: resource.resource}
						_, state := externalSecretsReadyState(ctx, kubeClient, dynClient, gvr, resource.namespace, resource.name)
						GinkgoLogr.Info("External Secrets failure diagnostics", "kind", resource.kind, "name", resource.name, "namespace", resource.namespace, "state", state)
					}
				}
			}, NodeTimeout(30*time.Second))
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

			By("waiting for guest DNS readiness before ESO exchanges workload identity tokens")
			Expect(waitForIngressCertificateDNS(ctx, kubeClient, configClient, esoNamespace,
				"login.microsoftonline.com", framework.IngressDNSVerifyTimeout, 5*time.Second)).To(Succeed(),
				"guest DNS must resolve Microsoft Entra before ESO authenticates to Key Vault")

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

			By("waiting for the ClusterSecretStore to authenticate using the federated identity")
			Expect(waitForExternalSecretsReady(ctx, kubeClient, dynClient, cssGVR, "", css.GetName(), externalSecretsReadyTimeout, 10*time.Second)).To(Succeed(),
				"customer Key Vault store must become Ready before creating the ExternalSecret")

			_, err = dynClient.Resource(essGVR).Namespace(ingressNamespace).Create(ctx, ess, metav1.CreateOptions{})
			if err != nil && !apierrors.IsAlreadyExists(err) {
				Fail(fmt.Sprintf("creating ExternalSecret: %v", err))
			}

			By("waiting for ESO to read the Key Vault certificate and create the TLS Secret")
			Expect(waitForExternalSecretsReady(ctx, kubeClient, dynClient, essGVR, ingressNamespace, ingressSecretName, externalSecretsReadyTimeout, 10*time.Second)).To(Succeed(),
				"ExternalSecret must become Ready after Key Vault access and TLS templating succeed")

			By("waiting for the TLS Secret to contain valid cert v1 with the correct wildcard SAN")
			var lastErr string
			var customerCertificate *x509.Certificate
			Eventually(func() error {
				certificate, err := expectTLSSecretCertificate(ctx, kubeClient, ingressNamespace, ingressSecretName, v1.SHA256, appsWildcard, roots)
				customerCertificate = certificate
				if err != nil {
					if msg := err.Error(); msg != lastErr {
						GinkgoLogr.Info("waiting for Secret to match cert v1", "error", msg)
						lastErr = msg
					}
				}
				return err
			}).WithContext(ctx).WithTimeout(5*time.Minute).WithPolling(10*time.Second).Should(Succeed(),
				"ingress-tls Secret should match cert v1 SHA256 and contain a trusted certificate with SAN %s", appsWildcard)

			By("deploying a sample application before verifying the default ingress certificate")
			sampleApp, err := framework.DeploySampleApp(ctx, adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to deploy sample application")
			DeferCleanup(func(ctx context.Context) {
				err := kubeClient.CoreV1().Namespaces().Delete(ctx, sampleApp.Namespace, metav1.DeleteOptions{})
				if !apierrors.IsNotFound(err) {
					Expect(err).NotTo(HaveOccurred(), "failed to delete sample application namespace %q", sampleApp.Namespace)
				}
			})

			By("verifying ACM's ingress certificate must not be created")
			Expect(verifiers.VerifyACMIngressCertificateAbsent().Verify(ctx, adminRESTConfig)).To(Succeed(), "ACM must not create its ingress certificate secret")

			By("pointing IngressController/default at the customer's DigiCert certificate")
			icPatch := []byte(fmt.Sprintf(`{"spec":{"defaultCertificate":{"name":%q}}}`, ingressSecretName))
			icGVR := schema.GroupVersionResource{Group: "operator.openshift.io", Version: "v1", Resource: "ingresscontrollers"}
			_, err = dynClient.Resource(icGVR).Namespace("openshift-ingress-operator").Patch(ctx, "default", types.MergePatchType, icPatch, metav1.PatchOptions{})
			Expect(err).NotTo(HaveOccurred(), "failed to patch IngressController default certificate")

			By("verifying the sample application serves cert v1 with a valid DigiCert chain")
			Expect(verifiers.VerifyCustomIngressCertificate(sampleApp.RouteHost, appsWildcard, customerCertificate, roots, 10*time.Minute).Verify(ctx, adminRESTConfig)).To(Succeed(), "sample application must serve cert v1 with a trusted DigiCert chain")

			By("verifying ACM's ingress certificate must remain absent after the customer's override")
			Expect(verifiers.VerifyACMIngressCertificateAbsent().Verify(ctx, adminRESTConfig)).To(Succeed(), "ACM's ingress certificate must remain absent after the customer's override")

			By("rotating: issuing cert v2 through the customer Key Vault's DigiCert issuer")
			v2, err := framework.CreateOrRotateDigiCertKVCert(ctx, cred, vaultURL, kvCertName, kvIssuerName, appsBaseDomain, 6, 80, certificateIssuanceTimeout)
			Expect(err).NotTo(HaveOccurred(), "failed to rotate the cert through the Key Vault DigiCert integration")
			Expect(v2.SHA256).NotTo(Equal(v1.SHA256), "rotation should produce a different certificate")
			GinkgoLogr.Info("issued cert v2", "version", v2.Version, "sha256", v2.SHA256)

			By("waiting for the TLS Secret to contain valid cert v2 with the correct wildcard SAN")
			lastErr = ""
			Eventually(func() error {
				certificate, err := expectTLSSecretCertificate(ctx, kubeClient, ingressNamespace, ingressSecretName, v2.SHA256, appsWildcard, roots)
				customerCertificate = certificate
				if err != nil {
					if msg := err.Error(); msg != lastErr {
						GinkgoLogr.Info("waiting for Secret to match cert v2", "error", msg)
						lastErr = msg
					}
				}
				return err
			}).WithContext(ctx).WithTimeout(5*time.Minute).WithPolling(10*time.Second).Should(Succeed(),
				"ingress-tls Secret should match cert v2 SHA256 and contain a trusted certificate with SAN %s", appsWildcard)

			By("verifying the sample application automatically serves cert v2 with a valid DigiCert chain")
			Expect(verifiers.VerifyCustomIngressCertificate(sampleApp.RouteHost, appsWildcard, customerCertificate, roots, 10*time.Minute).Verify(ctx, adminRESTConfig)).To(Succeed(), "sample application must automatically serve cert v2 with a trusted DigiCert chain")

			By("verifying ACM's ingress certificate must remain absent after rotation")
			Expect(verifiers.VerifyACMIngressCertificateAbsent().Verify(ctx, adminRESTConfig)).To(Succeed(), "ACM's ingress certificate must remain absent after rotation")
		})
})

// expectTLSSecretCertificate returns the leaf certificate only if the TLS Secret
// matches the Key Vault certificate's SHA-256, contains a matching private key and
// the expected SAN, and has a valid chain to an explicitly trusted root.
func expectTLSSecretCertificate(ctx context.Context, kubeClient kubernetes.Interface, ns, name, want, expectedSAN string, roots *x509.CertPool) (*x509.Certificate, error) {
	if roots == nil {
		return nil, fmt.Errorf("secret %s/%s certificate verification requires an explicit trusted CA pool", ns, name)
	}
	s, err := kubeClient.CoreV1().Secrets(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("getting secret %s/%s: %w", ns, name, err)
	}
	if s.Type != corev1.SecretTypeTLS {
		return nil, fmt.Errorf("secret %s/%s has type %q, want kubernetes.io/tls", ns, name, s.Type)
	}
	pair, err := tls.X509KeyPair(s.Data[corev1.TLSCertKey], s.Data[corev1.TLSPrivateKeyKey])
	if err != nil {
		return nil, fmt.Errorf("parsing TLS key pair in %s/%s: %w", ns, name, err)
	}
	var certs []*x509.Certificate
	for _, der := range pair.Certificate {
		cert, err := x509.ParseCertificate(der)
		if err != nil {
			return nil, fmt.Errorf("parsing certificate in %s/%s: %w", ns, name, err)
		}
		certs = append(certs, cert)
	}
	leaf := certs[0]
	sum := sha256.Sum256(leaf.Raw)
	got := hex.EncodeToString(sum[:])
	if got != want {
		return nil, fmt.Errorf("secret %s/%s leaf SHA256=%s, want %s", ns, name, got, want)
	}
	if !slices.Contains(leaf.DNSNames, expectedSAN) {
		return nil, fmt.Errorf("secret %s/%s leaf cert SANs %v do not include expected %q", ns, name, leaf.DNSNames, expectedSAN)
	}
	if err := verifyCertChain(certs, roots); err != nil {
		return nil, fmt.Errorf("secret %s/%s certificate chain verification against trusted roots failed: %w", ns, name, err)
	}
	return leaf, nil
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
