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
	"fmt"
	"github.com/go-logr/logr"
	"slices"

	"k8s.io/utils/ptr"

	"github.com/Azure/ARO-HCP/fleet/pkg/azure/skucache"
	"github.com/Azure/ARO-HCP/fleet/pkg/compute"
	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v9"
)

func completePoolConfigs(ctx context.Context, o *validatedOptions, skuCache *skucache.SKUCache, cluster *armcontainerservice.ManagedCluster) (system, user, infra poolConfig, err error) {
	var observed []*armcontainerservice.ManagedClusterAgentPoolProfile
	if cluster != nil {
		observed = cluster.Properties.AgentPoolProfiles
	}
	metadata, err := skuCache.SKUMetadataByVMSize(ctx, o.subscriptionID)
	if err != nil {
		return system, user, infra, utils.TrackError(fmt.Errorf("fetching SKU metadata: %w", err))
	}
	for _, block := range []struct {
		input  validatedPoolConfig
		output *poolConfig
	}{
		{o.system, &system}, {o.user, &user}, {o.infra, &infra},
	} {
		cfg := block.input
		if o.systemPoolOnly && cfg.class.Role != compute.PoolRoleSystem {
			continue
		}
		meta := metadata[cfg.vmSize]
		if meta == nil {
			return system, user, infra, utils.TrackError(fmt.Errorf("%s pool VM size %q not found in region SKUs", cfg.class.Role, cfg.vmSize))
		}
		zones, existing := resolvePoolZones(ctx, cfg, meta.Zones, observed)
		if !existing {
			if meta.LocationRestricted {
				return system, user, infra, utils.TrackError(fmt.Errorf("VM size %q is unavailable in this region", cfg.vmSize))
			}
			selectedZones := zones
			if cfg.mode == compute.PoolModePerZone {
				selectedZones = zones[:min(len(zones), cfg.poolCount)]
			}
			for _, zone := range selectedZones {
				if !slices.Contains(meta.Zones, zone) {
					return system, user, infra, utils.TrackError(fmt.Errorf("zone %q is unavailable for VM size %q", zone, cfg.vmSize))
				}
			}
		}
		*block.output = poolConfig{validatedPoolConfig: cfg, zones: zones}
	}
	return system, user, infra, nil
}

// resolvePoolZones prefers existing zones, then explicit zones, then SKU zones.
// The boolean reports whether matching pools already exist.
func resolvePoolZones(ctx context.Context, cfg validatedPoolConfig, defaultZones []string, existing []*armcontainerservice.ManagedClusterAgentPoolProfile) ([]string, bool) {
	var zones []string
	found := false
	for _, pool := range existing {
		if pool == nil {
			continue
		}
		if cfg.class.Role == compute.PoolRoleSystem {
			if ptr.Deref(pool.Mode, "") != armcontainerservice.AgentPoolModeSystem {
				continue
			}
		} else if ptr.Deref(pool.NodeLabels[compute.RoleLabel], "") != string(cfg.class.Role) {
			continue
		}
		found = true
		for _, zone := range pool.AvailabilityZones {
			if zone != nil && !slices.Contains(zones, *zone) {
				zones = append(zones, *zone)
			}
		}
	}
	if !found {
		if len(cfg.explicitZones) > 0 {
			return slices.Clone(cfg.explicitZones), false
		}
		return slices.Clone(defaultZones), false
	}
	if cfg.mode == compute.PoolModePerZone {
		slices.Sort(zones)
	}
	if len(cfg.explicitZones) > 0 {
		configured := slices.Clone(cfg.explicitZones)
		preserved := slices.Clone(zones)
		slices.Sort(configured)
		slices.Sort(preserved)
		if !slices.Equal(configured, preserved) {
			if logger, err := logr.FromContext(ctx); err == nil {
				logger.Info("WARNING: ignoring configured zones in favor of existing pool zones", "role", cfg.class.Role, "configuredZones", cfg.explicitZones, "existingZones", zones)
			}
		}
	}
	return zones, true
}
