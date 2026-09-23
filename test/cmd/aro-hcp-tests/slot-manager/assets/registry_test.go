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

package assets

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/slots"
)

type fakeHandler struct {
	kind       Kind
	declared   bool
	lease      bool
	calls      *[]string
	prepareErr error
}

func (h *fakeHandler) Kind() Kind {
	return h.kind
}

func (h *fakeHandler) Declared(slots.Pool) bool {
	return h.declared
}

func (h *fakeHandler) AcquireLease(context.Context, LeaseRequest) error {
	*h.calls = append(*h.calls, "resolve:"+string(h.kind))
	return nil
}

func (h *fakeHandler) ReleaseLease(context.Context, LeaseRequest) error { return nil }

func (h *fakeHandler) ApplyPools(context.Context, PoolRequest) error {
	*h.calls = append(*h.calls, "apply:"+string(h.kind))
	return nil
}

func (h *fakeHandler) ValidatePools(context.Context, PoolRequest) error {
	*h.calls = append(*h.calls, "validate-pool:"+string(h.kind))
	return nil
}

func (h *fakeHandler) PrepareLease(context.Context, LeaseRequest) error {
	*h.calls = append(*h.calls, "prepare:"+string(h.kind))
	return h.prepareErr
}

func (h *fakeHandler) ValidateLease(context.Context, LeaseRequest) error {
	*h.calls = append(*h.calls, "validate-lease:"+string(h.kind))
	return nil
}

func (h *fakeHandler) PublishLease(_ context.Context, _ LeaseRequest, contract *slots.RuntimeContractBuilder) error {
	*h.calls = append(*h.calls, "publish:"+string(h.kind))
	return contract.Add(string(h.kind), "EXPORT_"+string(h.kind), "value")
}

func TestRegistryRejectsDuplicateKinds(t *testing.T) {
	t.Parallel()

	calls := []string{}
	_, err := NewRegistry(
		&fakeHandler{kind: "duplicate", calls: &calls},
		&fakeHandler{kind: "duplicate", calls: &calls},
	)
	if err == nil {
		t.Fatal("expected duplicate asset kinds to be rejected")
	}
}

func TestRegistrySelectsCanonicalKindsOnly(t *testing.T) {
	t.Parallel()

	calls := []string{}
	registry, err := NewRegistry(&fakeHandler{kind: KindE2EIdentities, declared: true, calls: &calls})
	if err != nil {
		t.Fatal(err)
	}
	request := PoolRequest{Pools: []slots.Pool{{}}}
	for _, operation := range []func(context.Context, PoolRequest, ...Kind) error{registry.ApplyPools, registry.ValidatePools} {
		if err := operation(context.Background(), request, "e2e_identities"); err != nil {
			t.Fatalf("canonical kind was rejected: %v", err)
		}
		if err := operation(context.Background(), request, "e2e-identities"); err == nil || !strings.Contains(err.Error(), `unknown asset kind "e2e-identities"`) {
			t.Fatalf("expected noncanonical selector rejection, got %v", err)
		}
	}
	if want := []string{"apply:e2e_identities", "validate-pool:e2e_identities"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("unexpected handler calls: got %v want %v", calls, want)
	}
}

