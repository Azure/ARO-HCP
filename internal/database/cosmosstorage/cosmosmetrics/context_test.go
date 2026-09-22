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

package cosmosmetrics

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestContextDefaults(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	name, ok := InformerNameFromContext(ctx)
	require.Empty(t, name)
	require.False(t, ok)
	name, ok = InformerNameFromContext(ContextWithInformerName(ctx, ""))
	require.Empty(t, name)
	require.True(t, ok, "an explicitly empty informer name remains present")
	for _, ctx := range []context.Context{ctx, contextWithQuery(ContextWithCallSite(ctx, ""), "", "")} {
		require.Equal(t, "unknown", CallSiteFromContext(ctx))
		shape, scope := queryFromContext(ctx)
		require.Equal(t, "none", shape)
		require.Equal(t, "none", scope)
	}
	shape, scope := queryFromContext(contextWithQuery(ctx, "by_type", ""))
	require.Equal(t, "by_type", shape)
	require.Equal(t, "none", scope)
	shape, scope = queryFromContext(contextWithQuery(ctx, "", "partition"))
	require.Equal(t, "none", shape)
	require.Equal(t, "partition", scope)
}

func TestContextPreservesParent(t *testing.T) {
	t.Parallel()
	type parentKey struct{}
	deadline := time.Now().Add(time.Minute)
	parent, cancel := context.WithDeadline(context.WithValue(context.Background(), parentKey{}, "parent-value"), deadline)
	defer cancel()
	ctx := ContextWithInformerName(parent, "ClusterInformer")
	ctx = ContextWithCallSite(ctx, "list_clusters")
	ctx = contextWithQuery(ctx, "by_type", "partition")
	require.Equal(t, "parent-value", ctx.Value(parentKey{}))
	actualDeadline, ok := ctx.Deadline()
	require.True(t, ok)
	require.Equal(t, deadline, actualDeadline)
	name, ok := InformerNameFromContext(ctx)
	require.True(t, ok)
	require.Equal(t, "ClusterInformer", name)
	require.Equal(t, "list_clusters", CallSiteFromContext(ctx))
	shape, scope := queryFromContext(ctx)
	require.Equal(t, "by_type", shape)
	require.Equal(t, "partition", scope)
	require.Equal(t, "unknown", CallSiteFromContext(parent), "derived values must not change the parent")
	require.Equal(t, "get_cluster", CallSiteFromContext(ContextWithCallSite(ctx, "get_cluster")))
	require.Equal(t, "list_clusters", CallSiteFromContext(ctx), "overrides must not change the parent")
	cancel()
	require.Equal(t, parent.Done(), ctx.Done())
	require.ErrorIs(t, ctx.Err(), context.Canceled)
}
