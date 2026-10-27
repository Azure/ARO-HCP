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
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// EgressIPGVR identifies the cluster-scoped OVN-Kubernetes EgressIP custom
// resource that cloud-network-config-controller's NodeController and
// CloudPrivateIPConfigController react to.
var EgressIPGVR = schema.GroupVersionResource{Group: "k8s.ovn.org", Version: "v1", Resource: "egressips"}

// EgressAssignableLabel is the node label OVN-Kubernetes requires before it
// will consider a node eligible to host an EgressIP. This applies on every
// supported platform, including Azure: the cloud-network-config-controller
// only performs the cloud-side NIC attachment once OVN-Kubernetes has
// already picked an eligible node from among those carrying this label; it
// does not relax the eligibility gate itself.
const EgressAssignableLabel = "k8s.ovn.org/egress-assignable"

// LabelNodeEgressAssignable applies EgressAssignableLabel to nodeName so
// OVN-Kubernetes will consider it eligible to host an EgressIP.
func LabelNodeEgressAssignable(ctx context.Context, adminRESTConfig *rest.Config, nodeName string) error {
	kubeClient, err := kubernetes.NewForConfig(adminRESTConfig)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	patch := fmt.Appendf(nil, `{"metadata":{"labels":{%q:""}}}`, EgressAssignableLabel)
	if _, err := kubeClient.CoreV1().Nodes().Patch(ctx, nodeName, types.MergePatchType, patch, metav1.PatchOptions{}); err != nil {
		return fmt.Errorf("failed to apply %s label to node %q: %w", EgressAssignableLabel, nodeName, err)
	}
	return nil
}

// UnlabelNodeEgressAssignable removes EgressAssignableLabel from nodeName. A
// NotFound error (node already gone) is treated as success.
func UnlabelNodeEgressAssignable(ctx context.Context, adminRESTConfig *rest.Config, nodeName string) error {
	kubeClient, err := kubernetes.NewForConfig(adminRESTConfig)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	patch := fmt.Appendf(nil, `{"metadata":{"labels":{%q:null}}}`, EgressAssignableLabel)
	if _, err := kubeClient.CoreV1().Nodes().Patch(ctx, nodeName, types.MergePatchType, patch, metav1.PatchOptions{}); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to remove %s label from node %q: %w", EgressAssignableLabel, nodeName, err)
	}
	return nil
}

// CreateEgressIP creates an EgressIP object requesting egressIP as a fixed
// egress address. Its namespaceSelector targets a label no namespace in the
// cluster carries: assignment of the EgressIP to a node and NIC depends only
// on node eligibility, not on any namespace actually matching the selector.
func CreateEgressIP(ctx context.Context, adminRESTConfig *rest.Config, name, egressIP string) error {
	dynamicClient, err := dynamic.NewForConfig(adminRESTConfig)
	if err != nil {
		return fmt.Errorf("failed to create dynamic client: %w", err)
	}

	obj := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "k8s.ovn.org/v1",
			"kind":       "EgressIP",
			"metadata": map[string]any{
				"name": name,
			},
			"spec": map[string]any{
				"egressIPs": []any{egressIP},
				"namespaceSelector": map[string]any{
					"matchLabels": map[string]any{
						"e2e-egressip-test": name,
					},
				},
			},
		},
	}

	if _, err := dynamicClient.Resource(EgressIPGVR).Create(ctx, obj, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("failed to create EgressIP %q: %w", name, err)
	}
	return nil
}

// DeleteEgressIP deletes the named EgressIP object. A NotFound error is
// treated as success.
func DeleteEgressIP(ctx context.Context, adminRESTConfig *rest.Config, name string) error {
	dynamicClient, err := dynamic.NewForConfig(adminRESTConfig)
	if err != nil {
		return fmt.Errorf("failed to create dynamic client: %w", err)
	}

	if err := dynamicClient.Resource(EgressIPGVR).Delete(ctx, name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to delete EgressIP %q: %w", name, err)
	}
	return nil
}

// WaitForEgressIPAssignedNode polls the named EgressIP's status until
// OVN-Kubernetes reports egressIP assigned to a node, and returns that
// node's name.
func WaitForEgressIPAssignedNode(ctx context.Context, adminRESTConfig *rest.Config, name, egressIP string, timeout time.Duration) (string, error) {
	dynamicClient, err := dynamic.NewForConfig(adminRESTConfig)
	if err != nil {
		return "", fmt.Errorf("failed to create dynamic client: %w", err)
	}

	var nodeName string
	var lastErr error
	err = wait.PollUntilContextTimeout(ctx, 10*time.Second, timeout, true, func(ctx context.Context) (bool, error) {
		current, getErr := dynamicClient.Resource(EgressIPGVR).Get(ctx, name, metav1.GetOptions{})
		if getErr != nil {
			lastErr = getErr
			return false, nil
		}

		items, _, _ := unstructured.NestedSlice(current.Object, "status", "items")
		for _, item := range items {
			itemMap, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if ip, _, _ := unstructured.NestedString(itemMap, "egressIP"); ip != egressIP {
				continue
			}
			if node, _, _ := unstructured.NestedString(itemMap, "node"); node != "" {
				nodeName = node
				return true, nil
			}
		}
		lastErr = fmt.Errorf("EgressIP %q is not yet assigned to a node", name)
		return false, nil
	})
	if err != nil {
		if lastErr != nil {
			return "", fmt.Errorf("timed out waiting for EgressIP %q to be assigned to a node: %w", name, lastErr)
		}
		return "", fmt.Errorf("timed out waiting for EgressIP %q to be assigned to a node: %w", name, err)
	}

	return nodeName, nil
}
