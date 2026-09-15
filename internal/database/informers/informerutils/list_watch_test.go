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

package informerutils

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/tools/cache"

	"github.com/Azure/ARO-HCP/internal/utils"
)

// TestListWatchWithoutWatchListSemanticsAttribution is a direct, synchronous
// call into the generic attribution mechanism every Cosmos-backed informer
// shares (ListWatchWithoutWatchListSemantics), with no reflector, informer,
// or relist machinery involved. It is the single place that needs to prove
// the wrapper attaches InformerName and preserves the caller's context.
func TestListWatchWithoutWatchListSemanticsAttribution(t *testing.T) {
	t.Parallel()
	var observed []context.Context
	lw := ListWatchWithoutWatchListSemantics{
		ListWatch: &cache.ListWatch{
			ListWithContextFunc: func(ctx context.Context, _ metav1.ListOptions) (runtime.Object, error) {
				observed = append(observed, ctx)
				return &metav1.List{}, nil
			},
			WatchFuncWithContext: func(ctx context.Context, _ metav1.ListOptions) (watch.Interface, error) {
				observed = append(observed, ctx)
				return watch.NewEmptyWatch(), nil
			},
		},
		InformerName: "ClusterInformer",
	}

	parent, cancel := context.WithTimeout(utils.ContextWithControllerName(context.Background(), "ReconcileClusters"), time.Minute)
	defer cancel()

	_, err := lw.ListWithContext(parent, metav1.ListOptions{})
	require.NoError(t, err)
	_, err = lw.WatchWithContext(parent, metav1.ListOptions{})
	require.NoError(t, err)

	require.Len(t, observed, 2)
	for _, ctx := range observed {
		name, ok := InformerNameFromContext(ctx)
		require.True(t, ok)
		require.Equal(t, "ClusterInformer", name)
		controller, ok := utils.ControllerNameFromContext(ctx)
		require.True(t, ok)
		require.Equal(t, "ReconcileClusters", controller)
		deadline, ok := ctx.Deadline()
		parentDeadline, _ := parent.Deadline()
		require.True(t, ok)
		require.Equal(t, parentDeadline, deadline)
	}

	_, ok := InformerNameFromContext(parent)
	require.False(t, ok, "wrapper must not mutate the caller's context")
}

func TestListWatchWithoutWatchListSemanticsPanicsWithoutName(t *testing.T) {
	t.Parallel()
	lw := ListWatchWithoutWatchListSemantics{ListWatch: &cache.ListWatch{
		ListWithContextFunc: func(context.Context, metav1.ListOptions) (runtime.Object, error) { return &metav1.List{}, nil },
	}}
	require.Panics(t, func() { _, _ = lw.ListWithContext(context.Background(), metav1.ListOptions{}) })
}
