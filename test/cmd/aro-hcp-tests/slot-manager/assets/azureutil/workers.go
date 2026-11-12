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

package azureutil

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"golang.org/x/sync/errgroup"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
)

// RunWithWorkers runs every operation with at most workers running at once and
// joins their errors. Once ctx is done no further operation starts, and the
// context error is part of the result. Joined error order is unspecified when
// workers > 1.
func RunWithWorkers(ctx context.Context, operations []func(context.Context) error, workers int) error {
	var (
		group errgroup.Group
		lock  sync.Mutex
		errs  []error
	)
	group.SetLimit(max(workers, 1))
	for _, operation := range operations {
		if ctx.Err() != nil {
			break
		}
		group.Go(func() error {
			// Go blocks while all workers are busy, so ctx may be done by the time we run.
			if ctx.Err() != nil {
				return nil
			}
			if err := runOperation(ctx, operation); err != nil {
				lock.Lock()
				errs = append(errs, err)
				lock.Unlock()
			}
			return nil
		})
	}
	_ = group.Wait()
	errs = append(errs, ctx.Err())
	return errors.Join(errs...)
}

func runOperation(ctx context.Context, operation func(context.Context) error) (err error) {
	// Preserve fail-closed errors when ReallyCrash is false without bypassing
	// the process crash policy when it is true.
	defer utilruntime.HandleCrash(func(value interface{}) {
		err = fmt.Errorf("identity admission operation panicked: %v", value)
	})
	return operation(ctx)
}
