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

// Karpenter and OpenShiftAzureNodeClass resource identities.
//
// The test module does not import sigs.k8s.io/karpenter or the
// karpenter-provider-azure fork. Both are large dependencies for a few
// unstructured creates. The group, version, and resource are hardcoded here.
// Keep these values in sync with the versions that the deployed
// karpenter-operator and karpenter-provider-azure serve.
var (
	karpenterNodePoolGVR = schema.GroupVersionResource{Group: "karpenter.sh", Version: "v1", Resource: "nodepools"}
	// The guest cluster blocks direct AKSNodeClass writes through the
	// karpenter.aksnodeclass.hypershift.io ValidatingAdmissionPolicy.
	// A test must create an OpenShiftAzureNodeClass instead. The
	// karpenter-operator reconciles it into an AKSNodeClass with the same
	// name. The karpenter-operator also copies the conditions back.
	openShiftAzureNodeClassGVR = schema.GroupVersionResource{Group: "karpenter.hypershift.openshift.io", Version: "v1alpha1", Resource: "openshiftazurenodeclasses"}
)

// KarpenterNodePoolLabel is the label that Karpenter sets on each Node and
// NodeClaim it provisions. The label value is the name of the owning NodePool.
// This is a Karpenter label. It is not the HyperShift NodePool label.
const KarpenterNodePoolLabel = "karpenter.sh/nodepool"

// NodeUninitializedTaint is the standard cloud-provider taint. The value is the
// same as k8s.io/cloud-provider/api.TaintExternalCloudProvider.
//
// Each kubelet sets this taint on itself at startup. The
// cloud-controller-manager removes it, after the node is initialized.
//
// The value is hardcoded here to avoid an import for one string constant.
const NodeUninitializedTaint = "node.cloudprovider.kubernetes.io/uninitialized"

// ValidateKarpenterInstanceTypes checks the input before the caller creates a
// resource. The instance type list must contain at least one item.
func ValidateKarpenterInstanceTypes(instanceTypes []string) error {
	if len(instanceTypes) == 0 {
		return fmt.Errorf("instanceTypes must not be empty")
	}
	return nil
}

type openShiftAzureNodeClassOptions struct {
	osDiskSizeGiB int64
	vnetSubnetID  string
	version       string
}

// OpenShiftAzureNodeClassOption sets one option on a test-owned NodeClass before
// creation.
type OpenShiftAzureNodeClassOption func(*openShiftAzureNodeClassOptions)

// WithNodeClassOSDiskSizeGiB sets the node OS disk size in GiB.
func WithNodeClassOSDiskSizeGiB(gib int64) OpenShiftAzureNodeClassOption {
	return func(options *openShiftAzureNodeClassOptions) {
		options.osDiskSizeGiB = gib
	}
}

// WithNodeClassVNetSubnetID sets the subnet for the node network interfaces. The
// subnet must belong to the virtual network of the cluster.
func WithNodeClassVNetSubnetID(subnetID string) OpenShiftAzureNodeClassOption {
	return func(options *openShiftAzureNodeClassOptions) {
		options.vnetSubnetID = subnetID
	}
}

// WithNodeClassVersion pins the OpenShift version of the nodes. The version must
// be in the update channel of the cluster and must not be newer than the control
// plane. A version change replaces the nodes.
func WithNodeClassVersion(version string) OpenShiftAzureNodeClassOption {
	return func(options *openShiftAzureNodeClassOptions) {
		options.version = version
	}
}

// nodeClassSpec builds the NodeClass spec. An empty option is omitted, so the
// operator applies its default.
func nodeClassSpec(options openShiftAzureNodeClassOptions) map[string]any {
	spec := map[string]any{"osDiskSizeGiB": options.osDiskSizeGiB}
	if options.vnetSubnetID != "" {
		spec["vnetSubnetID"] = options.vnetSubnetID
	}
	if options.version != "" {
		spec["version"] = options.version
	}
	return spec
}

// ApplyOpenShiftAzureNodeClass creates a test-owned NodeClass. The resource has
// cluster scope.
//
// The NodeClass controller of the karpenter-operator fills the AKSNodeClass
// from the node ignition Secret. The HyperShift ignition pipeline produces that
// Secret.
//
// A caller can create the NodePool in parallel. Karpenter retries until the
// NodeClass is ready. This recipe waits for the Ready condition first. Thus a
// broken NodeClass fails here with the NodeClass status. Karpenter also does not
// create and delete NodeClaims in the meantime.
func ApplyOpenShiftAzureNodeClass(ctx context.Context, adminRESTConfig *rest.Config, name string, options ...OpenShiftAzureNodeClassOption) error {
	opts := openShiftAzureNodeClassOptions{osDiskSizeGiB: 128}
	for _, option := range options {
		option(&opts)
	}
	dynamicClient, err := dynamic.NewForConfig(adminRESTConfig)
	if err != nil {
		return fmt.Errorf("failed to create dynamic client: %w", err)
	}

	nodeClass := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "karpenter.hypershift.openshift.io/v1alpha1",
			"kind":       "OpenShiftAzureNodeClass",
			"metadata": map[string]any{
				"name": name,
			},
			"spec": nodeClassSpec(opts),
		},
	}
	if _, err := dynamicClient.Resource(openShiftAzureNodeClassGVR).Create(ctx, nodeClass, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("failed to create OpenShiftAzureNodeClass %q: %w", name, err)
	}
	return nil
}

