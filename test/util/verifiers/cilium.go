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

package verifiers

import (
	"context"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/onsi/ginkgo/v2"
	"go.yaml.in/yaml/v2"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"
)

type verifyCiliumSetup struct {
	ciliumVersion string
	podCIDR       string
	hostPrefix    int32
}

func (v verifyCiliumSetup) Name() string {
	return "VerifyCiliumSetup " + v.ciliumVersion
}

func (v verifyCiliumSetup) Verify(ctx context.Context, adminRESTConfig *rest.Config) error {
	klog.SetOutput(ginkgo.GinkgoWriter)

	kubeClient, err := kubernetes.NewForConfig(adminRESTConfig)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	namespace, err := kubeClient.CoreV1().Namespaces().Create(
		ctx,
		&corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: "cilium",
				Labels: map[string]string{
					"security.openshift.io/scc.podSecurityLabelSync": "false",
					"pod-security.kubernetes.io/enforce":             "privileged",
					"pod-security.kubernetes.io/audit":               "privileged",
					"pod-security.kubernetes.io/warn":                "privileged",
				},
			},
		},
		metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create cilium namespace: %w", err)
	}

	dynamicClient, err := dynamic.NewForConfig(adminRESTConfig)
	if err != nil {
		return fmt.Errorf("failed to create dynamic client: %w", err)
	}

	// Deploy all YAML files from the cilium directory
	ciliumDir := fmt.Sprintf("artifacts/cilium-%s", v.ciliumVersion)
	entries, err := fs.ReadDir(staticFiles, ciliumDir)
	if err != nil {
		return fmt.Errorf("failed to read cilium artifacts directory: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "cluster-network-") {
			continue
		}

		filePath := filepath.Join(ciliumDir, entry.Name())
		deploymentYAML, err := staticFiles.ReadFile(filePath)
		if err != nil {
			return fmt.Errorf("failed to read file %s: %w", filePath, err)
		}

		resource, err := createArbitraryResource(ctx, dynamicClient, namespace.Name, deploymentYAML)
		if err != nil {
			return fmt.Errorf("failed to create cilium resource from %s: %w", filePath, err)
		}

		klog.InfoS("created resource",
			"file", entry.Name(),
			"kind", resource.GetKind(),
			"name", resource.GetName(),
			"namespace", resource.GetNamespace(),
		)
	}

	// Configure Cilium via CiliumConfig resource
	configFilePath := filepath.Join(ciliumDir, "ciliumconfig.yaml")
	configYAML := must(staticFiles.ReadFile(configFilePath))

	var configMap map[string]any
	if err := yaml.Unmarshal(configYAML, &configMap); err != nil {
		return fmt.Errorf("failed to unmarshal config YAML: %w", err)
	}

	// Update CiliumConfig spec with cluster-specific network settings
	if spec, ok := configMap["spec"].(map[string]any); ok {
		// Set nativeRoutingCIDR
		spec["nativeRoutingCIDR"] = v.podCIDR
		// Set IPAM operator settings
		if ipam, ok := spec["ipam"].(map[string]any); ok {
			if operator, ok := ipam["operator"].(map[string]any); ok {
				operator["clusterPoolIPv4PodCIDRList"] = []string{v.podCIDR}
				operator["clusterPoolIPv4MaskSize"] = v.hostPrefix
			}
		}
	}

	configYAML, err = yaml.Marshal(configMap)
	if err != nil {
		return fmt.Errorf("failed to marshal modified config: %w", err)
	}

	resource, err := createArbitraryResource(ctx, dynamicClient, namespace.Name, configYAML)
	if err != nil {
		return fmt.Errorf("failed to create deployment: %w", err)
	}

	klog.InfoS("created resource",
		"kind", resource.GetKind(),
		"name", resource.GetName(),
		"namespace", namespace.Name,
	)

	return nil
}

// Installs Cilium on a hosted cluster (assuming the cluster has CNI disabled
// and doesn't have node pools) verifying that the installation proceeds as
// expected without obvious errors. Full verification requires one to deploy
// node pool and run actual workload though.
// Based on cucushift-hypershift-extended-cilium step from Openshift CI Step
// Registry, see
// https://steps.ci.openshift.org/reference/cucushift-hypershift-extended-cilium
func VerifyCiliumSetup(version string, podCIDR string, hostPrefix int32) HostedClusterVerifier {
	return verifyCiliumSetup{ciliumVersion: version, podCIDR: podCIDR, hostPrefix: hostPrefix}
}
