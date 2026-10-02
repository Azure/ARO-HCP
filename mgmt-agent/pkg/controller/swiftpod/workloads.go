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

package swiftpod

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	resourcehelper "k8s.io/component-helpers/resource"

	"github.com/Azure/ARO-HCP/internal/kuberesources"
)

// Reports whether the Pod has succeeded or failed.
func terminal(pod *corev1.Pod) bool {
	return pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed
}

// Reports whether the Pod is Ready and not terminating.
func podReady(pod *corev1.Pod) bool {
	if pod.DeletionTimestamp != nil {
		return false
	}
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodReady {
			return condition.Status == corev1.ConditionTrue
		}
	}
	return false
}

// Checks readiness and the minimum Ready duration.
func podAvailable(pod *corev1.Pod, minReadySeconds int32, now time.Time) bool {
	if !podReady(pod) {
		return false
	}
	if minReadySeconds == 0 {
		return true
	}
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodReady {
			return !condition.LastTransitionTime.IsZero() &&
				condition.LastTransitionTime.Add(time.Duration(minReadySeconds)*time.Second).Before(now)
		}
	}
	return false
}

// Reports whether the labels match the selector.
func selected(selector metav1.LabelSelector, values map[string]string) bool {
	parsed, err := metav1.LabelSelectorAsSelector(&selector)
	return err == nil && parsed.Matches(labels.Set(values))
}

// Rejects Pod features this mitigation cannot safely handle.
func supportedPod(pod *corev1.Pod) error {
	if pod.Annotations[corev1.MirrorPodAnnotationKey] != "" || pod.Spec.HostNetwork ||
		pod.Spec.HostPID || pod.Spec.HostIPC || len(pod.Spec.EphemeralContainers) > 0 {
		return fmt.Errorf("static, host-namespace or debug workload")
	}
	if pod.Spec.SchedulerName != "" && pod.Spec.SchedulerName != corev1.DefaultSchedulerName {
		return fmt.Errorf("unsupported scheduler")
	}
	if len(pod.Spec.SchedulingGates) != 0 || len(pod.Spec.ResourceClaims) != 0 {
		return fmt.Errorf("unsupported scheduling gates or resource claims")
	}
	for _, volume := range pod.Spec.Volumes {
		if volume.ConfigMap == nil && volume.Secret == nil && volume.Projected == nil &&
			volume.DownwardAPI == nil && volume.EmptyDir == nil {
			return fmt.Errorf("unsupported stateful or local volume %s", volume.Name)
		}
	}
	for _, containers := range [][]corev1.Container{pod.Spec.InitContainers, pod.Spec.Containers} {
		for _, container := range containers {
			for _, port := range container.Ports {
				if port.HostPort != 0 {
					return fmt.Errorf("host ports require a workload-specific placement policy")
				}
			}
		}
	}
	if len(pod.Finalizers) != 0 {
		return fmt.Errorf("pod finalizers require operator resolution")
	}
	if pod.Spec.TerminationGracePeriodSeconds == nil || *pod.Spec.TerminationGracePeriodSeconds <= 0 {
		return fmt.Errorf("positive termination grace period required")
	}
	return nil
}

// Removes finite NoExecute tolerations, which cannot qualify a placement destination.
func placementTolerations(tolerations []corev1.Toleration) []corev1.Toleration {
	return slices.DeleteFunc(slices.Clone(tolerations), func(t corev1.Toleration) bool {
		return t.Effect == corev1.TaintEffectNoExecute && t.TolerationSeconds != nil
	})
}

// Normalizes known admission-added tolerations for comparison with the ReplicaSet template.
func templateTolerations(spec corev1.PodSpec) []corev1.Toleration {
	result := placementTolerations(spec.Tolerations)
	demand := requests(&corev1.Pod{Spec: spec}, resourcehelper.PodResourcesOptions{})
	nics, cpu, memory := demand[kuberesources.SwiftNICResourceName], demand[corev1.ResourceCPU], demand[corev1.ResourceMemory]
	return slices.DeleteFunc(result, func(t corev1.Toleration) bool {
		if t.Operator != corev1.TolerationOpExists || t.Effect != corev1.TaintEffectNoSchedule ||
			t.Value != "" || t.TolerationSeconds != nil {
			return false
		}
		switch t.Key {
		case string(kuberesources.SwiftNICResourceName):
			return nics.Sign() > 0
		case corev1.TaintNodeMemoryPressure:
			return cpu.Sign() > 0 || memory.Sign() > 0
		default:
			return false
		}
	})
}

