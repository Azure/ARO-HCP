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

package infrastructureidentities

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/spf13/pflag"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"

	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/assets"
	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/assets/azureutil"
	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/slots"
	"github.com/Azure/ARO-HCP/test/pkg/leaseenv"
	"github.com/Azure/ARO-HCP/test/util/framework"
)

type subscriptionIDResolverFunc func(ctx context.Context, name string) (string, error)

type Handler struct {
	topology         Topology
	poolDependencies func() (azcore.TokenCredential, subscriptionIDResolverFunc, error)
	newPoolClients   func(subscriptionID string, credential azcore.TokenCredential) (*poolClients, error)
	newAdmitClients  func(subscriptionID string, credential azcore.TokenCredential) (*admitClients, error)
	cleanupWorkers   int
}

type Option func(*Handler)

const defaultCleanupWorkers = 1

func NewHandler(opts ...Option) *Handler {
	handler := &Handler{
		topology:         DefaultTopology(),
		cleanupWorkers:   defaultCleanupWorkers,
		poolDependencies: azurePoolDependencies,
		newPoolClients:   azurePoolClients,
		newAdmitClients:  azureAdmitClients,
	}
	for _, opt := range opts {
		opt(handler)
	}
	return handler
}

func WithTopology(topology Topology) Option {
	return func(h *Handler) { h.topology = topology }
}

func WithPoolDependencies(fn func() (azcore.TokenCredential, subscriptionIDResolverFunc, error)) Option {
	return func(h *Handler) { h.poolDependencies = fn }
}

func WithPoolClients(fn func(string, azcore.TokenCredential) (*poolClients, error)) Option {
	return func(h *Handler) { h.newPoolClients = fn }
}

func WithAdmitClients(fn func(string, azcore.TokenCredential) (*admitClients, error)) Option {
	return func(h *Handler) { h.newAdmitClients = fn }
}

func (h *Handler) AttachOptions(flags *pflag.FlagSet) {
	flags.Var(&azureutil.PositiveInt{Target: &h.cleanupWorkers, Default: defaultCleanupWorkers}, "cleanup-workers",
		"Maximum concurrent federated-credential and role assignment deletes during admission (at least 1). "+
			"ARM throttles deletes per subscription and service principal, so raise it only where few slots admit concurrently.")
}

func (h *Handler) Kind() assets.Kind {
	return assets.KindInfrastructureIdentities
}

func (h *Handler) Declared(pool slots.Pool) bool {
	asset := pool.SlotAssets.InfrastructureIdentities
	return asset != nil && (asset.UnitsPerSlot == 0 || asset.UnitsPerSlot == 1)
}

func (h *Handler) AcquireLease(_ context.Context, request assets.LeaseRequest) error {
	if request.AcquiredSlotState == nil {
		return fmt.Errorf("acquired slot state is nil")
	}
	leases := request.AcquiredSlotState.Leases.Assets[h.Kind()]
	if len(leases) != 1 {
		return fmt.Errorf("infrastructure identity asset requires exactly one leased bundle, got %d", len(leases))
	}
	resolved, err := resolveBundle(h.topology, leases[0].ResourceName)
	if err != nil {
		return fmt.Errorf("resolving infrastructure identity bundle: %w", err)
	}
	request.AcquiredSlotState.Slot.Assets.InfrastructureIdentities = resolved
	return nil
}

func (h *Handler) ReleaseLease(ctx context.Context, request assets.LeaseRequest) error {
	return request.LeaseJournal.ReleaseAsset(ctx, h.Kind())
}

func (h *Handler) ApplyPools(ctx context.Context, request assets.PoolRequest) error {
	credential, inventories, err := h.resolveInventories(ctx, request)
	if err != nil {
		return err
	}
	return applyInventories(ctx, credential, inventories, h.topology, h.newPoolClients)
}

func (h *Handler) ValidatePools(ctx context.Context, request assets.PoolRequest) error {
	credential, inventories, err := h.resolveInventories(ctx, request)
	if err != nil {
		return err
	}
	out := request.Out
	if out == nil {
		out = io.Discard
	}
	return validateInventories(ctx, credential, inventories, h.topology, h.newPoolClients, out)
}

func (h *Handler) AdmitLease(ctx context.Context, request assets.LeaseRequest) error {
	credential, subscriptionID, err := leaseCredential(request)
	if err != nil {
		return err
	}
	clients, err := h.newAdmitClients(subscriptionID, credential)
	if err != nil {
		return fmt.Errorf("failed creating infrastructure identity admission clients: %w", err)
	}
	return admitIdentityLeaseWithClients(ctx, request, h.topology, clients, h.cleanupWorkers)
}

func (h *Handler) PublishLease(_ context.Context, request assets.LeaseRequest, contract *slots.RuntimeContractBuilder) error {
	if err := validatePublishRequest(request); err != nil {
		return err
	}
	bundle, err := h.topology.Bundle(request.AcquiredSlotState.Slot.Assets.InfrastructureIdentities.ResourceGroups[0])
	if err != nil {
		return fmt.Errorf("resolving published infrastructure identity bundle: %w", err)
	}
	payload, err := json.Marshal(bundle)
	if err != nil {
		return fmt.Errorf("failed encoding infrastructure identity bundle: %w", err)
	}
	return contract.Add(string(h.Kind()), leaseenv.InfrastructureIdentityBundle, string(payload))
}

func validatePublishRequest(request assets.LeaseRequest) error {
	if request.AcquiredSlotState == nil || request.AcquiredSlotState.Slot.Assets.InfrastructureIdentities == nil {
		return fmt.Errorf("resolved infrastructure identities asset is missing")
	}
	if got := len(request.AcquiredSlotState.Slot.Assets.InfrastructureIdentities.ResourceGroups); got != 1 {
		return fmt.Errorf("infrastructure identity asset requires exactly one leased bundle, got %d", got)
	}
	return nil
}

func resolveBundle(topology Topology, leaseName string) (*slots.ResolvedInfrastructureIdentities, error) {
	if _, err := topology.Bundle(leaseName); err != nil {
		return nil, err
	}
	return &slots.ResolvedInfrastructureIdentities{
		Allocation:     slots.AllocationLeased,
		ResourceGroups: []string{leaseName},
		Identities:     topology.IdentityInventory(),
	}, nil
}

func azurePoolDependencies() (azcore.TokenCredential, subscriptionIDResolverFunc, error) {
	tc := framework.NewTestContext()
	credential, err := tc.AzureCredential()
	if err != nil {
		return nil, nil, fmt.Errorf("failed getting Azure credential: %w", err)
	}
	subscriptionClientFactory, err := tc.GetARMSubscriptionsClientFactory()
	if err != nil {
		return nil, nil, fmt.Errorf("failed getting ARM subscriptions client factory: %w", err)
	}
	subscriptionClient := subscriptionClientFactory.NewClient()
	return credential, func(ctx context.Context, name string) (string, error) {
		return framework.GetSubscriptionID(ctx, subscriptionClient, name)
	}, nil
}
