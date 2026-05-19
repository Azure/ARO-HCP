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

package kubeapplier_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/database/listers"
	"github.com/Azure/ARO-HCP/internal/database/listertesting"
	unionkubeapplier "github.com/Azure/ARO-HCP/internal/database/unionlisters/kubeapplier"
)

const (
	testSub      = "00000000-0000-0000-0000-000000000001"
	testRG       = "rg"
	testCluster  = "c"
	testNodePool = "np"
)

var (
	mgmtAID = api.Must(azcorearm.ParseResourceID(
		"/providers/microsoft.redhatopenshift/stamps/1/managementclusters/mgmt-a"))
	mgmtBID = api.Must(azcorearm.ParseResourceID(
		"/providers/microsoft.redhatopenshift/stamps/2/managementclusters/mgmt-b"))
	mgmtUnregistered = api.Must(azcorearm.ParseResourceID(
		"/providers/microsoft.redhatopenshift/stamps/9/managementclusters/mgmt-z"))
)

func mustParseID(t *testing.T, s string) *azcorearm.ResourceID {
	t.Helper()
	id, err := azcorearm.ParseResourceID(s)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return id
}

// --- ApplyDesire fixtures ------------------------------------------------

func newApplyDesire(t *testing.T, idStr string, mgmt *azcorearm.ResourceID) *kubeapplier.ApplyDesire {
	t.Helper()
	return &kubeapplier.ApplyDesire{
		CosmosMetadata: api.CosmosMetadata{ResourceID: mustParseID(t, idStr)},
		Spec:           kubeapplier.ApplyDesireSpec{ManagementCluster: mgmt},
	}
}

// applySublisters returns two SliceApplyDesireListers, one per MC, populated
// with disjoint fixtures so test assertions can tell them apart.
func applySublisters(t *testing.T) (a, b *listertesting.SliceApplyDesireLister) {
	t.Helper()
	a = &listertesting.SliceApplyDesireLister{
		Desires: []*kubeapplier.ApplyDesire{
			newApplyDesire(t,
				kubeapplier.ToClusterScopedApplyDesireResourceIDString(testSub, testRG, testCluster, "a1"),
				mgmtAID),
			newApplyDesire(t,
				kubeapplier.ToNodePoolScopedApplyDesireResourceIDString(testSub, testRG, testCluster, testNodePool, "a2"),
				mgmtAID),
		},
	}
	b = &listertesting.SliceApplyDesireLister{
		Desires: []*kubeapplier.ApplyDesire{
			newApplyDesire(t,
				kubeapplier.ToClusterScopedApplyDesireResourceIDString(testSub, testRG, "other-cluster", "b1"),
				mgmtBID),
		},
	}
	return a, b
}

// --- DeleteDesire fixtures -----------------------------------------------

func newDeleteDesire(t *testing.T, idStr string, mgmt *azcorearm.ResourceID) *kubeapplier.DeleteDesire {
	t.Helper()
	return &kubeapplier.DeleteDesire{
		CosmosMetadata: api.CosmosMetadata{ResourceID: mustParseID(t, idStr)},
		Spec:           kubeapplier.DeleteDesireSpec{ManagementCluster: mgmt},
	}
}

func deleteSublisters(t *testing.T) (a, b *listertesting.SliceDeleteDesireLister) {
	t.Helper()
	a = &listertesting.SliceDeleteDesireLister{
		Desires: []*kubeapplier.DeleteDesire{
			newDeleteDesire(t,
				kubeapplier.ToClusterScopedDeleteDesireResourceIDString(testSub, testRG, testCluster, "a1"),
				mgmtAID),
			newDeleteDesire(t,
				kubeapplier.ToNodePoolScopedDeleteDesireResourceIDString(testSub, testRG, testCluster, testNodePool, "a2"),
				mgmtAID),
		},
	}
	b = &listertesting.SliceDeleteDesireLister{
		Desires: []*kubeapplier.DeleteDesire{
			newDeleteDesire(t,
				kubeapplier.ToClusterScopedDeleteDesireResourceIDString(testSub, testRG, "other-cluster", "b1"),
				mgmtBID),
		},
	}
	return a, b
}

