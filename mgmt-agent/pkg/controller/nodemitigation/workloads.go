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

package nodemitigation

import (
	"context"
	"fmt"
	"slices"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
)

func terminal(pod *corev1.Pod) bool {
	return pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed
}

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

func selected(selector metav1.LabelSelector, values map[string]string) bool {
	parsed, err := metav1.LabelSelectorAsSelector(&selector)
	return err == nil && parsed.Matches(labels.Set(values))
}

func supportedPod(pod *corev1.Pod) error {
	if pod.Annotations[corev1.MirrorPodAnnotationKey] != "" || pod.Spec.HostNetwork ||
		pod.Spec.HostPID || pod.Spec.HostIPC || len(pod.Spec.EphemeralContainers) > 0 {
		return fmt.Errorf("static, host-namespace or debug workload")
	}
	if pod.Spec.SchedulerName != "" && pod.Spec.SchedulerName != corev1.DefaultSchedulerName {
		return fmt.Errorf("unsupported scheduler")
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

// workload verifies the recreating owner chain, not a pod name or labels alone.
func workload(ctx context.Context, client kubernetes.Interface, pod *corev1.Pod, cfg Config) (*appsv1.Deployment, *WorkloadPolicy, error) {
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
	if rs.Spec.Template.Spec.NodeName != "" {
		return nil, nil, fmt.Errorf("ReplicaSet template bypasses scheduler placement")
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
	if deployment.Spec.Template.Spec.NodeName != "" {
		return nil, nil, fmt.Errorf("deployment template bypasses scheduler placement")
	}
	namespace, err := client.CoreV1().Namespaces().Get(ctx, pod.Namespace, metav1.GetOptions{})
	if err != nil {
		return nil, nil, err
	}
	if namespace.DeletionTimestamp != nil {
		return nil, nil, fmt.Errorf("namespace is terminating")
	}
	for _, policy := range cfg.Workloads {
		if selected(policy.NamespaceSelector, namespace.Labels) &&
			selected(policy.PodSelector, pod.Labels) && selected(policy.DeploymentSelector, deployment.Labels) {
			if cfg.hasAcceptedPolicy {
				approved := false
				for _, accepted := range cfg.acceptedWorkloads {
					if selected(accepted.NamespaceSelector, namespace.Labels) &&
						selected(accepted.PodSelector, pod.Labels) && selected(accepted.DeploymentSelector, deployment.Labels) {
						approved = true
						policy.AllowUnhealthyDeletion = policy.AllowUnhealthyDeletion && accepted.AllowUnhealthyDeletion
						policy.AllowEmptyDir = policy.AllowEmptyDir && accepted.AllowEmptyDir
						break
					}
				}
				if !approved {
					return nil, nil, fmt.Errorf("workload is outside the accepted plan policy")
				}
			}
			for _, volume := range pod.Spec.Volumes {
				if volume.EmptyDir != nil && !policy.AllowEmptyDir {
					return nil, nil, fmt.Errorf("emptyDir removal is not permitted by workload policy")
				}
			}
			return deployment, &policy, nil
		}
	}
	return nil, nil, fmt.Errorf("no matching workload policy")
}

func permittedDaemonSet(ctx context.Context, client kubernetes.Interface, pod *corev1.Pod, cfg Config) (bool, error) {
	owner := metav1.GetControllerOf(pod)
	if owner == nil || owner.Kind != "DaemonSet" || owner.APIVersion != "apps/v1" {
		return false, nil
	}
	if !slices.Contains(cfg.DaemonSets, pod.Namespace+"/"+owner.Name) ||
		(cfg.hasAcceptedPolicy && !slices.Contains(cfg.acceptedDaemonSets, pod.Namespace+"/"+owner.Name)) {
		return false, fmt.Errorf("DaemonSet is not explicitly permitted at Node deletion")
	}
	ds, err := client.AppsV1().DaemonSets(pod.Namespace).Get(ctx, owner.Name, metav1.GetOptions{})
	if err != nil {
		return false, err
	}
	if ds.UID != owner.UID || ds.DeletionTimestamp != nil {
		return false, fmt.Errorf("DaemonSet owner identity changed")
	}
	return true, nil
}
