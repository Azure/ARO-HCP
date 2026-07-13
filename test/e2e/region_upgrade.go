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
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
)

// nodePoolHash computes a deterministic SHA-256 over the Kubernetes node objects
// belonging to nodePoolName. The hash covers node Name, UID, KubeletVersion, and
// OSImage so that both identity (name) and version (kubelet/OS) changes are detected.
// UID inclusion catches VM replacements that preserve the node name.
func nodePoolHash(ctx context.Context, kubeClient kubernetes.Interface, nodePoolName string) (string, error) {
	nodeList, err := kubeClient.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return "", fmt.Errorf("list nodes: %w", err)
	}

	poolNodes, err := framework.SelectNodesBelongingToNodePool(nodeList.Items, nodePoolName)
	if err != nil {
		return "", fmt.Errorf("select nodes for pool %q: %w", nodePoolName, err)
	}

	if len(poolNodes) == 0 {
		return "", fmt.Errorf("no nodes found for pool %q", nodePoolName)
	}

	sort.Slice(poolNodes, func(i, j int) bool {
		return poolNodes[i].Name < poolNodes[j].Name
	})

	h := sha256.New()
	for _, node := range poolNodes {
		// UID changes whenever the node object is recreated (e.g. VMSS instance replaced),
		// catching redeployments that preserve the node name, version, and OS image.
		fmt.Fprintf(h, "%s/%s/%s/%s\n",
			node.Name,
			node.UID,
			node.Status.NodeInfo.KubeletVersion,
			node.Status.NodeInfo.OSImage,
		)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// haProxyImage returns the container image used by the kube-apiserver-proxy
// static pod(s) running in kube-system on the hosted cluster nodes. Hypershift
// embeds this pod (named "kube-apiserver-proxy") in a MachineConfig; kubelet
// appends the node name so each instance appears as
// "kube-apiserver-proxy-<nodeName>" in the API. All pods originating from the
// same MachineConfig carry the same image; the function errors if inconsistent
// images are found across pods.
func haProxyImage(ctx context.Context, kubeClient kubernetes.Interface) (string, error) {
	pods, err := kubeClient.CoreV1().Pods("kube-system").List(ctx, metav1.ListOptions{
		LabelSelector: "k8s-app=kube-apiserver-proxy",
	})
	if err != nil {
		return "", fmt.Errorf("list kube-apiserver-proxy pods in kube-system: %w", err)
	}

	var image string
	for _, pod := range pods.Items {
		for _, c := range pod.Spec.Containers {
			if c.Image == "" {
				continue
			}
			if image == "" {
				image = c.Image
			} else if image != c.Image {
				return "", fmt.Errorf("inconsistent haproxy container images across pods: %q vs %q", image, c.Image)
			}
		}
	}
	if image == "" {
		return "", fmt.Errorf("no kube-apiserver-proxy pod found in kube-system (label k8s-app=kube-apiserver-proxy)")
	}
	return image, nil
}

// machineDeploymentGVR is the GroupVersionResource for CAPI MachineDeployments.
var machineDeploymentGVR = schema.GroupVersionResource{
	Group:    "cluster.x-k8s.io",
	Version:  "v1beta2",
	Resource: "machinedeployments",
}

// machineDeploymentRef identifies a MachineDeployment by its namespace and name.
type machineDeploymentRef struct {
	Namespace string
	Name      string
}

func (r machineDeploymentRef) String() string {
	return r.Namespace + "/" + r.Name
}

// resolveMachineDeploymentRef returns the namespace and name of the
// MachineDeployment that Hypershift manages for nodePoolName on the management
// cluster. It is intended to be called once before the observation loop; the
// ref is stable for the lifetime of the NodePool.
//
// Hypershift does not label MachineDeployments with the NodePool name; instead
// it sets the annotation hypershift.openshift.io/nodePool=<namespace>/<crName>
// where <crName> is <clusterID>-<nodepoolName> on ARO-HCP management clusters.
// The function lists all MachineDeployments, strips the namespace prefix from
// the annotation value, and matches on the CR name ending with -<nodepoolName>.
func resolveMachineDeploymentRef(ctx context.Context, mcClient dynamic.Interface, nodePoolName string) (machineDeploymentRef, error) {
	list, err := mcClient.Resource(machineDeploymentGVR).Namespace(metav1.NamespaceAll).List(ctx, metav1.ListOptions{})
	if err != nil {
		return machineDeploymentRef{}, fmt.Errorf("list MachineDeployments: %w", err)
	}

	const nodepoolAnnotation = "hypershift.openshift.io/nodePool"
	var matches []machineDeploymentRef
	// Collect all annotation values for diagnostics when no match is found.
	annotationValues := make([]string, 0, len(list.Items))
	for _, md := range list.Items {
		v := md.GetAnnotations()[nodepoolAnnotation]
		annotationValues = append(annotationValues, fmt.Sprintf("%s=%q", md.GetName(), v))
		// Annotation format: "<namespace>/<crName>" where <crName> is
		// "<clusterID>-<nodepoolName>" on ARO-HCP management clusters.
		// Match the nodepool name as an exact suffix of the CR name component.
		crName := v
		if i := strings.LastIndex(v, "/"); i >= 0 {
			crName = v[i+1:]
		}
		if crName == nodePoolName || strings.HasSuffix(crName, "-"+nodePoolName) {
			matches = append(matches, machineDeploymentRef{Namespace: md.GetNamespace(), Name: md.GetName()})
		}
	}
	if len(matches) == 0 {
		return machineDeploymentRef{}, fmt.Errorf("no MachineDeployment found for nodepool %q; found %d MachineDeployments with annotations: %v",
			nodePoolName, len(list.Items), annotationValues)
	}
	if len(matches) > 1 {
		return machineDeploymentRef{}, fmt.Errorf("expected 1 MachineDeployment for nodepool %q, found %d: %v", nodePoolName, len(matches), matches)
	}
	return matches[0], nil
}

// machineDeploymentDataSecretName returns the bootstrap DataSecretName from the
// MachineDeployment identified by ref. It performs a single GET rather than a
// list, making it cheap to call on every poll cycle.
//
// If the MachineDeployment no longer exists (e.g. because the upgrade caused it
// to be deleted and recreated under a new name), the GET returns a not-found
// error which surfaces as a Consistently failure — the correct outcome since
// MachineDeployment recreation itself triggers a node rollout.
func machineDeploymentDataSecretName(ctx context.Context, mcClient dynamic.Interface, ref machineDeploymentRef) (string, error) {
	md, err := mcClient.Resource(machineDeploymentGVR).Namespace(ref.Namespace).Get(ctx, ref.Name, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("get MachineDeployment %s: %w", ref, err)
	}

	secretName, found, err := unstructured.NestedString(
		md.Object,
		"spec", "template", "spec", "bootstrap", "dataSecretName",
	)
	if err != nil {
		return "", fmt.Errorf("reading dataSecretName from MachineDeployment %s: %w", ref, err)
	}
	if !found || secretName == "" {
		return "", fmt.Errorf("dataSecretName not set in MachineDeployment %s", ref)
	}
	return secretName, nil
}

// repoRoot derives the repository root from the test binary path.
// The binary is built at <repo-root>/test/aro-hcp-tests (see test/Makefile),
// so the repo root is two directory levels above the executable.
// Using the executable path rather than os.Getwd() makes make invocations
// resilient when the test is launched from any working directory.
func repoRoot() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolving executable path: %w", err)
	}
	real, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return "", fmt.Errorf("evaluating symlinks for %q: %w", exe, err)
	}
	// real == <repo-root>/test/aro-hcp-tests → Dir→ test/ → Dir→ repo root
	return filepath.Dir(filepath.Dir(real)), nil
}

