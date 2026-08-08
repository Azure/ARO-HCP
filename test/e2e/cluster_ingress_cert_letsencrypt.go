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
	"crypto/x509"
	"embed"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/google/uuid"

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

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	armauthorization "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v3"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/dns/armdns"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/msi/armmsi"

	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

// Let's Encrypt staging root CAs from https://letsencrypt.org/docs/staging-environment/
//
//go:embed letsencrypt-stg-cas/*.crt
var letsEncryptStagingCAs embed.FS

// Pinned upstream cert-manager release. Bump deliberately; do not float to :latest.
const certManagerManifestURL = "https://github.com/cert-manager/cert-manager/releases/download/v1.20.2/cert-manager.yaml"

var _ = Describe("Customer", func() {
	It("should be able to issue the default ingress serving certificate from Let's Encrypt using cert-manager with ACME DNS-01 challenges",
		labels.RequireNothing,
		labels.High,
		labels.Positive,
		labels.CreateCluster,
		labels.AroRpApiCompatible,
		func(ctx context.Context) {
			const (
				customerClusterName  = "ingress-cert-le"
				customerNodePoolName = "np-1"
				certManagerNamespace = "cert-manager"
				certManagerSAName    = "cert-manager"
				ingressSecretName    = "letsencrypt-ingress-secret"
				ingressNamespace     = "openshift-ingress"
				issuerName           = "letsencrypt-staging" // we're using staging as prod has strict limits on cert signing
				acmeEmail            = "noreply@redhat.com"
				acmeServer           = "https://acme-staging-v02.api.letsencrypt.org/directory"
			)
			tc := framework.NewTestContext()
			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, framework.IdentityContainerAssignmentRetryInterval)
				Expect(err).NotTo(HaveOccurred(), "failed to assign pooled identity containers")
			}

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "ingress-cert-le", tc.Location())
			Expect(err).NotTo(HaveOccurred(), "failed to create resource group")

			By("creating cluster parameters")
			clusterParams := framework.NewDefaultClusterParams20240610()
			clusterParams.ClusterName = customerClusterName
			managedResourceGroupName := framework.SuffixName(*resourceGroup.Name, "-managed", 64)
			clusterParams.ManagedResourceGroupName = managedResourceGroupName

			By("creating customer resources")
			clusterParams, err = tc.CreateClusterCustomerResources20240610(ctx,
				resourceGroup,
				clusterParams,
				map[string]interface{}{},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create customer resources")

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

			By("getting the cluster's OIDC issuer URL and DNS domain")
			Expect(clusterResp.Properties).NotTo(BeNil(), "cluster Properties was nil")
			Expect(clusterResp.Properties.Platform).NotTo(BeNil(), "cluster Properties.Platform was nil")
			Expect(clusterResp.Properties.Platform.IssuerURL).NotTo(BeNil(), "cluster OIDC IssuerURL was nil")
			oidcIssuerURL := *clusterResp.Properties.Platform.IssuerURL

			Expect(clusterResp.Properties.DNS).NotTo(BeNil(), "cluster Properties.DNS was nil")
			Expect(clusterResp.Properties.DNS.BaseDomain).NotTo(BeNil(), "cluster DNS.BaseDomain was nil")
			Expect(clusterResp.Properties.DNS.BaseDomainPrefix).NotTo(BeNil(), "cluster DNS.BaseDomainPrefix was nil")
			baseDomain := *clusterResp.Properties.DNS.BaseDomain
			baseDomainPrefix := *clusterResp.Properties.DNS.BaseDomainPrefix
			delegatedDomain := fmt.Sprintf("aro.%s.%s", baseDomainPrefix, baseDomain)
			appsWildcard := fmt.Sprintf("*.apps.%s", delegatedDomain)

			GinkgoLogr.Info("cluster information for certificate issuance",
				"oidcIssuer", oidcIssuerURL,
				"baseDomain", baseDomain,
				"baseDomainPrefix", baseDomainPrefix,
				"delegatedDomain", delegatedDomain,
				"appsWildcard", appsWildcard,
			)

			By("deriving the DNS zone from the cluster's base domain")
			subscriptionID, err := tc.SubscriptionID(ctx)
			Expect(err).NotTo(HaveOccurred(), "failed to get subscription ID")
			cred, err := tc.AzureCredential()
			Expect(err).NotTo(HaveOccurred(), "failed to get Azure credential")

			dnsZoneResourceID := fmt.Sprintf(
				"/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Network/dnsZones/%s",
				subscriptionID, managedResourceGroupName, delegatedDomain,
			)
			GinkgoLogr.Info("derived DNS zone", "zone", delegatedDomain, "resourceGroup", managedResourceGroupName, "resourceID", dnsZoneResourceID)

			By("creating a CAA record to authorize Let's Encrypt to issue certificates for the zone")
			recordSetsClient, err := armdns.NewRecordSetsClient(subscriptionID, cred, nil)
			Expect(err).NotTo(HaveOccurred(), "failed to create DNS record sets client")
			_, err = recordSetsClient.CreateOrUpdate(ctx, managedResourceGroupName, delegatedDomain, "@", armdns.RecordTypeCAA, armdns.RecordSet{
				Properties: &armdns.RecordSetProperties{
					TTL: to.Ptr[int64](3600),
					CaaRecords: []*armdns.CaaRecord{
						{
							Flags: to.Ptr[int32](0),
							Tag:   to.Ptr("issue"),
							Value: to.Ptr("letsencrypt.org"),
						},
					},
				},
			}, nil)
			Expect(err).NotTo(HaveOccurred(), "failed to create CAA record authorizing letsencrypt.org")

			By("creating a UAMI for cert-manager and federating it to the in-cluster ServiceAccount")
			msiClient, err := armmsi.NewUserAssignedIdentitiesClient(subscriptionID, cred, nil)
			Expect(err).NotTo(HaveOccurred(), "failed to create UAMI client")
			msiResp, err := msiClient.CreateOrUpdate(ctx, *resourceGroup.Name, "cert-manager", armmsi.Identity{
				Location: resourceGroup.Location,
			}, nil)
			Expect(err).NotTo(HaveOccurred(), "failed to create UAMI")
			Expect(msiResp.Properties).NotTo(BeNil(), "UAMI properties was nil")
			Expect(msiResp.Properties.ClientID).NotTo(BeNil(), "UAMI ClientID was nil")
			Expect(msiResp.Properties.PrincipalID).NotTo(BeNil(), "UAMI PrincipalID was nil")
			uamiClientID := *msiResp.Properties.ClientID
			uamiPrincipalID := *msiResp.Properties.PrincipalID

			tenantID := tc.TenantID()
			if len(tenantID) == 0 {
				Expect(msiResp.Properties.TenantID).NotTo(BeNil(), "user assigned managed identity tenantID is nil")
				tenantID = *msiResp.Properties.TenantID
			}

			ficClient, err := armmsi.NewFederatedIdentityCredentialsClient(subscriptionID, cred, nil)
			Expect(err).NotTo(HaveOccurred(), "failed to create FIC client")
			ficSubject := fmt.Sprintf("system:serviceaccount:%s:%s", certManagerNamespace, certManagerSAName)
			_, err = ficClient.CreateOrUpdate(ctx, *resourceGroup.Name, "cert-manager", "cert-manager-fic", armmsi.FederatedIdentityCredential{
				Properties: &armmsi.FederatedIdentityCredentialProperties{
					Issuer:    to.Ptr(oidcIssuerURL),
					Subject:   to.Ptr(ficSubject),
					Audiences: []*string{to.Ptr("api://AzureADTokenExchange")},
				},
			}, nil)
			Expect(err).NotTo(HaveOccurred(), "failed to create federated identity credential")

			By("granting the UAMI DNS Zone Contributor on the DNS zone in the managed resource group")
			// DNS Zone Contributor built-in role: befefa01-2a29-4197-83a8-272ff33ce314
			const dnsZoneContributorRoleID = "befefa01-2a29-4197-83a8-272ff33ce314"
			Eventually(func() error {
				// TODO: do we relax assigning role assignments over the managed resource group?
				err := assignBuiltInRoleAtScope(ctx, newRoleAssignmentsClient(subscriptionID, cred), subscriptionID, fmt.Sprintf("/subscriptions/%s", subscriptionID), uamiPrincipalID, dnsZoneContributorRoleID)
				if err != nil && !isPrincipalNotFoundError(err) {
					return StopTrying(err.Error()).Wrap(err)
				}
				return err
			}).WithContext(ctx).WithTimeout(5*time.Minute).WithPolling(15*time.Second).Should(Succeed(),
				"DNS Zone Contributor role assignment should succeed once principal propagates")

			By("installing cert-manager from the pinned upstream release manifest")
			kubeClient, err := kubernetes.NewForConfig(adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to create kube client")
			dynClient, err := dynamic.NewForConfig(adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to create dynamic client")
			mapper := newDiscoveryMapper(adminRESTConfig)

			Expect(applyManifestFromURL(ctx, dynClient, mapper, certManagerManifestURL)).To(Succeed(), "failed to install cert-manager manifests")

			By("waiting for the cert-manager controller deployment to become Available")
			var lastDeployErr string
			Eventually(func() error {
				dep, err := kubeClient.AppsV1().Deployments(certManagerNamespace).Get(ctx, "cert-manager", metav1.GetOptions{})
				if err != nil {
					return err
				}
				if dep.Status.ReadyReplicas < 1 {
					msg := fmt.Sprintf("cert-manager deployment not ready: ready=%d desired=%d", dep.Status.ReadyReplicas, dep.Status.Replicas)
					if msg != lastDeployErr {
						GinkgoLogr.Info("waiting for cert-manager", "status", msg)
						lastDeployErr = msg
					}
					return fmt.Errorf("%s", msg)
				}
				return nil
			}).WithContext(ctx).WithTimeout(10*time.Minute).WithPolling(10*time.Second).Should(Succeed(),
				"cert-manager deployment should become ready")

			By("annotating the cert-manager ServiceAccount with workload identity credentials")
			saPatch := fmt.Sprintf(`{
				"metadata": {
					"annotations": {
						"azure.workload.identity/client-id": %q,
						"azure.workload.identity/tenant-id": %q
					}
				}
			}`, uamiClientID, tenantID)
			_, err = kubeClient.CoreV1().ServiceAccounts(certManagerNamespace).Patch(
				ctx, certManagerSAName, types.MergePatchType, []byte(saPatch), metav1.PatchOptions{})
			Expect(err).NotTo(HaveOccurred(), "failed to patch cert-manager ServiceAccount with workload identity annotations")

			By("labeling the cert-manager deployment pods for workload identity injection and restarting")
			depPatch := []byte(`{
				"spec": {
					"template": {
						"metadata": {
							"labels": {
								"azure.workload.identity/use": "true"
							}
						}
					}
				}
			}`)
			_, err = kubeClient.AppsV1().Deployments(certManagerNamespace).Patch(
				ctx, "cert-manager", types.MergePatchType, depPatch, metav1.PatchOptions{})
			Expect(err).NotTo(HaveOccurred(), "failed to patch cert-manager deployment with workload identity label")

			By("waiting for the cert-manager deployment to roll out with workload identity")
			Eventually(func() error {
				dep, err := kubeClient.AppsV1().Deployments(certManagerNamespace).Get(ctx, "cert-manager", metav1.GetOptions{})
				if err != nil {
					return err
				}
				if dep.Status.UpdatedReplicas != dep.Status.Replicas || dep.Status.ReadyReplicas != dep.Status.Replicas {
					return fmt.Errorf("cert-manager rollout in progress: updated=%d ready=%d desired=%d",
						dep.Status.UpdatedReplicas, dep.Status.ReadyReplicas, dep.Status.Replicas)
				}
				if dep.Status.ObservedGeneration < dep.Generation {
					return fmt.Errorf("cert-manager deployment generation not yet observed: observed=%d current=%d",
						dep.Status.ObservedGeneration, dep.Generation)
				}
				return nil
			}).WithContext(ctx).WithTimeout(5*time.Minute).WithPolling(10*time.Second).Should(Succeed(),
				"cert-manager deployment should complete rollout with workload identity")

			By("creating a ClusterIssuer with ACME DNS-01 solver targeting the DNS zone in the managed resource group")
			issuerObj := unstructuredClusterIssuer(
				issuerName, acmeEmail, acmeServer,
				delegatedDomain, managedResourceGroupName, subscriptionID, uamiClientID,
			)
			issuerGVR := schema.GroupVersionResource{Group: "cert-manager.io", Version: "v1", Resource: "clusterissuers"}
			Eventually(func() error {
				_, err := dynClient.Resource(issuerGVR).Create(ctx, issuerObj, metav1.CreateOptions{})
				if err != nil && !apierrors.IsAlreadyExists(err) {
					return err
				}
				return nil
			}).WithContext(ctx).WithTimeout(2*time.Minute).WithPolling(5*time.Second).Should(Succeed(),
				"ClusterIssuer CRD should be installed and the resource creatable")

			By("waiting for the ClusterIssuer to become Ready")
			var lastIssuerStatus string
			Eventually(func() error {
				issuer, err := dynClient.Resource(issuerGVR).Get(ctx, issuerName, metav1.GetOptions{})
				if err != nil {
					return err
				}
				ready, msg := isResourceReady(issuer)
				if !ready {
					if msg != lastIssuerStatus {
						GinkgoLogr.Info("waiting for ClusterIssuer", "status", msg)
						lastIssuerStatus = msg
					}
					return fmt.Errorf("clusterIssuer not ready: %s", msg)
				}
				return nil
			}).WithContext(ctx).WithTimeout(5*time.Minute).WithPolling(10*time.Second).Should(Succeed(),
				"ClusterIssuer should become Ready (ACME account registered)")

			By("creating a Certificate for the apps wildcard domain")
			certObj := unstructuredCertificate("cluster-ingress-cert", ingressNamespace, ingressSecretName, issuerName, appsWildcard)
			certGVR := schema.GroupVersionResource{Group: "cert-manager.io", Version: "v1", Resource: "certificates"}
			_, err = dynClient.Resource(certGVR).Namespace(ingressNamespace).Create(ctx, certObj, metav1.CreateOptions{})
			if err != nil && !apierrors.IsAlreadyExists(err) {
				Fail(fmt.Sprintf("creating Certificate: %v", err))
			}

			By("waiting for the Certificate to be issued")
			var lastCertStatus string
			Eventually(func() error {
				cert, err := dynClient.Resource(certGVR).Namespace(ingressNamespace).Get(ctx, "cluster-ingress-cert", metav1.GetOptions{})
				if err != nil {
					return err
				}
				ready, msg := isResourceReady(cert)
				if !ready {
					if msg != lastCertStatus {
						GinkgoLogr.Info("waiting for Certificate issuance", "status", msg)
						lastCertStatus = msg
					}
					return fmt.Errorf("certificate not ready: %s", msg)
				}
				return nil
			}).WithContext(ctx).WithTimeout(10*time.Minute).WithPolling(15*time.Second).Should(Succeed(),
				"Certificate should be issued by Let's Encrypt staging via ACME DNS-01")

			By("verifying the TLS Secret contains a valid certificate with the correct SAN")
			err = expectTLSSecretIssuedByLetsEncrypt(ctx, kubeClient, ingressNamespace, ingressSecretName, appsWildcard)
			Expect(err).NotTo(HaveOccurred(), "TLS Secret should contain a valid cert issued by Let's Encrypt staging with SAN %s", appsWildcard)

			// TODO: once we stop managing ingress certificates with ACM, we should plumb the openshift default ingresscontroller with this certificate
			// 	signed by staging LetsEncrypt server

			//By("pointing IngressController/default at the new TLS Secret")
			//icPatch := []byte(fmt.Sprintf(`{"spec":{"defaultCertificate":{"name":%q}}}`, ingressSecretName))
			//icGVR := schema.GroupVersionResource{Group: "operator.openshift.io", Version: "v1", Resource: "ingresscontrollers"}
			//_, err = dynClient.Resource(icGVR).Namespace("openshift-ingress-operator").Patch(ctx, "default", types.MergePatchType, icPatch, metav1.PatchOptions{})
			//Expect(err).NotTo(HaveOccurred(), "failed to patch IngressController default certificate")
		})
})

