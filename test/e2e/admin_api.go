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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"k8s.io/client-go/rest"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

var _ = Describe("SRE", func() {
	BeforeEach(func() {
		// do nothing.  per test initialization usually ages better than shared.
	})

	It("should be able to log into a cluster via a breakglass session",
		labels.RequireNothing,
		labels.High,
		labels.Positive,
		labels.CoreInfraService,
		labels.DevelopmentOnly,
		labels.AroRpApiCompatible,
		func(ctx context.Context) {
			const (
				engineeringNetworkSecurityGroupName = "sre-nsg-name"
				engineeringVnetName                 = "sre-vnet-name"
				engineeringVnetSubnetName           = "sre-vnet-subnet1"
				engineeringClusterName              = "sre-hcp-cluster"
			)
			tc := framework.NewTestContext()

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, 60*time.Second)
				Expect(err).NotTo(HaveOccurred())
			}

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "admin-api-breakglass", tc.Location())
			Expect(err).NotTo(HaveOccurred())

			By("creating cluster parameters")
			clusterParams := framework.NewDefaultClusterParams()
			clusterParams.ClusterName = engineeringClusterName
			managedResourceGroupName := framework.SuffixName(*resourceGroup.Name, "-managed", 64)
			clusterParams.ManagedResourceGroupName = managedResourceGroupName

			By("creating customer resources")
			clusterParams, err = tc.CreateClusterCustomerResources(ctx,
				resourceGroup,
				clusterParams,
				map[string]interface{}{
					"customerNsgName":        engineeringNetworkSecurityGroupName,
					"customerVnetName":       engineeringVnetName,
					"customerVnetSubnetName": engineeringVnetSubnetName,
				},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred())

			By("creating the HCP cluster")
			err = tc.CreateHCPClusterFromParam(
				ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
				45*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			hcpResourceID := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.RedHatOpenshift/hcpOpenShiftClusters/%s", api.Must(tc.SubscriptionID(ctx)), *resourceGroup.Name, engineeringClusterName)

			// commonVerifiers are run for both aro-sre-pso and aro-sre-csa access levels.
			// They cover actual data access smoke tests and SSAR-based read permission
			// checks matching the system:aro-sre ClusterRole (which is the lower bound
			// that both levels must satisfy).
			commonVerifiers := []verifiers.HostedClusterVerifier{
				// Actual data access smoke tests
				verifiers.VerifyListNamespaced("kube-system", "pods", "configmaps"),
				verifiers.VerifyList("nodes", "namespaces"),
				//verifiers.VerifyGetDeploymentLogs("openshift-monitoring", "prometheus-operator", ""),
				// Read access across API groups via SSAR (system:aro-sre ClusterRole)
				verifiers.VerifyRBACAllowed(
					// core API resources
					verifiers.CanList("", "services"),
					verifiers.CanList("", "endpoints"),
					verifiers.CanList("", "events"),
					verifiers.CanList("", "persistentvolumeclaims"),
					verifiers.CanGet("", "persistentvolumes"),
					verifiers.CanList("", "serviceaccounts"),
					verifiers.CanList("", "resourcequotas"),
					verifiers.CanList("", "limitranges"),
					verifiers.CanList("", "replicationcontrollers"),
					verifiers.CanGet("", "componentstatuses"),
					// core subresource
					verifiers.CanGetSubresource("", "pods", "log"),
					// apps
					verifiers.CanList("apps", "deployments"),
					verifiers.CanList("apps", "daemonsets"),
					verifiers.CanList("apps", "statefulsets"),
					verifiers.CanList("apps", "replicasets"),
					// batch
					verifiers.CanList("batch", "jobs"),
					verifiers.CanList("batch", "cronjobs"),
					// networking
					verifiers.CanList("networking.k8s.io", "networkpolicies"),
					verifiers.CanList("networking.k8s.io", "ingresses"),
					// rbac
					verifiers.CanList("rbac.authorization.k8s.io", "clusterroles"),
					verifiers.CanList("rbac.authorization.k8s.io", "clusterrolebindings"),
					verifiers.CanList("rbac.authorization.k8s.io", "roles"),
					verifiers.CanList("rbac.authorization.k8s.io", "rolebindings"),
					// storage
					verifiers.CanList("storage.k8s.io", "storageclasses"),
					verifiers.CanList("storage.k8s.io", "volumeattachments"),
					// apiextensions
					verifiers.CanList("apiextensions.k8s.io", "customresourcedefinitions"),
					// policy
					verifiers.CanList("policy", "poddisruptionbudgets"),
					// autoscaling
					verifiers.CanList("autoscaling", "horizontalpodautoscalers"),
					// coordination
					verifiers.CanList("coordination.k8s.io", "leases"),
					// discovery
					verifiers.CanList("discovery.k8s.io", "endpointslices"),
					// certificates
					verifiers.CanList("certificates.k8s.io", "certificatesigningrequests"),
					// flowcontrol
					verifiers.CanList("flowcontrol.apiserver.k8s.io", "flowschemas"),
					verifiers.CanList("flowcontrol.apiserver.k8s.io", "prioritylevelconfigurations"),
					// scheduling
					verifiers.CanList("scheduling.k8s.io", "priorityclasses"),
					// node
					verifiers.CanList("node.k8s.io", "runtimeclasses"),
					// admissionregistration
					verifiers.CanList("admissionregistration.k8s.io", "mutatingwebhookconfigurations"),
					verifiers.CanList("admissionregistration.k8s.io", "validatingwebhookconfigurations"),
					// apiregistration
					verifiers.CanList("apiregistration.k8s.io", "apiservices"),
					// OpenShift: config
					verifiers.CanList("config.openshift.io", "clusterversions"),
					verifiers.CanList("config.openshift.io", "clusteroperators"),
					verifiers.CanList("config.openshift.io", "infrastructures"),
					// OpenShift: monitoring
					verifiers.CanList("monitoring.coreos.com", "prometheusrules"),
					verifiers.CanList("monitoring.coreos.com", "servicemonitors"),
					// OpenShift: machine
					verifiers.CanList("machine.openshift.io", "machines"),
					verifiers.CanList("machine.openshift.io", "machinesets"),
					// OpenShift: operator
					verifiers.CanList("operator.openshift.io", "ingresscontrollers"),
					// OpenShift: security
					verifiers.CanList("security.openshift.io", "securitycontextconstraints"),
					// OpenShift: route
					verifiers.CanList("route.openshift.io", "routes"),
					// OpenShift: image
					verifiers.CanList("image.openshift.io", "imagestreams"),
					// auth: create permissions
					verifiers.CanCreate("authentication.k8s.io", "tokenreviews"),
					verifiers.CanCreate("authorization.k8s.io", "selfsubjectaccessreviews"),
					verifiers.CanCreate("authorization.k8s.io", "subjectaccessreviews"),
				),
			}

			By("resolving current Azure identity")
			currentIdentity, err := tc.GetCurrentAzureIdentityDetails(ctx)
			Expect(err).NotTo(HaveOccurred())

			// aro-sre-pso access — bound to system:aro-sre ClusterRole (read-only)

			By("creating SRE breakglass credentials with aro-sre-pso permissions")
			aroSrePsoRestConfig, expiresAt, err := tc.CreateSREBreakglassCredentials(ctx, hcpResourceID, 2*time.Minute, "aro-sre-pso", currentIdentity)
			Expect(err).NotTo(HaveOccurred())
			err = runCreateSREBreakglassCredentialsVerifier(ctx, "aro-sre-pso", aroSrePsoRestConfig, append(commonVerifiers,
				// Negative: secrets read is forbidden (actual access test)
				verifiers.ExpectForbidden(verifiers.VerifyListNamespaced("kube-system", "secrets")),
				// Negative: write operations are forbidden
				verifiers.VerifyRBACDenied(
					verifiers.CanCreate("", "pods"),
					verifiers.CanDelete("", "pods"),
					verifiers.CanUpdate("", "pods"),
					verifiers.CanCreate("", "configmaps"),
					verifiers.CanDelete("", "configmaps"),
					verifiers.CanCreate("", "namespaces"),
					verifiers.CanDelete("", "namespaces"),
					verifiers.CanDelete("", "nodes"),
					verifiers.CanUpdate("apps", "deployments"),
					verifiers.CanDelete("apps", "deployments"),
					verifiers.CanCreate("apps", "deployments"),
					verifiers.CanGet("", "secrets"),
				),
			))
			Expect(err).NotTo(HaveOccurred())
			By("waiting for the session to expire")
			waitForSessionExpiration(expiresAt)
			By("verifying the session is expired")
			Eventually(func() error {
				return verifiers.VerifyList("namespaces").Verify(ctx, aroSrePsoRestConfig)
			}, 30*time.Second, 2*time.Second).Should(HaveOccurred())

			// aro-sre-csa access — bound to cluster-admin ClusterRole (full access)

			By("creating SRE breakglass credentials with aro-sre-csa permissions")
			aroSreCsaRestConfig, expiresAt, err := tc.CreateSREBreakglassCredentials(ctx, hcpResourceID, 2*time.Minute, "aro-sre-csa", currentIdentity)
			Expect(err).NotTo(HaveOccurred())
			err = runCreateSREBreakglassCredentialsVerifier(ctx, "aro-sre-csa", aroSreCsaRestConfig, append(commonVerifiers,
				// Positive: can read secrets (cluster-admin)
				verifiers.VerifyListNamespaced("kube-system", "secrets"),
				// Positive: has full write access (cluster-admin)
				verifiers.VerifyRBACAllowed(
					verifiers.CanGet("", "secrets"),
					verifiers.CanCreate("", "pods"),
					verifiers.CanDelete("", "pods"),
					verifiers.CanCreate("", "namespaces"),
					verifiers.CanCreate("apps", "deployments"),
					verifiers.CanDelete("apps", "deployments"),
					verifiers.CanUpdate("apps", "deployments"),
				),
			))
			Expect(err).NotTo(HaveOccurred())
			By("waiting for the session to expire")
			waitForSessionExpiration(expiresAt)
			By("verifying the session is expired")
			Eventually(func() error {
				return verifiers.VerifyList("namespaces").Verify(ctx, aroSreCsaRestConfig)
			}, 30*time.Second, 2*time.Second).Should(HaveOccurred())

			// owner access restriction

			By("trying to access a breakglass session of another user")
			otherUserRestConfig, _, err := tc.CreateSREBreakglassCredentials(ctx, hcpResourceID, 1*time.Minute, "aro-sre-pso", &framework.AzureIdentityDetails{
				PrincipalName: "other-app-oid",
				PrincipalType: framework.PrincipalTypeAADServicePrincipal,
			})
			Expect(err).NotTo(HaveOccurred())
			By("and expecting cluster access to be denied")
			Expect(verifiers.VerifyWhoAmI("aro-sre").Verify(ctx, otherUserRestConfig)).To(HaveOccurred())
		})

	It("should be able to retrieve serial console logs for a VM",
		labels.RequireNothing,
		labels.Medium,
		labels.Positive,
		labels.CoreInfraService,
		labels.DevelopmentOnly,
		labels.AroRpApiCompatible,
		func(ctx context.Context) {
			const (
				engineeringNetworkSecurityGroupName = "sre-nsg-name"
				engineeringVnetName                 = "sre-vnet-name"
				engineeringVnetSubnetName           = "sre-vnet-subnet1"
				engineeringClusterName              = "sre-hcp-cluster-sc"
			)
			tc := framework.NewTestContext()

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, 60*time.Second)
				Expect(err).NotTo(HaveOccurred())
			}

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "admin-api-serialconsole", tc.Location())
			Expect(err).NotTo(HaveOccurred())

			By("creating cluster parameters")
			clusterParams := framework.NewDefaultClusterParams()
			clusterParams.ClusterName = engineeringClusterName
			managedResourceGroupName := framework.SuffixName(*resourceGroup.Name, "-managed", 64)
			clusterParams.ManagedResourceGroupName = managedResourceGroupName

			By("creating customer resources")
			clusterParams, err = tc.CreateClusterCustomerResources(ctx,
				resourceGroup,
				clusterParams,
				map[string]interface{}{
					"customerNsgName":        engineeringNetworkSecurityGroupName,
					"customerVnetName":       engineeringVnetName,
					"customerVnetSubnetName": engineeringVnetSubnetName,
				},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred())

			By("creating the HCP cluster")
			err = tc.CreateHCPClusterFromParam(
				ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
				45*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			By("creating a nodepool to provision worker VMs")
			nodePoolParams := framework.NewDefaultNodePoolParams()
			nodePoolParams.ClusterName = engineeringClusterName
			nodePoolParams.NodePoolName = "worker"
			nodePoolParams.Replicas = int32(1)

			err = tc.CreateNodePoolFromParam(ctx,
				*resourceGroup.Name,
				engineeringClusterName,
				nodePoolParams,
				45*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			hcpResourceID := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.RedHatOpenshift/hcpOpenShiftClusters/%s", api.Must(tc.SubscriptionID(ctx)), *resourceGroup.Name, engineeringClusterName)

			By("resolving current Azure identity")
			currentIdentity, err := tc.GetCurrentAzureIdentityDetails(ctx)
			Expect(err).NotTo(HaveOccurred())

			By("getting VM name from managed resource group")
			vmName, err := tc.GetFirstVMFromManagedResourceGroup(ctx, managedResourceGroupName)
			Expect(err).NotTo(HaveOccurred())
			Expect(vmName).NotTo(BeEmpty())

			By(fmt.Sprintf("retrieving serial console logs for VM %s", vmName))
			logs, err := tc.GetSerialConsoleLogs(ctx, hcpResourceID, vmName, currentIdentity)
			Expect(err).NotTo(HaveOccurred())
			Expect(logs).NotTo(BeEmpty())

			By("verifying serial console logs contain boot information")
			// Serial console logs typically contain boot messages, kernel output, or systemd logs
			// We just verify that we got some content back
			Expect(len(logs)).To(BeNumerically(">", 0))
		})
})

// waitForSessionExpiration sleeps until the session's expiration time has passed.
// If the expiration is already in the past (e.g. session creation took longer
// than the TTL), this returns immediately.
func waitForSessionExpiration(expiresAt time.Time) {
	if remaining := time.Until(expiresAt); remaining > 0 {
		time.Sleep(remaining)
	}
}

func runCreateSREBreakglassCredentialsVerifier(ctx context.Context, expectedGroup string, restConfig *rest.Config, tests []verifiers.HostedClusterVerifier) error {
	By(fmt.Sprintf("verifying %s group membership", expectedGroup))
	Eventually(func() error {
		return verifiers.VerifyWhoAmI(expectedGroup).Verify(ctx, restConfig)
	}, 30*time.Second, 2*time.Second).Should(Succeed())
	for _, verifier := range tests {
		By(fmt.Sprintf("verifying %s", verifier.Name()))
		Eventually(func() error {
			return verifier.Verify(ctx, restConfig)
		}, 30*time.Second, 2*time.Second).Should(Succeed())
	}
	return nil
}
