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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"

	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

var _ = Describe("Customer", func() {
	It("should install firewall rules and remove them by deleting only the daemonset",
		labels.RequireNothing,
		labels.High,
		labels.Positive,
		labels.MIContainers(1),
		func(ctx context.Context) {
			input := os.Getenv("FIREWALL_TEST_INPUT")
			if input == "" {
				Skip("FIREWALL_TEST_INPUT must point to a private procedure input file")
			}
			data, err := os.ReadFile(input)
			Expect(err).NotTo(HaveOccurred(), "failed to read private procedure input")
			var procedure struct {
				Manifest           string `json:"manifest"`
				VerificationScript string `json:"verification_script"`
				Namespace          string `json:"namespace"`
				DaemonSet          string `json:"daemonset"`
				Family             string `json:"family"`
				Table              string `json:"table"`
				Rule               string `json:"rule"`
				ExpectedVersion    string `json:"expected_version"`
			}
			err = json.Unmarshal(data, &procedure)
			Expect(err).NotTo(HaveOccurred(), "failed to decode private procedure input")
			manifest, err := os.ReadFile(procedure.Manifest)
			Expect(err).NotTo(HaveOccurred(), "failed to read supplied manifest")
			_, err = os.Stat(procedure.VerificationScript)
			Expect(err).NotTo(HaveOccurred(), "failed to read supplied verification script")
			for key, value := range map[string]string{"namespace": procedure.Namespace, "daemonset": procedure.DaemonSet, "family": procedure.Family, "table": procedure.Table, "rule": procedure.Rule} {
				Expect(value).NotTo(BeEmpty(), "procedure input field %s is required", key)
			}
			_, err = exec.LookPath("oc")
			Expect(err).NotTo(HaveOccurred(), "oc must be available before provisioning")
			artifacts := os.Getenv("ARTIFACT_DIR")
			Expect(artifacts).NotTo(BeEmpty(), "ARTIFACT_DIR is required to retain test evidence locally")
			tc := framework.NewTestContext()
			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, framework.IdentityContainerAssignmentRetryInterval)
				Expect(err).NotTo(HaveOccurred(), "failed to assign pooled identity containers")
			}

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "firewall-test", tc.Location())
			Expect(err).NotTo(HaveOccurred(), "failed to create worker readiness test resource group")

			suffix := rand.String(6)
			clusterParams := framework.NewDefaultClusterParams20261001()
			clusterParams.ClusterName = "firewall-" + suffix
			clusterParams.ManagedResourceGroupName = framework.SuffixName(*resourceGroup.Name, "-managed", 64)
			clusterParams.DisableSwift = false
			// Start from production resource sizing.
			clusterParams.Tags = nil
			// Exact patch versions are encoded by the framework's supported version-pin tag.
			clusterClient := tc.Get20261001ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient()
			DeferCleanup(func(cleanupCtx context.Context) {
				By("deleting the cluster through the current ARM API before resource-group cleanup")
				deleteCtx, cancel := context.WithTimeout(cleanupCtx, framework.HCPClusterDeletionTimeout)
				defer cancel()
				poller, deleteErr := clusterClient.BeginDelete(deleteCtx, *resourceGroup.Name, clusterParams.ClusterName, nil)
				var responseError *azcore.ResponseError
				if !(errors.As(deleteErr, &responseError) && responseError.StatusCode == 404) {
					Expect(deleteErr).NotTo(HaveOccurred(), "failed to begin cluster deletion")
					_, deleteErr = poller.PollUntilDone(deleteCtx, nil)
					Expect(deleteErr).NotTo(HaveOccurred(), "failed to finish cluster deletion")
					_, getErr := clusterClient.Get(deleteCtx, *resourceGroup.Name, clusterParams.ClusterName, nil)
					Expect(errors.As(getErr, &responseError) && responseError.StatusCode == 404).To(BeTrue(), "cluster still exists after deletion: %v", getErr)
				}
				resources, factoryErr := tc.GetARMResourcesClientFactory(deleteCtx)
				Expect(factoryErr).NotTo(HaveOccurred(), "failed to create resource-group client")
				waitErr := wait.PollUntilContextTimeout(deleteCtx, 15*time.Second, framework.HCPClusterDeletionTimeout, true, func(ctx context.Context) (bool, error) {
					_, err := resources.NewResourceGroupsClient().Get(ctx, clusterParams.ManagedResourceGroupName, nil)
					if errors.As(err, &responseError) && responseError.StatusCode == 404 {
						return true, nil
					}
					return false, err
				})
				Expect(waitErr).NotTo(HaveOccurred(), "managed resource group did not disappear")
				groupClient := resources.NewResourceGroupsClient()
				groupPoller, groupErr := groupClient.BeginDelete(deleteCtx, *resourceGroup.Name, nil)
				Expect(groupErr).NotTo(HaveOccurred(), "failed to begin customer resource group deletion")
				_, groupErr = groupPoller.PollUntilDone(deleteCtx, nil)
				Expect(groupErr).NotTo(HaveOccurred(), "failed to finish customer resource group deletion")
				_, groupErr = groupClient.Get(deleteCtx, *resourceGroup.Name, nil)
				Expect(errors.As(groupErr, &responseError) && responseError.StatusCode == 404).To(BeTrue(), "customer resource group still exists: %v", groupErr)
			})

			By("creating customer resources without an NSG on the Swift integration subnet")
			clusterParams, err = tc.CreateClusterCustomerResources20261001(ctx,
				resourceGroup,
				clusterParams,
				map[string]interface{}{},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create cluster customer resources")

			By("creating the Swift-enabled HCP cluster")
			err = tc.CreateHCPClusterFromParam20261001(ctx, GinkgoLogr,
				*resourceGroup.Name, clusterParams, nil, framework.ClusterCreationTimeout)
			Expect(err).NotTo(HaveOccurred(), "failed to create HCP cluster %s", clusterParams.ClusterName)

			By("getting cluster admin credentials")
			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster20261001(ctx,
				tc.Get20261001ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
				*resourceGroup.Name, clusterParams.ClusterName, framework.GetAdminRESTConfigTimeout)
			Expect(err).NotTo(HaveOccurred(), "failed to get admin REST config for cluster %s", clusterParams.ClusterName)

			nodePoolParams := framework.NewDefaultNodePoolParams20261001()
			nodePoolParams.ClusterName = clusterParams.ClusterName
			nodePoolParams.NodePoolName = "workers-" + suffix
			nodePoolParams.Replicas = 2
			nodePoolParams.VMSize = "Standard_D4s_v6"
			nodePoolParams.AvailabilityZone = "1"
			nodePoolParams.Tags = nil

			By("creating one node pool with two Standard_D4s_v6 workers in zone 1")
			err = tc.CreateNodePoolFromParam20261001(ctx, GinkgoLogr,
				*resourceGroup.Name, clusterParams.ManagedResourceGroupName,
				clusterParams.ClusterName, nodePoolParams, framework.NodePoolCreationTimeout)
			Expect(err).NotTo(HaveOccurred(), "failed to create node pool %s", nodePoolParams.NodePoolName)

			By("verifying exactly two Ready nodes belonging to the new pool")
			err = verifiers.VerifyHCPCluster(ctx, adminRESTConfig,
				verifiers.VerifyNodeCount(clusterParams.ClusterName, 2),
				verifiers.VerifyNodesReady(),
				verifiers.VerifyAllNodesFromNodePool(nodePoolParams.NodePoolName),
			)
			Expect(err).NotTo(HaveOccurred(), "failed to verify two Ready nodes in node pool %s", nodePoolParams.NodePoolName)
			By("applying the supplied manifest without changing its bytes")
			runDir := filepath.Join(artifacts, "firewall-procedure-"+suffix)
			err = os.MkdirAll(runDir, 0700)
			Expect(err).NotTo(HaveOccurred(), "failed to create private artifact directory")
			manifestPath := filepath.Join(runDir, "manifest.yaml")
			err = os.WriteFile(manifestPath, manifest, 0600)
			Expect(err).NotTo(HaveOccurred(), "failed to retain supplied manifest")
			config, err := framework.GenerateKubeconfig(adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to serialize cluster credentials")
			configPath := filepath.Join(runDir, "kubeconfig")
			err = os.WriteFile(configPath, []byte(config), 0600)
			Expect(err).NotTo(HaveOccurred(), "failed to write private cluster credentials")
			defer os.Remove(configPath)
			commandNumber := 0
			runCommand := func(commandCtx context.Context, name, binary string, args ...string) ([]byte, error) {
				commandNumber++
				commandCtx, cancel := context.WithTimeout(commandCtx, 5*time.Minute)
				defer cancel()
				command := exec.CommandContext(commandCtx, binary, args...)
				command.Env = append(os.Environ(), "KUBECONFIG="+configPath)
				var stdout, stderr bytes.Buffer
				command.Stdout, command.Stderr = &stdout, &stderr
				commandErr := command.Run()
				stem := filepath.Join(runDir, fmt.Sprintf("%03d-%s", commandNumber, name))
				for suffix, data := range map[string][]byte{".stdout": stdout.Bytes(), ".stderr": stderr.Bytes()} {
					writeErr := os.WriteFile(stem+suffix, data, 0600)
					if writeErr != nil {
						return nil, fmt.Errorf("save command evidence: %w", writeErr)
					}
				}
				if commandErr != nil {
					return stdout.Bytes(), fmt.Errorf("%s failed: %w; inspect %s", name, commandErr, stem)
				}
				return stdout.Bytes(), nil
			}
			oc := func(commandCtx context.Context, name string, args ...string) ([]byte, error) {
				return runCommand(commandCtx, name, "oc", args...)
			}
			kubeClient, err := kubernetes.NewForConfig(adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to create Kubernetes client")
			if procedure.ExpectedVersion != "" {
				dynamicClient, versionErr := dynamic.NewForConfig(adminRESTConfig)
				Expect(versionErr).NotTo(HaveOccurred(), "failed to create cluster version client")
				version, versionErr := dynamicClient.Resource(schema.GroupVersionResource{Group: "config.openshift.io", Version: "v1", Resource: "clusterversions"}).Get(ctx, "version", metav1.GetOptions{})
				Expect(versionErr).NotTo(HaveOccurred(), "failed to read actual control plane version")
				actualVersion, _, versionErr := unstructured.NestedString(version.Object, "status", "desired", "version")
				Expect(versionErr).NotTo(HaveOccurred(), "failed to decode actual control plane version")
				Expect(actualVersion).To(Equal(procedure.ExpectedVersion), "unexpected control plane version")
			}
			workers, err := kubeClient.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
			Expect(err).NotTo(HaveOccurred(), "failed to list worker nodes")
			Expect(workers.Items).To(HaveLen(2), "expected exactly two worker nodes")
			originalNodes := map[string]types.UID{}
			for _, node := range workers.Items {
				originalNodes[node.Name] = node.UID
			}
			readTable := func(node string) ([]map[string]json.RawMessage, error) {
				output, readErr := oc(ctx, "node-rules", "debug", "node/"+node, "--", "chroot", "/host", "nft", "-j", "list", "ruleset")
				if readErr != nil {
					return nil, readErr
				}
				var ruleset struct {
					NFTables []map[string]json.RawMessage `json:"nftables"`
				}
				if readErr = json.Unmarshal(output, &ruleset); readErr != nil {
					return nil, fmt.Errorf("decode rules on %s: %w", node, readErr)
				}
				var entries []map[string]json.RawMessage
				for _, entry := range ruleset.NFTables {
					for kind, raw := range entry {
						if kind == "metainfo" {
							continue
						}
						var object struct {
							Family string `json:"family"`
							Table  string `json:"table"`
							Name   string `json:"name"`
						}
						if readErr = json.Unmarshal(raw, &object); readErr != nil {
							return nil, readErr
						}
						if object.Family == procedure.Family && (object.Table == procedure.Table || (kind == "table" && object.Name == procedure.Table)) {
							entries = append(entries, entry)
						}
					}
				}
				return entries, nil
			}
			By("confirming the firewall table is absent on every worker before installation")
			for node := range originalNodes {
				entries, readErr := readTable(node)
				Expect(readErr).NotTo(HaveOccurred(), "failed to inspect baseline firewall rules on %s", node)
				Expect(entries).To(BeEmpty(), "firewall table already exists on %s", node)
			}
			_, err = kubeClient.CoreV1().Namespaces().Get(ctx, procedure.Namespace, metav1.GetOptions{})
			Expect(apierrors.IsNotFound(err)).To(BeTrue(), "procedure namespace must not already exist: %v", err)
			DeferCleanup(func(cleanupCtx context.Context) {
				cleanupErr := kubeClient.CoreV1().Namespaces().Delete(cleanupCtx, procedure.Namespace, metav1.DeleteOptions{})
				if !apierrors.IsNotFound(cleanupErr) {
					Expect(cleanupErr).NotTo(HaveOccurred(), "failed to delete procedure namespace")
				}
			})
			_, err = oc(ctx, "apply", "apply", "--validate=strict", "-f", manifestPath)
			Expect(err).NotTo(HaveOccurred(), "the exact supplied manifest was rejected; rule installation and removal are unverified")
			err = verifiers.VerifyDaemonSetReady(procedure.Namespace, procedure.DaemonSet, 5*time.Minute).Verify(ctx, adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "firewall DaemonSet did not become Ready")
			_, err = oc(ctx, "overview", "-n", procedure.Namespace, "get", "daemonset,pods", "-o", "wide")
			Expect(err).NotTo(HaveOccurred(), "failed to inspect DaemonSet and pods")
			pods, err := kubeClient.CoreV1().Pods(procedure.Namespace).List(ctx, metav1.ListOptions{})
			Expect(err).NotTo(HaveOccurred(), "failed to list firewall pods")
			Expect(pods.Items).To(HaveLen(2), "expected one firewall pod per worker")
			covered := map[string]bool{}
			for _, pod := range pods.Items {
				covered[pod.Spec.NodeName] = true
			}
			By("verifying the installed firewall rule on every worker")
			for node := range originalNodes {
				Expect(covered[node]).To(BeTrue(), "firewall pod is missing from %s", node)
				entries, readErr := readTable(node)
				Expect(readErr).NotTo(HaveOccurred(), "failed to inspect installed firewall rules on %s", node)
				rules, chains := 0, 0
				for _, entry := range entries {
					if _, ok := entry["rule"]; ok {
						rules++
					}
					if raw, ok := entry["chain"]; ok {
						chains++
						var chain struct {
							Hook   string `json:"hook"`
							Prio   int    `json:"prio"`
							Policy string `json:"policy"`
						}
						decodeErr := json.Unmarshal(raw, &chain)
						Expect(decodeErr).NotTo(HaveOccurred(), "failed to decode chain on %s", node)
						Expect(chain.Hook).To(Equal("input"), "unexpected chain hook on %s", node)
						Expect(chain.Prio).To(Equal(-310), "unexpected chain priority on %s", node)
						Expect(chain.Policy).To(Equal("accept"), "unexpected chain policy on %s", node)
					}
				}
				Expect(rules).To(Equal(1), "unexpected firewall rule count on %s", node)
				Expect(chains).To(Equal(1), "unexpected firewall chain count on %s", node)
				output, readErr := oc(ctx, "installed-chain", "debug", "node/"+node, "--", "chroot", "/host", "nft", "list", "chain", procedure.Family, procedure.Table, "input")
				Expect(readErr).NotTo(HaveOccurred(), "failed to read installed chain on %s", node)
				Expect(string(output)).To(ContainSubstring(procedure.Rule), "expected rule is missing from %s", node)
				Expect(string(output)).To(MatchRegexp(`counter packets \d+ bytes \d+ drop`), "expected counter and drop action are missing from %s", node)
			}
			By("running the supplied verification commands without modification")
			output, verificationErr := runCommand(ctx, "documented-verification", "bash", procedure.VerificationScript)
			var procedureErrors []error
			if verificationErr != nil {
				procedureErrors = append(procedureErrors, verificationErr)
			}
			counts := regexp.MustCompile(`:\s*(\d+)/(\d+) rules installed`).FindAllSubmatch(output, -1)
			for _, count := range counts {
				if string(count[1]) != string(count[2]) {
					procedureErrors = append(procedureErrors, fmt.Errorf("documented verification reported incomplete rule counts"))
					break
				}
			}
			supportingUIDs := func() map[types.UID]bool {
				uids := map[types.UID]bool{}
				configMaps, listErr := kubeClient.CoreV1().ConfigMaps(procedure.Namespace).List(ctx, metav1.ListOptions{})
				Expect(listErr).NotTo(HaveOccurred(), "failed to list retained ConfigMaps")
				for _, item := range configMaps.Items {
					uids[item.UID] = true
				}
				accounts, listErr := kubeClient.CoreV1().ServiceAccounts(procedure.Namespace).List(ctx, metav1.ListOptions{})
				Expect(listErr).NotTo(HaveOccurred(), "failed to list retained ServiceAccounts")
				for _, item := range accounts.Items {
					uids[item.UID] = true
				}
				bindings, listErr := kubeClient.RbacV1().RoleBindings(procedure.Namespace).List(ctx, metav1.ListOptions{})
				Expect(listErr).NotTo(HaveOccurred(), "failed to list retained RoleBindings")
				for _, item := range bindings.Items {
					uids[item.UID] = true
				}
				return uids
			}
			retained := supportingUIDs()
			Expect(retained).NotTo(BeEmpty(), "supporting resources must exist before DaemonSet deletion")
			By("deleting only the DaemonSet and waiting for its pods to disappear")
			_, err = oc(ctx, "delete-daemonset", "-n", procedure.Namespace, "delete", "daemonset", procedure.DaemonSet, "--wait=true", "--timeout=2m")
			Expect(err).NotTo(HaveOccurred(), "failed to delete only the DaemonSet")
			err = wait.PollUntilContextTimeout(ctx, 5*time.Second, 3*time.Minute, true, func(ctx context.Context) (bool, error) {
				remaining, listErr := kubeClient.CoreV1().Pods(procedure.Namespace).List(ctx, metav1.ListOptions{})
				if listErr != nil {
					return false, listErr
				}
				return len(remaining.Items) == 0, nil
			})
			Expect(err).NotTo(HaveOccurred(), "firewall pods did not terminate")
			By("checking every original worker for remaining firewall entries before removing supporting resources")
			workers, err = kubeClient.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
			Expect(err).NotTo(HaveOccurred(), "failed to check worker identities after deletion")
			Expect(workers.Items).To(HaveLen(len(originalNodes)), "worker count changed during the procedure")
			for _, node := range workers.Items {
				Expect(node.UID).To(Equal(originalNodes[node.Name]), "worker %s was replaced during the procedure", node.Name)
			}
			after := supportingUIDs()
			for uid := range retained {
				Expect(after[uid]).To(BeTrue(), "supporting resource %s was removed before checking firewall removal", uid)
			}
			for node := range originalNodes {
				entries, readErr := readTable(node)
				Expect(readErr).NotTo(HaveOccurred(), "failed to inspect firewall removal on %s", node)
				if len(entries) != 0 {
					procedureErrors = append(procedureErrors, fmt.Errorf("DaemonSet-only deletion left firewall entries on worker %s", node))
				}
			}
			Expect(errors.Join(procedureErrors...)).NotTo(HaveOccurred(), "firewall installation, verification, or DaemonSet-only removal procedure failed; inspect private artifacts in %s", runDir)

		},
	)
})