// --- ReadDesire fixtures -------------------------------------------------

func newReadDesire(t *testing.T, idStr string, mgmt *azcorearm.ResourceID) *kubeapplier.ReadDesire {
	t.Helper()
	return &kubeapplier.ReadDesire{
		CosmosMetadata: api.CosmosMetadata{ResourceID: mustParseID(t, idStr)},
		Spec:           kubeapplier.ReadDesireSpec{ManagementCluster: mgmt},
	}
}

func readSublisters(t *testing.T) (a, b *listertesting.SliceReadDesireLister) {
	t.Helper()
	a = &listertesting.SliceReadDesireLister{
		Desires: []*kubeapplier.ReadDesire{
			newReadDesire(t,
				kubeapplier.ToClusterScopedReadDesireResourceIDString(testSub, testRG, testCluster, "a1"),
				mgmtAID),
			newReadDesire(t,
				kubeapplier.ToNodePoolScopedReadDesireResourceIDString(testSub, testRG, testCluster, testNodePool, "a2"),
				mgmtAID),
		},
	}
	b = &listertesting.SliceReadDesireLister{
		Desires: []*kubeapplier.ReadDesire{
			newReadDesire(t,
				kubeapplier.ToClusterScopedReadDesireResourceIDString(testSub, testRG, "other-cluster", "b1"),
				mgmtBID),
		},
	}
	return a, b
}

// ============================================================================
// UnionApplyDesireLister
// ============================================================================

func TestUnionApplyDesireLister_EmptyUnion(t *testing.T) {
	ctx := context.Background()
	u := unionkubeapplier.NewUnionApplyDesireLister()

	if got, err := u.List(ctx); err != nil || len(got) != 0 {
		t.Errorf("empty List: got (%v, %v), want (empty, nil)", got, err)
	}
	if _, err := u.GetForCluster(ctx, testSub, testRG, testCluster, "a1"); !database.IsNotFoundError(err) {
		t.Errorf("empty GetForCluster: want NotFound, got %v", err)
	}
	if _, err := u.GetForNodePool(ctx, testSub, testRG, testCluster, testNodePool, "a2"); !database.IsNotFoundError(err) {
		t.Errorf("empty GetForNodePool: want NotFound, got %v", err)
	}
	if got, err := u.ListForManagementCluster(ctx, mgmtAID); err != nil || got != nil {
		t.Errorf("empty ListForManagementCluster: got (%v, %v), want (nil, nil)", got, err)
	}
	if got, err := u.ListForCluster(ctx, testSub, testRG, testCluster); err != nil || len(got) != 0 {
		t.Errorf("empty ListForCluster: got (%v, %v), want (empty, nil)", got, err)
	}
	if got, err := u.ListForNodePool(ctx, testSub, testRG, testCluster, testNodePool); err != nil || len(got) != 0 {
		t.Errorf("empty ListForNodePool: got (%v, %v), want (empty, nil)", got, err)
	}
}

func TestUnionApplyDesireLister_AggregatesAcrossSublisters(t *testing.T) {
	ctx := context.Background()
	a, b := applySublisters(t)
	u := unionkubeapplier.NewUnionApplyDesireLister()
	u.Add(mgmtAID, a)
	u.Add(mgmtBID, b)

	got, err := u.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 3 {
		t.Errorf("List len = %d, want 3 (2 from mgmt-a + 1 from mgmt-b)", len(got))
	}
}

