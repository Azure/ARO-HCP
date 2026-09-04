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

package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sync/atomic"
	"time"

	"github.com/go-logr/logr"
	"golang.org/x/sync/errgroup"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v8"

	"github.com/Azure/ARO-HCP/fleet/pkg/azure/agentpoolspec"
	"github.com/Azure/ARO-HCP/fleet/pkg/azure/quota"
	"github.com/Azure/ARO-HCP/fleet/pkg/compute"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// provisioningStateFailed is the AKS ProvisioningState that triggers a pool or
// cluster re-create on a resumed run.
const provisioningStateFailed = "Failed"

// run retries conflicts from a fresh observation, never by replaying a stale
// payload. A concurrent provisioner or fleet controller may have advanced the
// lifecycle or recorded a baseline while our conditional write was in flight.
func (o *completedOptions) run(ctx context.Context, logger logr.Logger) error {
	var lastConflict error
	err := wait.ExponentialBackoffWithContext(ctx, wait.Backoff{
		Duration: time.Second, Factor: 2, Jitter: 0.1, Steps: 6,
	}, func(ctx context.Context) (bool, error) {
		err := o.reconcileCluster(ctx, logger)
		var responseErr *azcore.ResponseError
		if errors.As(err, &responseErr) && (responseErr.StatusCode == http.StatusConflict || responseErr.StatusCode == http.StatusPreconditionFailed) {
			lastConflict = err
			logger.Info("conditional write conflicted, restarting reconciliation", "error", err)
			return false, nil
		}
		return err == nil, err
	})
	if err != nil && lastConflict != nil && wait.Interrupted(err) {
		return fmt.Errorf("cluster reconciliation retries interrupted: %w", errors.Join(err, lastConflict))
	}
	return err
}

func (o *completedOptions) reconcileCluster(ctx context.Context, logger logr.Logger) error {
	cluster, err := getCluster(ctx, o.clustersClient, o.resourceGroup, o.clusterName)
	if err != nil {
		return fmt.Errorf("getting cluster: %w", err)
	}
	// Only creation needs allocation before the cluster PUT. Existing clusters
	// reconcile configuration first, even if subsequent pool allocation fails.
	var pools []compute.Pool
	var bootstrapSystemPool *compute.Pool
	if cluster == nil {
		pools, bootstrapSystemPool, err = o.computeDesiredPools(ctx, logger)
		if err != nil {
			return err
		}
	}
	cluster, err = o.ensureManagedCluster(ctx, cluster, bootstrapSystemPool, logger)
	if err != nil {
		return err
	}
	var createdInlinePoolName string
	if bootstrapSystemPool != nil {
		createdInlinePoolName = bootstrapSystemPool.Name
	}
	if hasProvisioningTag(cluster.Tags) {
		if pools == nil {
			pools, _, err = o.computeDesiredPools(ctx, logger)
			if err != nil {
				return err
			}
		}
		cluster, err = o.provisionPools(ctx, cluster, pools, createdInlinePoolName, logger)
		if err != nil {
			return err
		}
	}
	return o.reconcileClusterTags(ctx, cluster)
}