// Checks that the replacement template fits the admitted Pod's scheduling and resource assumptions.
func validateTemplate(pod *corev1.Pod, template *corev1.PodTemplateSpec) error {
	replacement := &corev1.Pod{ObjectMeta: template.ObjectMeta, Spec: template.Spec}
	if err := supportedPod(replacement); err != nil {
		return fmt.Errorf("ReplicaSet template: %w", err)
	}
	if template.Spec.NodeName != "" {
		return fmt.Errorf("ReplicaSet template has an assigned Node")
	}
	if _, exists := template.Labels[ownershipLabel]; exists {
		return fmt.Errorf("ReplicaSet template must not inherit mitigation ownership")
	}
	podLabels := maps.Clone(pod.Labels)
	delete(podLabels, ownershipLabel)
	// Binding can add these Node labels. Placement rejects policies that depend
	// on their values instead of carrying source topology to a replacement.
	for _, key := range []string{corev1.LabelTopologyRegion, corev1.LabelTopologyZone} {
		if _, explicit := template.Labels[key]; !explicit {
			delete(podLabels, key)
		}
	}
	if !maps.Equal(podLabels, template.Labels) ||
		!maps.Equal(pod.Spec.NodeSelector, template.Spec.NodeSelector) ||
		!apiequality.Semantic.DeepEqual(pod.Spec.RuntimeClassName, template.Spec.RuntimeClassName) ||
		!apiequality.Semantic.DeepEqual(pod.Spec.Affinity, template.Spec.Affinity) ||
		!apiequality.Semantic.DeepEqual(pod.Spec.TopologySpreadConstraints, template.Spec.TopologySpreadConstraints) ||
		!slices.EqualFunc(templateTolerations(pod.Spec), templateTolerations(template.Spec),
			func(a, b corev1.Toleration) bool { return apiequality.Semantic.DeepEqual(a, b) }) {
		return fmt.Errorf("ReplicaSet template labels or scheduling constraints differ from the admitted Pod")
	}
	// Placement uses the admitted Pod, including injected resources. Its demand
	// must also cover the template that the ReplicaSet uses to recreate it.
	podRequests := requests(pod, resourcehelper.PodResourcesOptions{})
	templateRequests := requests(replacement, resourcehelper.PodResourcesOptions{})
	for name, amount := range templateRequests {
		admitted := podRequests[name]
		if admitted.Cmp(amount) < 0 {
			return fmt.Errorf("ReplicaSet template requests more %s than the admitted Pod", name)
		}
	}
	return nil
}

