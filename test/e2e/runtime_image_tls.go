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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"

	configv1client "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"

	"github.com/Azure/ARO-HCP/test/util/config"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

const (
	runtimeImageTLSClusterName = "runtime-image-tls"
	runtimeImageTLSNodePool    = "np-1"

	runtimeImageTLSNamespace = "runtime-image-tls-test"
	runtimeImageTLSPodName   = "kubectl-tls-check"

	// runtimeImageTLSAdminConfigTimeout bounds the admin credential request
	// LRO. The observed baseline is minutes; this leaves headroom for a slow
	// control plane without hiding a real failure.
	runtimeImageTLSAdminConfigTimeout = 10 * time.Minute
	// runtimeImageTLSNodeReadyTimeout bounds the wait for the single-node node
	// pool to become ready and schedulable so the check pod can be placed.
	runtimeImageTLSNodeReadyTimeout = 15 * time.Minute
	// runtimeImageTLSPodCompletionTimeout bounds the check pod itself: the
	// image pull is the only slow part, the in-pod check returns immediately.
	runtimeImageTLSPodCompletionTimeout = 5 * time.Minute
)

// kubectlTLS13PanicSignatures match the AROSLSRE-2021/AROSLSRE-2481
// regression: kubectl built against a Go/OpenSSL FIPS combination that
// panics during any TLS 1.3 handshake, before any HTTP traffic. See the
// AROSLSRE-2476 root cause and golang-fips/openssl#417.
var kubectlTLS13PanicSignatures = []string{
	"panic:",
	"OSSL_PARAM_BLD_push_octet_string",
}

// runtimeImageTLSCheckScript runs inside a pod of the image under test. It
// starts a TLS-1.3-only server on the pod's loopback interface (openssl
// s_server -tls1_3 cannot negotiate any other version) and then makes the
// image's own kubectl talk to it. Because the server only speaks TLS 1.3, a
// kubectl that completes the round trip has necessarily negotiated TLS 1.3 —
// this is what the check asserts; a TLS-1.2-only client fails the handshake.
// --insecure-skip-tls-verify only skips certificate verification, not the
// handshake itself, which is where the AROSLSRE-2481 panic lives. The dummy
// token exists because kubectl refuses to run without credentials; the server
// ignores it.
const runtimeImageTLSCheckScript = `cert=/tmp/tls-check.crt
key=/tmp/tls-check.key
openssl req -x509 -newkey rsa:2048 -nodes \
  -keyout "$key" -out "$cert" \
  -days 1 -subj "/CN=127.0.0.1" >/dev/null 2>&1
openssl s_server -accept 127.0.0.1:8443 -tls1_3 \
  -cert "$cert" -key "$key" -www -quiet \
  >/dev/null 2>&1 &
S_SERVER_PID=$!
ready=""
for attempt in 1 2 3 4 5; do
  if ! kill -0 "$S_SERVER_PID" 2>/dev/null; then
    echo "TLS-CHECK-ERROR: the local TLS 1.3-only server exited before serving"
    exit 3
  fi
  probe="$(timeout 10 openssl s_client -connect 127.0.0.1:8443 -tls1_3 -brief </dev/null 2>&1 || true)"
  if printf '%s\n' "$probe" | grep -q 'Protocol version: *TLSv1.3'; then
    ready=yes
    break
  fi
  sleep 2
done
if [ "$ready" != yes ]; then
  echo "TLS-CHECK-ERROR: the local TLS 1.3-only server did not come up"
  exit 3
fi
echo "TLS-CHECK: local TLS 1.3-only server is up on 127.0.0.1:8443"
kubectl get --raw / --server=https://127.0.0.1:8443 --insecure-skip-tls-verify --token=tls-check
status=$?
echo "TLS-CHECK: kubectl exited with status $status"
exit $status
`

