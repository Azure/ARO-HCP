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

package cincinnati

import (
	"context"
	"fmt"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blang/semver/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	clocktesting "k8s.io/utils/clock/testing"

	configv1 "github.com/openshift/api/config/v1"
)

type countingClient struct {
	inner Client
	calls atomic.Int64
}

func (c *countingClient) GetUpdates(ctx context.Context, uri *url.URL, desiredArch, currentArch, channel string, version semver.Version) (configv1.Release, []configv1.Release, []configv1.ConditionalUpdate, error) {
	c.calls.Add(1)
	return c.inner.GetUpdates(ctx, uri, desiredArch, currentArch, channel, version)
}

type staticClient struct {
	current configv1.Release
	updates []configv1.Release
	err     error
}

func (c *staticClient) GetUpdates(context.Context, *url.URL, string, string, string, semver.Version) (configv1.Release, []configv1.Release, []configv1.ConditionalUpdate, error) {
	return c.current, c.updates, nil, c.err
}

func TestCachingGetUpdates_CacheHit(t *testing.T) {
	t.Parallel()

	fakeClock := clocktesting.NewFakeClock(time.Now())
	inner := &countingClient{inner: &staticClient{
		current: configv1.Release{Version: "4.19.0"},
		updates: []configv1.Release{{Version: "4.19.5"}},
	}}
	client := NewCachingClient(inner, fakeClock, time.Hour)
	ctx := context.Background()
	uri, _ := url.Parse("http://localhost")

	current1, updates1, _, err := client.GetUpdates(ctx, uri, "multi", "multi", "stable-4.19", semver.MustParse("4.19.0"))
	require.NoError(t, err)
	assert.Equal(t, "4.19.0", current1.Version)
	assert.Len(t, updates1, 1)
	assert.Equal(t, int64(1), inner.calls.Load())

	current2, updates2, _, err := client.GetUpdates(ctx, uri, "multi", "multi", "stable-4.19", semver.MustParse("4.19.0"))
	require.NoError(t, err)
	assert.Equal(t, current1, current2)
	assert.Equal(t, updates1, updates2)
	assert.Equal(t, int64(1), inner.calls.Load())
}

func TestCachingGetUpdates_CacheExpiry(t *testing.T) {
	t.Parallel()

	fakeClock := clocktesting.NewFakeClock(time.Now())
	inner := &countingClient{inner: &staticClient{
		current: configv1.Release{Version: "4.19.0"},
		updates: []configv1.Release{{Version: "4.19.5"}},
	}}
	client := NewCachingClient(inner, fakeClock, time.Hour)
	ctx := context.Background()
	uri, _ := url.Parse("http://localhost")

	_, _, _, err := client.GetUpdates(ctx, uri, "multi", "multi", "stable-4.19", semver.MustParse("4.19.0"))
	require.NoError(t, err)
	assert.Equal(t, int64(1), inner.calls.Load())

	fakeClock.Step(59 * time.Minute)
	_, _, _, err = client.GetUpdates(ctx, uri, "multi", "multi", "stable-4.19", semver.MustParse("4.19.0"))
	require.NoError(t, err)
	assert.Equal(t, int64(1), inner.calls.Load())

	fakeClock.Step(2 * time.Minute)
	_, _, _, err = client.GetUpdates(ctx, uri, "multi", "multi", "stable-4.19", semver.MustParse("4.19.0"))
	require.NoError(t, err)
	assert.Equal(t, int64(2), inner.calls.Load())
}

func TestCachingGetUpdates_DifferentParams(t *testing.T) {
	t.Parallel()

	fakeClock := clocktesting.NewFakeClock(time.Now())
	inner := &countingClient{inner: &staticClient{
		current: configv1.Release{Version: "4.19.0"},
	}}
	client := NewCachingClient(inner, fakeClock, time.Hour)
	ctx := context.Background()
	uri, _ := url.Parse("http://localhost")

	_, _, _, _ = client.GetUpdates(ctx, uri, "multi", "multi", "stable-4.19", semver.MustParse("4.19.0"))
	_, _, _, _ = client.GetUpdates(ctx, uri, "multi", "multi", "stable-4.19", semver.MustParse("4.19.5"))
	_, _, _, _ = client.GetUpdates(ctx, uri, "multi", "multi", "stable-4.20", semver.MustParse("4.19.0"))

	assert.Equal(t, int64(3), inner.calls.Load())

	_, _, _, _ = client.GetUpdates(ctx, uri, "multi", "multi", "stable-4.19", semver.MustParse("4.19.0"))
	_, _, _, _ = client.GetUpdates(ctx, uri, "multi", "multi", "stable-4.19", semver.MustParse("4.19.5"))
	_, _, _, _ = client.GetUpdates(ctx, uri, "multi", "multi", "stable-4.20", semver.MustParse("4.19.0"))

	assert.Equal(t, int64(3), inner.calls.Load())
}

func TestCachingGetUpdates_CachesErrors(t *testing.T) {
	t.Parallel()

	fakeClock := clocktesting.NewFakeClock(time.Now())
	inner := &countingClient{inner: &staticClient{
		err: fmt.Errorf("version not found"),
	}}
	client := NewCachingClient(inner, fakeClock, time.Hour)
	ctx := context.Background()
	uri, _ := url.Parse("http://localhost")

	_, _, _, err1 := client.GetUpdates(ctx, uri, "multi", "multi", "stable-4.19", semver.MustParse("4.18.0"))
	require.Error(t, err1)
	assert.Equal(t, int64(1), inner.calls.Load())

	_, _, _, err2 := client.GetUpdates(ctx, uri, "multi", "multi", "stable-4.19", semver.MustParse("4.18.0"))
	require.Error(t, err2)
	assert.Equal(t, err1, err2)
	assert.Equal(t, int64(1), inner.calls.Load())
}

func TestCachingGetUpdates_ConcurrentAccess(t *testing.T) {
	t.Parallel()

	fakeClock := clocktesting.NewFakeClock(time.Now())
	inner := &countingClient{inner: &staticClient{
		current: configv1.Release{Version: "4.19.0"},
		updates: []configv1.Release{{Version: "4.19.5"}},
	}}
	client := NewCachingClient(inner, fakeClock, time.Hour)
	ctx := context.Background()
	uri, _ := url.Parse("http://localhost")

	var wg sync.WaitGroup
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, _, err := client.GetUpdates(ctx, uri, "multi", "multi", "stable-4.19", semver.MustParse("4.19.0"))
			assert.NoError(t, err)
		}()
	}
	wg.Wait()

	assert.LessOrEqual(t, inner.calls.Load(), int64(50))
	assert.GreaterOrEqual(t, inner.calls.Load(), int64(1))
}
