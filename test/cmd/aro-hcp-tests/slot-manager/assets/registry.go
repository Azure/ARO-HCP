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
	"reflect"
	"slices"
	"strings"

	"github.com/go-logr/logr"

	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/slots"
)

// Kind identifies an asset category shared by catalog declarations and handlers.
type Kind = slots.AssetKind

// KindE2EIdentities identifies reusable E2E managed identity containers.
const KindE2EIdentities = slots.KindE2EIdentities

// PoolRequest supplies catalog data and options for pool provisioning or validation.
// The registry scopes pools and inventories to each handler without changing capacity.
type PoolRequest struct {
	// AssetInventories contains independent asset pools with whole-catalog capacities.
	AssetInventories []slots.AssetInventory
	// Environment selects the deployment environment.
	Environment string
	// Pools contains the primary slot pools to process.
	Pools []slots.Pool
	// IncludeUnmanaged includes catalog pools marked for unmanaged provisioning.
	IncludeUnmanaged bool
	// Out receives validation reports.
	Out io.Writer
}

// LeaseRequest shares durable ownership and resolved slot state across
// acquisition, admission, publication, and release.
type LeaseRequest struct {
	// LeaseJournal records acquisitions and returns; it is required for leased assets.
	LeaseJournal *slots.LeaseJournal
	// AcquiredSlotState holds the slot, resolved assets, and recorded leases.
	AcquiredSlotState *slots.AcquiredSlotState
	// SelectedClusterProfileDir locates the selected cluster's subscription credentials.
	SelectedClusterProfileDir string
	// SkipAdmissionCleanup disables admission mutations, never reuse safety checks.
	// The registry sets this separately for each demanded asset.
	SkipAdmissionCleanup bool
	// IdentityConsumerGuardMode controls enforcement without disabling inventory auditing or cleanup.
	IdentityConsumerGuardMode string
	MinimumIdentityContainers int
}

// Handler implements pool management and the lease lifecycle for one asset kind.
// The registry owns shared leasing mechanics; handlers resolve and prepare assets.
type Handler interface {
	// Kind returns the unique asset kind handled by this implementation.
	Kind() Kind
	// Declared reports whether the handler applies to the pool.
	// Required assets always apply; opt-in assets depend on the pool's declaration.
	Declared(pool slots.Pool) bool
	// AcquireLease resolves assets after the registry acquires any required leases.
	AcquireLease(ctx context.Context, request LeaseRequest) error
	// ReleaseLease performs kind-specific release before the registry's journal fallback.
	ReleaseLease(ctx context.Context, request LeaseRequest) error
	// ApplyPools provisions assets for the handler-scoped pools and inventories.
	ApplyPools(ctx context.Context, request PoolRequest) error
	// ValidatePools checks provisioned assets against the handler-scoped catalog data.
	ValidatePools(ctx context.Context, request PoolRequest) error
	// AdmitLease checks safe reuse even when SkipAdmissionCleanup disables mutations.
	AdmitLease(ctx context.Context, request LeaseRequest) error
	// PublishLease adds the assets' runtime exports after successful admission.
	PublishLease(ctx context.Context, request LeaseRequest, contract *slots.RuntimeContractBuilder) error
}

// Registry selects asset handlers and coordinates their lifecycle in registration order.
type Registry struct {
	handlers []Handler
	byKind   map[Kind]Handler
}

