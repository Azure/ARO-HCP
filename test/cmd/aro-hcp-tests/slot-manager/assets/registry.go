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
	"fmt"
	"io"
	"strings"

	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/slots"
)

type Kind = slots.AssetKind

const KindE2EIdentities = slots.KindE2EIdentities

type PoolRequest struct {
	Inventories      []slots.AssetInventory
	Environment      string
	Pools            []slots.Pool
	IncludeUnmanaged bool
	Out              io.Writer
}

type LeaseRequest struct {
	Inventories               []slots.AssetInventory
	Journal                   *slots.LeaseJournal
	State                     *slots.AcquiredSlotState
	SelectedClusterProfileDir string
}

type Handler interface {
	Kind() Kind
	Declared(pool slots.Pool) bool
	AcquireLease(ctx context.Context, request LeaseRequest) error
	ReleaseLease(ctx context.Context, request LeaseRequest) error
	ApplyPools(ctx context.Context, request PoolRequest) error
	ValidatePools(ctx context.Context, request PoolRequest) error
	// AdmitLease establishes readiness for exclusive reuse before publication.
	AdmitLease(ctx context.Context, request LeaseRequest) error
	PublishLease(ctx context.Context, request LeaseRequest, contract *slots.RuntimeContractBuilder) error
}

type Registry struct {
	handlers []Handler
	byKind   map[Kind]Handler
}

func NewRegistry(handlers ...Handler) (*Registry, error) {
	registry := &Registry{
		handlers: make([]Handler, 0, len(handlers)),
		byKind:   make(map[Kind]Handler, len(handlers)),
	}
	for _, handler := range handlers {
		if handler == nil {
			return nil, errors.New("asset handler is nil")
		}
		kind := Kind(strings.TrimSpace(string(handler.Kind())))
		if kind == "" {
			return nil, errors.New("asset handler kind is empty")
		}
		if kind != handler.Kind() {
			return nil, fmt.Errorf("asset handler kind %q contains surrounding whitespace", handler.Kind())
		}
		if _, found := registry.byKind[kind]; found {
			return nil, fmt.Errorf("asset handler kind %q is registered more than once", kind)
		}
		registry.handlers = append(registry.handlers, handler)
		registry.byKind[kind] = handler
	}
	return registry, nil
}

func (r *Registry) ApplyPools(ctx context.Context, request PoolRequest, selectedKinds ...Kind) error {
	return r.forDeclaredPoolHandlers(ctx, request, selectedKinds, "applying", Handler.ApplyPools)
}

func (r *Registry) ValidatePools(ctx context.Context, request PoolRequest, selectedKinds ...Kind) error {
	return r.forDeclaredPoolHandlers(ctx, request, selectedKinds, "validating", Handler.ValidatePools)
}

func (r *Registry) AcquireLease(ctx context.Context, request LeaseRequest) error {
	return r.forLeaseHandlers(request, func(handler Handler) error {
		for _, requirement := range request.State.Slot.Requirements {
			if requirement.Kind != handler.Kind() || requirement.Allocation != slots.AllocationLeased {
				continue
			}
			var inventory *slots.AssetInventory
			for i := range request.Inventories {
				if request.Inventories[i].Pool.Name == requirement.AssetPool && request.Inventories[i].Pool.Kind == requirement.Kind {
					inventory = &request.Inventories[i]
				}
			}
			if inventory == nil {
				return fmt.Errorf("unresolved inventory for demanded asset %q in pool %q", requirement.Kind, requirement.AssetPool)
			}
			if request.Journal == nil {
				return fmt.Errorf("missing lease journal for demanded asset %q in pool %q", requirement.Kind, requirement.AssetPool)
			}
			if requirement.UnitsPerSlot <= 0 {
				return fmt.Errorf("invalid units_per_slot %d for demanded asset %q in pool %q: must be positive", requirement.UnitsPerSlot, requirement.Kind, requirement.AssetPool)
			}
			for range requirement.UnitsPerSlot {
				if _, err := request.Journal.AcquireAsset(ctx, handler.Kind(), *inventory); err != nil {
					return fmt.Errorf("acquiring asset %q: %w", handler.Kind(), err)
				}
			}
		}
		if err := handler.AcquireLease(ctx, request); err != nil {
			return fmt.Errorf("resolving asset %q for slot %q: %w", handler.Kind(), request.State.Slot.ResourceName, err)
		}
		if request.Journal != nil {
			return request.Journal.Persist()
		}
		return nil
	})
}

// ReleaseLease calls known handlers but always falls back to the journal for
// every recorded name, including kinds removed since acquisition.
func (r *Registry) ReleaseLease(ctx context.Context, request LeaseRequest) error {
	if request.State == nil || request.Journal == nil {
		return errors.New("release requires state and lease journal")
	}
	var errs []error
	for _, handler := range r.handlers {
		if len(request.State.Leases.Assets[handler.Kind()]) > 0 {
			bounded, cancel := context.WithTimeout(context.WithoutCancel(ctx), request.Journal.Timeout)
			errs = append(errs, handler.ReleaseLease(bounded, request))
			cancel()
		}
	}
	errs = append(errs, request.Journal.ReleaseAll(ctx))
	return errors.Join(errs...)
}

