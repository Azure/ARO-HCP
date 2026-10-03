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

package rollout

import (
	"context"
	"sync"
	"time"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/utils/clock"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// Producers run independently of each other and of the homogeneous worker queue.
// A producer failure is logged and retried on its next tick.
type periodicRolloutController[T comparable] struct {
	*controllerutil.TypedController[T]
	producers    []func(context.Context) error
	clock        clock.WithTicker
	producerName string
}

func newPeriodicRolloutController[T comparable](name string, sync func(context.Context, T) error, producers ...func(context.Context) error) *periodicRolloutController[T] {
	return &periodicRolloutController[T]{
		TypedController: controllerutil.NewTypedController(name, sync, controllerutils.ReconcileTotal),
		producers:       producers,
		clock:           clock.RealClock{},
		producerName:    name,
	}
}

func (c *periodicRolloutController[T]) Run(ctx context.Context, workers int) {
	defer utilruntime.HandleCrash()
	ctx, cancel := context.WithCancel(ctx)
	var producers sync.WaitGroup
	defer producers.Wait()
	defer cancel()
	if c.WaitForCacheSync(ctx) {
		producerCtx := utils.ContextWithControllerName(ctx, c.producerName)
		producerCtx = utils.ContextWithLogger(producerCtx, utils.LoggerFromContext(ctx).WithValues(utils.LogValues{}.AddControllerName(c.producerName)...))
		for _, produce := range c.producers {
			producers.Add(1)
			go func() {
				defer utilruntime.HandleCrash()
				defer producers.Done()
				// Keep a fixed cadence, but never overlap calls to a slow producer.
				ticker := c.clock.NewTicker(5 * time.Minute)
				defer ticker.Stop()
				for ctx.Err() == nil {
					if err := produce(producerCtx); err != nil {
						utilruntime.HandleErrorWithContext(producerCtx, err, "Rollout producer failed; retrying on next tick")
					}
					select {
					case <-ctx.Done():
						return
					case <-ticker.C():
					}
				}
			}()
		}
	}
	c.TypedController.Run(ctx, workers)
}