// NewRegistry registers handlers in the supplied order, rejecting nil handlers
// and empty, whitespace-padded, or duplicate kinds.
func NewRegistry(handlers ...Handler) (*Registry, error) {
	registry := &Registry{
		handlers: make([]Handler, 0, len(handlers)),
		byKind:   make(map[Kind]Handler, len(handlers)),
	}
	for _, handler := range handlers {
		if handler == nil {
			return nil, errors.New("asset handler is nil")
		}
		value := reflect.ValueOf(handler)
		switch value.Kind() {
		case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
			if value.IsNil() {
				return nil, errors.New("asset handler is nil")
			}
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

// ApplyPools validates catalog demands, then provisions each selected handler's
// declared pools. Omitting selectedKinds selects all handlers.
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

// ValidatePools validates catalog demands, then checks each selected handler's
// declared pools. Omitting selectedKinds selects all handlers.
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

// AcquireLeases validates demands against assetInventories, acquires required
// independent asset leases, and resolves all demanded assets through their handlers.
// It persists state; the caller must release partial acquisitions on failure.
func (r *Registry) AcquireLeases(ctx context.Context, request LeaseRequest, assetInventories []slots.AssetInventory) error {
	if request.AcquiredSlotState == nil {
		return errors.New("acquired slot state is nil")
	}
	handlers, err := r.HandlersForRequirements(request.AcquiredSlotState.Slot.AssetRequirements)
	if err != nil {
		return err
	}
	if err := request.validateAcquisition(assetInventories); err != nil {
		return err
	}
	for _, handler := range handlers {
		for _, assetRequirement := range request.AcquiredSlotState.Slot.AssetRequirements {
			if assetRequirement.Kind != handler.Kind() || assetRequirement.Allocation != slots.AllocationLeased {
				continue
			}
			assetInventory, found := slots.AssetInventoryForRequirement(assetInventories, assetRequirement)
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

// ValidateRequirements checks that every pool demand has a registered handler
// that accepts the pool's declaration.
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

// AdmitLease prepares and checks demanded assets for exclusive reuse.
// Call it after acquisition and before publishing runtime exports.
// Explicitly disabled kinds skip cleanup, not safety checks; unknown kinds are rejected.
func (r *Registry) AdmitLease(ctx context.Context, request LeaseRequest, disabledKinds ...Kind) error {
	if request.AcquiredSlotState == nil {
		return errors.New("acquired slot state is nil")
	}
	handlers, err := r.HandlersForRequirements(request.AcquiredSlotState.Slot.AssetRequirements)
	if err != nil {
		return err
	}
	disabled := map[Kind]bool{}
	if len(disabledKinds) > 0 {
		disabledHandlers, err := r.FilterHandlers(disabledKinds)
		if err != nil {
			return err
		}
		for _, handler := range disabledHandlers {
			disabled[handler.Kind()] = true
		}
	}
	for _, handler := range handlers {
		request.SkipAdmissionCleanup = disabled[handler.Kind()]
		if disabled[handler.Kind()] {
			logr.FromContextOrDiscard(ctx).Info("WARNING: asset admission explicitly disabled for cleanup; reuse safety checks still apply",
				"assetKind", handler.Kind(),
				"slotName", request.AcquiredSlotState.Slot.ResourceName,
			)
		}
		if err := handler.AdmitLease(ctx, request); err != nil {
			return fmt.Errorf("admitting asset %q for slot %q: %w", handler.Kind(), request.AcquiredSlotState.Slot.ResourceName, err)
		}
	}
	return nil
}

// PublishLease adds demanded assets' runtime exports to contract.
// The caller must first admit every lease, including explicitly disabled asset kinds.
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

// GetHandler looks up an exact asset kind and reports whether it is registered.
func (r *Registry) GetHandler(assetKind Kind) (Handler, bool) {
	handler, found := r.byKind[assetKind]
	return handler, found
}

// FilterHandlers resolves CLI kind selectors in registration order, trimming
// whitespace and removing duplicates. Empty selection means all handlers;
// unknown kinds return an error.
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

func (request LeaseRequest) validateAcquisition(assetInventories []slots.AssetInventory) error {
	for _, assetRequirement := range request.AcquiredSlotState.Slot.AssetRequirements {
		if assetRequirement.Allocation != slots.AllocationLeased {
			continue
		}
		if _, found := slots.AssetInventoryForRequirement(assetInventories, assetRequirement); !found {
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