// Verifies the live Pod-to-Deployment owner chain and requires a supported router matching the workload policy.
func workload(ctx context.Context, client kubernetes.Interface, pod *corev1.Pod, cfg Config) (*appsv1.Deployment, *WorkloadPolicy, error) {
	if pod.Labels["app"] != "private-router" {
		return nil, nil, fmt.Errorf("not a private-router Pod")
	}
	if err := supportedPod(pod); err != nil {
		return nil, nil, err
	}
	owner := metav1.GetControllerOf(pod)
	if owner == nil || owner.APIVersion != "apps/v1" || owner.Kind != "ReplicaSet" {
		return nil, nil, fmt.Errorf("pod has no supported recreating owner")
	}
	rs, err := client.AppsV1().ReplicaSets(pod.Namespace).Get(ctx, owner.Name, metav1.GetOptions{})
	if err != nil {
		return nil, nil, err
	}
	if rs.UID != owner.UID || rs.DeletionTimestamp != nil || rs.Spec.Replicas == nil || *rs.Spec.Replicas < 1 {
		return nil, nil, fmt.Errorf("ReplicaSet identity or desired replicas changed")
	}
	if err := validateTemplate(pod, &rs.Spec.Template); err != nil {
		return nil, nil, err
	}
	deploymentOwner := metav1.GetControllerOf(rs)
	if deploymentOwner == nil || deploymentOwner.APIVersion != "apps/v1" || deploymentOwner.Kind != "Deployment" {
		return nil, nil, fmt.Errorf("ReplicaSet is not managed by a Deployment")
	}
	deployment, err := client.AppsV1().Deployments(pod.Namespace).Get(ctx, deploymentOwner.Name, metav1.GetOptions{})
	if err != nil {
		return nil, nil, err
	}
	if deployment.UID != deploymentOwner.UID || deployment.DeletionTimestamp != nil ||
		deployment.Spec.Replicas == nil || *deployment.Spec.Replicas < 1 ||
		deployment.Status.ObservedGeneration < deployment.Generation ||
		deployment.Status.UpdatedReplicas != *deployment.Spec.Replicas ||
		deployment.Status.Replicas != *deployment.Spec.Replicas {
		return nil, nil, fmt.Errorf("deployment identity, rollout or desired replicas changed")
	}
	if deployment.Name != "router" || deployment.Labels["hypershift.openshift.io/managed-by"] != "control-plane-operator" {
		return nil, nil, fmt.Errorf("not a control-plane-operator router Deployment")
	}
	if deployment.Spec.Template.Spec.NodeName != "" || len(deployment.Spec.Template.Spec.SchedulingGates) != 0 ||
		len(deployment.Spec.Template.Spec.ResourceClaims) != 0 {
		return nil, nil, fmt.Errorf("deployment template has unsupported scheduling constraints")
	}
	namespace, err := client.CoreV1().Namespaces().Get(ctx, pod.Namespace, metav1.GetOptions{})
	if err != nil {
		return nil, nil, err
	}
	if namespace.DeletionTimestamp != nil {
		return nil, nil, fmt.Errorf("namespace is terminating")
	}
	policy := cfg.Workload
	if selected(policy.NamespaceSelector, namespace.Labels) &&
		selected(policy.PodSelector, pod.Labels) && selected(policy.DeploymentSelector, deployment.Labels) {
		for _, volume := range pod.Spec.Volumes {
			if volume.EmptyDir != nil && !policy.AllowEmptyDir {
				return nil, nil, fmt.Errorf("emptyDir removal is not permitted by workload policy")
			}
		}
		for _, volume := range rs.Spec.Template.Spec.Volumes {
			if volume.EmptyDir != nil && !policy.AllowEmptyDir {
				return nil, nil, fmt.Errorf("ReplicaSet template emptyDir is not permitted by workload policy")
			}
		}
		return deployment, &policy, nil
	}
	return nil, nil, fmt.Errorf("no matching workload policy")
}

// Verifies the workload and requires enough available replicas on healthy, non-excluded Nodes.
func (c *Controller) availableWorkload(ctx context.Context, pod *corev1.Pod, cfg Config, snapshot ClusterSnapshot, excluded map[string]bool) (*appsv1.Deployment, error) {
	deployment, policy, err := workload(ctx, c.kube, pod, cfg)
	if err != nil {
		return nil, err
	}
	if policy.MinAvailableReplicas == nil {
		return nil, fmt.Errorf("workload availability floor missing")
	}
	pods, err := c.kube.CoreV1().Pods(pod.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	replicaSets, err := c.kube.AppsV1().ReplicaSets(pod.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	ownedReplicaSets := map[types.UID]string{}
	for i := range replicaSets.Items {
		rs := &replicaSets.Items[i]
		parent := metav1.GetControllerOf(rs)
		if rs.DeletionTimestamp == nil && parent != nil && parent.Kind == "Deployment" &&
			parent.APIVersion == "apps/v1" && parent.UID == deployment.UID {
			ownedReplicaSets[rs.UID] = rs.Name
		}
	}
	healthyNodes := map[string]bool{}
	for _, node := range snapshot.Nodes {
		healthyNodes[node.Name] = ready(node) && !excluded[node.Name]
	}
	available := int32(0)
	now := c.clock()
	for i := range pods.Items {
		other := &pods.Items[i]
		owner := metav1.GetControllerOf(other)
		if !podAvailable(other, deployment.Spec.MinReadySeconds, now) || !healthyNodes[other.Spec.NodeName] || owner == nil ||
			owner.Kind != "ReplicaSet" || owner.APIVersion != "apps/v1" {
			continue
		}
		if name, belongs := ownedReplicaSets[owner.UID]; belongs && name == owner.Name {
			available++
		}
	}
	available = min(available, deployment.Status.AvailableReplicas)
	if available < *policy.MinAvailableReplicas {
		return nil, fmt.Errorf("workload availability %d is below floor %d", available, *policy.MinAvailableReplicas)
	}
	return deployment, nil
}
