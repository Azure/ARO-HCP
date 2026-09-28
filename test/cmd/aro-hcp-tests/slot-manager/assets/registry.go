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
	"slices"
	"strings"

	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/slots"
)

type Kind = slots.AssetKind

const KindE2EIdentities = slots.KindE2EIdentities

type PoolRequest struct {
	AssetInventories []slots.AssetInventory
	Environment      string
	Pools            []slots.Pool
	IncludeUnmanaged bool
	Out              io.Writer
}

type LeaseRequest struct {
	AssetInventories          []slots.AssetInventory
	LeaseJournal              *slots.LeaseJournal
	AcquiredSlotState         *slots.AcquiredSlotState
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
	if err := r.validatePoolRequest(request); err != nil {
		return err
	}
	handlers, err := r.FilterHandlers(selectedKinds)
	if err != nil {
		return err
	}
	for _, handler := range handlers {
		scopedRequest := request.forHandler(handler)
		if len(scopedRequest.Pools) == 0 {
			continue
		}
		if err := handler.ApplyPools(ctx, scopedRequest); err != nil {
			return fmt.Errorf("applying asset %q: %w", handler.Kind(), err)
		}
	}
	return nil
}

func (r *Registry) ValidatePools(ctx context.Context, request PoolRequest, selectedKinds ...Kind) error {
	if err := r.validatePoolRequest(request); err != nil {
		return err
	}
	handlers, err := r.FilterHandlers(selectedKinds)
	if err != nil {
		return err
	}
	for _, handler := range handlers {
		scopedRequest := request.forHandler(handler)
		if len(scopedRequest.Pools) == 0 {
			continue
		}
		if err := handler.ValidatePools(ctx, scopedRequest); err != nil {
			return fmt.Errorf("validating asset %q: %w", handler.Kind(), err)
		}
	}
	return nil
}

func (r *Registry) AcquireLease(ctx context.Context, request LeaseRequest) error {
	if request.AcquiredSlotState == nil {
		return errors.New("acquired slot state is nil")
	}
	handlers, err := r.HandlersForRequirements(request.AcquiredSlotState.Slot.AssetRequirements)
	if err != nil {
		return err
	}
	if err := request.validateAcquisition(); err != nil {
		return err
	}
	for _, handler := range handlers {
		for _, assetRequirement := range request.AcquiredSlotState.Slot.AssetRequirements {
			if assetRequirement.Kind != handler.Kind() || assetRequirement.Allocation != slots.AllocationLeased {
				continue
			}
			assetInventory, found := slots.AssetInventoryForRequirement(request.AssetInventories, assetRequirement)
			if !found {
				return fmt.Errorf("unresolved inventory for demanded asset %q in pool %q", assetRequirement.Kind, assetRequirement.AssetPoolName)
			}
			for range assetRequirement.UnitsPerSlot {
				if _, err := request.LeaseJournal.AcquireAsset(ctx, handler.Kind(), assetInventory); err != nil {
					return fmt.Errorf("acquiring asset %q: %w", handler.Kind(), err)
				}
			}
		}
		if err := handler.AcquireLease(ctx, request); err != nil {
			return fmt.Errorf("resolving asset %q for slot %q: %w", handler.Kind(), request.AcquiredSlotState.Slot.ResourceName, err)
		}
		if request.LeaseJournal != nil {
			if err := request.LeaseJournal.Persist(); err != nil {
				return err
			}
		}
	}
	return nil
}

// ReleaseLease calls known handlers but always falls back to the journal for
// every recorded name, including kinds removed since acquisition.
func (r *Registry) ReleaseLease(ctx context.Context, request LeaseRequest) error {
	if request.AcquiredSlotState == nil || request.LeaseJournal == nil {
		return errors.New("release requires state and lease journal")
	}
	var errs []error
	for _, handler := range r.ListHandlers() {
		if len(request.AcquiredSlotState.Leases.Assets[handler.Kind()]) > 0 {
			bounded, cancel := context.WithTimeout(context.WithoutCancel(ctx), request.LeaseJournal.Timeout)
			errs = append(errs, handler.ReleaseLease(bounded, request))
			cancel()
		}
	}
	errs = append(errs, request.LeaseJournal.ReleaseAll(ctx))
	return errors.Join(errs...)
}

func (r *Registry) ValidateRequirements(pools []slots.Pool) error {
	for _, pool := range pools {
		for _, assetRequirement := range pool.Requirements() {
			handler, found := r.GetHandler(assetRequirement.Kind)
			if !found {
				return fmt.Errorf("pool %q demands asset %q without an implemented handler", pool.Name, assetRequirement.Kind)
			}
			if !handler.Declared(pool) {
				return fmt.Errorf("handler %q does not accept demanded asset in pool %q", assetRequirement.Kind, pool.Name)
			}
		}
	}
	return nil
}

func (r *Registry) AdmitLease(ctx context.Context, request LeaseRequest) error {
	if request.AcquiredSlotState == nil {
		return errors.New("acquired slot state is nil")
	}
	handlers, err := r.HandlersForRequirements(request.AcquiredSlotState.Slot.AssetRequirements)
	if err != nil {
		return err
	}
	for _, handler := range handlers {
		if err := handler.AdmitLease(ctx, request); err != nil {
			return fmt.Errorf("admitting asset %q for slot %q: %w", handler.Kind(), request.AcquiredSlotState.Slot.ResourceName, err)
		}
	}
	return nil
}

