// Copyright 2025 Microsoft Corporation
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

package controller

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	coreinformers "k8s.io/client-go/informers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
)

// PodWatcher watches Pod resources using a typed informer and logs
// create, delete, and state-change update events via structured logging.
type PodWatcher struct {
	podSynced cache.InformerSynced
}

// NewPodWatcher creates a new PodWatcher. It registers event handlers
// on the given pod informer to log pod lifecycle events.
func NewPodWatcher(podInformer coreinformers.PodInformer) (*PodWatcher, error) {
	w := &PodWatcher{
		podSynced: podInformer.Informer().HasSynced,
	}

	if _, err := podInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			pod, ok := obj.(*corev1.Pod)
			if !ok {
				return
			}
			logPodEvent("Add", pod)
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			oldPod, ok := oldObj.(*corev1.Pod)
			if !ok {
				return
			}
			newPod, ok := newObj.(*corev1.Pod)
			if !ok {
				return
			}
			if containerStateChanged(oldPod, newPod) {
				logPodEvent("Update", newPod)
			}
		},
		DeleteFunc: func(obj interface{}) {
			if tombstone, ok := obj.(cache.DeletedFinalStateUnknown); ok {
				obj = tombstone.Obj
			}
			pod, ok := obj.(*corev1.Pod)
			if !ok {
				return
			}
			logPodEvent("Delete", pod)
		},
	}); err != nil {
		return nil, fmt.Errorf("failed to add event handler: %w", err)
	}

	return w, nil
}

// Run waits for the pod informer cache to sync and blocks until the context
// is cancelled.
func (w *PodWatcher) Run(ctx context.Context) error {
	logger := klog.FromContext(ctx)
	logger.Info("Starting pod watcher")

	logger.Info("Waiting for pod informer cache to sync")
	if ok := cache.WaitForCacheSync(ctx.Done(), w.podSynced); !ok {
		return fmt.Errorf("failed to wait for pod informer cache to sync")
	}

	logger.Info("Pod watcher informer synced and running")
	<-ctx.Done()
	logger.Info("Shutting down pod watcher")
	return nil
}

// containerStateChanged returns true if any container's state type changed
// between the old and new pod (e.g. Waiting->Running). It checks
// ContainerStatuses, InitContainerStatuses, and EphemeralContainerStatuses.
// Field-level changes within the same state type (e.g. a different
// Waiting.Reason) are not considered a change.
func containerStateChanged(oldPod, newPod *corev1.Pod) bool {
	oldStates := buildContainerStateMap(oldPod.Status.ContainerStatuses)
	newStates := buildContainerStateMap(newPod.Status.ContainerStatuses)
	if !stateMapEqual(oldStates, newStates) {
		return true
	}

	oldInitStates := buildContainerStateMap(oldPod.Status.InitContainerStatuses)
	newInitStates := buildContainerStateMap(newPod.Status.InitContainerStatuses)
	if !stateMapEqual(oldInitStates, newInitStates) {
		return true
	}

	oldEphemeralStates := buildContainerStateMap(oldPod.Status.EphemeralContainerStatuses)
	newEphemeralStates := buildContainerStateMap(newPod.Status.EphemeralContainerStatuses)
	return !stateMapEqual(oldEphemeralStates, newEphemeralStates)
}

// stateMapEqual returns true if two state maps have the same keys and values.
func stateMapEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// buildContainerStateMap builds a map of container name to state type string
// ("waiting", "running", "terminated", or "unknown") from a slice of
// ContainerStatus.
func buildContainerStateMap(statuses []corev1.ContainerStatus) map[string]string {
	m := make(map[string]string, len(statuses))
	for _, s := range statuses {
		m[s.Name] = containerStateType(s.State)
	}
	return m
}

// containerStateType returns a string identifying the active state type.
func containerStateType(state corev1.ContainerState) string {
	switch {
	case state.Running != nil:
		return "running"
	case state.Waiting != nil:
		return "waiting"
	case state.Terminated != nil:
		return "terminated"
	default:
		return "unknown"
	}
}

// logPodEvent logs a pod event with structured key-value pairs including
// the full pod object. A shallow copy is made so that setting TypeMeta
// (which typed informers leave empty) does not mutate the shared cache object.
func logPodEvent(eventType string, pod *corev1.Pod) {
	podCopy := *pod
	podCopy.SetGroupVersionKind(corev1.SchemeGroupVersion.WithKind("Pod"))
	klog.InfoS("pod event",
		"snapshotType", "kubernetes",
		"event", eventType,
		"namespace", podCopy.Namespace,
		"name", podCopy.Name,
		"object", &podCopy,
	)
}
