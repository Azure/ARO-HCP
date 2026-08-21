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

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v6"
)

// WorkerPoolLabel is the AKS node label key that identifies HCP worker pools.
// Pools with aro-hcp.azure.com/role=worker are the only pools considered for
// scheduling capacity. This must match the label selector used by the
// mgmt-agent CapacityReport controller to filter nodes.
const WorkerPoolLabel = "aro-hcp.azure.com/role"

// WorkerPoolLabelValue is the expected value for WorkerPoolLabel on HCP worker pools.
const WorkerPoolLabelValue = "worker"

// IsWorkerPool returns true if the AKS agent pool is labeled as an HCP worker.
func IsWorkerPool(pool armcontainerservice.AgentPool) bool {
	if pool.Properties == nil || pool.Properties.NodeLabels == nil {
		return false
	}
	role := pool.Properties.NodeLabels[WorkerPoolLabel]
	return role != nil && *role == WorkerPoolLabelValue
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