func (r *Registry) ValidateRequirements(pools []slots.Pool) error {
	for _, pool := range pools {
		for _, requirement := range pool.Requirements() {
			handler, found := r.byKind[requirement.Kind]
			if !found {
				return fmt.Errorf("pool %q demands asset %q without an implemented handler", pool.Name, requirement.Kind)
			}
			if !handler.Declared(pool) {
				return fmt.Errorf("handler %q does not accept demanded asset in pool %q", requirement.Kind, pool.Name)
			}
		}
	}
	return nil
}

func (r *Registry) AdmitLease(ctx context.Context, request LeaseRequest) error {
	return r.forLeaseHandlers(request, func(handler Handler) error {
		if err := handler.AdmitLease(ctx, request); err != nil {
			return fmt.Errorf("admitting asset %q for slot %q: %w", handler.Kind(), request.State.Slot.ResourceName, err)
		}
		return nil
	})
}

func (r *Registry) PublishLease(ctx context.Context, request LeaseRequest, contract *slots.RuntimeContractBuilder) error {
	if contract == nil {
		return errors.New("runtime contract builder is nil")
	}
	return r.forLeaseHandlers(request, func(handler Handler) error {
		if err := handler.PublishLease(ctx, request, contract); err != nil {
			return fmt.Errorf("publishing asset %q for slot %q: %w", handler.Kind(), request.State.Slot.ResourceName, err)
		}
		return nil
	})
}

func (r *Registry) forDeclaredPoolHandlers(ctx context.Context, request PoolRequest, selectedKinds []Kind, action string, operation func(Handler, context.Context, PoolRequest) error) error {
	if err := r.ValidateRequirements(request.Pools); err != nil {
		return err
	}
	for _, pool := range request.Pools {
		for _, requirement := range pool.Requirements() {
			if requirement.Allocation != slots.AllocationLeased {
				continue
			}
			found := false
			for _, inventory := range request.Inventories {
				if inventory.Pool.Name == requirement.AssetPool && inventory.Pool.Kind == requirement.Kind && inventory.Capacity > 0 {
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("unresolved inventory for demanded asset pool %q", requirement.AssetPool)
			}
		}
	}
	selected, err := r.selectedHandlers(selectedKinds)
	if err != nil {
		return err
	}
	for _, handler := range selected {
		pools := make([]slots.Pool, 0, len(request.Pools))
		for _, pool := range request.Pools {
			if handler.Declared(pool) {
				pools = append(pools, pool)
			}
		}
		if len(pools) == 0 {
			continue
		}
		filtered := request
		filtered.Pools = pools
		filtered.Inventories = selectedInventories(request.Inventories, pools, handler.Kind())
		if err := operation(handler, ctx, filtered); err != nil {
			return fmt.Errorf("%s asset %q: %w", action, handler.Kind(), err)
		}
	}
	return nil
}

func (r *Registry) forLeaseHandlers(request LeaseRequest, operation func(Handler) error) error {
	if request.State == nil {
		return errors.New("acquired slot state is nil")
	}
	demanded := map[Kind]bool{}
	for _, requirement := range request.State.Slot.Requirements {
		if _, found := r.byKind[requirement.Kind]; !found {
			return fmt.Errorf("demanded asset %q has no implemented handler", requirement.Kind)
		}
		if requirement.Allocation != slots.AllocationDedicated && requirement.Allocation != slots.AllocationLeased {
			return fmt.Errorf("demanded asset %q has invalid allocation %q", requirement.Kind, requirement.Allocation)
		}
		if demanded[requirement.Kind] {
			return fmt.Errorf("duplicate demanded asset %q", requirement.Kind)
		}
		demanded[requirement.Kind] = true
	}
	for _, handler := range r.handlers {
		if !demanded[handler.Kind()] {
			continue
		}
		if err := operation(handler); err != nil {
			return err
		}
	}
	return nil
}

func (r *Registry) selectedHandlers(selectedKinds []Kind) ([]Handler, error) {
	if len(selectedKinds) == 0 {
		return r.handlers, nil
	}
	selected := map[Kind]struct{}{}
	for _, kind := range selectedKinds {
		kind = Kind(strings.TrimSpace(string(kind)))
		if _, found := r.byKind[kind]; !found {
			return nil, fmt.Errorf("unknown asset kind %q", kind)
		}
		selected[kind] = struct{}{}
	}
	handlers := make([]Handler, 0, len(selected))
	for _, handler := range r.handlers {
		if _, found := selected[handler.Kind()]; found {
			handlers = append(handlers, handler)
		}
	}
	return handlers, nil
}

func selectedInventories(inventories []slots.AssetInventory, pools []slots.Pool, kind Kind) []slots.AssetInventory {
	references := map[string]bool{}
	for _, pool := range pools {
		for _, requirement := range pool.Requirements() {
			if requirement.Kind == kind && requirement.Allocation == slots.AllocationLeased {
				references[requirement.AssetPool] = true
			}
		}
	}
	var selected []slots.AssetInventory
	for _, inventory := range inventories {
		if references[inventory.Pool.Name] {
			selected = append(selected, inventory)
		}
	}
	return selected
}