var _ = Describe("Region in-place upgrade", func() {
	It("validates node pool stability after full region upgrade",
		labels.Critical,
		labels.Positive,
		labels.UpgradeInPlace,
		func(ctx context.Context) {
			const (
				// Time to observe for unexpected node pool hash changes after the
				// pipeline deploy completes.
				rolloutObservationWindow = 20 * time.Minute
				rolloutPollInterval      = 30 * time.Second
			)

			// Validate required env vars before provisioning any resources.
			overrideConfigFile := os.Getenv("OVERRIDE_CONFIG_FILE")
			Expect(overrideConfigFile).NotTo(BeEmpty(),
				"OVERRIDE_CONFIG_FILE must be set to the region override config path")

			deployEnv := os.Getenv("DEPLOY_ENV")
			Expect(deployEnv).NotTo(BeEmpty(), "DEPLOY_ENV must be set for make entrypoint/Region")

			suffix := rand.String(6)
			clusterName := framework.SuffixName("e2e-upgrade", suffix, 64)
			nodePoolName := "nodepool-" + suffix

			tc := framework.NewTestContext()

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, framework.IdentityContainerAssignmentRetryInterval)
				Expect(err).NotTo(HaveOccurred(), "failed to assign pooled identity containers")
			}

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "e2e-upgrade", tc.Location())
			Expect(err).NotTo(HaveOccurred(), "failed to create resource group for upgrade test")

			resourceGroupName := *resourceGroup.Name

			clusterParams := framework.NewDefaultClusterParams20240610()
			clusterParams.ClusterName = clusterName
			managedResourceGroupName := framework.SuffixName(resourceGroupName, "-managed", 64)
			clusterParams.ManagedResourceGroupName = managedResourceGroupName

			By("creating customer resources (infrastructure and managed identities)")
			clusterParams, err = tc.CreateClusterCustomerResources20240610(ctx,
				resourceGroup,
				clusterParams,
				map[string]interface{}{},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create customer resources for cluster %q", clusterName)

			By("creating the HCP cluster")
			err = tc.CreateHCPClusterFromParam20240610(ctx,
				GinkgoLogr,
				resourceGroupName,
				clusterParams,
				framework.ClusterCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create HCP cluster %q", clusterName)

			By("creating the node pool")
			nodePoolParams := framework.NewDefaultNodePoolParams20240610()
			nodePoolParams.ClusterName = clusterName
			nodePoolParams.NodePoolName = nodePoolName

			err = tc.CreateNodePoolFromParam20240610(ctx,
				GinkgoLogr,
				resourceGroupName,
				managedResourceGroupName,
				clusterName,
				nodePoolParams,
				framework.NodePoolCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create node pool %q for cluster %q", nodePoolName, clusterName)

			By("capturing baseline node pool hash before upgrade")
			hcpClusterClient := tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient()
			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster20240610(
				ctx,
				hcpClusterClient,
				resourceGroupName,
				clusterName,
				framework.GetAdminRESTConfigTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to get admin REST config for cluster %q", clusterName)

			kubeClient, err := kubernetes.NewForConfig(adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to create kubernetes client for cluster %q", clusterName)

			baselineHash, err := nodePoolHash(ctx, kubeClient, nodePoolName)
			Expect(err).NotTo(HaveOccurred(), "failed to compute baseline node pool hash for %q", nodePoolName)

			baselineHAProxyImage, err := haProxyImage(ctx, kubeClient)
			Expect(err).NotTo(HaveOccurred(), "failed to capture baseline haproxy image for cluster %q", clusterName)

			mcRESTConfig, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
				clientcmd.NewDefaultClientConfigLoadingRules(),
				&clientcmd.ConfigOverrides{},
			).ClientConfig()
			Expect(err).NotTo(HaveOccurred(), "failed to load management cluster kubeconfig — ensure KUBECONFIG is set")
			mcClient, err := dynamic.NewForConfig(mcRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to create MC dynamic client")

			mdRef, err := resolveMachineDeploymentRef(ctx, mcClient, nodePoolName)
			Expect(err).NotTo(HaveOccurred(), "failed to resolve MachineDeployment ref for nodepool %q", nodePoolName)

			baselineDataSecretName, err := machineDeploymentDataSecretName(ctx, mcClient, mdRef)
			Expect(err).NotTo(HaveOccurred(), "failed to get baseline MC DataSecretName for nodepool %q", nodePoolName)

			GinkgoLogr.Info("baseline captured",
				"nodepool", nodePoolName,
				"hash", baselineHash,
				"haproxyImage", baselineHAProxyImage,
				"dataSecretName", baselineDataSecretName,
			)

			By("running make entrypoint/Region to upgrade all regional services")
			// MakeRunner inherits all environment variables from the test process so that
			// OVERRIDE_CONFIG_FILE, DEPLOY_ENV, and any pipeline flags (SKIP_CONFIRM,
			// PERSIST) set by the openshift/release step script are passed through.
			// stdout/stderr are forwarded to GinkgoWriter so they appear in the test log.
			// Infrastructure (bicep) steps are idempotent when only image digests change,
			// so re-running the full Region entrypoint is safe and upgrades all services
			// on both the service and management clusters in one invocation.
			root, err := repoRoot()
			Expect(err).NotTo(HaveOccurred(), "failed to determine repo root for make invocation")
			makeRunner := &framework.MakeRunner{
				WorkDir:  root,
				ExtraEnv: []string{"SKIP_CONFIRM=true"},
				Logger:   GinkgoLogr,
			}
			err = makeRunner.RunWithOutput(ctx, "entrypoint/Region", GinkgoWriter, GinkgoWriter)
			Expect(err).NotTo(HaveOccurred(), "make entrypoint/Region failed")
			GinkgoLogr.Info("Region entrypoint deploy completed")

			By("confirming node pool hash remains stable after upgrade")
			GinkgoLogr.Info("starting stability observation",
				"nodepool", nodePoolName,
				"baselineHash", baselineHash,
				"window", rolloutObservationWindow,
			)
			observationStart := time.Now()
			Consistently(func(g Gomega) {
				elapsed := time.Since(observationStart).Round(time.Second)

				currentHash, hashErr := nodePoolHash(ctx, kubeClient, nodePoolName)
				g.Expect(hashErr).NotTo(HaveOccurred(), "failed to compute post-upgrade node pool hash for %q", nodePoolName)
				g.Expect(currentHash).To(Equal(baselineHash),
					"node pool %q hash changed after %s (cluster %q): was %s, now %s",
					nodePoolName, elapsed, clusterName, baselineHash, currentHash,
				)

				currentHAProxyImage, haproxyErr := haProxyImage(ctx, kubeClient)
				g.Expect(haproxyErr).NotTo(HaveOccurred(), "failed to retrieve haproxy image for cluster %q", clusterName)
				g.Expect(currentHAProxyImage).To(Equal(baselineHAProxyImage),
					"haproxy image changed after %s (cluster %q): registry-override substitution fired — was %s, now %s",
					elapsed, clusterName, baselineHAProxyImage, currentHAProxyImage,
				)

				currentDataSecretName, dsErr := machineDeploymentDataSecretName(ctx, mcClient, mdRef)
				g.Expect(dsErr).NotTo(HaveOccurred(), "failed to retrieve MC DataSecretName for nodepool %q", nodePoolName)
				g.Expect(currentDataSecretName).To(Equal(baselineDataSecretName),
					"MC DataSecretName changed after %s (nodepool %q, cluster %q): mcoRawConfig hash rotated — was %s, now %s",
					elapsed, nodePoolName, clusterName, baselineDataSecretName, currentDataSecretName,
				)
			}, rolloutObservationWindow, rolloutPollInterval).Should(Succeed(),
				"unexpected change detected within %s after upgrade (cluster %q, nodepool %q)",
				rolloutObservationWindow, clusterName, nodePoolName,
			)
		})
})