func TestUnionApplyDesireLister_ListForManagementCluster_DelegatesToSingleSublister(t *testing.T) {
	ctx := context.Background()
	a, b := applySublisters(t)
	u := unionkubeapplier.NewUnionApplyDesireLister()
	u.Add(mgmtAID, a)
	u.Add(mgmtBID, b)

	gotA, err := u.ListForManagementCluster(ctx, mgmtAID)
	if err != nil {
		t.Fatalf("ListForManagementCluster mgmt-a: %v", err)
	}
	if len(gotA) != 2 {
		t.Errorf("mgmt-a len = %d, want 2", len(gotA))
	}

	gotB, err := u.ListForManagementCluster(ctx, mgmtBID)
	if err != nil {
		t.Fatalf("ListForManagementCluster mgmt-b: %v", err)
	}
	if len(gotB) != 1 {
		t.Errorf("mgmt-b len = %d, want 1", len(gotB))
	}

	// Unregistered MC returns (nil, nil).
	gotZ, err := u.ListForManagementCluster(ctx, mgmtUnregistered)
	if err != nil {
		t.Fatalf("ListForManagementCluster unregistered: %v", err)
	}
	if gotZ != nil {
		t.Errorf("unregistered MC: want nil, got %v", gotZ)
	}

	// Case-insensitive lookup: the registered key is lowercased.
	upper := mustParseID(t, strings.ToUpper(mgmtAID.String()))
	gotUpper, err := u.ListForManagementCluster(ctx, upper)
	if err != nil {
		t.Fatalf("ListForManagementCluster uppercased mgmt-a: %v", err)
	}
	if len(gotUpper) != 2 {
		t.Errorf("uppercased mgmt-a len = %d, want 2 (case-insensitive)", len(gotUpper))
	}
}

func TestUnionApplyDesireLister_GetForCluster_FirstHitWins(t *testing.T) {
	ctx := context.Background()
	a, b := applySublisters(t)
	u := unionkubeapplier.NewUnionApplyDesireLister()
	u.Add(mgmtAID, a)
	u.Add(mgmtBID, b)

	// Exists in sublister a.
	got, err := u.GetForCluster(ctx, testSub, testRG, testCluster, "a1")
	if err != nil {
		t.Fatalf("GetForCluster a1: %v", err)
	}
	if got == nil {
		t.Fatal("GetForCluster a1: nil")
	}

	// Exists in sublister b.
	got, err = u.GetForCluster(ctx, testSub, testRG, "other-cluster", "b1")
	if err != nil {
		t.Fatalf("GetForCluster b1: %v", err)
	}
	if got == nil {
		t.Fatal("GetForCluster b1: nil")
	}

	// Nowhere — NotFound (both sublisters reported NotFound, union folds them).
	if _, err := u.GetForCluster(ctx, testSub, testRG, testCluster, "missing"); !database.IsNotFoundError(err) {
		t.Errorf("GetForCluster missing: want NotFound, got %v", err)
	}
}

func TestUnionApplyDesireLister_GetForCluster_NonNotFoundShortCircuits(t *testing.T) {
	ctx := context.Background()
	sentinel := errors.New("boom")
	u := unionkubeapplier.NewUnionApplyDesireLister()
	u.Add(mgmtAID, &erroringApplyLister{err: sentinel})

	_, err := u.GetForCluster(ctx, testSub, testRG, testCluster, "a1")
	if !errors.Is(err, sentinel) {
		t.Errorf("GetForCluster: want sentinel error, got %v", err)
	}
}

func TestUnionApplyDesireLister_RemoveDropsSublister(t *testing.T) {
	ctx := context.Background()
	a, b := applySublisters(t)
	u := unionkubeapplier.NewUnionApplyDesireLister()
	u.Add(mgmtAID, a)
	u.Add(mgmtBID, b)

	u.Remove(mgmtAID)

	got, err := u.List(ctx)
	if err != nil {
		t.Fatalf("List after Remove: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("after Remove mgmt-a: len = %d, want 1", len(got))
	}
	if got, _ := u.ListForManagementCluster(ctx, mgmtAID); got != nil {
		t.Errorf("ListForManagementCluster mgmt-a after Remove: want nil, got %v", got)
	}

	// Remove of an unregistered MC is a no-op.
	u.Remove(mgmtUnregistered)
	if got, err := u.List(ctx); err != nil || len(got) != 1 {
		t.Errorf("List after no-op Remove: len = %d, err = %v; want 1, nil", len(got), err)
	}

	// Nil rid is a no-op.
	u.Remove(nil)
	u.Add(nil, b) // also a no-op
	if got, _ := u.List(ctx); len(got) != 1 {
		t.Errorf("List after nil Add/Remove: len = %d, want 1", len(got))
	}
}

func TestUnionApplyDesireLister_AddReplaces(t *testing.T) {
	ctx := context.Background()
	a1, _ := applySublisters(t)
	a2 := &listertesting.SliceApplyDesireLister{
		Desires: []*kubeapplier.ApplyDesire{
			newApplyDesire(t,
				kubeapplier.ToClusterScopedApplyDesireResourceIDString(testSub, testRG, testCluster, "replacement"),
				mgmtAID),
		},
	}
	u := unionkubeapplier.NewUnionApplyDesireLister()
	u.Add(mgmtAID, a1)
	u.Add(mgmtAID, a2) // second Add under the same MC replaces the first

	got, err := u.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("after replacing Add: len = %d, want 1", len(got))
	}
}

