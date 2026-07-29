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

// The HyperShift router ConfigMap has no distinguishing labels, so we select by name.
const RouterConfigMapName = "router"

// ConfigMapWatcher watches ConfigMap resources using a typed informer and logs
// create, update, and delete events via structured logging. It is intended to
// be used with a field-selector-scoped informer factory so that only ConfigMaps
// with a specific name (e.g. "router") are watched.
type ConfigMapWatcher struct {
	cmSynced cache.InformerSynced
}

// NewConfigMapWatcher creates a new ConfigMapWatcher. It registers event handlers
// on the given ConfigMap informer to log ConfigMap lifecycle events.
func NewConfigMapWatcher(cmInformer coreinformers.ConfigMapInformer) (*ConfigMapWatcher, error) {
	w := &ConfigMapWatcher{
		cmSynced: cmInformer.Informer().HasSynced,
	}

	if _, err := cmInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			cm, ok := obj.(*corev1.ConfigMap)
			if !ok {
				return
			}
			logConfigMapEvent("Add", cm)
		},
		UpdateFunc: func(_, newObj interface{}) {
			cm, ok := newObj.(*corev1.ConfigMap)
			if !ok {
				return
			}
			logConfigMapEvent("Update", cm)
		},
		DeleteFunc: func(obj interface{}) {
			if tombstone, ok := obj.(cache.DeletedFinalStateUnknown); ok {
				obj = tombstone.Obj
			}
			cm, ok := obj.(*corev1.ConfigMap)
			if !ok {
				return
			}
			logConfigMapEvent("Delete", cm)
		},
	}); err != nil {
		return nil, fmt.Errorf("failed to add event handler: %w", err)
	}

	return w, nil
}

// Run waits for the ConfigMap informer cache to sync and blocks until the
// context is cancelled.
func (w *ConfigMapWatcher) Run(ctx context.Context) error {
	logger := klog.FromContext(ctx)
	logger.Info("Starting ConfigMap watcher")

	logger.Info("Waiting for ConfigMap informer cache to sync")
	if ok := cache.WaitForCacheSync(ctx.Done(), w.cmSynced); !ok {
		return fmt.Errorf("failed to wait for ConfigMap informer cache to sync")
	}

	logger.Info("ConfigMap watcher informer synced and running")
	<-ctx.Done()
	logger.Info("Shutting down ConfigMap watcher")
	return nil
}

func logConfigMapEvent(eventType string, cm *corev1.ConfigMap) {
	cm.SetGroupVersionKind(corev1.SchemeGroupVersion.WithKind("ConfigMap"))
	klog.InfoS("configmap event",
		"event", eventType,
		"namespace", cm.Namespace,
		"name", cm.Name,
		"object", cm,
	)
}
