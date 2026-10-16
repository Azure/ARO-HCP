// Copyright 2026 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
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

	"github.com/onsi/ginkgo/v2"

	appsv1 "k8s.io/api/apps/v1"
	certificatesv1 "k8s.io/api/certificates/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/util/retry"
)

// Karpenter/AKSNodeClass resource identity. These GVRs deliberately are not
// imported from sigs.k8s.io/karpenter or the karpenter-provider-azure fork:
// neither is (or should become) a dependency of this test module for a
// handful of unstructured object creates, so the group/version/resource is
// hardcoded here and must be kept in sync with whatever the deployed
// karpenter-operator/karpenter-provider-azure actually serves.
var (
	karpenterNodePoolGVR = schema.GroupVersionResource{Group: "karpenter.sh", Version: "v1", Resource: "nodepools"}
	aksNodeClassGVR      = schema.GroupVersionResource{Group: "karpenter.azure.com", Version: "v1beta1", Resource: "aksnodeclasses"}
)

// KarpenterNodePoolLabel is the label Karpenter sets on every Node and
// NodeClaim it provisions, naming the owning NodePool. It is Karpenter's own
// label (sigs.k8s.io/karpenter), not HyperShift's NodePoolLabel.
const KarpenterNodePoolLabel = "karpenter.sh/nodepool"

// NodeUninitializedTaint is the well-known cloud-provider taint
// (k8s.io/cloud-provider/api.TaintExternalCloudProvider) that every kubelet
// sets on itself at startup and that only an external cloud-controller-manager
// clears once it has initialized the node. Hardcoded here rather than
// importing k8s.io/cloud-provider for one string constant.
const NodeUninitializedTaint = "node.cloudprovider.kubernetes.io/uninitialized"

// KarpenterMarketplaceImage identifies the Azure Marketplace RHCOS image an
// AKSNodeClass should provision. This must be kept in lockstep with whatever
// OpenShift version the calling test creates the cluster with — there is no
// way to derive one from the other at the API level (the AKSNodeClass has no
// notion of "match the cluster's version"), so callers own that consistency.
type KarpenterMarketplaceImage struct {
	Publisher string
	Offer     string
	SKU       string
	Version   string
}

// ApplyAKSNodeClassAndNodePool creates the (cluster-scoped) AKSNodeClass and
// NodePool this test needs to prove Karpenter provisions a real node. Both
// resources are named `name`; the NodePool's pod template carries the label
// `autonode: "true"`. It must not also set KarpenterNodePoolLabel: Karpenter
// stamps that label onto every Node/NodeClaim it provisions itself, and
// rejects NodePools that try to set it in spec.template.metadata.labels as
// "restricted". Nodes provisioned by this NodePool still end up with
// KarpenterNodePoolLabel=name automatically. instanceTypes must be non-empty.
func ApplyAKSNodeClassAndNodePool(ctx context.Context, adminRESTConfig *rest.Config, name string, image KarpenterMarketplaceImage, instanceTypes []string) error {
	if len(instanceTypes) == 0 {
		return fmt.Errorf("instanceTypes must not be empty")
	}

	dynamicClient, err := dynamic.NewForConfig(adminRESTConfig)
	if err != nil {
		return fmt.Errorf("failed to create dynamic client: %w", err)
	}

	nodeClass := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "karpenter.azure.com/v1beta1",
			"kind":       "AKSNodeClass",
			"metadata": map[string]any{
				"name": name,
			},
			"spec": map[string]any{
				"marketplaceImage": map[string]any{
					"publisher": image.Publisher,
					"offer":     image.Offer,
					"sku":       image.SKU,
					"version":   image.Version,
				},
				// vnetSubnetID intentionally omitted: Karpenter falls back to
				// the HostedCluster's own subnet.
				"osDiskSizeGB": int64(128),
			},
		},
	}
	if _, err := dynamicClient.Resource(aksNodeClassGVR).Create(ctx, nodeClass, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("failed to create AKSNodeClass %q: %w", name, err)
	}

	instanceTypeValues := make([]any, 0, len(instanceTypes))
	for _, it := range instanceTypes {
		instanceTypeValues = append(instanceTypeValues, it)
	}
	nodePool := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "karpenter.sh/v1",
			"kind":       "NodePool",
			"metadata": map[string]any{
				"name": name,
			},
			"spec": map[string]any{
				"template": map[string]any{
					"metadata": map[string]any{
						"labels": map[string]any{
							"autonode": "true",
						},
					},
					"spec": map[string]any{
						"expireAfter": "720h",
						"nodeClassRef": map[string]any{
							"group": "karpenter.azure.com",
							"kind":  "AKSNodeClass",
							"name":  name,
						},
						"requirements": []any{
							map[string]any{"key": "kubernetes.io/arch", "operator": "In", "values": []any{"amd64"}},
							map[string]any{"key": "kubernetes.io/os", "operator": "In", "values": []any{"linux"}},
							map[string]any{"key": "karpenter.sh/capacity-type", "operator": "In", "values": []any{"on-demand"}},
							map[string]any{"key": "node.kubernetes.io/instance-type", "operator": "In", "values": instanceTypeValues},
						},
					},
				},
			},
		},
	}
	if _, err := dynamicClient.Resource(karpenterNodePoolGVR).Create(ctx, nodePool, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("failed to create NodePool %q: %w", name, err)
	}

	return nil
}