func TestUnionApplyDesireLister_ConcurrentAddRemoveVsRead(t *testing.T) {
	ctx := context.Background()
	a, b := applySublisters(t)
	u := unionkubeapplier.NewUnionApplyDesireLister()

	stop := make(chan struct{})
	var wg sync.WaitGroup

	wg.Add(2)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				u.Add(mgmtAID, a)
				u.Add(mgmtBID, b)
				u.Remove(mgmtAID)
				u.Remove(mgmtBID)
			}
		}
	}()
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				_, _ = u.List(ctx)
				_, _ = u.GetForCluster(ctx, testSub, testRG, testCluster, "a1")
				_, _ = u.ListForManagementCluster(ctx, mgmtAID)
			}
		}
	}()

	// Run for many iterations; race detector catches data races if any.
	for i := 0; i < 1000; i++ {
		_, _ = u.List(ctx)
	}
	close(stop)
	wg.Wait()
}

// ============================================================================
// UnionDeleteDesireLister
// ============================================================================

func TestUnionDeleteDesireLister(t *testing.T) {
	ctx := context.Background()
	a, b := deleteSublisters(t)
	u := unionkubeapplier.NewUnionDeleteDesireLister()
	u.Add(mgmtAID, a)
	u.Add(mgmtBID, b)

	t.Run("List aggregates", func(t *testing.T) {
		got, err := u.List(ctx)
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(got) != 3 {
			t.Errorf("List len = %d, want 3", len(got))
		}
	})

	t.Run("GetForCluster first-hit-wins", func(t *testing.T) {
		if _, err := u.GetForCluster(ctx, testSub, testRG, testCluster, "a1"); err != nil {
			t.Errorf("GetForCluster a1: %v", err)
		}
		if _, err := u.GetForCluster(ctx, testSub, testRG, "other-cluster", "b1"); err != nil {
			t.Errorf("GetForCluster b1: %v", err)
		}
		if _, err := u.GetForCluster(ctx, testSub, testRG, testCluster, "missing"); !database.IsNotFoundError(err) {
			t.Errorf("GetForCluster missing: want NotFound, got %v", err)
		}
	})

	t.Run("GetForNodePool", func(t *testing.T) {
		if _, err := u.GetForNodePool(ctx, testSub, testRG, testCluster, testNodePool, "a2"); err != nil {
			t.Errorf("GetForNodePool a2: %v", err)
		}
		if _, err := u.GetForNodePool(ctx, testSub, testRG, testCluster, testNodePool, "missing"); !database.IsNotFoundError(err) {
			t.Errorf("GetForNodePool missing: want NotFound, got %v", err)
		}
	})

	t.Run("ListForManagementCluster delegates", func(t *testing.T) {
		gotA, err := u.ListForManagementCluster(ctx, mgmtAID)
		if err != nil {
			t.Fatalf("ListForManagementCluster mgmt-a: %v", err)
		}
		if len(gotA) != 2 {
			t.Errorf("mgmt-a len = %d, want 2", len(gotA))
		}
		gotZ, err := u.ListForManagementCluster(ctx, mgmtUnregistered)
		if err != nil {
			t.Fatalf("ListForManagementCluster unregistered: %v", err)
		}
		if gotZ != nil {
			t.Errorf("unregistered MC: want nil, got %v", gotZ)
		}
	})

	t.Run("Remove", func(t *testing.T) {
		u2 := unionkubeapplier.NewUnionDeleteDesireLister()
		u2.Add(mgmtAID, a)
		u2.Add(mgmtBID, b)
		u2.Remove(mgmtAID)
		got, err := u2.List(ctx)
		if err != nil {
			t.Fatalf("List after Remove: %v", err)
		}
		if len(got) != 1 {
			t.Errorf("after Remove mgmt-a: len = %d, want 1", len(got))
		}
	})
}

