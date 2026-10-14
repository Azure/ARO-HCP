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

	"github.com/go-logr/logr"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v8"

	"github.com/Azure/ARO-HCP/fleet/pkg/azure/skucache"
	"github.com/Azure/ARO-HCP/fleet/pkg/compute"
)

// The Azure SDK exposes cluster and agent pool provisioning states as strings.
const (
	provisioningStateSucceeded = "Succeeded"
	provisioningStateFailed    = "Failed"
	provisioningStateCanceled  = "Canceled"
)

const deploymentName = "aks-cluster-create"

// Per-role pod density and scheduling policy, hardcoded to match what the
// mgmt-cluster Bicep (aks-cluster-base.bicep and modules/aks/pool.bicep) set
// before this tool replaced it.
const (
	systemMaxPods = 100
	workerMaxPods = 225
	infraMaxPods  = 225
)

// run delegates cluster and pool writes and their polling to ARM. A stable
// deployment name lets a restarted invocation wait for an interrupted deployment
// before building a new payload from the latest cluster observation.
func (o *completedOptions) run(ctx context.Context) error {
	logger := logr.FromContextOrDiscard(ctx)

	// Phase 1: resolve and validate the configured pools.
	pools, err := o.desiredPools(ctx)
	if err != nil {
		return err
	}

	// Phase 2: wait for previous operations and observe the current cluster.
	if err := o.waitForDeployment(ctx, deploymentName); err != nil {
		return err
	}
	cluster, err := getCluster(ctx, o.clustersClient, o.resourceGroup, o.clusterName)
	if err != nil {
		return fmt.Errorf("getting cluster: %w", err)
	}
	if cluster != nil {
		cluster, err = o.waitForManagedCluster(ctx, cluster)
		if err != nil {
			return err
		}
	}

	// Phase 3: deploy changed cluster configuration and configured pools.
	deployment, changed, err := o.buildDeployment(cluster, pools)
	if err != nil {
		return err
	}
	logger.Info("deploying configured resources", "deployment", deploymentName, "poolCount", len(pools), "clusterFields", changed)
	poller, err := o.deploymentsClient.BeginCreateOrUpdate(ctx, o.resourceGroup, deploymentName, deployment, nil)
	if err != nil {
		o.logDeploymentOperations(ctx, deploymentName)
		return fmt.Errorf("submitting deployment %s: %w", deploymentName, err)
	}
	_, err = poller.PollUntilDone(ctx, nil)
	o.logDeploymentOperations(ctx, deploymentName)
	if err != nil {
		return fmt.Errorf("waiting for deployment %s: %w", deploymentName, err)
	}

	// Phase 4: refresh the cluster and finalize handover tags.
	// Deployment success is the barrier for handover. Refresh the cluster and
	// its ETag once, rather than observing every agent pool separately.
	cluster, err = getCluster(ctx, o.clustersClient, o.resourceGroup, o.clusterName)
	if err != nil {
		return fmt.Errorf("refreshing cluster after deployment: %w", err)
	}
	if cluster == nil {
		return fmt.Errorf("cluster missing after deployment %s", deploymentName)
	}
	return o.reconcileClusterTags(ctx, cluster)
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

// poolPlacement is one concrete pool (name plus its availability zones)
// produced by expanding a configured pool block, reproducing the naming and
// zonal-vs-nonzonal split of dev-infrastructure/modules/aks/pool.bicep.
type poolPlacement struct {
	name  string
	zones []string
}

// desiredPools builds the static provisioning plan from per-pool
// configuration, reproducing the pool shapes the mgmt-cluster Bicep created.
// Each pool's VM size is validated against the Azure Resource SKUs of the
// region.
func (o *completedOptions) desiredPools(ctx context.Context) ([]compute.Pool, error) {
	logger := logr.FromContextOrDiscard(ctx)
	metadata, err := o.skuCache.SKUMetadataByVMSize(ctx, o.subscriptionID)
	if err != nil {
		return nil, fmt.Errorf("fetching SKU metadata: %w", err)
	}

	// The system pool is a single pool spanning all its zones (matching the
	// inline agentPoolProfiles entry in aks-cluster-base.bicep), while the
	// worker and infra blocks expand into one pool per zone plus non-zonal
	// pools (matching modules/aks/pool.bicep).
	systemPools, err := buildPools(o.system, compute.PoolRoleSystem, metadata)
	if err != nil {
		return nil, err
	}
	workerPools, err := buildPools(o.user, compute.PoolRoleWorker, metadata)
	if err != nil {
		return nil, err
	}
	infraPools, err := buildPools(o.infra, compute.PoolRoleInfra, metadata)
	if err != nil {
		return nil, err
	}

	pools := make([]compute.Pool, 0, len(systemPools)+len(workerPools)+len(infraPools))
	pools = append(pools, systemPools...)
	pools = append(pools, workerPools...)
	pools = append(pools, infraPools...)

	logger.Info("configured pools", "system", len(systemPools), "worker", len(workerPools), "infra", len(infraPools))
	return pools, nil
}

// buildPools expands one configured pool block into concrete compute.Pools for
// the role. PoolModeRegional builds a single pool spanning all zones;
// PoolModePerZone expands into per-zone and non-zonal pools. Worker pools attach
// the SKU's secondary NIC ceiling; system and infra pools attach none.
func buildPools(cfg poolConfig, role compute.PoolRole, metadata map[string]*skucache.SKUMetadata) ([]compute.Pool, error) {
	meta := metadata[cfg.vmSize]
	if meta == nil {
		return nil, fmt.Errorf("%s pool VM size %q not found in region SKUs", role, cfg.vmSize)
	}

	var (
		maxPods     int32
		taints      []string
		mode        compute.PoolMode
		enableSwift bool
	)
	switch role {
	case compute.PoolRoleSystem:
		maxPods = systemMaxPods
		taints = []string{compute.TaintCriticalAddonsOnly}
		mode = compute.PoolModeRegional
		enableSwift = true
	case compute.PoolRoleWorker:
		maxPods = workerMaxPods
		mode = compute.PoolModePerZone
		enableSwift = true
	case compute.PoolRoleInfra:
		maxPods = infraMaxPods
		taints = []string{compute.TaintInfra}
		mode = compute.PoolModePerZone
	}

	spec := compute.NewVMSpecFromSKU(meta)
	// Only worker pools attach secondary NICs, sized to the SKU's NIC ceiling
	// (set by NewVMSpecFromSKU). System and infra pools carry none.
	if role != compute.PoolRoleWorker {
		spec.SecondaryNICs = 0
	}

	var placements []poolPlacement
	if mode == compute.PoolModeRegional {
		placements = []poolPlacement{{name: cfg.name, zones: cfg.zones}}
	} else {
		var err error
		placements, err = expandPoolPlacements(cfg.name, cfg.zones, cfg.poolCount)
		if err != nil {
			return nil, err
		}
	}

	pools := make([]compute.Pool, 0, len(placements))
	for _, placement := range placements {
		pools = append(pools, compute.Pool{
			Role:              role,
			Name:              placement.name,
			Spec:              spec,
			AvailabilityZones: placement.zones,
			MaxCount:          cfg.maxCount,
			MinCount:          cfg.minCount,
			OSDiskSizeGB:      cfg.osDiskSizeGB,
			MaxPods:           maxPods,
			Labels:            map[string]string{compute.RoleLabel: string(role)},
			Taints:            taints,
			EnableSwift:       enableSwift,
		})
	}
	return pools, nil
}

// expandPoolPlacements creates one pool per zone (named base+zone), one for
// each of the first poolCount zones. Names are truncated to 12 characters,
// matching the Bicep take(name, 12).
func expandPoolPlacements(base string, zones []string, poolCount int) ([]poolPlacement, error) {
	if poolCount > len(zones) {
		return nil, fmt.Errorf("pool count %d exceeds available zones %d", poolCount, len(zones))
	}
	placements := make([]poolPlacement, 0, poolCount)
	for i := range poolCount {
		placements = append(placements, poolPlacement{
			name:  truncate12(base + zones[i]),
			zones: []string{zones[i]},
		})
	}
	return placements, nil
}

// truncate12 mirrors Bicep's take(name, 12) used for agent pool names.
// i know this is bad but we had this in bicep and this is going away soon
// when the fleet controller takes over full pool lifecycle including naming
func truncate12(name string) string {
	if len(name) > 12 {
		return name[:12]
	}
	return name
}