// DeleteAKSNodeClassAndNodePool deletes the NodePool then the AKSNodeClass
// created by ApplyAKSNodeClassAndNodePool. Deleting the NodePool first lets
// Karpenter deprovision any NodeClaims/Nodes it still owns before the
// AKSNodeClass they reference disappears. Not-found is treated as success.
func DeleteAKSNodeClassAndNodePool(ctx context.Context, adminRESTConfig *rest.Config, name string) error {
	dynamicClient, err := dynamic.NewForConfig(adminRESTConfig)
	if err != nil {
		return fmt.Errorf("failed to create dynamic client: %w", err)
	}

	if err := dynamicClient.Resource(karpenterNodePoolGVR).Delete(ctx, name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to delete NodePool %q: %w", name, err)
	}
	if err := dynamicClient.Resource(aksNodeClassGVR).Delete(ctx, name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to delete AKSNodeClass %q: %w", name, err)
	}
	return nil
}

// DeployPendingWorkload creates a Deployment with 0 replicas in the given
// namespace (created if it doesn't exist), whose pod template's nodeSelector
// is exactly nodeSelector. It starts at 0 replicas deliberately: the caller
// scales it up separately, so the moment scheduling pressure begins is
// explicit and observable, rather than racing pod creation against this
// function's return.
func DeployPendingWorkload(ctx context.Context, adminRESTConfig *rest.Config, namespace, name string, nodeSelector map[string]string) error {
	kubeClient, err := kubernetes.NewForConfig(adminRESTConfig)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	if _, err := kubeClient.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: namespace},
	}, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to create namespace %q: %w", namespace, err)
	}

	labels := map[string]string{"app": name}
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: appsv1.DeploymentSpec{
			Replicas: to32Ptr(0),
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					NodeSelector: nodeSelector,
					Containers: []corev1.Container{{
						Name:    name,
						Image:   "quay.io/openshift/origin-pod:4.22.0",
						Command: []string{"/bin/sh", "-c", "sleep infinity"},
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("100m"),
								corev1.ResourceMemory: resource.MustParse("64Mi"),
							},
							Limits: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("250m"),
								corev1.ResourceMemory: resource.MustParse("128Mi"),
							},
						},
					}},
				},
			},
		},
	}
	if _, err := kubeClient.AppsV1().Deployments(namespace).Create(ctx, deployment, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("failed to create deployment %q: %w", name, err)
	}
	return nil
}

// ScaleDeployment patches a Deployment's replica count via the scale
// subresource, retrying on a resourceVersion conflict: the Deployment
// controller itself can reconcile (bumping resourceVersion, e.g. right after
// a freshly-created Deployment) in the window between GetScale and
// UpdateScale, which a single-shot get-then-update would otherwise fail on.
func ScaleDeployment(ctx context.Context, adminRESTConfig *rest.Config, namespace, name string, replicas int32) error {
	kubeClient, err := kubernetes.NewForConfig(adminRESTConfig)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %w", err)
	}
	err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
		scale, err := kubeClient.AppsV1().Deployments(namespace).GetScale(ctx, name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("failed to get scale for deployment %q: %w", name, err)
		}
		scale.Spec.Replicas = replicas
		_, err = kubeClient.AppsV1().Deployments(namespace).UpdateScale(ctx, name, scale, metav1.UpdateOptions{})
		return err
	})
	if err != nil {
		return fmt.Errorf("failed to scale deployment %q to %d replicas: %w", name, replicas, err)
	}
	return nil
}

// DeleteNamespace deletes a namespace (and everything in it). Not-found is
// treated as success.
func DeleteNamespace(ctx context.Context, adminRESTConfig *rest.Config, namespace string) error {
	kubeClient, err := kubernetes.NewForConfig(adminRESTConfig)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %w", err)
	}
	if err := kubeClient.CoreV1().Namespaces().Delete(ctx, namespace, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to delete namespace %q: %w", namespace, err)
	}
	return nil
}

