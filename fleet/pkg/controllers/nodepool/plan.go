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

package nodepool

import (
	"maps"
	"slices"
	"sort"

	"github.com/Azure/ARO-HCP/fleet/pkg/compute"
)

// findNextAction determines the single next operation to advance current state
// toward desired state. It computes per-family vCPU headroom as the budget
// (quota limit minus live usage) minus the committed-but-not-running ceiling of
// current pools. Same-family transitions converge iteratively: grow (step 4)
// runs before shrink (step 5), and every reduction preserves the accepted
// per-role transition floor.
//
// Returns nil when converged or blocked. Returns waitAction when any pool is in progress.
func findNextAction(desired []compute.Pool, current []PoolState, availableVCPUs map[compute.VMFamily]int64, capacityFloor compute.CapacityByRole, networkConfig compute.NetworkConfig) Action {
	if len(desired) == 0 && len(current) > 0 {
		return nil
	}

	if blocker := firstInProgressPool(current); blocker != nil {
		return newWaitAction(blocker.Name, blocker.Spec.Size, blocker.ZoneString(), requeueAfterDrainStep)
	}

	desiredByName := make(map[string]compute.Pool, len(desired))
	for _, pool := range desired {
		desiredByName[pool.Name] = pool
	}

	currentByName := make(map[string]PoolState, len(current))
	for _, pool := range current {
		currentByName[pool.Name] = pool
	}

	headroom := computeFamilyHeadroom(availableVCPUs, current)

	// 1. Reconcile desired pools stuck in Failed state.
	if action, ok := findReconcileAction(desired, currentByName); ok {
		return action
	}

	// 2. Correct desired pools that are over-spec'd or need unfreezing.
	if action, ok := findCorrectDesiredAction(desired, currentByName, headroom, current, capacityFloor); ok {
		return action
	}

	// 3. Reconcile mutable config drift (labels/taints) on matched pools.
	//    Not headroom-gated: labels/taints do not change vCPU or NIC ceiling.
	if action, ok := findDriftAction(desired, currentByName); ok {
		return action
	}

	// 4. Grow desired pools when budget headroom allows.
	if action, ok := findGrowAction(desired, currentByName, headroom, networkConfig); ok {
		return action
	}

	// 5. Shrink undesired pools (identity-based, not headroom-gated).
	//    Each reduction must preserve the per-role transition floor.
	if action, ok := findShrinkAction(current, desiredByName, capacityFloor); ok {
		return action
	}

	return nil
}

func findReconcileAction(desired []compute.Pool, currentByName map[string]PoolState) (Action, bool) {
	for _, pool := range desired {
		cur, exists := currentByName[pool.Name]
		if exists && cur.ProvisioningState == provisioningStateFailed {
			return newReconcileAction(pool.Name, pool.Spec.Size, pool.ZoneString(), cur.ETag), true
		}
	}
	return nil, false
}

// findCorrectDesiredAction handles desired pools that exist but are
// misconfigured: frozen pools that need unfreezing, pools with maxCount above
// target, or pools whose count exceeds desired max and need draining.
func findCorrectDesiredAction(desired []compute.Pool, currentByName map[string]PoolState, headroom map[compute.VMFamily]int64, current []PoolState, capacityFloor compute.CapacityByRole) (Action, bool) {
	for _, pool := range desired {
		cur, exists := currentByName[pool.Name]
		if !exists {
			continue
		}

		if !cur.AutoScalingEnabled {
			if cur.Count <= pool.MaxCount {
				ceilingIncrease := int64(pool.MaxCount-cur.Count) * pool.Spec.VCPUs
				if ceilingIncrease <= headroom[pool.Spec.Family] {
					return newUnfreezeAction(pool.Name, pool.Spec.Size, pool.ZoneString(), cur.ETag, min(cur.MinCount, pool.MaxCount), pool.MaxCount), true
				}
				continue
			}
			if cur.Count > 0 {
				// Corrections can dip below the new desired total during a
				// rebalance, but must preserve the accepted transition floor.
				if !allowsCapacityReduction(current, cur, int64(cur.Count-1), capacityFloor) {
					continue
				}
				return newReduceAction(pool.Name, pool.Spec.Size, pool.ZoneString(), cur.ETag, cur.Count-1), true
			}
			continue
		}

		if cur.MaxCount > pool.MaxCount {
			if pool.MaxCount >= cur.Count {
				if !allowsCapacityReduction(current, cur, int64(pool.MaxCount), capacityFloor) {
					continue
				}
				return newSetScalingBoundsAction(pool.Name, pool.Spec.Size, pool.ZoneString(), cur.ETag, min(cur.MinCount, pool.MaxCount), pool.MaxCount), true
			}
			if !allowsCapacityReduction(current, cur, int64(cur.Count), capacityFloor) {
				continue
			}
			return newFreezeAction(pool.Name, pool.Spec.Size, pool.ZoneString(), cur.ETag, cur.Count), true
		}
	}
	return nil, false
}

