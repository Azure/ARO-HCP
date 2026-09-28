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
	kind     Kind
	declared bool
	calls    *[]string
	admitErr error
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

func (h *fakeHandler) AdmitLease(context.Context, LeaseRequest) error {
	*h.calls = append(*h.calls, "admit:"+string(h.kind))
	return h.admitErr
}

func (h *fakeHandler) PublishLease(_ context.Context, _ LeaseRequest, contract *slots.RuntimeContractBuilder) error {
	*h.calls = append(*h.calls, "publish:"+string(h.kind))
	return contract.Add(string(h.kind), "EXPORT_"+string(h.kind), "value")
}

func TestRegistryRejectsNilHandlers(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		handler Handler
	}{
		{"nil interface", nil},
		{"typed nil pointer", (*fakeHandler)(nil)},
	} {
		t.Run(test.name, func(t *testing.T) {
			registry, err := NewRegistry(test.handler)
			if registry != nil || err == nil || err.Error() != "asset handler is nil" {
				t.Fatalf("expected nil-handler error and no registry, got %v, %v", registry, err)
			}
		})
	}
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

func TestRegistryAcquireLeasesDiagnostics(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name           string
		assetInventory bool
		journal        bool
		units          int
		want           string
	}{
		{"missing inventory", false, true, 1, `unresolved inventory for demanded asset "infrastructure_identities" in pool "bundles"`},
		{"missing journal", true, false, 1, `missing lease journal for demanded asset "infrastructure_identities" in pool "bundles"`},
		{"zero units", true, true, 0, `invalid units_per_slot 0 for demanded asset "infrastructure_identities" in pool "bundles": must be positive`},
		{"negative units", true, true, -2, `invalid units_per_slot -2 for demanded asset "infrastructure_identities" in pool "bundles": must be positive`},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := []string{}
			registry, err := NewRegistry(
				&fakeHandler{kind: KindE2EIdentities, calls: &calls},
				&fakeHandler{kind: slots.KindInfrastructureIdentities, calls: &calls},
			)
			if err != nil {
				t.Fatal(err)
			}
			request := LeaseRequest{AcquiredSlotState: &slots.AcquiredSlotState{Slot: slots.ExpandedSlot{
				AssetRequirements: []slots.AssetRequirement{
					{Kind: KindE2EIdentities, Allocation: slots.AllocationDedicated},
					{
						Kind: slots.KindInfrastructureIdentities, Allocation: slots.AllocationLeased,
						AssetPoolName: "bundles", UnitsPerSlot: test.units,
					},
				},
			}}}
			var assetInventories []slots.AssetInventory
			if test.assetInventory {
				assetInventories = []slots.AssetInventory{{AssetPool: slots.AssetPool{Name: "bundles", Kind: slots.KindInfrastructureIdentities}}}
			}
			if test.journal {
				request.LeaseJournal = &slots.LeaseJournal{}
			}
			if err := registry.AcquireLeases(context.Background(), request, assetInventories); err == nil || err.Error() != test.want {
				t.Fatalf("expected %q, got %v", test.want, err)
			}
			if len(calls) != 0 {
				t.Fatalf("invalid request reached handler: %v", calls)
			}
		})
	}
}

func TestRegistryDistinguishesEmptySelectionFromEmptyDemand(t *testing.T) {
	t.Parallel()
	calls := []string{}
	first := &fakeHandler{kind: "first", calls: &calls}
	second := &fakeHandler{kind: "second", calls: &calls}
	registry, err := NewRegistry(first, second)
	if err != nil {
		t.Fatal(err)
	}
	handlers, err := registry.FilterHandlers(nil)
	if err != nil || !reflect.DeepEqual(handlers, []Handler{first, second}) {
		t.Fatalf("empty CLI selection must select all handlers in registration order: %v, %v", handlers, err)
	}
	handlers[0] = second
	if got := registry.ListHandlers(); !reflect.DeepEqual(got, []Handler{first, second}) {
		t.Fatalf("modifying a selection changed registry order: %v", got)
	}
	handlers, err = registry.HandlersForRequirements(nil)
	if err != nil || len(handlers) != 0 {
		t.Fatalf("empty demand must select no handlers: %v, %v", handlers, err)
	}
	handlers, err = registry.HandlersForRequirements([]slots.AssetRequirement{
		{Kind: "second", Allocation: slots.AllocationDedicated},
		{Kind: "first", Allocation: slots.AllocationDedicated},
	})
	if err != nil || !reflect.DeepEqual(handlers, []Handler{first, second}) {
		t.Fatalf("requirement order must not change registration order: %v, %v", handlers, err)
	}
}