// The aks/command/runtime image is executed in the deployment path (the
// unannotate-default-sc, azure-cns-cpu-limits and Velero install hook Jobs all
// run its kubectl), and it has shipped a kubectl that panics on TLS 1.3
// handshakes twice (AROSLSRE-2021, AROSLSRE-2481). curl-style checks cannot
// catch that: the panic is in kubectl's own Go/OpenSSL binding, so the check
// has to make kubectl itself perform a TLS 1.3 handshake — against a
// TLS-1.3-only endpoint, so success proves the version and not just any
// handshake. The spec reads the image reference from the service config, so it
// validates whatever digest is actually shipped in each environment, and will
// follow the first-party toolbox image from AROSLSRE-2528 when it replaces
// this one in the hook Jobs.
var _ = Describe("ARO-HCP", func() {
	It("should complete a TLS 1.3 handshake using the runtime image's kubectl",
		labels.RequireNothing,
		labels.High,
		labels.Positive,
		labels.RequiresConfig,
		labels.AroRpApiCompatible,
		labels.CreateCluster,
		labels.MIContainers(1),
		func(ctx context.Context) {
			By("reading the runtime image reference from the service config")
			serviceConfig, err := config.GetServiceConfig()
			Expect(err).NotTo(HaveOccurred(), "failed to load service config")
			runtimeImageRegistry, err := config.GetStringByPath(serviceConfig, "aksCommandRuntime.image.registry")
			Expect(err).NotTo(HaveOccurred(), "failed to read aksCommandRuntime.image.registry from the service config")
			runtimeImageRepository, err := config.GetStringByPath(serviceConfig, "aksCommandRuntime.image.repository")
			Expect(err).NotTo(HaveOccurred(), "failed to read aksCommandRuntime.image.repository from the service config")
			runtimeImageDigest, err := config.GetStringByPath(serviceConfig, "aksCommandRuntime.image.digest")
			Expect(err).NotTo(HaveOccurred(), "failed to read aksCommandRuntime.image.digest from the service config")
			runtimeImage := fmt.Sprintf("%s/%s@%s", runtimeImageRegistry, runtimeImageRepository, runtimeImageDigest)
			GinkgoLogr.Info("runtime image under test", "image", runtimeImage)

			By("creating cluster parameters")
			clusterParams := framework.NewDefaultClusterParams20261001()
			clusterParams.ClusterName = runtimeImageTLSClusterName

			tc := framework.NewTestContext()

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, framework.IdentityContainerAssignmentRetryInterval)
				Expect(err).NotTo(HaveOccurred(), "failed to assign pooled identity containers")
			}

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "runtime-image-tls", tc.Location())
			Expect(err).NotTo(HaveOccurred(), "failed to create resource group")
			clusterParams.ManagedResourceGroupName = framework.SuffixName(*resourceGroup.Name, "-managed", 64)

			By("creating customer resources (infrastructure and managed identities)")
			clusterParams, err = tc.CreateClusterCustomerResources20261001(ctx,
				resourceGroup, clusterParams, map[string]any{},
				TestArtifactsFS, framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create cluster customer resources")

			By("creating the cluster")
			clusterResource, err := framework.BuildHCPClusterFromParams20261001(clusterParams, tc.Location(), nil)
			Expect(err).NotTo(HaveOccurred(), "failed to build HCP cluster from params")
			hcpClient := tc.Get20261001ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient()
			_, err = framework.CreateHCPClusterAndWait20261001(ctx, GinkgoLogr, hcpClient,
				*resourceGroup.Name, runtimeImageTLSClusterName, clusterResource, framework.ClusterCreationTimeout)
			Expect(err).NotTo(HaveOccurred(), "failed to create HCP cluster")

			By("getting admin credentials")
			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster20261001(
				ctx, hcpClient, *resourceGroup.Name, runtimeImageTLSClusterName,
				runtimeImageTLSAdminConfigTimeout)
			Expect(err).NotTo(HaveOccurred(), "failed to get admin REST config")

			By("verifying cluster health")
			err = verifiers.VerifyHCPCluster(ctx, adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "cluster health check failed")

			// The check pod needs a worker to schedule on. Node pools need a
			// concrete Major.Minor.Patch (unlike the control plane's bare
			// major.minor), so the desired version is taken from
			// ClusterVersion.status.desired.
			By("deriving the node pool version from the installed control plane")
			configClient, err := configv1client.NewForConfig(adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to create OpenShift config client")
			var nodePoolVersion string
			Eventually(func() error {
				clusterVersion, err := configClient.ClusterVersions().Get(ctx, "version", metav1.GetOptions{})
				if err != nil {
					return fmt.Errorf("failed to get ClusterVersion: %w", err)
				}
				if clusterVersion.Status.Desired.Version == "" {
					return fmt.Errorf("ClusterVersion desired version not yet available")
				}
				nodePoolVersion = clusterVersion.Status.Desired.Version
				return nil
			}).WithContext(ctx).WithTimeout(runtimeImageTLSAdminConfigTimeout).WithPolling(5*time.Second).Should(Succeed(),
				"failed to retrieve ClusterVersion desired version")

			By("creating a one-node node pool for the check pod")
			nodePoolParams := framework.NewDefaultNodePoolParams20261001()
			nodePoolParams.ChannelGroup = clusterParams.ChannelGroup
			nodePoolParams.OpenshiftVersionId = nodePoolVersion
			nodePoolParams.ClusterName = runtimeImageTLSClusterName
			nodePoolParams.NodePoolName = runtimeImageTLSNodePool
			nodePoolParams.Replicas = int32(1)
			err = tc.CreateNodePoolFromParam20261001(ctx, GinkgoLogr,
				*resourceGroup.Name, clusterParams.ManagedResourceGroupName,
				runtimeImageTLSClusterName, nodePoolParams, framework.NodePoolCreationTimeout)
			Expect(err).NotTo(HaveOccurred(), "failed to create node pool")

			Eventually(func() error {
				return verifiers.VerifyNodePoolReadyAndSchedulableNodeCount(runtimeImageTLSNodePool, 1).Verify(ctx, adminRESTConfig)
			}).WithContext(ctx).WithTimeout(runtimeImageTLSNodeReadyTimeout).WithPolling(30*time.Second).Should(Succeed(),
				"node pool %s never reached 1 ready (and schedulable) node", runtimeImageTLSNodePool)

			kubeClient, err := kubernetes.NewForConfig(adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to create kubernetes client")

			By("creating a namespace for the kubectl TLS check")
			DeferCleanup(func(ctx context.Context) {
				err := kubeClient.CoreV1().Namespaces().Delete(ctx, runtimeImageTLSNamespace, metav1.DeleteOptions{})
				if err != nil {
					GinkgoLogr.Info("failed to delete namespace during cleanup", "namespace", runtimeImageTLSNamespace, "error", err)
				}
			})
			Eventually(func() error {
				_, err := kubeClient.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
					ObjectMeta: metav1.ObjectMeta{Name: runtimeImageTLSNamespace},
				}, metav1.CreateOptions{})
				if err != nil && apierrors.IsAlreadyExists(err) {
					_, err = kubeClient.CoreV1().Namespaces().Get(ctx, runtimeImageTLSNamespace, metav1.GetOptions{})
				}
				return err
			}).WithContext(ctx).WithTimeout(30*time.Second).WithPolling(1*time.Second).Should(Succeed(),
				"failed to create namespace %s", runtimeImageTLSNamespace)

			By("creating a service account for the kubectl TLS check pod")
			// A dedicated service account plus the UID-range wait below avoid
			// racing the controllers that provision a new namespace (default
			// service account, SCC UID range): without them, pod admission
			// can fail before kubectl ever runs and reject a healthy image.
			// See the equivalent wait in oidc_issuer_workload_identity.go.
			var checkSA *corev1.ServiceAccount
			Eventually(func() error {
				var err error
				checkSA, err = kubeClient.CoreV1().ServiceAccounts(runtimeImageTLSNamespace).Create(ctx, &corev1.ServiceAccount{
					ObjectMeta: metav1.ObjectMeta{Name: "kubectl-tls-check"},
				}, metav1.CreateOptions{})
				if err != nil && apierrors.IsAlreadyExists(err) {
					checkSA, err = kubeClient.CoreV1().ServiceAccounts(runtimeImageTLSNamespace).Get(ctx, "kubectl-tls-check", metav1.GetOptions{})
				}
				return err
			}).WithContext(ctx).WithTimeout(30*time.Second).WithPolling(1*time.Second).Should(Succeed(),
				"failed to create service account in %s", runtimeImageTLSNamespace)

			By("waiting for the namespace SCC UID range annotation")
			Eventually(func() bool {
				updatedNS, err := kubeClient.CoreV1().Namespaces().Get(ctx, runtimeImageTLSNamespace, metav1.GetOptions{})
				if err != nil {
					return false
				}
				_, ok := updatedNS.Annotations["openshift.io/sa.scc.uid-range"]
				return ok
			}).WithContext(ctx).WithTimeout(5*time.Minute).WithPolling(5*time.Second).Should(BeTrue(),
				"namespace %s was never annotated with openshift.io/sa.scc.uid-range", runtimeImageTLSNamespace)

			By("deploying a pod that runs the image's kubectl against a local TLS 1.3-only server")
			_, err = kubeClient.CoreV1().Pods(runtimeImageTLSNamespace).Create(ctx, &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      runtimeImageTLSPodName,
					Namespace: runtimeImageTLSNamespace,
				},
				Spec: corev1.PodSpec{
					ServiceAccountName:           checkSA.Name,
					AutomountServiceAccountToken: to.Ptr(false),
					RestartPolicy:                corev1.RestartPolicyNever,
					Containers: []corev1.Container{
						{
							Name:            "kubectl-tls-check",
							Image:           runtimeImage,
							Command:         []string{"/bin/bash", "-c", runtimeImageTLSCheckScript},
							ImagePullPolicy: corev1.PullIfNotPresent,
							SecurityContext: &corev1.SecurityContext{
								AllowPrivilegeEscalation: to.Ptr(false),
								RunAsNonRoot:             to.Ptr(true),
								SeccompProfile: &corev1.SeccompProfile{
									Type: corev1.SeccompProfileTypeRuntimeDefault,
								},
								Capabilities: &corev1.Capabilities{
									Drop: []corev1.Capability{"ALL"},
								},
							},
						},
					},
				},
			}, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred(), "failed to create the kubectl TLS check pod")

			By("waiting for the kubectl TLS check pod to finish")
			// The wait is a manual deadline loop rather than Eventually so
			// that a stall (image pull, scheduling) captures pod and event
			// diagnostics before the assertion fails, instead of timing out
			// with nothing but a phase name.
			var podPhase corev1.PodPhase
			waitErr := func() error {
				deadline := time.Now().Add(runtimeImageTLSPodCompletionTimeout)
				for {
					pod, err := kubeClient.CoreV1().Pods(runtimeImageTLSNamespace).Get(ctx, runtimeImageTLSPodName, metav1.GetOptions{})
					if err != nil {
						return fmt.Errorf("failed to get the kubectl TLS check pod: %w", err)
					}
					switch pod.Status.Phase {
					case corev1.PodSucceeded:
						podPhase = corev1.PodSucceeded
						return nil
					case corev1.PodFailed:
						podPhase = corev1.PodFailed
						return nil
					}
					if time.Now().After(deadline) {
						return fmt.Errorf("the kubectl TLS check pod is stalled in phase %q (image pull or scheduling)", pod.Status.Phase)
					}
					select {
					case <-ctx.Done():
						return ctx.Err()
					case <-time.After(5 * time.Second):
					}
				}
			}()
			if waitErr != nil {
				verifiers.CapturePodFailureDiagnostics(ctx, waitErr, kubeClient,
					runtimeImageTLSNamespace, runtimeImageTLSPodName, tc.LogDirPath)
			}
			Expect(waitErr).NotTo(HaveOccurred(), "the kubectl TLS check pod never finished")

			if podPhase == corev1.PodFailed {
				verifiers.CapturePodFailureDiagnostics(ctx,
					fmt.Errorf("kubectl TLS check pod finished in phase Failed"), kubeClient,
					runtimeImageTLSNamespace, runtimeImageTLSPodName, tc.LogDirPath)
			}

			By("fetching the kubectl TLS check pod logs")
			logs, logErr := kubeClient.CoreV1().Pods(runtimeImageTLSNamespace).
				GetLogs(runtimeImageTLSPodName, &corev1.PodLogOptions{}).Do(ctx).Raw()
			if logErr != nil {
				verifiers.CapturePodFailureDiagnostics(ctx, logErr, kubeClient,
					runtimeImageTLSNamespace, runtimeImageTLSPodName, tc.LogDirPath)
			}
			Expect(logErr).NotTo(HaveOccurred(), "failed to fetch the kubectl TLS check pod logs")
			GinkgoLogr.Info("kubectl TLS check pod output", "phase", podPhase, "logs", string(logs))

			// The panic happens during the handshake, before any HTTP
			// traffic, so these signatures are the direct regression signal.
			for _, signature := range kubectlTLS13PanicSignatures {
				Expect(string(logs)).NotTo(ContainSubstring(signature),
					"kubectl in the runtime image %s panicked during the TLS 1.3 handshake (signature %q): this is the AROSLSRE-2021/AROSLSRE-2481 regression class (root cause in AROSLSRE-2476, golang-fips/openssl#417); the image must not ship until its kubectl passes this check",
					runtimeImage, signature)
			}

			// Success proves TLS 1.3 by construction: the in-pod server only
			// negotiates TLS 1.3, so a kubectl that completed the round trip
			// cannot have used TLS 1.2.
			Expect(podPhase).To(Equal(corev1.PodSucceeded),
				"kubectl from the runtime image %s did not complete a round trip against the TLS 1.3-only server (pod logs above; a TLS panic is the AROSLSRE-2021/AROSLSRE-2481 regression class, and any other handshake failure means the image's kubectl cannot negotiate TLS 1.3)",
				runtimeImage)
		})
})
