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

// delete-extra-infra-nodepools: removes AKS agent pools that exist in live
// state but are not part of the current rendered configuration.
//
// Background
// ----------
// When the rendered config changes (e.g. a pool rename, pool-count reduction,
// or zone-redundancy mode change), the ARM bicep deployment converges desired
// state on the new pool names. The old pool names are not declared in the new
// deployment and AKS does not delete them automatically — they become orphaned
// infra pools that consume capacity and add noise to cluster-autoscaler logs.
//
// This binary computes the expected pool name set from the same naming
// algorithm used in pool.bicep (zonal: {baseName}{zone}, non-zonal:
// {baseName}nz{N}), lists live pools with the configured role tag, and
// deletes any pool not in the expected set.
//
// Safety invariant (hard gate)
// ----------------------------
// Deletion only proceeds when EVERY expected pool:
//   (a) exists in ARM, and
//   (b) has provisioningState == "Succeeded", and
//   (c) has at least POOL_MIN_COUNT schedulable-ready Kubernetes nodes.
//
// If any expected pool fails this check the binary exits non-zero and no
// pool is touched. This ensures we never remove infra capacity when the
// desired state is not yet healthy.
//
// Additionally, as an independent production-safety invariant, the binary
// refuses to delete every live pool for the role: if the computed extras
// would cover all live pools (leaving zero pools, and therefore zero nodes),
// it aborts and touches nothing — regardless of what the expected-set
// computation produced.
//
// Inputs (env vars, set by pipeline step)
// -----------------------------------------
//   CLUSTER_NAME        AKS cluster name (e.g. stg-uksouth-mgmt-1)
//   RESOURCE_GROUP      Resource group containing the cluster
//   SUBSCRIPTION_ID     Azure subscription ID
//   POOL_TAG            aro-hcp.azure.com/role label value to target (e.g. infra)
//   POOL_BASE_NAME      Expected pool base name from rendered config (e.g. infrasd4ds5)
//   POOL_COUNT          Expected number of pools from rendered config (integer)
//   ZONE_REDUNDANT_MODE Enabled | Auto | Disabled
//   POOL_ZONES          CSV of AZ numbers (e.g. "1,2") or empty
//   POOL_MIN_COUNT      Min schedulable-ready nodes required per expected pool
//                       before deletion is allowed (default 1)
//   DRY_RUN             "true" / "1" / "yes" — print intent, make no writes
//                       (default: "true")
//   DRAIN_TIMEOUT_MIN   Drain timeout per node in minutes (default 10)
//   READY_TIMEOUT_MIN   Timeout to wait for expected-pool readiness (default 5)
//   LOG_VERBOSITY       logr verbosity level (default 0)

package main

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Azure/ARO-HCP/dev-infrastructure/scripts/internal/akslog"
	"github.com/Azure/ARO-HCP/dev-infrastructure/scripts/internal/azregions"
	"github.com/Azure/ARO-HCP/dev-infrastructure/scripts/internal/poolnaming"
)

const overallTimeoutMin = 60