// RunKarpenterNodeCSRApprover approves pending kubelet-bootstrap
// CertificateSigningRequests (requestor system:node:* or the
// node-bootstrapper service account) every pollInterval, until ctx is done.
// It is meant to run in a background goroutine for the duration of a wait for
// node readiness: a Karpenter-provisioned node normally submits two CSRs
// (client, then serving) at unpredictable points before it fully initializes,
// with no reliable "no more are coming" signal to poll for instead - so
// rather than guessing when CSRs have "settled", this just keeps approving
// anything pending until the caller's context says the wait is over (success
// or timeout). Returns the total number approved and any terminal error
// (context cancellation/deadline is not treated as an error - that is the
// normal way this function stops).
//
// This is a workaround, not a fix: there is no CSR auto-approver for
// Karpenter-provisioned nodes on Azure today (unlike ROSA's
// MachineApproverController) - it's a capability that was left behind when
// Karpenter was split out of the HyperShift-embedded operator into the
// standalone karpenter-operator and never reimplemented there. Track the real
// fix separately; do not read a passing e2e run as evidence this gap is closed.
func RunKarpenterNodeCSRApprover(ctx context.Context, adminRESTConfig *rest.Config, pollInterval time.Duration) (int, error) {
	kubeClient, err := kubernetes.NewForConfig(adminRESTConfig)
	if err != nil {
		return 0, fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	approved := 0
	err = wait.PollUntilContextCancel(ctx, pollInterval, true, func(ctx context.Context) (bool, error) {
		csrs, err := kubeClient.CertificatesV1().CertificateSigningRequests().List(ctx, metav1.ListOptions{})
		if err != nil {
			return false, fmt.Errorf("failed to list CertificateSigningRequests: %w", err)
		}
		for i := range csrs.Items {
			csr := &csrs.Items[i]
			if !isKubeletBootstrapCSR(csr) || isCSRApproved(csr) {
				continue
			}
			csr.Status.Conditions = append(csr.Status.Conditions, certificatesv1.CertificateSigningRequestCondition{
				Type:    certificatesv1.CertificateApproved,
				Status:  corev1.ConditionTrue,
				Reason:  "E2EKarpenterCSRApprover",
				Message: "approved by the AutoNode e2e test as a workaround for the missing Azure Karpenter CSR auto-approver",
			})
			if _, err := kubeClient.CertificatesV1().CertificateSigningRequests().UpdateApproval(ctx, csr.Name, csr, metav1.UpdateOptions{}); err != nil {
				return false, fmt.Errorf("failed to approve CertificateSigningRequest %q: %w", csr.Name, err)
			}
			approved++
			// Logged per-approval (not just the final count) so a run's timeline
			// can be correlated against node/NodeClaim creation timestamps when
			// debugging why Karpenter provisioned more nodes than expected -
			// requestor/signerName distinguish the client-cert vs serving-cert
			// CSR a node typically submits.
			ginkgo.GinkgoLogr.Info("approved Karpenter node CSR",
				"name", csr.Name, "requestor", csr.Spec.Username, "signerName", csr.Spec.SignerName,
				"creationTimestamp", csr.CreationTimestamp.Time, "totalApproved", approved)
		}
		return false, nil // never "done" on its own; only ctx ending stops this loop
	})
	if err != nil && ctx.Err() == nil {
		// A real, non-cancellation error (e.g. a CSR approval call failed).
		return approved, err
	}
	return approved, nil
}

func isCSRApproved(csr *certificatesv1.CertificateSigningRequest) bool {
	for _, c := range csr.Status.Conditions {
		if c.Type == certificatesv1.CertificateApproved && c.Status == corev1.ConditionTrue {
			return true
		}
		if c.Type == certificatesv1.CertificateDenied && c.Status == corev1.ConditionTrue {
			return true // denied is terminal too; don't try to approve it
		}
	}
	return false
}

// isKubeletBootstrapCSR reports whether csr looks like a kubelet bootstrap
// request (client or serving) rather than some unrelated CSR in the cluster.
func isKubeletBootstrapCSR(csr *certificatesv1.CertificateSigningRequest) bool {
	switch {
	case csr.Spec.Username == "system:serviceaccount:openshift-machine-config-operator:node-bootstrapper":
		return true
	case len(csr.Spec.Username) > len("system:node:") && csr.Spec.Username[:len("system:node:")] == "system:node:":
		return true
	case csr.Spec.SignerName == certificatesv1.KubeletServingSignerName:
		return true
	case csr.Spec.SignerName == certificatesv1.KubeAPIServerClientKubeletSignerName:
		return true
	default:
		return false
	}
}

func to32Ptr(i int32) *int32 { return &i }