// unstructuredClusterIssuer builds a cert-manager.io/v1 ClusterIssuer that uses
// ACME DNS-01 challenges with the Azure DNS solver.
func unstructuredClusterIssuer(name, email, server, hostedZoneName, resourceGroupName, subscriptionID, uamiClientID string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "cert-manager.io/v1",
		"kind":       "ClusterIssuer",
		"metadata":   map[string]interface{}{"name": name},
		"spec": map[string]interface{}{
			"acme": map[string]interface{}{
				"email":  email,
				"server": server,
				"privateKeySecretRef": map[string]interface{}{
					"name": name + "-account-key",
				},
				"solvers": []interface{}{
					map[string]interface{}{
						"dns01": map[string]interface{}{
							"azureDNS": map[string]interface{}{
								"environment":       "AzurePublicCloud",
								"hostedZoneName":    hostedZoneName,
								"resourceGroupName": resourceGroupName,
								"subscriptionID":    subscriptionID,
								"managedIdentity": map[string]interface{}{
									"clientID": uamiClientID,
								},
							},
						},
					},
				},
			},
		},
	}}
}

// unstructuredCertificate builds a cert-manager.io/v1 Certificate that requests
// a TLS cert for the given DNS name from the named ClusterIssuer.
func unstructuredCertificate(name, namespace, secretName, issuerName, dnsName string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "cert-manager.io/v1",
		"kind":       "Certificate",
		"metadata": map[string]interface{}{
			"name":      name,
			"namespace": namespace,
		},
		"spec": map[string]interface{}{
			"secretName": secretName,
			"privateKey": map[string]interface{}{
				"rotationPolicy": "Always",
			},
			"dnsNames": []interface{}{dnsName},
			"issuerRef": map[string]interface{}{
				"name": issuerName,
				"kind": "ClusterIssuer",
			},
		},
	}}
}