// findDriftAction returns an action to reconcile node labels or taints on a
// desired pool whose live values have drifted from the desired spec. Labels are
// compared for exact equality; taints are compared as a set (order-insensitive)
// so AKS reordering does not trigger a spurious update loop.
func findDriftAction(desired []compute.Pool, currentByName map[string]PoolState) (Action, bool) {
	for _, pool := range desired {
		cur, exists := currentByName[pool.Name]
		if !exists {
			continue
		}
		if maps.Equal(pool.Labels, cur.Labels) && taintsEqual(pool.Taints, cur.Taints) {
			continue
		}
		return newUpdateConfigAction(pool, cur.ETag), true
	}
	return nil, false
}

// taintsEqual reports whether two taint lists contain the same elements,
// ignoring order.
func taintsEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	sortedA := slices.Clone(a)
	sortedB := slices.Clone(b)
	slices.Sort(sortedA)
	slices.Sort(sortedB)
	return slices.Equal(sortedA, sortedB)
}

// findGrowAction creates missing pools or increases maxCount on desired pools,
// limited by per-family headroom. When headroom is large (no competing pools),
// growth reaches the full target in one action. When headroom is small (freed
// one slot at a time by shrinking), growth is naturally throttled.
func findGrowAction(desired []compute.Pool, currentByName map[string]PoolState, headroom map[compute.VMFamily]int64, networkConfig compute.NetworkConfig) (Action, bool) {
	for _, pool := range desired {
		if _, exists := currentByName[pool.Name]; !exists {
			canGrow := headroom[pool.Spec.Family] / pool.Spec.VCPUs
			if canGrow <= 0 {
				continue
			}
			createPool := pool
			if int64(createPool.MaxCount) > canGrow {
				createPool.MaxCount = int32(canGrow)
				// Lowering the ceiling to fit headroom can drop it below the
				// seeded floor; keep min <= max so the create PUT is valid.
				createPool.InitialMinCount = min(createPool.InitialMinCount, createPool.MaxCount)
			}
			return newCreateAction(createPool, networkConfig), true
		}
	}

	for _, pool := range desired {
		cur, exists := currentByName[pool.Name]
		if !exists || !cur.AutoScalingEnabled || cur.MaxCount >= pool.MaxCount {
			continue
		}
		canGrow := headroom[pool.Spec.Family] / pool.Spec.VCPUs
		if canGrow <= 0 {
			continue
		}
		gap := int64(pool.MaxCount - cur.MaxCount)
		if gap > canGrow {
			gap = canGrow
		}
		targetMax := cur.MaxCount + int32(gap)
		return newSetScalingBoundsAction(pool.Name, pool.Spec.Size, pool.ZoneString(), cur.ETag, min(cur.MinCount, targetMax), targetMax), true
	}

	return nil, false
}

