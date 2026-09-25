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

package slots

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func journalTestState() *AcquiredSlotState {
	return &AcquiredSlotState{
		Version: 2,
		Leases: LeaseSet{
			Primary: Lease{ResourceType: "slot", ResourceName: "slot-00"},
			Assets: map[AssetKind][]Lease{
				"removed-handler": {
					{ResourceType: "bundles", ResourceName: "bundle-04"},
					{ResourceType: "bundles", ResourceName: "bundle-07"},
				},
			},
		},
	}
}

func TestJournalRejectsInvalidAssetNameWithoutPoisoningCleanup(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"", " \t\n", " bundle-00", "bundle-00 ", "\tbundle-00\n", "\u00a0bundle-00", "slot-00", "bundle-04"} {
		t.Run(name, func(t *testing.T) {
			state := journalTestState()
			dir := t.TempDir()
			writes := 0
			var returned []string
			journal := &LeaseJournal{
				State: state, Timeout: time.Second,
				Persist: func() error {
					writes++
					return WriteAcquiredSlotState(dir, state)
				},
				Acquire: func(context.Context, string, time.Duration) (string, error) {
					return name, nil
				},
				Return: func(_ context.Context, name string, _ time.Duration) error {
					returned = append(returned, name)
					return nil
				},
			}
			_, err := journal.AcquireAsset(context.Background(), KindInfrastructureIdentities,
				AssetInventory{Pool: AssetPool{ResourceType: "bundles", ResourceNamePrefix: "bundle"}, Capacity: 8})
			if err == nil || !strings.Contains(err.Error(), "resource name") {
				t.Fatalf("expected invalid or duplicate name rejection, got %v", err)
			}
			if writes != 0 || len(state.Leases.Assets[KindInfrastructureIdentities]) != 0 {
				t.Fatal("invalid name was journaled")
			}
			if err := journal.ReleaseAll(context.Background()); err != nil {
				t.Fatalf("invalid response prevented earlier lease cleanup: %v", err)
			}
			if !reflect.DeepEqual(returned, []string{"bundle-04", "bundle-07", "slot-00"}) {
				t.Fatalf("cleanup did not return existing leases: %v", returned)
			}
		})
	}
}

func TestJournalReleaseContinuesAfterFailureAndCancellation(t *testing.T) {
	t.Parallel()
	state := journalTestState()
	calls := []string{}
	failure := errors.New("Boskos return failed")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	journal := &LeaseJournal{
		State: state, Timeout: time.Second,
		Persist: func() error { return nil },
		Return: func(ctx context.Context, name string, _ time.Duration) error {
			if ctx.Err() != nil {
				t.Errorf("return %s inherited canceled context", name)
			}
			if _, bounded := ctx.Deadline(); !bounded {
				t.Error("cleanup has no deadline")
			}
			calls = append(calls, name)
			if name == "bundle-04" {
				return failure
			}
			return nil
		},
	}
	if err := journal.ReleaseAll(ctx); !errors.Is(err, failure) {
		t.Fatalf("joined release error lost failure: %v", err)
	}
	if !reflect.DeepEqual(calls, []string{"bundle-04", "bundle-07", "slot-00"}) {
		t.Fatalf("cleanup stopped early: %v", calls)
	}
}

func TestJournalInterruptedReturnCannotReturnReassignedName(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	state := &AcquiredSlotState{Version: 2, Leases: LeaseSet{Primary: Lease{ResourceType: "slot", ResourceName: "slot-00"}}}
	writes, returns := 0, 0
	journal := &LeaseJournal{
		State: state, Timeout: time.Second,
		Persist: func() error {
			writes++
			if writes == 2 {
				return errors.New("disk failed after successful return")
			}
			return WriteAcquiredSlotState(dir, state)
		},
		Return: func(context.Context, string, time.Duration) error { returns++; return nil },
	}
	if err := journal.ReleaseAll(context.Background()); err == nil {
		t.Fatal("post-return write failure must be reported")
	}
	reloaded, err := LoadAcquiredSlotState(dir)
	if err != nil {
		t.Fatal(err)
	}
	retry := &LeaseJournal{
		State: reloaded, Timeout: time.Second,
		Persist: func() error { return WriteAcquiredSlotState(dir, reloaded) },
		Return:  func(context.Context, string, time.Duration) error { returns++; return nil },
	}
	if err := retry.ReleaseAll(context.Background()); err == nil || !strings.Contains(err.Error(), "uncertain") {
		t.Fatalf("interrupted return must require ownership reconciliation: %v", err)
	}
	if returns != 1 {
		t.Fatalf("already returned name was returned again: %d", returns)
	}
}

func TestJournalIndependentAcquireDeadlineAndPersistence(t *testing.T) {
	t.Parallel()
	state := &AcquiredSlotState{Version: 2, Leases: LeaseSet{Primary: Lease{ResourceType: "slot", ResourceName: "slot-00"}}}
	inventory := AssetInventory{Pool: AssetPool{ResourceType: "bundle-type", ResourceNamePrefix: "bundle"}, Capacity: 5}
	saved := false
	journal := &LeaseJournal{
		State: state, Timeout: 10 * time.Millisecond,
		Persist: func() error { saved = true; return errors.New("state write failure") },
		Acquire: func(ctx context.Context, _ string, _ time.Duration) (string, error) {
			if _, ok := ctx.Deadline(); !ok {
				t.Fatal("independent acquisition missing deadline")
			}
			return "bundle-04", nil
		},
	}
	lease, err := journal.AcquireAsset(context.Background(), KindInfrastructureIdentities, inventory)
	if err == nil || !saved || lease.ResourceName != "bundle-04" || len(state.Leases.Assets[KindInfrastructureIdentities]) != 1 {
		t.Fatalf("failed write lost exact acquired lease: %+v, %+v, %v", lease, state, err)
	}
	journal.Acquire = func(ctx context.Context, _ string, _ time.Duration) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	}
	if _, err := journal.AcquireAsset(context.Background(), KindInfrastructureIdentities, inventory); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("independent acquisition waited without deadline: %v", err)
	}
}