// isResourceReady checks the status.conditions of an unstructured resource for
// a condition with type=Ready. Returns (true, "") if Ready=True, or
// (false, summary) describing the current state.
func isResourceReady(obj *unstructured.Unstructured) (bool, string) {
	conditions, found, err := unstructured.NestedSlice(obj.Object, "status", "conditions")
	if err != nil || !found {
		return false, "no status.conditions found"
	}
	for _, c := range conditions {
		cond, ok := c.(map[string]interface{})
		if !ok {
			continue
		}
		condType, _, _ := unstructured.NestedString(cond, "type")
		condStatus, _, _ := unstructured.NestedString(cond, "status")
		condMsg, _, _ := unstructured.NestedString(cond, "message")
		condReason, _, _ := unstructured.NestedString(cond, "reason")
		if condType == "Ready" {
			if condStatus == "True" {
				return true, ""
			}
			return false, fmt.Sprintf("reason=%s message=%s", condReason, condMsg)
		}
	}
	return false, "Ready condition not present"
}

// expectTLSSecretIssuedByLetsEncrypt verifies that the named Secret exists, is
// of type kubernetes.io/tls, has a leaf cert whose SAN matches expectedSAN,
// and that the certificate chain validates up to a Let's Encrypt staging root CA.
func expectTLSSecretIssuedByLetsEncrypt(ctx context.Context, kubeClient kubernetes.Interface, ns, name, expectedSAN string) error {
	s, err := kubeClient.CoreV1().Secrets(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("getting secret %s/%s: %w", ns, name, err)
	}
	if s.Type != corev1.SecretTypeTLS {
		return fmt.Errorf("secret %s/%s has type %q, want kubernetes.io/tls", ns, name, s.Type)
	}
	crtPEM, ok := s.Data[corev1.TLSCertKey]
	if !ok || len(crtPEM) == 0 {
		return fmt.Errorf("secret %s/%s has no tls.crt data", ns, name)
	}

	var certs []*x509.Certificate
	rest := crtPEM
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return fmt.Errorf("parsing certificate in %s/%s: %w", ns, name, err)
		}
		certs = append(certs, cert)
	}
	if len(certs) == 0 {
		return fmt.Errorf("secret %s/%s tls.crt contains no valid PEM certificates", ns, name)
	}

	leaf := certs[0]
	sanFound := false
	for _, san := range leaf.DNSNames {
		if san == expectedSAN {
			sanFound = true
			break
		}
	}
	if !sanFound {
		return fmt.Errorf("leaf cert SANs %v do not include expected %q", leaf.DNSNames, expectedSAN)
	}

	roots, err := loadEmbeddedCAs(letsEncryptStagingCAs, "letsencrypt-stg-cas")
	if err != nil {
		return fmt.Errorf("loading Let's Encrypt staging root CAs: %w", err)
	}

	if err := verifyCertChain(certs, roots); err != nil {
		return fmt.Errorf("certificate chain verification against Let's Encrypt staging roots failed: %w", err)
	}

	GinkgoLogr.Info("verified TLS Secret certificate chain to Let's Encrypt staging root",
		"secret", fmt.Sprintf("%s/%s", ns, name),
		"san", leaf.DNSNames,
		"issuerCN", leaf.Issuer.CommonName,
		"notBefore", leaf.NotBefore,
		"notAfter", leaf.NotAfter,
	)
	return nil
}

// newRoleAssignmentsClient creates a role assignments client. Extracted to keep
// the test body readable.
func newRoleAssignmentsClient(subscriptionID string, cred azcore.TokenCredential) *armauthorization.RoleAssignmentsClient {
	client, err := armauthorization.NewRoleAssignmentsClient(subscriptionID, cred, nil)
	Expect(err).NotTo(HaveOccurred(), "failed to create role assignments client")
	return client
}

func applyManifestFromURL(ctx context.Context, dyn dynamic.Interface, mapper meta.RESTMapper, manifestURL string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, manifestURL, nil)
	if err != nil {
		return err
	}
	httpClient := &http.Client{Timeout: 2 * time.Minute}
	resp, err := httpClient.Do(req)
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
		},
	}, nil)
	if err != nil {
		var respErr *azcore.ResponseError
		if errors.As(err, &respErr) && respErr.ErrorCode == "RoleAssignmentExists" {
			return nil
		}
		return fmt.Errorf("assigning role %s on %s to %s: %w", roleDefinitionID, scope, principalID, err)
	}
	return nil
}

func isPrincipalNotFoundError(err error) bool {
	var respErr *azcore.ResponseError
	return errors.As(err, &respErr) && respErr.ErrorCode == "PrincipalNotFound"
}