func TestRegistryValidatesAllRequirementsBeforeLeaseHandlers(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name             string
		assetRequirement slots.AssetRequirement
		want             string
	}{
		{"unknown kind", slots.AssetRequirement{Kind: "unknown", Allocation: slots.AllocationDedicated}, `demanded asset "unknown" has no implemented handler`},
		{"invalid allocation", slots.AssetRequirement{Kind: "second", Allocation: "invalid"}, `demanded asset "second" has invalid allocation "invalid"`},
		{"duplicate kind", slots.AssetRequirement{Kind: "first", Allocation: slots.AllocationDedicated}, `duplicate demanded asset "first"`},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := []string{}
			registry, err := NewRegistry(
				&fakeHandler{kind: "first", calls: &calls},
				&fakeHandler{kind: "second", calls: &calls},
			)
			if err != nil {
				t.Fatal(err)
			}
			request := LeaseRequest{AcquiredSlotState: &slots.AcquiredSlotState{Slot: slots.ExpandedSlot{
				AssetRequirements: []slots.AssetRequirement{
					{Kind: "first", Allocation: slots.AllocationDedicated},
					test.assetRequirement,
				},
			}}}
			for _, operation := range []func(context.Context, LeaseRequest) error{
				func(ctx context.Context, request LeaseRequest) error {
					return registry.AcquireLeases(ctx, request, nil)
				},
				registry.AdmitLease,
				func(ctx context.Context, request LeaseRequest) error {
					return registry.PublishLease(ctx, request, slots.NewRuntimeContractBuilder())
				},
			} {
				if err := operation(t.Context(), request); err == nil || err.Error() != test.want {
					t.Fatalf("expected %q before invoking any handler, got %v", test.want, err)
				}
				if len(calls) != 0 {
					t.Fatalf("invalid demand reached handlers: %v", calls)
				}
			}
		})
	}
}

func TestRegistryFiltersKindsInRegistrationOrder(t *testing.T) {
	t.Parallel()

	calls := []string{}
	registry, err := NewRegistry(
		&fakeHandler{kind: KindE2EIdentities, declared: true, calls: &calls},
		&fakeHandler{kind: "second", declared: true, calls: &calls},
		&fakeHandler{kind: "unselected", declared: true, calls: &calls},
	)
	if err != nil {
		t.Fatalf("expected registry construction to succeed: %v", err)
	}
	request := PoolRequest{Pools: []slots.Pool{{}}}
	for _, operation := range []func(context.Context, PoolRequest, ...Kind) error{registry.ApplyPools, registry.ValidatePools} {
		err = operation(context.Background(), request, "second", "e2e_identities", "second")
		if err != nil {
			t.Fatalf("expected filtered operation to succeed: %v", err)
		}
		if err := operation(context.Background(), request, "e2e-identities"); err == nil || !strings.Contains(err.Error(), `unknown asset kind "e2e-identities"`) {
			t.Fatalf("expected noncanonical selector rejection, got %v", err)
		}
	}
	if want := []string{"apply:e2e_identities", "apply:second", "validate-pool:e2e_identities", "validate-pool:second"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("unexpected handler order: got %v want %v", calls, want)
	}
}

func TestRegistryStopsLeaseAdmissionOnFirstFailure(t *testing.T) {
	t.Parallel()

	calls := []string{}
	expectedErr := errors.New("dirty asset")
	registry, err := NewRegistry(
		&fakeHandler{kind: "first", calls: &calls, admitErr: expectedErr},
		&fakeHandler{kind: "second", calls: &calls},
	)
	if err != nil {
		t.Fatalf("expected registry construction to succeed: %v", err)
	}
	request := LeaseRequest{AcquiredSlotState: &slots.AcquiredSlotState{Slot: slots.ExpandedSlot{ResourceName: "slot-00", AssetRequirements: []slots.AssetRequirement{
		{Kind: "first", Allocation: slots.AllocationDedicated}, {Kind: "second", Allocation: slots.AllocationDedicated},
	}}}}
	err = registry.AdmitLease(context.Background(), request)
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected admission error to be preserved, got %v", err)
	}
	if want := []string{"admit:first"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("unexpected calls after failure: got %v want %v", calls, want)
	}
}