type karpenterNodePoolOptions struct {
	startupTaints []corev1.Taint
}

// KarpenterNodePoolOption sets one option on a test-owned NodePool before
// creation.
type KarpenterNodePoolOption func(*karpenterNodePoolOptions)

// WithKarpenterStartupTaints declares taints that Karpenter must ignore while
// it plans capacity. The component that owns each taint removes it from the node
// after initialization.
//
// Karpenter sets these taints on the node at registration. A pod does not need a
// toleration for Karpenter to start provisioning.
//
// A wrong declaration makes Karpenter treat the new node as unschedulable for
// the pending pod. Karpenter then provisions more nodes. Use the key and the
// effect of each taint that the owning component adds.
//
// The AutoNode Cilium spec uses node.cilium.io/agent-not-ready. The
// cilium-operator adds that taint to each node. The cilium-operator removes it
// when the Cilium agent pod on the node is Ready.
func WithKarpenterStartupTaints(taints ...corev1.Taint) KarpenterNodePoolOption {
	copied := append([]corev1.Taint(nil), taints...)
	return func(options *karpenterNodePoolOptions) {
		options.startupTaints = append([]corev1.Taint(nil), copied...)
	}
}

// ApplyKarpenterNodePool creates a NodePool. The NodePool references the
// AKSNodeClass that the karpenter-operator creates with the same name.
//
// The instance type list must not be empty.
//
// Karpenter adds the KarpenterNodePoolLabel to each Node and NodeClaim. The pod
// template must not set this label.
func ApplyKarpenterNodePool(ctx context.Context, adminRESTConfig *rest.Config, name string, instanceTypes []string, options ...KarpenterNodePoolOption) error {
	if err := ValidateKarpenterInstanceTypes(instanceTypes); err != nil {
		return err
	}
	dynamicClient, err := dynamic.NewForConfig(adminRESTConfig)
	if err != nil {
		return fmt.Errorf("failed to create dynamic client: %w", err)
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
	var opts karpenterNodePoolOptions
	for _, option := range options {
		option(&opts)
	}
	if len(opts.startupTaints) > 0 {
		taints := make([]any, 0, len(opts.startupTaints))
		for _, taint := range opts.startupTaints {
			entry := map[string]any{"key": taint.Key, "effect": string(taint.Effect)}
			if taint.Value != "" {
				entry["value"] = taint.Value
			}
			taints = append(taints, entry)
		}
		if err := unstructured.SetNestedSlice(nodePool.Object, taints, "spec", "template", "spec", "startupTaints"); err != nil {
			return fmt.Errorf("failed to set startup taints on NodePool %q: %w", name, err)
		}
	}
	if _, err := dynamicClient.Resource(karpenterNodePoolGVR).Create(ctx, nodePool, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("failed to create NodePool %q: %w", name, err)
	}

	return nil
}

// DeleteOpenShiftAzureNodeClassAndNodePool deletes the NodePool first. It
// deletes the OpenShiftAzureNodeClass second.
//
// The karpenter-operator deletes the AKSNodeClass that has the same name. Then
// it removes the finalizer from the OpenShiftAzureNodeClass.
//
// Admission blocks a direct delete of an AKSNodeClass.
//
// A successful DELETE request does not confirm the removal of the nodes or the
// NodeClass. The caller must verify the removal.
//
// A missing resource counts as success.
func DeleteOpenShiftAzureNodeClassAndNodePool(ctx context.Context, adminRESTConfig *rest.Config, name string) error {
	dynamicClient, err := dynamic.NewForConfig(adminRESTConfig)
	if err != nil {
		return fmt.Errorf("failed to create dynamic client: %w", err)
	}

	if err := dynamicClient.Resource(karpenterNodePoolGVR).Delete(ctx, name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to delete NodePool %q: %w", name, err)
	}
	if err := dynamicClient.Resource(openShiftAzureNodeClassGVR).Delete(ctx, name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to delete OpenShiftAzureNodeClass %q: %w", name, err)
	}
	return nil
}

type pendingWorkloadOptions struct {
	antiAffinityOnHostname bool
}

// PendingWorkloadOption sets one option on the pending workload before creation.
type PendingWorkloadOption func(*pendingWorkloadOptions)

// WithPodAntiAffinityOnHostname adds a required anti-affinity rule on the host
// name. Two replicas cannot run on the same node. Use it to make Karpenter
// provision one node per replica.
func WithPodAntiAffinityOnHostname() PendingWorkloadOption {
	return func(options *pendingWorkloadOptions) {
		options.antiAffinityOnHostname = true
	}
}

// newPendingWorkloadDeployment builds the workload Deployment. It keeps the
// payload construction separate from the API call, so a unit test can check the
// result without a server.
func newPendingWorkloadDeployment(namespace, name string, nodeSelector map[string]string, options pendingWorkloadOptions) *appsv1.Deployment {
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
	if options.antiAffinityOnHostname {
		deployment.Spec.Template.Spec.Affinity = &corev1.Affinity{
			PodAntiAffinity: &corev1.PodAntiAffinity{
				RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{{
					LabelSelector: &metav1.LabelSelector{MatchLabels: labels},
					TopologyKey:   corev1.LabelHostname,
				}},
			},
		}
	}

	return deployment
}

// DeployPendingWorkload creates a Deployment with 0 replicas in the namespace.
// It creates the namespace if the namespace does not exist. The pod template
// uses exactly the given nodeSelector.
//
// The Deployment starts at 0 replicas on purpose. The caller scales it up in a
// separate step. Thus the start of the scheduling pressure is explicit and
// observable.
func DeployPendingWorkload(ctx context.Context, adminRESTConfig *rest.Config, namespace, name string, nodeSelector map[string]string, options ...PendingWorkloadOption) error {
	kubeClient, err := kubernetes.NewForConfig(adminRESTConfig)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	if _, err := kubeClient.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: namespace},
	}, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to create namespace %q: %w", namespace, err)
	}

	var opts pendingWorkloadOptions
	for _, option := range options {
		option(&opts)
	}
	deployment := newPendingWorkloadDeployment(namespace, name, nodeSelector, opts)
	if _, err := kubeClient.AppsV1().Deployments(namespace).Create(ctx, deployment, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("failed to create deployment %q: %w", name, err)
	}
	return nil
}