func TestRegistryAcquireLeaseDiagnostics(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name      string
		inventory bool
		journal   bool
		units     int
		want      string
	}{
		{"missing inventory", false, true, 1, `unresolved inventory for demanded asset "infrastructure_identities" in pool "bundles"`},
		{"missing journal", true, false, 1, `missing lease journal for demanded asset "infrastructure_identities" in pool "bundles"`},
		{"zero units", true, true, 0, `invalid units_per_slot 0 for demanded asset "infrastructure_identities" in pool "bundles": must be positive`},
		{"negative units", true, true, -2, `invalid units_per_slot -2 for demanded asset "infrastructure_identities" in pool "bundles": must be positive`},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := []string{}
			registry, err := NewRegistry(&fakeHandler{kind: slots.KindInfrastructureIdentities, calls: &calls})
			if err != nil {
				t.Fatal(err)
			}
			request := LeaseRequest{State: &slots.AcquiredSlotState{Slot: slots.ExpandedSlot{
				Requirements: []slots.AssetRequirement{{
					Kind: slots.KindInfrastructureIdentities, Allocation: slots.AllocationLeased,
					AssetPool: "bundles", UnitsPerSlot: test.units,
				}},
			}}}
			if test.inventory {
				request.Inventories = []slots.AssetInventory{{Pool: slots.AssetPool{Name: "bundles", Kind: slots.KindInfrastructureIdentities}}}
			}
			if test.journal {
				request.Journal = &slots.LeaseJournal{}
			}
			if err := registry.AcquireLease(context.Background(), request); err == nil || err.Error() != test.want {
				t.Fatalf("expected %q, got %v", test.want, err)
			}
			if len(calls) != 0 {
				t.Fatalf("invalid request reached handler: %v", calls)
			}
		})
	}
}

func TestRegistryUsesRegistrationOrderWithFilters(t *testing.T) {
	t.Parallel()

	calls := []string{}
	registry, err := NewRegistry(
		&fakeHandler{kind: "first", declared: true, calls: &calls},
		&fakeHandler{kind: "second", declared: true, calls: &calls},
	)
	if err != nil {
		t.Fatalf("expected registry construction to succeed: %v", err)
	}
	err = registry.ApplyPools(context.Background(), PoolRequest{Pools: []slots.Pool{{}}}, "second", "first")
	if err != nil {
		t.Fatalf("expected filtered apply to succeed: %v", err)
	}
	if want := []string{"apply:first", "apply:second"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("unexpected handler order: got %v want %v", calls, want)
	}
}

func TestRegistryStopsLeaseAdmissionOnFirstFailure(t *testing.T) {
	t.Parallel()

	calls := []string{}
	expectedErr := errors.New("dirty asset")
	registry, err := NewRegistry(
		&fakeHandler{kind: "first", lease: true, calls: &calls, prepareErr: expectedErr},
		&fakeHandler{kind: "second", lease: true, calls: &calls},
	)
	if err != nil {
		t.Fatalf("expected registry construction to succeed: %v", err)
	}
	request := LeaseRequest{State: &slots.AcquiredSlotState{Slot: slots.ExpandedSlot{ResourceName: "slot-00", Requirements: []slots.AssetRequirement{
		{Kind: "first", Allocation: slots.AllocationDedicated}, {Kind: "second", Allocation: slots.AllocationDedicated},
	}}}}
	err = registry.PrepareLease(context.Background(), request)
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected preparation error to be preserved, got %v", err)
	}
	if want := []string{"prepare:first"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("unexpected calls after failure: got %v want %v", calls, want)
	}
}

func TestRegistryPublishesEveryLeasedAsset(t *testing.T) {
	t.Parallel()

	calls := []string{}
	registry, err := NewRegistry(
		&fakeHandler{kind: "first", lease: true, calls: &calls},
		&fakeHandler{kind: "second", lease: true, calls: &calls},
	)
	if err != nil {
		t.Fatalf("expected registry construction to succeed: %v", err)
	}
	request := LeaseRequest{State: &slots.AcquiredSlotState{Slot: slots.ExpandedSlot{ResourceName: "slot-00", Requirements: []slots.AssetRequirement{
		{Kind: "first", Allocation: slots.AllocationDedicated}, {Kind: "second", Allocation: slots.AllocationDedicated},
	}}}}
	contract := slots.NewRuntimeContractBuilder()
	if err := registry.PublishLease(context.Background(), request, contract); err != nil {
		t.Fatalf("expected publication to succeed: %v", err)
	}
	if want := []string{"publish:first", "publish:second"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("unexpected publication calls: got %v want %v", calls, want)
	}
}
