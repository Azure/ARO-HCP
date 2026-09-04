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

// Package agentpools provides shared helpers for working with AKS agent pools
// in fleet controllers.
package agentpools

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v6"

	"github.com/Azure/ARO-HCP/fleet/pkg/compute"
)

// NewClientFactory builds a subscription-scoped AgentPoolsClient factory for
// credential, resolving a nil clientOptions the same way every caller needs
// to. It also returns the resolved ARM client options so callers that build
// other subscription-scoped clients (e.g. armcompute.UsageClient) can reuse
// them instead of resolving clientOptions a second time.
func NewClientFactory(credential azcore.TokenCredential, clientOptions *policy.ClientOptions) (func(subscriptionID string) (*armcontainerservice.AgentPoolsClient, error), *azcorearm.ClientOptions) {
	if clientOptions == nil {
		clientOptions = &policy.ClientOptions{}
	}
	armClientOptions := &azcorearm.ClientOptions{ClientOptions: *clientOptions}

	factory := func(subscriptionID string) (*armcontainerservice.AgentPoolsClient, error) {
		return armcontainerservice.NewAgentPoolsClient(subscriptionID, credential, armClientOptions)
	}
	return factory, armClientOptions
}

// PoolRole returns the role label value for the given agent pool.
// Returns "" when the pool has no role label.
func PoolRole(pool armcontainerservice.AgentPool) string {
	if pool.Properties == nil || pool.Properties.NodeLabels == nil {
		return ""
	}
	role := pool.Properties.NodeLabels[compute.RoleLabel]
	if role == nil {
		return ""
	}
	return *role
}

// IsManagedPool returns true if the agent pool has a role label, meaning it
// is managed by the controller (system, infra, or worker).
func IsManagedPool(pool armcontainerservice.AgentPool) bool {
	return len(PoolRole(pool)) > 0
}

// PoolMaxCount returns the pool's node ceiling: MaxCount when autoscaling is
// enabled, otherwise the static Count. Returns 0 when Count is unset.
func PoolMaxCount(pool armcontainerservice.AgentPool) int64 {
	if pool.Properties == nil || pool.Properties.Count == nil {
		return 0
	}
	count := int64(*pool.Properties.Count)
	if pool.Properties.EnableAutoScaling != nil && *pool.Properties.EnableAutoScaling && pool.Properties.MaxCount != nil {
		count = int64(*pool.Properties.MaxCount)
	}
	return count
}

// ListAgentPools lists the current AKS agent pools of a management cluster.
// It is fetched live on every reconcile (not cached): it's a single,
// targeted call scoped to one management cluster, and pool state (Count)
// changes frequently as the autoscaler reacts to load.
func ListAgentPools(
	ctx context.Context,
	agentPoolClientFactory func(subscriptionID string) (*armcontainerservice.AgentPoolsClient, error),
	aksResourceID *azcorearm.ResourceID,
) ([]armcontainerservice.AgentPool, error) {
	client, err := agentPoolClientFactory(aksResourceID.SubscriptionID)
	if err != nil {
		return nil, fmt.Errorf("creating agent pool client: %w", err)
	}

	var pools []armcontainerservice.AgentPool
	pager := client.NewListPager(aksResourceID.ResourceGroupName, aksResourceID.Name, nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("listing agent pools: %w", err)
		}
		for _, pool := range page.Value {
			if pool != nil {
				pools = append(pools, *pool)
			}
		}
	}
	return pools, nil
}
