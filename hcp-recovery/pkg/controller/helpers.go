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
	"fmt"

	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
)

// registerInformer sets up event handlers on an informer that enqueue items to a workqueue.
func registerInformer[T comparable](informer cache.SharedIndexInformer, keyFunc func(obj interface{}) (T, error), workQueue workqueue.TypedRateLimitingInterface[T]) error {
	_, err := informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			key, err := keyFunc(obj)
			if err != nil {
				return
			}
			workQueue.Add(key)
		},
		UpdateFunc: func(old, new interface{}) {
			key, err := keyFunc(new)
			if err != nil {
				return
			}
			workQueue.Add(key)
		},
		DeleteFunc: func(obj interface{}) {
			key, err := keyFunc(obj)
			if err != nil {
				return
			}
			workQueue.Add(key)
		},
	})
	if err != nil {
		return fmt.Errorf("failed to add event handler for informer: %w", err)
	}
	return nil
}

// keyForObject extracts the object namespace/name workqueue key from objects.
func keyForObject(obj interface{}) (cache.ObjectName, error) {
	key, err := cache.DeletionHandlingObjectToName(obj)
	if err != nil {
		return cache.ObjectName{}, fmt.Errorf("could not determine queue key: %w", err)
	}
	return key, nil
}

// TODO: Add additional key extraction functions as needed, e.g.:
// - Key from owner reference (for owned resources like Secrets)
// - Key from annotation (for cross-cluster resources)
// - Index key functions for custom informer indexes
