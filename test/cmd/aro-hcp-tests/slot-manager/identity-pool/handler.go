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

package identitypool

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"

	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/assets"
	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/slots"
	"github.com/Azure/ARO-HCP/test/e2e"
	"github.com/Azure/ARO-HCP/test/util/framework"
)

type Handler struct {
	poolDependencies func() (azcore.TokenCredential, subscriptionIDResolverFunc, error)
}

func NewHandler() *Handler {
	return &Handler{}
}

func (h *Handler) Kind() assets.Kind {
	return assets.KindE2EIdentities
}

func (h *Handler) Declared(pool slots.Pool) bool {
	return pool.SlotAssets.E2EIdentities != nil || pool.IdentityContainerCount > 0
}

func (h *Handler) AcquireLease(_ context.Context, request assets.LeaseRequest) error {
	if request.State == nil {
		return fmt.Errorf("acquired slot state is nil")
	}
	slot := &request.State.Slot
	if slot.Assets.E2EIdentities == nil {
		slot.Assets.E2EIdentities = &slots.ResolvedE2EIdentitiesAsset{
			Allocation:     slots.AllocationDedicated,
			ResourceGroups: slot.IdentityContainerNames(),
		}
	}
	if len(slot.Assets.E2EIdentities.ResourceGroups) == 0 {
		return fmt.Errorf("resolved E2E identities asset has no resource groups")
	}
	return nil
}

func (h *Handler) ReleaseLease(ctx context.Context, request assets.LeaseRequest) error {
	return request.Journal.ReleaseAsset(ctx, h.Kind())
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

	return (&ApplyOptions{
		Template:        bicepTemplateMap,
		IdentityPools:   pools,
		AzureCredential: credential,
	}).Run(ctx)
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
	return (&ValidateOptions{
		IdentityPools: pools,
		LoadInventory: func(ctx context.Context, subscriptionID string) (subscriptionInventory, error) {
			return loadSubscriptionInventory(ctx, subscriptionID, credential)
		},
		Out: out,
	}).Run(ctx)
}

func (h *Handler) resolvePools(ctx context.Context, request assets.PoolRequest) (azcore.TokenCredential, []identityPool, error) {
	dependencies := h.poolDependencies
	if dependencies == nil {
		dependencies = azurePoolDependencies
	}
	credential, resolveSubscriptionID, err := dependencies()
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

func (h *Handler) PrepareLease(ctx context.Context, request assets.LeaseRequest) error {
	return prepareE2EIdentityLease(ctx, request)
}

func (h *Handler) ValidateLease(ctx context.Context, request assets.LeaseRequest) error {
	return validateE2EIdentityLease(ctx, request)
}

func (h *Handler) PublishLease(_ context.Context, request assets.LeaseRequest, contract *slots.RuntimeContractBuilder) error {
	return contract.Add(string(h.Kind()), "LEASED_MSI_CONTAINERS", strings.Join(request.State.Slot.IdentityContainerNames(), " "))
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

func unmanagedFilter(request assets.PoolRequest) []string {
	if !request.IncludeUnmanaged {
		return nil
	}
	subscriptions := make([]string, 0, len(request.Pools))
	for _, pool := range request.Pools {
		subscriptions = append(subscriptions, pool.E2ESubscriptionName())
	}
	return subscriptions
}