func (r *Registry) PublishLease(ctx context.Context, request LeaseRequest, contract *slots.RuntimeContractBuilder) error {
	if contract == nil {
		return errors.New("runtime contract builder is nil")
	}
	if request.AcquiredSlotState == nil {
		return errors.New("acquired slot state is nil")
	}
	handlers, err := r.HandlersForRequirements(request.AcquiredSlotState.Slot.AssetRequirements)
	if err != nil {
		return err
	}
	for _, handler := range handlers {
		if err := handler.PublishLease(ctx, request, contract); err != nil {
			return fmt.Errorf("publishing asset %q for slot %q: %w", handler.Kind(), request.AcquiredSlotState.Slot.ResourceName, err)
		}
	}
	return nil
}

func (r *Registry) validatePoolRequest(request PoolRequest) error {
	if err := r.ValidateRequirements(request.Pools); err != nil {
		return err
	}
	for _, pool := range request.Pools {
		for _, assetRequirement := range pool.Requirements() {
			if assetRequirement.Allocation != slots.AllocationLeased {
				continue
			}
			assetInventory, found := slots.AssetInventoryForRequirement(request.AssetInventories, assetRequirement)
			if !found || assetInventory.Capacity <= 0 {
				return fmt.Errorf("unresolved inventory for demanded asset pool %q", assetRequirement.AssetPoolName)
			}
		}
	}
	return nil
}

// HandlersForRequirements validates demand before selecting handlers in registration
// order. Unlike an empty CLI filter, empty demand selects no handlers.
func (r *Registry) HandlersForRequirements(assetRequirements []slots.AssetRequirement) ([]Handler, error) {
	demanded := map[Kind]bool{}
	for _, assetRequirement := range assetRequirements {
		if _, found := r.GetHandler(assetRequirement.Kind); !found {
			return nil, fmt.Errorf("demanded asset %q has no implemented handler", assetRequirement.Kind)
		}
		if assetRequirement.Allocation != slots.AllocationDedicated && assetRequirement.Allocation != slots.AllocationLeased {
			return nil, fmt.Errorf("demanded asset %q has invalid allocation %q", assetRequirement.Kind, assetRequirement.Allocation)
		}
		if demanded[assetRequirement.Kind] {
			return nil, fmt.Errorf("duplicate demanded asset %q", assetRequirement.Kind)
		}
		demanded[assetRequirement.Kind] = true
	}
	handlers := make([]Handler, 0, len(demanded))
	for _, handler := range r.ListHandlers() {
		if demanded[handler.Kind()] {
			handlers = append(handlers, handler)
		}
	}
	return handlers, nil
}

// ListHandlers returns a copy of the registry's handlers in registration order.
func (r *Registry) ListHandlers() []Handler {
	return slices.Clone(r.handlers)
}

func (r *Registry) GetHandler(assetKind Kind) (Handler, bool) {
	handler, found := r.byKind[assetKind]
	return handler, found
}

// FilterHandlers treats an empty CLI selection as all registered handlers.
func (r *Registry) FilterHandlers(selectedKinds []Kind) ([]Handler, error) {
	if len(selectedKinds) == 0 {
		return r.ListHandlers(), nil
	}
	selected := map[Kind]struct{}{}
	for _, kind := range selectedKinds {
		kind = Kind(strings.TrimSpace(string(kind)))
		if _, found := r.GetHandler(kind); !found {
			return nil, fmt.Errorf("unknown asset kind %q", kind)
		}
		selected[kind] = struct{}{}
	}
	handlers := make([]Handler, 0, len(selected))
	for _, handler := range r.ListHandlers() {
		if _, found := selected[handler.Kind()]; found {
			handlers = append(handlers, handler)
		}
	}
	return handlers, nil
}

func (request PoolRequest) forHandler(handler Handler) PoolRequest {
	scopedRequest := request
	scopedRequest.Pools = make([]slots.Pool, 0, len(request.Pools))
	for _, pool := range request.Pools {
		if handler.Declared(pool) {
			scopedRequest.Pools = append(scopedRequest.Pools, pool)
		}
	}
	scopedRequest.AssetInventories = assetInventoriesForPools(request.AssetInventories, scopedRequest.Pools, handler.Kind())
	return scopedRequest
}

func assetInventoriesForPools(assetInventories []slots.AssetInventory, pools []slots.Pool, kind Kind) []slots.AssetInventory {
	references := map[string]bool{}
	for _, pool := range pools {
		for _, assetRequirement := range pool.Requirements() {
			if assetRequirement.Kind == kind && assetRequirement.Allocation == slots.AllocationLeased {
				references[assetRequirement.AssetPoolName] = true
			}
		}
	}
	var selected []slots.AssetInventory
	for _, assetInventory := range assetInventories {
		if assetInventory.AssetPool.Kind == kind && references[assetInventory.AssetPool.Name] {
			selected = append(selected, assetInventory)
		}
	}
	return selected
}

func (request LeaseRequest) validateAcquisition() error {
	for _, assetRequirement := range request.AcquiredSlotState.Slot.AssetRequirements {
		if assetRequirement.Allocation != slots.AllocationLeased {
			continue
		}
		if _, found := slots.AssetInventoryForRequirement(request.AssetInventories, assetRequirement); !found {
			return fmt.Errorf("unresolved inventory for demanded asset %q in pool %q", assetRequirement.Kind, assetRequirement.AssetPoolName)
		}
		if request.LeaseJournal == nil {
			return fmt.Errorf("missing lease journal for demanded asset %q in pool %q", assetRequirement.Kind, assetRequirement.AssetPoolName)
		}
		if assetRequirement.UnitsPerSlot <= 0 {
			return fmt.Errorf("invalid units_per_slot %d for demanded asset %q in pool %q: must be positive", assetRequirement.UnitsPerSlot, assetRequirement.Kind, assetRequirement.AssetPoolName)
		}
	}
	return nil
}