func run() error {
	ctx, cancel := context.WithTimeout(context.Background(), overallTimeoutMin*time.Minute)
	defer cancel()

	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}

	akslog.Banner("CONFIG")
	cfg.logEnv()

	az, err := newClients(cfg)
	if err != nil {
		return fmt.Errorf("azure clients: %w", err)
	}

	akslog.Banner("STEP 1 — cluster state")
	mc, err := az.getCluster(ctx)
	if err != nil {
		return fmt.Errorf("get cluster: %w", err)
	}
	if mc.Properties == nil {
		return fmt.Errorf("cluster %s has nil Properties — aborting to avoid unsafe pool deletion", cfg.clusterName)
	}
	clusterState := akslog.Deref(mc.Properties.ProvisioningState)
	akslog.Logf("cluster %s: provisioningState=%s", cfg.clusterName, clusterState)
	if clusterState != "Succeeded" {
		if cfg.dryRun {
			akslog.Logf("DRY_RUN: cluster %s is not in Succeeded state (got %q) — skipping orphan detection (no pools touched)", cfg.clusterName, clusterState)
			return nil
		}
		return fmt.Errorf("cluster %s is not in Succeeded state (got %q) — aborting to avoid unsafe pool deletion during an active LRO", cfg.clusterName, clusterState)
	}

	akslog.Banner("STEP 2 — compute expected vs live pools")
	// Mirror pool.bicep's zone resolution: when a pool's configured zones are
	// empty, svc-/mgmt-cluster.bicep fall back to the region's availability
	// zones (locationAvailabilityZoneList) before pool.bicep computes names.
	// Reproduce that fallback here so expected names match what ARM deployed.
	effectiveZones := cfg.poolZones
	if len(effectiveZones) == 0 && cfg.zoneRedundantMode != "Disabled" {
		regionZones, ok := azregions.AvailabilityZones(akslog.Deref(mc.Location))
		if !ok {
			region := akslog.Deref(mc.Location)
			if cfg.dryRun {
				akslog.Logf("DRY_RUN: region %q is not in the availability-zone table — cannot reproduce bicep's effective pool zones; skipping orphan detection (no pools touched)", region)
				return nil
			}
			return fmt.Errorf("region %q is not in the availability-zone table — aborting to avoid unsafe pool deletion (cannot reproduce bicep's effective pool zones)", region)
		}
		effectiveZones = regionZones
		akslog.Logf("POOL_ZONES empty; resolved effective zones for region %q from the availability-zone table: %v", akslog.Deref(mc.Location), effectiveZones)
	}
	expectedNames := poolnaming.Expected(cfg.poolBaseName, cfg.poolCount, cfg.zoneRedundantMode, effectiveZones)
	expectedSet := make(map[string]struct{}, len(expectedNames))
	for _, n := range expectedNames {
		expectedSet[n] = struct{}{}
	}
	akslog.Logf("expected pools (%d): %v", len(expectedNames), expectedNames)

	livePools, err := az.listPoolsByTag(ctx)
	if err != nil {
		return fmt.Errorf("list pools: %w", err)
	}
	liveNames := make([]string, 0, len(livePools))
	for n := range livePools {
		liveNames = append(liveNames, n)
	}
	sort.Strings(liveNames)
	akslog.Logf("live pools with role=%q (%d): %v", cfg.poolTag, len(liveNames), liveNames)

	var extras []string
	for _, n := range liveNames {
		if _, ok := expectedSet[n]; !ok {
			extras = append(extras, n)
		}
	}
	sort.Strings(extras)

	if len(extras) == 0 {
		akslog.Logf("no extra pools found — nothing to do")
		return nil
	}
	akslog.Logf("extra pools to delete (%d): %v", len(extras), extras)

	// Production-safety invariant (independent of the expected-set computation
	// and the readiness gate below): never delete every live pool for this
	// role. If the computed extras cover all live pools, deletion would leave
	// the role with zero pools — and therefore zero nodes — so refuse and touch
	// nothing. Retained pools are additionally verified to have at least
	// POOL_MIN_COUNT schedulable-ready nodes by the safety gate (STEP 4), so a
	// non-empty retained set guarantees surviving nodes.
	retained := retainedPools(liveNames, extras)
	if len(retained) == 0 {
		msg := fmt.Sprintf("refusing to delete all %d live %q pool(s) %v: cleanup would leave zero nodes for role %q", len(liveNames), cfg.poolTag, extras, cfg.poolTag)
		if cfg.dryRun {
			akslog.Logf("DRY_RUN: %s (no pools touched)", msg)
			return nil
		}
		return fmt.Errorf("%s — aborting (no pools touched)", msg)
	}
	akslog.Logf("cleanup will retain %d %q pool(s): %v", len(retained), cfg.poolTag, retained)

	akslog.Banner("STEP 3 — bootstrap kube client")
	if err := az.bootstrapKube(ctx, akslog.Deref(mc.ID)); err != nil {
		if cfg.dryRun {
			akslog.Logf("DRY_RUN: could not bootstrap kube client (%v) — skipping readiness safety check; would delete %d extra pool(s): %v (no pools touched)", err, len(extras), extras)
			return nil
		}
		return fmt.Errorf("bootstrap kube: %w", err)
	}

	akslog.Banner("STEP 4 — safety gate: verify expected pools are healthy")
	if err := az.allExpectedPoolsReady(ctx, expectedNames, livePools); err != nil {
		if cfg.dryRun {
			akslog.Logf("DRY_RUN: safety gate would BLOCK deletion: %v — would delete %d extra pool(s): %v (no pools touched)", err, len(extras), extras)
			return nil
		}
		return err
	}

	if cfg.dryRun {
		akslog.Logf("DRY_RUN=true — safety gate passed; would delete %d extra pool(s): %v — set DRY_RUN=false to proceed", len(extras), extras)
		return nil
	}

	akslog.Banner("STEP 5 — drain and delete extra pools")
	var errs []string
	for _, poolName := range extras {
		akslog.Logf("=== processing extra pool %s ===", poolName)
		if err := az.drainPool(ctx, poolName); err != nil {
			akslog.Logf("ERROR: drain pool %s: %v — skipping deletion", poolName, err)
			errs = append(errs, fmt.Sprintf("drain %s: %v", poolName, err))
			continue
		}
		if err := az.deletePool(ctx, poolName); err != nil {
			akslog.Logf("ERROR: delete pool %s: %v", poolName, err)
			errs = append(errs, fmt.Sprintf("delete %s: %v", poolName, err))
		}
	}

	akslog.Banner("DONE")
	if len(errs) > 0 {
		return fmt.Errorf("%d pool(s) failed:\n  %s", len(errs), strings.Join(errs, "\n  "))
	}
	akslog.Logf("all %d extra pool(s) deleted successfully", len(extras))
	return nil
}

// retainedPools returns the live pools that would remain after deleting the
// given extras. It is used by the production-safety invariant in run() to
// refuse a cleanup that would delete every live pool for the role.
func retainedPools(live, extras []string) []string {
	del := make(map[string]struct{}, len(extras))
	for _, e := range extras {
		del[e] = struct{}{}
	}
	var keep []string
	for _, p := range live {
		if _, ok := del[p]; !ok {
			keep = append(keep, p)
		}
	}
	return keep
}
