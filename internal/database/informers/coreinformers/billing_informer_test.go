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

package coreinformers

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-logr/logr/testr"
	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/require"

	"k8s.io/client-go/tools/cache"
	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"

	"github.com/Azure/ARO-HCP/internal/apitesting/coreapitesting"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/billingcosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type versionedBillingTestLister struct {
	current atomic.Pointer[billingcosmosstorage.BillingDocument]
}

func (l *versionedBillingTestLister) List(context.Context, *cosmosstorageutils.DBClientListResourceDocsOptions) (cosmosstorageutils.DBClientIterator[billingcosmosstorage.BillingDocument], error) {
	return snapshotTestLister[billingcosmosstorage.BillingDocument]{item: l.current.Load()}, nil
}

func TestBillingInformerVersionedUpdates(t *testing.T) {
	for _, tc := range []struct {
		name         string
		initialETag  azcore.ETag
		updatedETag  azcore.ETag
		wantVersions [2]string
	}{
		{
			name:         "existing version changes",
			initialETag:  azcore.ETag(`"version-1"`),
			updatedETag:  azcore.ETag(`"version-2"`),
			wantVersions: [2]string{`"version-1"`, `"version-2"`},
		},
		{
			name:         "initially missing version becomes available",
			initialETag:  "",
			updatedETag:  azcore.ETag(`"version-2"`),
			wantVersions: [2]string{"", `"version-2"`},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(utils.ContextWithLogger(t.Context(), testr.New(t)), 15*time.Second)
			defer cancel()
			initial := billingcosmosstorage.NewBillingDocument("cluster-uid", mustParseResourceID(t, coreapitesting.TestClusterResourceID))
			initial.CosmosETag = tc.initialETag
			initial.ManagedResourceGroup = "old-rg"
			lister := &versionedBillingTestLister{}
			lister.current.Store(initial)
			informer := NewBillingInformerWithRelistDuration(lister, 100*time.Millisecond)
			updates := make(chan [2]string, 10)
			registration, err := informer.AddEventHandlerWithOptions(cache.ResourceEventHandlerFuncs{
				UpdateFunc: func(oldObj, newObj any) {
					oldDoc := oldObj.(*billingcosmosstorage.BillingDocument)
					newDoc := newObj.(*billingcosmosstorage.BillingDocument)
					select {
					case updates <- [2]string{oldDoc.GetObjectMeta().GetResourceVersion(), newDoc.GetObjectMeta().GetResourceVersion()}:
					case <-ctx.Done():
					}
				},
			}, cache.HandlerOptions{ResyncPeriod: ptr.To(time.Duration(0))})
			require.NoError(t, err)
			done := make(chan struct{})
			go func() { defer close(done); informer.RunWithContext(ctx) }()
			defer func() { cancel(); <-done }()
			require.True(t, cache.WaitForCacheSync(ctx.Done(), registration.HasSynced))

			changed := initial.DeepCopy()
			changed.CosmosETag = tc.updatedETag
			changed.ManagedResourceGroup = "managed-rg"
			lister.current.Store(changed)
			for {
				select {
				case got := <-updates:
					if got[1] != tc.wantVersions[1] {
						continue
					}
					if diff := cmp.Diff(tc.wantVersions, got); diff != "" {
						t.Fatalf("resource versions (-want +got):\n%s", diff)
					}
					return
				case <-ctx.Done():
					t.Fatal("changed billing document did not notify listener with resync disabled")
				}
			}
		})
	}
}
