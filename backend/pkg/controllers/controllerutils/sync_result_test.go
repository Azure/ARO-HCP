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

package controllerutils

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"k8s.io/client-go/util/workqueue"

	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
)

func TestHandleSyncResult_ErrorIgnoresRequeueAfter(t *testing.T) {
	queue := workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[string]())
	defer queue.ShutDown()

	queue.Add("key")
	key, shutdown := queue.Get()
	require.False(t, shutdown)

	HandleSyncResult(queue, key, controllerutil.RequeueAfterDuration(time.Hour), errors.New("boom"))
	queue.Done(key)

	require.Eventually(t, func() bool {
		return queue.Len() > 0
	}, time.Second, 10*time.Millisecond)
}

func TestHandleSyncResult_SuccessHonorsRequeueAfter(t *testing.T) {
	queue := workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[string]())
	defer queue.ShutDown()

	queue.Add("key")
	key, shutdown := queue.Get()
	require.False(t, shutdown)

	HandleSyncResult(queue, key, controllerutil.RequeueAfterDuration(time.Hour), nil)
	queue.Done(key)

	time.Sleep(20 * time.Millisecond)
	require.Equal(t, 0, queue.Len())
}