// ============================================================================
// UnionReadDesireLister
// ============================================================================

func TestUnionReadDesireLister(t *testing.T) {
	ctx := context.Background()
	a, b := readSublisters(t)
	u := unionkubeapplier.NewUnionReadDesireLister()
	u.Add(mgmtAID, a)
	u.Add(mgmtBID, b)

	t.Run("List aggregates", func(t *testing.T) {
		got, err := u.List(ctx)
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(got) != 3 {
			t.Errorf("List len = %d, want 3", len(got))
		}
	})

	t.Run("GetForCluster first-hit-wins", func(t *testing.T) {
		if _, err := u.GetForCluster(ctx, testSub, testRG, testCluster, "a1"); err != nil {
			t.Errorf("GetForCluster a1: %v", err)
		}
		if _, err := u.GetForCluster(ctx, testSub, testRG, "other-cluster", "b1"); err != nil {
			t.Errorf("GetForCluster b1: %v", err)
		}
		if _, err := u.GetForCluster(ctx, testSub, testRG, testCluster, "missing"); !database.IsNotFoundError(err) {
			t.Errorf("GetForCluster missing: want NotFound, got %v", err)
		}
	})

	t.Run("GetForNodePool", func(t *testing.T) {
		if _, err := u.GetForNodePool(ctx, testSub, testRG, testCluster, testNodePool, "a2"); err != nil {
			t.Errorf("GetForNodePool a2: %v", err)
		}
		if _, err := u.GetForNodePool(ctx, testSub, testRG, testCluster, testNodePool, "missing"); !database.IsNotFoundError(err) {
			t.Errorf("GetForNodePool missing: want NotFound, got %v", err)
		}
	})

	t.Run("ListForManagementCluster delegates", func(t *testing.T) {
		gotA, err := u.ListForManagementCluster(ctx, mgmtAID)
		if err != nil {
			t.Fatalf("ListForManagementCluster mgmt-a: %v", err)
		}
		if len(gotA) != 2 {
			t.Errorf("mgmt-a len = %d, want 2", len(gotA))
		}
	})

	t.Run("Remove", func(t *testing.T) {
		u2 := unionkubeapplier.NewUnionReadDesireLister()
		u2.Add(mgmtAID, a)
		u2.Add(mgmtBID, b)
		u2.Remove(mgmtAID)
		got, err := u2.List(ctx)
		if err != nil {
			t.Fatalf("List after Remove: %v", err)
		}
		if len(got) != 1 {
			t.Errorf("after Remove mgmt-a: len = %d, want 1", len(got))
		}
	})
}

// --- helpers -------------------------------------------------------------

// erroringApplyLister returns the configured error from every method. Used to
// verify that non-NotFound errors short-circuit the union's Get methods.
type erroringApplyLister struct {
	err error
}

var _ listers.ApplyDesireLister = &erroringApplyLister{}

func (e *erroringApplyLister) List(ctx context.Context) ([]*kubeapplier.ApplyDesire, error) {
	return nil, e.err
}
func (e *erroringApplyLister) GetForCluster(ctx context.Context, _, _, _, _ string) (*kubeapplier.ApplyDesire, error) {
	return nil, e.err
}
func (e *erroringApplyLister) GetForNodePool(ctx context.Context, _, _, _, _, _ string) (*kubeapplier.ApplyDesire, error) {
	return nil, e.err
}
func (e *erroringApplyLister) ListForManagementCluster(ctx context.Context, _ *azcorearm.ResourceID) ([]*kubeapplier.ApplyDesire, error) {
	return nil, e.err
}
func (e *erroringApplyLister) ListForCluster(ctx context.Context, _, _, _ string) ([]*kubeapplier.ApplyDesire, error) {
	return nil, e.err
}
func (e *erroringApplyLister) ListForNodePool(ctx context.Context, _, _, _, _ string) ([]*kubeapplier.ApplyDesire, error) {
	return nil, e.err
}
