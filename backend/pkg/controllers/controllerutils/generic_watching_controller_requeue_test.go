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
	"context"
	"errors"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/require"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestProcessNextWorkItem_RequeueAfterIgnoredWithError(t *testing.T) {
	testErr := errors.New("sync failed")

	syncer := &configurableStringSyncer{
		syncOnce: func(context.Context, string) (controllerutil.SyncResult, error) {
			return controllerutil.RequeueAfterDuration(time.Hour), testErr
		},
	}
	c := newGenericWatchingController("requeue-test", api.Must(azcorearm.ParseResourceID(testClusterARMID)).ResourceType, syncer)

	key := "test-key"
	c.queue.Add(key)

	ctx := utils.ContextWithLogger(context.Background(), logr.Discard())
	require.True(t, c.processNextWorkItem(ctx))

	require.Eventually(t, func() bool {
		return c.queue.Len() > 0
	}, time.Second, 10*time.Millisecond, "error path should rate-limit retry and ignore RequeueAfter")
	_, shutdown := c.queue.Get()
	require.False(t, shutdown)
	c.queue.Done(key)
	c.queue.Forget(key)
}

func TestProcessNextWorkItem_ErrorWithoutRequeueAfterUsesRateLimiter(t *testing.T) {
	syncer := &configurableStringSyncer{
		syncOnce: func(context.Context, string) (controllerutil.SyncResult, error) {
			return controllerutil.SyncResult{}, errors.New("sync failed")
		},
	}
	c := newGenericWatchingController("rate-limit-test", api.Must(azcorearm.ParseResourceID(testClusterARMID)).ResourceType, syncer)

	key := "test-key"
	c.queue.Add(key)

	ctx := utils.ContextWithLogger(context.Background(), logr.Discard())
	require.True(t, c.processNextWorkItem(ctx))

	require.Eventually(t, func() bool {
		return c.queue.Len() > 0
	}, time.Second, 10*time.Millisecond, "rate-limited error retry should requeue quickly")
	_, shutdown := c.queue.Get()
	require.False(t, shutdown)
	c.queue.Done(key)
	c.queue.Forget(key)
	require.Equal(t, key, key)
}

func TestProcessNextWorkItem_RequeueAfterHonoredWithoutError(t *testing.T) {
	syncer := &configurableStringSyncer{
		syncOnce: func(context.Context, string) (controllerutil.SyncResult, error) {
			return controllerutil.RequeueAfterDuration(time.Hour), nil
		},
	}
	c := newGenericWatchingController("success-delay-test", api.Must(azcorearm.ParseResourceID(testClusterARMID)).ResourceType, syncer)

	key := "test-key"
	c.queue.Add(key)

	ctx := utils.ContextWithLogger(context.Background(), logr.Discard())
	require.True(t, c.processNextWorkItem(ctx))

	time.Sleep(20 * time.Millisecond)
	require.Equal(t, 0, c.queue.Len())
}

type configurableStringSyncer struct {
	syncOnce func(context.Context, string) (controllerutil.SyncResult, error)
	cooldown controllerutil.CooldownChecker
}

func (s *configurableStringSyncer) MakeKey(rid *azcorearm.ResourceID) string {
	if rid == nil {
		return ""
	}
	return rid.String()
}

func (s *configurableStringSyncer) SyncOnce(ctx context.Context, key string) (controllerutil.SyncResult, error) {
	if s.syncOnce != nil {
		return s.syncOnce(ctx, key)
	}
	return controllerutil.SyncResult{}, nil
}

func (s *configurableStringSyncer) CooldownChecker() controllerutil.CooldownChecker {
	if s.cooldown != nil {
		return s.cooldown
	}
	return alwaysAllowCooldown{}
}
