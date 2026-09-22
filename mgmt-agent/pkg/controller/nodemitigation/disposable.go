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
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

type DaemonSetPolicy struct {
	Namespace      string    `json:"namespace"`
	Name           string    `json:"name"`
	UID            types.UID `json:"uid"`
	Role           string    `json:"role"`
	TemplateSHA256 string    `json:"templateSHA256"`
	PodSpecSHA256  string    `json:"podSpecSHA256"`
}

func policyHash(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", sha256.Sum256(data)), nil
}

// DisposablePodSpecHash pins the admitted Pod spec, including injected storage
// and containers. Per-node bindings and generated service-account volume names
// are normalized; projection contents and mount properties remain pinned.
func DisposablePodSpecHash(pod *corev1.Pod) (string, error) {
	spec := pod.Spec.DeepCopy()
	spec.NodeName = ""
	tokenVolume := ""
	for i := range spec.Volumes {
		volume := &spec.Volumes[i]
		if !strings.HasPrefix(volume.Name, "kube-api-access-") || volume.Projected == nil {
			continue
		}
		if tokenVolume != "" {
			return "", fmt.Errorf("multiple generated service-account volumes cannot be normalized")
		}
		tokenVolume = volume.Name
		volume.Name = "__KUBE_API_ACCESS__"
	}
	if tokenVolume != "" {
		for _, containers := range [][]corev1.Container{spec.Containers, spec.InitContainers} {
			for i := range containers {
				for j := range containers[i].VolumeMounts {
					if containers[i].VolumeMounts[j].Name == tokenVolume {
						containers[i].VolumeMounts[j].Name = "__KUBE_API_ACCESS__"
					}
				}
			}
		}
	}
	if spec.Affinity != nil && spec.Affinity.NodeAffinity != nil &&
		spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution != nil {
		terms := spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms
		for i := range terms {
			for j := range terms[i].MatchFields {
				field := &terms[i].MatchFields[j]
				if field.Key == "metadata.name" && field.Operator == corev1.NodeSelectorOpIn &&
					len(field.Values) == 1 && field.Values[0] == pod.Spec.NodeName {
					field.Values[0] = "__NODE_NAME__"
				}
			}
		}
	}
	return policyHash(spec)
}

func (c *Controller) disposablePod(ctx context.Context, pod *corev1.Pod, cfg Config) error {
	blocked := fmt.Errorf("pod %s/%s requires shutdown or data protection, or is not an approved disposable node-local DaemonSet", pod.Namespace, pod.Name)
	if pod.DeletionTimestamp != nil || len(pod.Finalizers) > 0 ||
		len(pod.Spec.EphemeralContainers) > 0 || len(pod.Spec.ResourceClaims) > 0 {
		return blocked
	}
	for _, key := range []string{corev1.MirrorPodAnnotationKey, "kubernetes.io/config.source"} {
		if _, exists := pod.Annotations[key]; exists {
			return blocked
		}
	}
	needsStorageApproval := false
	for _, volume := range pod.Spec.Volumes {
		if volume.ConfigMap == nil && volume.Secret == nil && volume.Projected == nil &&
			volume.DownwardAPI == nil && volume.EmptyDir == nil && volume.HostPath == nil {
			return blocked
		}
		needsStorageApproval = needsStorageApproval || volume.EmptyDir != nil || volume.HostPath != nil
	}
	for _, containers := range [][]corev1.Container{pod.Spec.Containers, pod.Spec.InitContainers} {
		for _, container := range containers {
			if container.Lifecycle != nil && container.Lifecycle.PreStop != nil {
				return blocked
			}
		}
	}
	if terminal(pod) && !needsStorageApproval {
		return nil
	}
	owner := metav1.GetControllerOf(pod)
	if owner == nil || owner.APIVersion != "apps/v1" || owner.Kind != "DaemonSet" || owner.UID == "" {
		return blocked
	}
	for _, policy := range cfg.DisposableDaemonSets {
		if policy.Namespace != pod.Namespace || policy.Name != owner.Name || policy.UID != owner.UID {
			continue
		}
		ds, err := c.kube.AppsV1().DaemonSets(pod.Namespace).Get(ctx, owner.Name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("verify disposable DaemonSet owner: %w", err)
		}
		if ds.UID != owner.UID || ds.DeletionTimestamp != nil {
			return blocked
		}
		templateHash, err := policyHash(ds.Spec.Template)
		if err != nil {
			return err
		}
		podHash, err := DisposablePodSpecHash(pod)
		if err != nil {
			return err
		}
		if templateHash == policy.TemplateSHA256 && podHash == policy.PodSpecSHA256 {
			return nil
		}
		return fmt.Errorf("%w: approved template or admitted Pod spec changed", blocked)
	}
	return blocked
}
