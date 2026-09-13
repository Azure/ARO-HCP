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
	"reflect"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	appsinformers "k8s.io/client-go/informers/apps/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
)

const (
	hypershiftNamespace   = "hypershift"
	hostedClusterNSPrefix = "ocm-"
)

// DeploymentWatcher watches Deployment resources using a typed informer and
// logs create, update, and delete events via structured logging. Only
// Deployments in the hypershift namespace or hosted cluster namespaces
// (ocm-* prefix) are logged.
type DeploymentWatcher struct {
	deploymentSynced cache.InformerSynced
}

// NewDeploymentWatcher creates a new DeploymentWatcher. It registers event
// handlers on the given Deployment informer to log Deployment lifecycle events.
func NewDeploymentWatcher(deploymentInformer appsinformers.DeploymentInformer) (*DeploymentWatcher, error) {
	w := &DeploymentWatcher{
		deploymentSynced: deploymentInformer.Informer().HasSynced,
	}

	if _, err := deploymentInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			deploy, ok := obj.(*appsv1.Deployment)
			if !ok || !isWatchedNamespace(deploy.Namespace) {
				return
			}
			logDeploymentEvent("Add", deploy)
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			oldDeploy, ok := oldObj.(*appsv1.Deployment)
			if !ok {
				return
			}
			newDeploy, ok := newObj.(*appsv1.Deployment)
			if !ok || !isWatchedNamespace(newDeploy.Namespace) {
				return
			}
			if deploymentStateChanged(oldDeploy, newDeploy) {
				logDeploymentEvent("Update", newDeploy)
			}
		},
		DeleteFunc: func(obj interface{}) {
			if tombstone, ok := obj.(cache.DeletedFinalStateUnknown); ok {
				obj = tombstone.Obj
			}
			deploy, ok := obj.(*appsv1.Deployment)
			if !ok || !isWatchedNamespace(deploy.Namespace) {
				return
			}
			logDeploymentEvent("Delete", deploy)
		},
	}); err != nil {
		return nil, fmt.Errorf("failed to add event handler: %w", err)
	}

	return w, nil
}

// Run waits for the Deployment informer cache to sync and blocks until the
// context is cancelled.
func (w *DeploymentWatcher) Run(ctx context.Context) error {
	logger := klog.FromContext(ctx)
	logger.Info("Starting deployment watcher")

	logger.Info("Waiting for deployment informer cache to sync")
	if ok := cache.WaitForCacheSync(ctx.Done(), w.deploymentSynced); !ok {
		return fmt.Errorf("failed to wait for deployment informer cache to sync")
	}

	logger.Info("Deployment watcher informer synced and running")
	<-ctx.Done()
	logger.Info("Shutting down deployment watcher")
	return nil
}

func isWatchedNamespace(ns string) bool {
	return ns == hypershiftNamespace || strings.HasPrefix(ns, hostedClusterNSPrefix)
}

// deploymentStateChanged returns true if diagnostically meaningful fields
// changed between old and new. It ignores noisy fields like condition
// timestamps, resourceVersion, and managedFields.
func deploymentStateChanged(oldDeploy, newDeploy *appsv1.Deployment) bool {
	oldStatus := oldDeploy.Status
	newStatus := newDeploy.Status

	if oldStatus.ObservedGeneration != newStatus.ObservedGeneration ||
		oldStatus.Replicas != newStatus.Replicas ||
		oldStatus.UpdatedReplicas != newStatus.UpdatedReplicas ||
		oldStatus.ReadyReplicas != newStatus.ReadyReplicas ||
		oldStatus.AvailableReplicas != newStatus.AvailableReplicas ||
		oldStatus.UnavailableReplicas != newStatus.UnavailableReplicas {
		return true
	}

	if !conditionStatusMapEqual(oldStatus.Conditions, newStatus.Conditions) {
		return true
	}

	if !reflect.DeepEqual(oldDeploy.Spec.Template, newDeploy.Spec.Template) {
		return true
	}

	return false
}

// conditionStatusMapEqual returns true if both slices have the same set of
// condition Type→Status pairs. It ignores LastUpdateTime, LastTransitionTime,
// Reason, and Message — those change frequently without diagnostic value.
func conditionStatusMapEqual(a, b []appsv1.DeploymentCondition) bool {
	if len(a) != len(b) {
		return false
	}
	aMap := make(map[appsv1.DeploymentConditionType]corev1.ConditionStatus, len(a))
	for _, c := range a {
		aMap[c.Type] = c.Status
	}
	for _, c := range b {
		if aMap[c.Type] != c.Status {
			return false
		}
	}
	return true
}

func logDeploymentEvent(eventType string, deploy *appsv1.Deployment) {
	deployCopy := *deploy
	deployCopy.SetGroupVersionKind(appsv1.SchemeGroupVersion.WithKind("Deployment"))
	deployCopy.ManagedFields = nil
	klog.InfoS("deployment event",
		"snapshotType", "kubernetes",
		"event", eventType,
		"namespace", deployCopy.Namespace,
		"name", deployCopy.Name,
		"object", &deployCopy,
	)
}
