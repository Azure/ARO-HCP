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

	appsv1 "k8s.io/api/apps/v1"
	appsinformers "k8s.io/client-go/informers/apps/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
)

// DeploymentWatcher watches Deployment resources using a typed informer and
// logs create, update, and delete events via structured logging. All updates
// are logged because Deployments are low-volume and all changes (spec, status,
// conditions) are diagnostically valuable.
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
			if !ok {
				return
			}
			logDeploymentEvent("Add", deploy)
		},
		UpdateFunc: func(_, newObj interface{}) {
			deploy, ok := newObj.(*appsv1.Deployment)
			if !ok {
				return
			}
			logDeploymentEvent("Update", deploy)
		},
		DeleteFunc: func(obj interface{}) {
			if tombstone, ok := obj.(cache.DeletedFinalStateUnknown); ok {
				obj = tombstone.Obj
			}
			deploy, ok := obj.(*appsv1.Deployment)
			if !ok {
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

// logDeploymentEvent logs a Deployment event with structured key-value pairs
// including the full Deployment object. A shallow copy is made so that setting
// TypeMeta (which typed informers leave empty) does not mutate the shared cache
// object.
func logDeploymentEvent(eventType string, deploy *appsv1.Deployment) {
	deployCopy := *deploy
	deployCopy.SetGroupVersionKind(appsv1.SchemeGroupVersion.WithKind("Deployment"))
	klog.InfoS("deployment event",
		"snapshotType", "kubernetes",
		"event", eventType,
		"namespace", deployCopy.Namespace,
		"name", deployCopy.Name,
		"object", &deployCopy,
	)
}
