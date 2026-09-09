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

package framework

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/blang/semver/v4"
	"helm.sh/helm/v4/pkg/action"
	"helm.sh/helm/v4/pkg/chart/v2/loader"
	"helm.sh/helm/v4/pkg/cli"
	"helm.sh/helm/v4/pkg/kube"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/utils/ptr"

	configv1client "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"
)

// Install Cilium helm chart using the helm Go SDK. Cilium configuration is
// passed via values argument.
func InstallCiliumChart(ctx context.Context, chartVersion string, values map[string]any, kubeconfigContent, ciliumNamespace string) error {
	const (
		releaseName   = "cilium"
		ciliumRepoURL = "https://helm.cilium.io/"
		chartName     = "cilium"
	)

	// generating kubeconfig file for helm client
	kubeconfigFile, err := os.CreateTemp("", "kubeconfig-cilium-*.yaml")
	if err != nil {
		return fmt.Errorf("failed to create temp kubeconfig file: %w", err)
	}
	defer os.Remove(kubeconfigFile.Name())

	_, err = kubeconfigFile.WriteString(kubeconfigContent)
	if err != nil {
		return fmt.Errorf("failed to write kubeconfig content: %w", err)
	}

	if err := kubeconfigFile.Close(); err != nil {
		return fmt.Errorf("failed to close kubeconfig file: %w", err)
	}

	// Initialize helm action configuration with the kubeconfig
	actionCfg := &action.Configuration{}
	cliOpts := &genericclioptions.ConfigFlags{
		KubeConfig: ptr.To(kubeconfigFile.Name()),
		Namespace:  ptr.To(ciliumNamespace),
	}
	if err := actionCfg.Init(cliOpts, ciliumNamespace, ""); err != nil {
		return fmt.Errorf("failed to init helm action config: %w", err)
	}

	// Locate and download the chart from the Cilium repo
	installClient := action.NewInstall(actionCfg)
	installClient.ReleaseName = releaseName
	installClient.Namespace = ciliumNamespace
	installClient.RepoURL = ciliumRepoURL
	installClient.WaitStrategy = kube.HookOnlyStrategy
	installClient.Version = chartVersion

	settings := cli.New()
	chartPath, err := installClient.LocateChart(chartName, settings)
	if err != nil {
		return fmt.Errorf("failed to locate cilium chart: %w", err)
	}

	chart, err := loader.Load(chartPath)
	if err != nil {
		return fmt.Errorf("failed to load cilium chart: %w", err)
	}

	_, err = installClient.RunWithContext(ctx, chart, values)
	if err != nil {
		return fmt.Errorf("failed to install cilium chart: %w", err)
	}

	return nil
}

var ciliumDNSHostAPIServerPolicyMinVersion = semver.MustParse("4.22.0")

func EnsureDNSAllowHostAPIServerCiliumNetworkPolicy(ctx context.Context, adminRESTConfig *rest.Config, nodePoolCreationTimeout time.Duration) error {
	const (
		dnsNamespace    = "openshift-dns"
		policyName      = "dns-allow-host-apiserver"
		apiServerPort   = "6443"
		dnsDaemonSetKey = "dns.operator.openshift.io/daemonset-dns"
	)

	configClient, err := configv1client.NewForConfig(adminRESTConfig)
	if err != nil {
		return fmt.Errorf("failed to create config client to resolve cluster version: %w", err)
	}

	clusterVersion, err := configClient.ClusterVersions().Get(ctx, "version", metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get clusterversion to resolve cluster version: %w", err)
	}

	currentVersion, err := semver.ParseTolerant(clusterVersion.Status.Desired.Version)
	if err != nil {
		return fmt.Errorf("failed to parse cluster version %q: %w", clusterVersion.Status.Desired.Version, err)
	}

	if currentVersion.LT(ciliumDNSHostAPIServerPolicyMinVersion) {
		// Below the NE-1476 boundary: the DNS operator does not create any
		// NetworkPolicy in openshift-dns, so there is nothing to work around.
		return nil
	}

	dynamicClient, err := dynamic.NewForConfig(adminRESTConfig)
	if err != nil {
		return fmt.Errorf("failed to create dynamic client for CiliumNetworkPolicy: %w", err)
	}

	cnpGVR := schema.GroupVersionResource{Group: "cilium.io", Version: "v2", Resource: "ciliumnetworkpolicies"}
	desired := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "cilium.io/v2",
			"kind":       "CiliumNetworkPolicy",
			"metadata": map[string]any{
				"name":      policyName,
				"namespace": dnsNamespace,
			},
			"spec": map[string]any{
				"endpointSelector": map[string]any{
					"matchLabels": map[string]any{
						dnsDaemonSetKey: "default",
					},
				},
				"egress": []any{
					map[string]any{
						"toEntities": []any{"host", "remote-node"},
						"toPorts": []any{
							map[string]any{
								"ports": []any{
									map[string]any{
										"port":     apiServerPort,
										"protocol": "TCP",
									},
								},
							},
						},
					},
				},
			},
		},
	}

	// The CiliumNetworkPolicy CRD is registered by cilium-operator, which can
	// only schedule once the node pool's worker nodes exist. Retry only the
	// CRD/GVR-not-found race; AlreadyExists means a previous attempt succeeded.
	var lastErr error
	err = wait.PollUntilContextTimeout(ctx, 10*time.Second, nodePoolCreationTimeout, true, func(ctx context.Context) (bool, error) {
		_, createErr := dynamicClient.Resource(cnpGVR).Namespace(dnsNamespace).Create(ctx, desired, metav1.CreateOptions{})
		if createErr == nil || apierrors.IsAlreadyExists(createErr) {
			return true, nil
		}
		if isRetryableCiliumNetworkPolicyCreateError(createErr) {
			lastErr = createErr
			return false, nil
		}
		return false, fmt.Errorf("failed to create CiliumNetworkPolicy %s/%s: %w", dnsNamespace, policyName, createErr)
	})
	if err != nil {
		if wait.Interrupted(err) && lastErr != nil {
			return fmt.Errorf("timed out creating CiliumNetworkPolicy %s/%s: %w", dnsNamespace, policyName, lastErr)
		}
		return err
	}
	return nil
}

// isRetryableCiliumNetworkPolicyCreateError reports whether create failed
// because the CiliumNetworkPolicy CRD/GVR is not served yet. Permanent errors
// such as Forbidden or Invalid must not be retried.
func isRetryableCiliumNetworkPolicyCreateError(err error) bool {
	return apierrors.IsNotFound(err) || meta.IsNoMatchError(err)
}