func findShrinkAction(current []PoolState, desiredByName map[string]compute.Pool, capacityFloor compute.CapacityByRole) (Action, bool) {
	undesired := undesiredPools(current, desiredByName)
	if len(undesired) == 0 {
		return nil, false
	}

	// Deterministic order for the direct-delete, squeeze, and freeze scans below;
	// the drain step re-sorts by count for its own lowest-first ordering.
	sort.Slice(undesired, func(i, j int) bool {
		return undesired[i].Name < undesired[j].Name
	})

	// System pools first: AKS enforces minCount 1 on Mode=System pools, so the
	// reduce-to-zero drain path below deadlocks on them (a PUT with count 0 is
	// rejected, and the failing pool re-sorts to the head of the drain order,
	// starving every other undesired pool). Delete them directly — AKS cordons
	// and drains the node on delete — but only while another system pool we
	// intend to keep remains, since AKS forbids deleting the last system pool.
	for _, cur := range undesired {
		if cur.Role != compute.PoolRoleSystem {
			continue
		}
		// Only delete when another system pool remains in the desired set; AKS
		// forbids removing the last system pool. cur is undesired, so it can
		// never be the survivor matched here.
		survivorRemains := false
		for _, other := range current {
			if other.Role != compute.PoolRoleSystem {
				continue
			}
			if _, desired := desiredByName[other.Name]; desired {
				survivorRemains = true
				break
			}
		}
		if !survivorRemains || !allowsCapacityReduction(current, cur, 0, capacityFloor) {
			continue
		}
		return newDeleteAction(cur.Name, cur.Spec.Size, cur.ZoneString(), cur.ETag), true
	}

	// Squeeze unused ceiling without evicting nodes. This may dip below the
	// new desired total but must preserve the accepted transition floor.
	for _, cur := range undesired {
		if cur.AutoScalingEnabled && cur.MaxCount > cur.Count {
			if !allowsCapacityReduction(current, cur, int64(cur.Count), capacityFloor) {
				continue
			}
			return newSetScalingBoundsAction(cur.Name, cur.Spec.Size, cur.ZoneString(), cur.ETag, min(cur.MinCount, cur.Count), cur.Count), true
		}
	}

	// Freeze: disable autoscaling when max <= count. The autoscaler can
	// no longer fight back, and we can start draining.
	for _, cur := range undesired {
		if cur.AutoScalingEnabled && cur.MaxCount <= cur.Count {
			return newFreezeAction(cur.Name, cur.Spec.Size, cur.ZoneString(), cur.ETag, cur.Count), true
		}
	}

	// Drain: reduce count by 1 on frozen pools (cordon + drain).
	// Process lowest count first to reach deletion sooner.
	sort.Slice(undesired, func(i, j int) bool {
		if undesired[i].Count != undesired[j].Count {
			return undesired[i].Count < undesired[j].Count
		}
		return undesired[i].Name < undesired[j].Name
	})
	for _, cur := range undesired {
		// System pools cannot be drained below minCount 1 (AKS rejects count 0);
		// they are removed by the direct-delete branch above, never here.
		if cur.Role == compute.PoolRoleSystem {
			continue
		}
		if !cur.AutoScalingEnabled && cur.Count > 0 {
			if !allowsCapacityReduction(current, cur, int64(cur.Count-1), capacityFloor) {
				continue
			}
			return newReduceAction(cur.Name, cur.Spec.Size, cur.ZoneString(), cur.ETag, cur.Count-1), true
		}
	}

	// Delete: remove frozen pools with zero nodes.
	for _, cur := range undesired {
		if !cur.AutoScalingEnabled && cur.Count == 0 {
			return newDeleteAction(cur.Name, cur.Spec.Size, cur.ZoneString(), cur.ETag), true
		}
	}

	return nil, false
}

func poolCeiling(pool PoolState) int64 {
	if pool.AutoScalingEnabled {
		return int64(pool.MaxCount)
	}
	return int64(pool.Count)
}

func computeFamilyHeadroom(availableVCPUs map[compute.VMFamily]int64, current []PoolState) map[compute.VMFamily]int64 {
	used := make(map[compute.VMFamily]int64)
	for _, pool := range current {
		// Pools whose SKU metadata did not resolve carry VCPUs==0 and an empty
		// family; skip them so their quota is not misattributed to family "".
		if pool.Spec.VCPUs == 0 {
			continue
		}
		// Subtract only the committed-but-not-yet-running ceiling. The budget is
		// derived from live quota usage (limit - currentValue), which already
		// counts running nodes; subtracting the full ceiling would double-count
		// them. poolCeiling-Count is the ceiling a pool has reserved beyond what
		// it already consumes. Unresolved-SKU pools (skipped above) stay fully
		// accounted for via currentValue in the budget.
		used[pool.Spec.Family] += (poolCeiling(pool) - int64(pool.Count)) * pool.Spec.VCPUs
	}

	headroom := make(map[compute.VMFamily]int64, len(availableVCPUs)+len(used))
	for family, budget := range availableVCPUs {
		headroom[family] = budget - used[family]
	}
	for family, u := range used {
		if _, exists := headroom[family]; !exists {
			headroom[family] = -u
		}
	}

	return headroom
}

func firstInProgressPool(current []PoolState) *PoolState {
	for i := range current {
		if isTransitionalState(current[i].ProvisioningState) {
			return &current[i]
		}
	}
	return nil
}

func isTransitionalState(state string) bool {
	switch state {
	case provisioningStateCreating, provisioningStateUpdating, provisioningStateScaling, provisioningStateDeleting, provisioningStateUpgrading, provisioningStateMigrating:
		return true
	default:
		return false
	}
}

func undesiredPools(current []PoolState, desiredByName map[string]compute.Pool) []PoolState {
	var result []PoolState
	for _, pool := range current {
		if _, exists := desiredByName[pool.Name]; !exists {
			result = append(result, pool)
		}
	}
	return result
}
