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

package e2eidentities

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/pflag"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armdeploymentstacks"

	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/assets"
	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/assets/azureutil"
	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/slots"
	"github.com/Azure/ARO-HCP/test/e2e"
	"github.com/Azure/ARO-HCP/test/pkg/leaseenv"
	"github.com/Azure/ARO-HCP/test/util/framework"
)

type Handler struct {
	poolDependencies func() (azcore.TokenCredential, subscriptionIDResolverFunc, error)
	newPoolClients   func(subscriptionID string, credential azcore.TokenCredential) (poolClients, error)
	newAdmitClients  func(subscriptionID string, credential azcore.TokenCredential) (*admitClients, error)
	cleanupWorkers   int
}

type Option func(*Handler)

const defaultCleanupWorkers = 1

func NewHandler(opts ...Option) *Handler {
	handler := &Handler{
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

func WithPoolDependencies(fn func() (azcore.TokenCredential, subscriptionIDResolverFunc, error)) Option {
	return func(h *Handler) { h.poolDependencies = fn }
}

func WithPoolClients(fn func(string, azcore.TokenCredential) (poolClients, error)) Option {
	return func(h *Handler) { h.newPoolClients = fn }
}

func WithAdmitClients(fn func(string, azcore.TokenCredential) (*admitClients, error)) Option {
	return func(h *Handler) { h.newAdmitClients = fn }
}

func (h *Handler) AttachOptions(flags *pflag.FlagSet) {
	flags.Var(&azureutil.PositiveInt{Target: &h.cleanupWorkers, Default: defaultCleanupWorkers}, "cleanup-workers",
		"Maximum concurrent role assignment deletes during admission (at least 1). "+
			"ARM throttles deletes per subscription and service principal, so raise it only where few slots admit concurrently.")
}

func (h *Handler) Kind() assets.Kind {
	return assets.KindE2EIdentities
}

func (h *Handler) Declared(_ slots.Pool) bool {
	return true
}

func (h *Handler) AcquireLease(_ context.Context, request assets.LeaseRequest) error {
	if request.AcquiredSlotState == nil {
		return fmt.Errorf("acquired slot state is nil")
	}
	slot := &request.AcquiredSlotState.Slot
	if slot.Assets.E2EIdentities == nil || len(slot.Assets.E2EIdentities.ResourceGroups) == 0 {
		return fmt.Errorf("resolved E2E identities asset has no resource groups")
	}
	return nil
}

func (h *Handler) ReleaseLease(ctx context.Context, request assets.LeaseRequest) error {
	return request.LeaseJournal.ReleaseAsset(ctx, h.Kind())
}

func (h *Handler) ApplyPools(ctx context.Context, request assets.PoolRequest) error {
	credential, pools, err := h.resolvePools(ctx, request)
	if err != nil {
		return err
	}
	template, err := e2e.TestArtifactsFS.ReadFile("test-artifacts/generated-test-artifacts/msi-pools.json")
	if err != nil {
		return fmt.Errorf("failed reading template file: %w", err)
	}
	bicepTemplateMap := map[string]interface{}{}
	if err := json.Unmarshal(template, &bicepTemplateMap); err != nil {
		return fmt.Errorf("failed to unmarshal Bicep template JSON: %w", err)
	}
	return applyPools(ctx, credential, pools, bicepTemplateMap, h.newPoolClients)
}

func (h *Handler) ValidatePools(ctx context.Context, request assets.PoolRequest) error {
	credential, pools, err := h.resolvePools(ctx, request)
	if err != nil {
		return err
	}
	out := request.Out
	if out == nil {
		out = io.Discard
	}
	return validatePools(ctx, pools, func(ctx context.Context, subscriptionID string) (subscriptionInventory, error) {
		return loadSubscriptionInventory(ctx, subscriptionID, credential)
	}, out)
}

func (h *Handler) resolvePools(ctx context.Context, request assets.PoolRequest) (azcore.TokenCredential, []identityPool, error) {
	credential, resolveSubscriptionID, err := h.poolDependencies()
	if err != nil {
		return nil, nil, err
	}
	pools, err := resolveIdentityPools(ctx, request.Environment, request.Pools, unmanagedFilter(request), resolveSubscriptionID)
	if err != nil {
		return nil, nil, err
	}
	if len(pools) == 0 {
		return nil, nil, fmt.Errorf("no E2E identity pools matched environment %q", request.Environment)
	}
	return credential, pools, nil
}

func (h *Handler) AdmitLease(ctx context.Context, request assets.LeaseRequest) error {
	credential, subscriptionID, err := leaseCredential(request)
	if err != nil {
		return err
	}
	clients, err := h.newAdmitClients(subscriptionID, credential)
	if err != nil {
		return fmt.Errorf("failed creating E2E identity admission clients: %w", err)
	}
	return admitIdentityLeaseWithClients(ctx, request, clients, h.cleanupWorkers)
}

func (h *Handler) PublishLease(_ context.Context, request assets.LeaseRequest, contract *slots.RuntimeContractBuilder) error {
	containers := request.AcquiredSlotState.Slot.IdentityContainerNames()
	return contract.Add(string(h.Kind()), leaseenv.MSIContainers, strings.Join(containers, " "))
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

func azurePoolClients(subscriptionID string, credential azcore.TokenCredential) (poolClients, error) {
	client, err := armdeploymentstacks.NewClient(subscriptionID, credential, nil)
	if err != nil {
		return poolClients{}, fmt.Errorf("failed creating deployment stack client: %w", err)
	}
	return poolClients{stacks: client}, nil
}

func unmanagedFilter(request assets.PoolRequest) []string {
	if !request.IncludeUnmanaged {
		return nil
	}
	subscriptions := make([]string, 0, len(request.Pools))
	for _, pool := range request.Pools {
		subscriptions = append(subscriptions, pool.Subscriptions.E2E)
	}
	return subscriptions
}
