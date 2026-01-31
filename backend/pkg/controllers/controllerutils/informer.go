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

package controllerutils

import (
	"context"
	"sync"

	"k8s.io/client-go/tools/cache"
)

type Informer[InternalAPIType any] interface {
	AddEventHandler(handler cache.ResourceEventHandlerDetailedFuncs)
	Run(ctx context.Context)
	HasSynced() bool

	// Temporary; until informers learn to sync for themselves.
	Sync(list []InternalAPIType, keyFunc cache.KeyFunc) error
}

type dumbInformerMap[InternalAPIType any] map[string]InternalAPIType

// dumbInformer is an Informer that must be spoon fed data
// to sync rather than it periodically syncing on its own.
type dumbInformer[InternalAPIType any] struct {
	mu       sync.Mutex
	ch       chan dumbInformerMap[InternalAPIType]
	handlers []cache.ResourceEventHandlerDetailedFuncs
	previous dumbInformerMap[InternalAPIType]
}

func NewDumbInformer[InternalAPIType any]() Informer[InternalAPIType] {
	return &dumbInformer[InternalAPIType]{
		ch: make(chan dumbInformerMap[InternalAPIType]),
	}
}

func (i *dumbInformer[InternalAPIType]) AddEventHandler(handler cache.ResourceEventHandlerDetailedFuncs) {
	i.mu.Lock()
	defer i.mu.Unlock()

	i.handlers = append(i.handlers, handler)

	if i.previous != nil && handler.AddFunc != nil {
		for _, newObj := range i.previous {
			handler.AddFunc(newObj, true)
		}
	}
}

func (i *dumbInformer[InternalAPIType]) handleSync(syncData dumbInformerMap[InternalAPIType]) {
	i.mu.Lock()
	defer i.mu.Unlock()

	if i.previous == nil {
		for _, newObj := range syncData {
			for _, handler := range i.handlers {
				if handler.AddFunc != nil {
					handler.AddFunc(newObj, true)
				}
			}
		}
	} else {
		for key, newObj := range syncData {
			oldObj, found := i.previous[key]
			if found {
				for _, handler := range i.handlers {
					if handler.UpdateFunc != nil {
						handler.UpdateFunc(oldObj, newObj)
					}
				}
				delete(i.previous, key)
			} else {
				for _, handler := range i.handlers {
					if handler.AddFunc != nil {
						handler.AddFunc(newObj, false)
					}
				}
			}
		}

		for _, oldObj := range i.previous {
			for _, handler := range i.handlers {
				if handler.DeleteFunc != nil {
					handler.DeleteFunc(oldObj)
				}
			}
		}
	}

	i.previous = syncData
}

func (i *dumbInformer[InternalAPIType]) Run(ctx context.Context) {
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case syncData := <-i.ch:
				i.handleSync(syncData)
			}
		}
	}()
}

func (i *dumbInformer[InternalAPIType]) HasSynced() bool {
	return i.previous != nil
}

func (i *dumbInformer[InternalAPIType]) Sync(list []InternalAPIType, keyFunc cache.KeyFunc) error {
	syncData := make(dumbInformerMap[InternalAPIType])

	for _, obj := range list {
		key, err := keyFunc(obj)
		if err != nil {
			return cache.KeyError{Obj: obj, Err: err}
		}
		syncData[key] = obj
	}

	i.ch <- syncData

	return nil
}