// provisionPools owns pools only until the provisioning marker is removed.
// Only a pool just created inline is skipped; resumed runs treat every pool
// alike, using each pool's own ETag for recovery.
func (o *completedOptions) provisionPools(ctx context.Context, cluster *armcontainerservice.ManagedCluster, pools []compute.Pool, createdInlinePoolName string, logger logr.Logger) (*armcontainerservice.ManagedCluster, error) {
	networkConfig := compute.NetworkConfig{VnetSubnetID: o.nodeSubnetID, PodSubnetID: o.podSubnetID}
	var poolsChanged atomic.Bool
	g, gctx := errgroup.WithContext(ctx)
	for _, pool := range pools {
		if pool.Name == createdInlinePoolName {
			continue
		}
		g.Go(func() error {
			defer utilruntime.HandleCrash()
			changed, err := ensurePool(gctx, o.poolsClient, o.resourceGroup, o.clusterName, pool, networkConfig, logger)
			if changed {
				poolsChanged.Store(true)
			}
			return err
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	if poolsChanged.Load() {
		// AgentPool writes can change the parent ETag. Refresh only when that
		// observation was invalidated; no tag helper performs a hidden GET.
		refreshed, err := getCluster(ctx, o.clustersClient, o.resourceGroup, o.clusterName)
		if err != nil {
			return nil, fmt.Errorf("refreshing cluster after pool updates: %w", err)
		}
		if refreshed == nil {
			return nil, fmt.Errorf("cluster disappeared during pool provisioning")
		}
		cluster = refreshed
	}
	return cluster, nil
}

// getCluster returns the cluster, or nil if it does not exist.
func getCluster(ctx context.Context, client *armcontainerservice.ManagedClustersClient, resourceGroup, clusterName string) (*armcontainerservice.ManagedCluster, error) {
	resp, err := client.Get(ctx, resourceGroup, clusterName, nil)
	if err != nil {
		var respErr *azcore.ResponseError
		if errors.As(err, &respErr) && respErr.StatusCode == http.StatusNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &resp.ManagedCluster, nil
}

// computeDesiredPools resolves and validates the provisioning plan using fleet's
// allocator. The bootstrap pointer refers to the first system pool in the result
// that is required when provisioning a cluster.
func (o *completedOptions) computeDesiredPools(ctx context.Context, logger logr.Logger) ([]compute.Pool, *compute.Pool, error) {
	ctx = utils.ContextWithLogger(ctx, logger)
	resolved, err := compute.ResolveDesiredPools(ctx, o.skuCache, o.subscriptionID, o.profile, o.zones, o.fetchUsage)
	if err != nil {
		return nil, nil, fmt.Errorf("computing desired pools: %w", err)
	}
	logger.Info("computed desired pools", "pools", len(resolved.Pools))
	if len(resolved.Failures) > 0 {
		logger.Info("some tiers could not be fully allocated", "allocationFailures", resolved.Failures)
	}
	if compute.RequiredTierFailed(resolved.Failures) {
		return nil, nil, fmt.Errorf("required tier allocation failed: %s", compute.FailureSummary(resolved.Failures))
	}
	bootstrapSystemPoolIndex := slices.IndexFunc(resolved.Pools, func(pool compute.Pool) bool {
		return pool.Role == compute.PoolRoleSystem
	})
	if bootstrapSystemPoolIndex < 0 {
		return nil, nil, fmt.Errorf("no system pool could be allocated: %s", compute.FailureSummary(resolved.Failures))
	}
	return resolved.Pools, &resolved.Pools[bootstrapSystemPoolIndex], nil
}

// fetchUsage adapts the tool's pre-built usage client to compute.FetchQuotaUsageFunc.
func (o *completedOptions) fetchUsage(ctx context.Context, families sets.Set[compute.VMFamily]) (map[compute.VMFamily]compute.QuotaUsage, error) {
	return quota.FetchUsage(ctx, o.usageClient, o.region, families)
}

// ensurePool creates missing pools and retries failed ones without overwriting a
// concurrent change. It reports successful writes so the caller can refresh the
// parent's ETag before finalizing tags.
func ensurePool(ctx context.Context, client *armcontainerservice.AgentPoolsClient, resourceGroup, clusterName string, pool compute.Pool, networkConfig compute.NetworkConfig, logger logr.Logger) (bool, error) {
	existing, err := client.Get(ctx, resourceGroup, clusterName, pool.Name, nil)
	options := &armcontainerservice.AgentPoolsClientBeginCreateOrUpdateOptions{}
	if err == nil {
		state := ""
		if existing.Properties != nil {
			state = ptr.Deref(existing.Properties.ProvisioningState, "")
		}
		if state != provisioningStateFailed {
			logger.Info("pool already exists, skipping", "pool", pool.Name, "provisioningState", state)
			return false, nil
		}
		if existing.Properties.ETag == nil || *existing.Properties.ETag == "" {
			return false, fmt.Errorf("pool %q has no ETag for update", pool.Name)
		}
		options.IfMatch = existing.Properties.ETag
		logger.Info("recovering failed pool", "pool", pool.Name)
	} else {
		var respErr *azcore.ResponseError
		if !errors.As(err, &respErr) || respErr.StatusCode != http.StatusNotFound {
			return false, fmt.Errorf("checking pool %s: %w", pool.Name, err)
		}
		options.IfNoneMatch = ptr.To("*")
		logger.Info("creating pool", "pool", pool.Name, "role", pool.Role, "vmSize", pool.Spec.Size)
	}
	agentPool := armcontainerservice.AgentPool{Properties: agentpoolspec.Build(pool, networkConfig)}
	poller, err := client.BeginCreateOrUpdate(ctx, resourceGroup, clusterName, pool.Name, agentPool, options)
	if err != nil {
		return false, fmt.Errorf("creating pool %s: %w", pool.Name, err)
	}
	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		return false, fmt.Errorf("waiting for pool %s: %w", pool.Name, err)
	}
	return true, nil
}