// ScaleDeployment sets the replica count through the scale subresource. It
// retries after a resourceVersion conflict.
//
// The Deployment controller can change the Deployment between the read and the
// write. For example, it can reconcile a newly created Deployment. A single
// read-then-write sequence can fail at that point.
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

// DeleteNamespace deletes the namespace and all resources in it. A missing
// namespace counts as success.
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

// TODO: remove this approver when Karpenter supplies an auto-approver for node
// CSRs.
//
// RunKarpenterNodeCSRApprover approves pending kubelet-bootstrap
// CertificateSigningRequests at each pollInterval. It stops when the context
// ends.
//
// Run it in a background goroutine while the test waits for node readiness. A
// Karpenter-provisioned node sends two CSRs at times that are not predictable:
// first a client CSR, then a serving CSR. No signal shows that no more CSRs
// will arrive. Therefore this function approves each pending CSR until the
// caller's context ends.
//
// The return values are the number of approved CSRs and a terminal error. A
// context cancellation is not an error. It is the usual way to stop this
// function.
//
// This function is a workaround. It is not a fix. Azure has no CSR
// auto-approver for Karpenter-provisioned nodes today. ROSA uses the
// MachineApproverController for this task. The capability was lost when
// Karpenter moved out of the HyperShift-embedded operator into the standalone
// karpenter-operator. The standalone karpenter-operator does not implement that
// capability. Track the real fix in a separate item. A passing e2e run does not
// show that this gap is closed.
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
			// Log each approval, not only the final count. Then a run timeline
			// can be compared with the Node and NodeClaim creation times when
			// Karpenter provisions more nodes than expected. The requestor and
			// the signerName identify the client CSR and the serving CSR of a
			// node.
			ginkgo.GinkgoLogr.Info("approved Karpenter node CSR",
				"name", csr.Name, "requestor", csr.Spec.Username, "signerName", csr.Spec.SignerName,
				"creationTimestamp", csr.CreationTimestamp.Time, "totalApproved", approved)
		}
		// Never report done here. Only the end of the context stops this loop.
		return false, nil
	})
	if err != nil && ctx.Err() == nil {
		// A real error. A failed CSR approval is an example. A context
		// cancellation is not an error.
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
			// A denied CSR is also final. Do not try to approve it.
			return true
		}
	}
	return false
}

// isKubeletBootstrapCSR reports whether the CSR is a kubelet bootstrap request
// for a client certificate or a serving certificate. It rejects unrelated CSRs
// in the cluster.
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
