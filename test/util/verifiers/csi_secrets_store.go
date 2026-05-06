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
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"

	"github.com/Azure/ARO-HCP/test/util/framework"
)

const (
	csiDriverOperatorNS      = "openshift-cluster-csi-drivers"
	azureProviderNS          = "kube-system"
	csiDriverDaemonSetName   = "secrets-store-csi-driver-node"
	azureProviderDaemonSet   = "csi-secrets-store-provider-azure"
	azureProviderRepoURL     = "https://azure.github.io/secrets-store-csi-driver-provider-azure/charts"
	azureProviderChartName   = "csi-secrets-store-provider-azure"
	azureProviderReleaseName = "csi-secrets-store-provider-azure"
)

// CSISecretsStoreInstaller installs the Secrets Store CSI Driver via OLM and
// the Azure Key Vault Provider via Helm, then waits for both DaemonSets to
// become ready.
type CSISecretsStoreInstaller struct {
	AzureProviderVersion string
}

func (i CSISecretsStoreInstaller) Install(ctx context.Context, adminRESTConfig *rest.Config) error {
	kubeClient, err := kubernetes.NewForConfig(adminRESTConfig)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %w", err)
	}
	dynClient, err := dynamic.NewForConfig(adminRESTConfig)
	if err != nil {
		return fmt.Errorf("failed to create dynamic client: %w", err)
	}

	if err := i.installOLMOperator(ctx, kubeClient, dynClient); err != nil {
		return fmt.Errorf("failed to install CSI driver operator: %w", err)
	}

	if err := waitForDaemonSet(ctx, dynClient, csiDriverOperatorNS, csiDriverDaemonSetName); err != nil {
		return fmt.Errorf("csi driver DaemonSet never became ready: %w", err)
	}

	if err := i.installAzureProvider(ctx, adminRESTConfig); err != nil {
		return fmt.Errorf("failed to install Azure KV provider: %w", err)
	}

	if err := waitForDaemonSet(ctx, dynClient, azureProviderNS, azureProviderDaemonSet); err != nil {
		return fmt.Errorf("azure KV provider DaemonSet never became ready: %w", err)
	}

	return nil
}

func (i CSISecretsStoreInstaller) installOLMOperator(ctx context.Context, kubeClient kubernetes.Interface, dynClient dynamic.Interface) error {
	_, err := kubeClient.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: csiDriverOperatorNS},
	}, metav1.CreateOptions{})
	if err != nil {
		klog.InfoS("namespace creation returned error (may already exist)", "namespace", csiDriverOperatorNS, "err", err)
	}

	operatorGroupGVR := schema.GroupVersionResource{Group: "operators.coreos.com", Version: "v1", Resource: "operatorgroups"}
	og := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "operators.coreos.com/v1",
		"kind":       "OperatorGroup",
		"metadata": map[string]any{
			"name":      "csi-driver-og",
			"namespace": csiDriverOperatorNS,
		},
		"spec": map[string]any{
			"targetNamespaces": []any{csiDriverOperatorNS},
		},
	}}
	if _, err := dynClient.Resource(operatorGroupGVR).Namespace(csiDriverOperatorNS).Create(ctx, og, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("failed to create OperatorGroup: %w", err)
	}

	subscriptionGVR := schema.GroupVersionResource{Group: "operators.coreos.com", Version: "v1alpha1", Resource: "subscriptions"}
	sub := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "operators.coreos.com/v1alpha1",
		"kind":       "Subscription",
		"metadata": map[string]any{
			"name":      "secrets-store-csi-driver-operator",
			"namespace": csiDriverOperatorNS,
		},
		"spec": map[string]any{
			"channel":         "preview",
			"name":            "secrets-store-csi-driver-operator",
			"source":          "redhat-operators",
			"sourceNamespace": "openshift-marketplace",
		},
	}}
	if _, err := dynClient.Resource(subscriptionGVR).Namespace(csiDriverOperatorNS).Create(ctx, sub, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("failed to create Subscription: %w", err)
	}

	return nil
}

func (i CSISecretsStoreInstaller) installAzureProvider(ctx context.Context, adminRESTConfig *rest.Config) error {
	kubeconfigContent, err := framework.GenerateKubeconfig(adminRESTConfig)
	if err != nil {
		return fmt.Errorf("failed to generate kubeconfig: %w", err)
	}

	return framework.InstallHelmChart(ctx, framework.HelmChartConfig{
		ReleaseName: azureProviderReleaseName,
		RepoURL:     azureProviderRepoURL,
		ChartName:   azureProviderChartName,
		Version:     i.AzureProviderVersion,
		Namespace:   azureProviderNS,
		Values: map[string]any{
			"secrets-store-csi-driver": map[string]any{
				"install": false,
			},
			"linux": map[string]any{
				"privileged": true,
			},
		},
	}, kubeconfigContent)
}

func waitForDaemonSet(ctx context.Context, dynClient dynamic.Interface, namespace, name string) error {
	daemonSetGVR := schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "daemonsets"}
	klog.InfoS("waiting for DaemonSet to become ready", "namespace", namespace, "name", name)

	return wait.PollUntilContextTimeout(ctx, 15*time.Second, 10*time.Minute, true, func(ctx context.Context) (bool, error) {
		ds, err := dynClient.Resource(daemonSetGVR).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return false, nil
		}
		desired, _, _ := unstructured.NestedInt64(ds.Object, "status", "desiredNumberScheduled")
		ready, _, _ := unstructured.NestedInt64(ds.Object, "status", "numberReady")
		if desired > 0 && ready == desired {
			klog.InfoS("DaemonSet is ready", "namespace", namespace, "name", name, "ready", ready)
			return true, nil
		}
		return false, nil
	})
}
