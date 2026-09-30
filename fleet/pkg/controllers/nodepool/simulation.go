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
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/Azure/ARO-HCP/fleet/pkg/compute"
)

type traceStep struct {
	Action         Action
	State          []PoolState
	HeadroomBefore map[compute.VMFamily]int64
	HeadroomAfter  map[compute.VMFamily]int64
	Capacity       compute.CapacityByRole
	CapacityFloor  compute.CapacityByRole
}

type trace struct {
	Desired         []compute.Pool
	Initial         []PoolState
	FamilyBudgets   map[compute.VMFamily]int64
	Steps           []traceStep
	FullyAllocated  bool
	InitialCapacity compute.CapacityByRole
	RejectedPlan    error
	Outcome         string
	Reason          string
}

// runningVCPU sums running-node vCPUs per family (count, not ceiling).
func runningVCPU(pools []PoolState) map[compute.VMFamily]int64 {
	m := make(map[compute.VMFamily]int64)
	for _, p := range pools {
		m[p.Spec.Family] += int64(p.Count) * p.Spec.VCPUs
	}
	return m
}

// liveBudget mirrors the controller's per-reconcile quota refetch. The passed
// budget is anchored to the initial usage snapshot (limit - currentValue at
// t0), so as the simulation starts and stops nodes we move the running-vCPU
// delta since t0 into the budget. Without this the budget would be frozen at
// t0 and headroom would ignore nodes the simulation itself created or deleted.
// Only tracked families (those in base) are adjusted; others (e.g. an
// undesired legacy family) are irrelevant to planning.
func liveBudget(base, runInitial, runNow map[compute.VMFamily]int64) map[compute.VMFamily]int64 {
	out := make(map[compute.VMFamily]int64, len(base))
	for family, b := range base {
		out[family] = b + runInitial[family] - runNow[family]
	}
	return out
}

// simulateAndTrace projects the existing planner, never executing its actions.
// Only ordinary successful changes are projected. Waits and failed-pool
// reconciliation end the projection without inventing an ARM outcome.
func simulateAndTrace(desired []compute.Pool, initial []PoolState, familyBudgets map[compute.VMFamily]int64, fullyAllocated bool, maxCycles int) (trace, error) {
	tr := trace{Desired: clonePools(desired), Initial: clonePoolStates(initial), FamilyBudgets: maps.Clone(familyBudgets), FullyAllocated: fullyAllocated}
	if maxCycles < 0 {
		return tr, fmt.Errorf("simulation step limit must be nonnegative")
	}
	for _, pool := range desired {
		if pool.MaxCount <= 0 || pool.MinCount < 0 || pool.MinCount > pool.MaxCount {
			return tr, fmt.Errorf("invalid desired bounds for pool %s", pool.Name)
		}
	}
	for _, pool := range initial {
		if pool.Count < 0 || pool.MinCount < 0 || pool.MaxCount < 0 {
			return tr, fmt.Errorf("negative observed count or bounds for pool %s", pool.Name)
		}
	}
	desiredCapacity, err := compute.PoolCapacities(desired)
	if err != nil {
		return tr, fmt.Errorf("desired capacity: %w", err)
	}
	capacity, err := stateCapacity(initial)
	if err != nil {
		return tr, fmt.Errorf("initial capacity: %w", err)
	}
	tr.InitialCapacity = capacity
	state := clonePoolStates(initial)
	runInitial := runningVCPU(initial)
	for {
		// A partial allocation protects the live ceiling at every step, not t0.
		capacityFloor, floorErr := desiredCapacity.ResolveEffectiveFloor(capacity, fullyAllocated)
		if floorErr != nil {
			// The planner's observed-operation wait takes precedence. No
			// capacity-changing action can use this unaccepted fallback floor.
			capacityFloor = maps.Clone(capacity)
		}
		live := liveBudget(familyBudgets, runInitial, runningVCPU(state))
		action := findNextAction(desired, state, live, capacityFloor, compute.NetworkConfig{})
		if _, waiting := action.(waitAction); floorErr != nil && !waiting {
			tr.RejectedPlan = floorErr
			tr.Outcome, tr.Reason = "rejected", floorErr.Error()
			return tr, nil
		}
		if action == nil {
			if configurationConverged(desired, state) {
				tr.Outcome, tr.Reason = "converged", "pool configuration matches desired"
			} else {
				tr.Outcome, tr.Reason = "blocked", "no safe planner action is available for the remaining configuration difference (quota, capacity floor, or pool safety constraints)"
			}
			return tr, nil
		}
		if len(tr.Steps) >= maxCycles {
			tr.Outcome, tr.Reason = "step-limit", fmt.Sprintf("projection stopped after %d actions; next action is %s on %s", maxCycles, action.kind(), action.poolName())
			return tr, nil
		}
		before := computeFamilyHeadroom(live, state)
		switch action.(type) {
		case waitAction, reconcileAction:
			tr.Steps = append(tr.Steps, traceStep{Action: action, State: clonePoolStates(state), HeadroomBefore: before, HeadroomAfter: maps.Clone(before), Capacity: maps.Clone(capacity), CapacityFloor: capacityFloor})
			tr.Outcome = "waiting"
			if action.kind() == actionWait {
				tr.Reason = fmt.Sprintf("pool %s has an observed in-progress operation; a new observation is required", action.poolName())
			} else {
				tr.Reason = fmt.Sprintf("pool %s requires reconciliation after failure; its result cannot be projected", action.poolName())
			}
			return tr, nil
		}
		state, err = applyAction(state, action)
		if err != nil {
			return tr, err
		}
		capacity, err = stateCapacity(state)
		if err != nil {
			return tr, fmt.Errorf("projected capacity: %w", err)
		}
		after := computeFamilyHeadroom(liveBudget(familyBudgets, runInitial, runningVCPU(state)), state)
		tr.Steps = append(tr.Steps, traceStep{Action: cloneAction(action), State: clonePoolStates(state), HeadroomBefore: before, HeadroomAfter: after, Capacity: capacity, CapacityFloor: capacityFloor})
		if err := capacity.EnsureMeetsBaseline(capacityFloor); err != nil {
			return tr, fmt.Errorf("projected %s on %s violates capacity floor: %w", action.kind(), action.poolName(), err)
		}
	}
}

func stateCapacity(state []PoolState) (compute.CapacityByRole, error) {
	pools := make([]compute.Pool, len(state))
	for i, cur := range state {
		pools[i] = cur.Pool
		pools[i].MaxCount = int32(poolCeiling(cur))
	}
	return compute.PoolCapacities(pools)
}

// Pool metadata is copied only at ownership boundaries: inputs, action payloads,
// and retained snapshots. Capacity and quota helpers only read their arguments.
func clonePool(pool compute.Pool) compute.Pool {
	pool.AvailabilityZones = slices.Clone(pool.AvailabilityZones)
	pool.Labels = maps.Clone(pool.Labels)
	pool.Taints = slices.Clone(pool.Taints)
	return pool
}

func clonePools(pools []compute.Pool) []compute.Pool {
	result := slices.Clone(pools)
	for i := range result {
		result[i] = clonePool(result[i])
	}
	return result
}

func clonePoolStates(pools []PoolState) []PoolState {
	result := slices.Clone(pools)
	for i := range result {
		result[i].Pool = clonePool(result[i].Pool)
	}
	return result
}

func cloneAction(action Action) Action {
	switch a := action.(type) {
	case createAction:
		a.Pool = clonePool(a.Pool)
		return a
	case updateConfigAction:
		a.Labels = maps.Clone(a.Labels)
		a.Taints = slices.Clone(a.Taints)
		return a
	default:
		return action
	}
}

// applyAction mutates exclusively simulator-owned working state. It models a
// successful ordinary operation, not ARM timing, retries, or recovery of failures.
func applyAction(current []PoolState, action Action) ([]PoolState, error) {
	if a, ok := action.(createAction); ok {
		return append(current, PoolState{
			Pool:               clonePool(a.Pool),
			MinCount:           a.Pool.MinCount,
			AutoScalingEnabled: true,
			Count:              a.Pool.MinCount,
			ProvisioningState:  "Succeeded",
			ETag:               fmt.Sprintf("etag-%s-new", a.poolName()),
		}), nil
	}
	for i := range current {
		if current[i].Name != action.poolName() {
			continue
		}
		pool := &current[i]
		switch a := action.(type) {
		case setScalingBoundsAction:
			pool.MaxCount, pool.MinCount = a.MaxCount, a.MinCount
			pool.ETag = fmt.Sprintf("etag-%s-%d", a.poolName(), a.MaxCount)
		case unfreezeAction:
			pool.AutoScalingEnabled = true
			pool.MaxCount, pool.MinCount = a.MaxCount, a.MinCount
			pool.ETag = fmt.Sprintf("etag-%s-unfrozen", a.poolName())
		case freezeAction:
			pool.AutoScalingEnabled = false
			pool.Count, pool.MaxCount = a.Count, a.Count
			pool.ETag = fmt.Sprintf("etag-%s-frozen", a.poolName())
		case reduceAction:
			pool.Count, pool.MaxCount = a.Count, a.Count
			pool.ETag = fmt.Sprintf("etag-%s-cnt%d", a.poolName(), a.Count)
		case updateConfigAction:
			pool.Labels, pool.Taints = maps.Clone(a.Labels), slices.Clone(a.Taints)
			pool.ETag = fmt.Sprintf("etag-%s-config", a.poolName())
		case deleteAction:
			return slices.Delete(current, i, i+1), nil
		default:
			return current, fmt.Errorf("cannot project action %T", action)
		}
		return current, nil
	}
	return current, fmt.Errorf("projected action targets missing pool %s", action.poolName())
}

func (tr trace) finalState() []PoolState {
	if len(tr.Steps) == 0 {
		return tr.Initial
	}
	return tr.Steps[len(tr.Steps)-1].State
}

func formatTrace(tr trace) string {
	var buf strings.Builder
	w := tabwriter.NewWriter(&buf, 0, 8, 2, ' ', 0)

	fmt.Fprintln(w, "conditional projection: proposed changes are assumed to succeed; no ARM writes are performed.")
	fmt.Fprintln(w, "Observed transitions and failed-pool reconciliation require a new observation; workload/autoscaler activity and external quota changes are not predicted.")
	fmt.Fprintln(w, "\ninitial:")
	if len(tr.Initial) == 0 {
		fmt.Fprintln(w, "  (none)")
	}
	for _, p := range tr.Initial {
		fmt.Fprintf(w, "  %s\t%s\t%s\tzone=%s\tmax=%d\tcount=%d\t%s\n",
			p.Name, p.Role, p.Spec.Size, p.ZoneString(), p.MaxCount, p.Count, autoScaleTag(p))
	}

	fmt.Fprintln(w, "\ndesired:")
	for _, p := range tr.Desired {
		fmt.Fprintf(w, "  %s\t%s\t%s\tzone=%s\tmax=%d\n", p.Name, p.Role, p.Spec.Size, p.ZoneString(), p.MaxCount)
	}

	families := sortedFamilies(tr.FamilyBudgets)
	initialHeadroom := computeFamilyHeadroom(tr.FamilyBudgets, tr.Initial)
	if len(families) > 0 {
		fmt.Fprintln(w, "\nbudget (vCPUs):")
		for _, family := range families {
			fmt.Fprintf(w, "  %s:\t%d\n", family, tr.FamilyBudgets[family])
		}
		fmt.Fprintln(w, "\ninitial headroom (vCPUs):")
		for _, family := range families {
			fmt.Fprintf(w, "  %s:\t%d\n", family, initialHeadroom[family])
		}
	}
	fmt.Fprintf(w, "\nfully allocated: %t\n", tr.FullyAllocated)
	fmt.Fprintln(w, "\ninitial capacity:")
	for _, role := range compute.CapacityRoles {
		fmt.Fprintf(w, "  %s:\t%s\n", role, formatCapacity(tr.InitialCapacity[role]))
	}
	if tr.RejectedPlan != nil {
		fmt.Fprintf(w, "\nrejected desired plan: %v\n", tr.RejectedPlan)
	}

	fmt.Fprintln(w, "\nactions:")
	prevState := tr.Initial
	for i, step := range tr.Steps {
		// Role isn't carried on the Action; look up the acted-on pool in the
		// post-action state (create), falling back to the pre-action state
		// (delete removes it from the post state).
		role := roleOf(step.Action.poolName(), step.State, prevState)
		action := fmt.Sprintf("  %3d.\t%s\t%s\t%s\t%s\t%s\t",
			i+1, step.Action.kind(), role, step.Action.vmSize(), step.Action.poolName(), actionDetail(step.Action))
		capacity := "floor: " + formatCapacity(step.CapacityFloor[role]) + "\tcapacity: " + formatCapacityAgainstFloor(step.Capacity[role], step.CapacityFloor[role])
		delta := formatHeadroomDelta(step.HeadroomBefore, step.HeadroomAfter, families)
		fmt.Fprintln(w, strings.TrimRight(action+capacity+"\t"+delta, "\t"))
		prevState = step.State
	}
	if len(tr.Steps) == 0 {
		fmt.Fprintln(w, "  (none)")
	}

	fmt.Fprintf(w, "\nsteps:\t%d\nconverged:\t%t\noutcome:\t%s\nreason:\t%s\n", len(tr.Steps), tr.Outcome == "converged", tr.Outcome, tr.Reason)
	_ = w.Flush() // strings.Builder writes cannot fail.
	lines := strings.Split(buf.String(), "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " ")
	}
	return strings.Join(lines, "\n")
}

func formatCapacity(capacity compute.RoleCapacity) string {
	return fmt.Sprintf("cpu=%d\tmemory=%dGiB\tswiftNICs=%d", capacity.VCPUs, capacity.MemoryBytes>>30, capacity.SwiftNICs)
}

func formatCapacityAgainstFloor(capacity, floor compute.RoleCapacity) string {
	return fmt.Sprintf("cpu=%d\t(%+d)\tmemory=%dGiB\t(%+dGiB)\tswiftNICs=%d\t(%+d)",
		capacity.VCPUs, capacity.VCPUs-floor.VCPUs,
		capacity.MemoryBytes>>30, (capacity.MemoryBytes-floor.MemoryBytes)>>30,
		capacity.SwiftNICs, capacity.SwiftNICs-floor.SwiftNICs)
}

func formatHeadroomDelta(before, after map[compute.VMFamily]int64, families []compute.VMFamily) string {
	var parts []string
	for _, family := range families {
		if before[family] != after[family] {
			parts = append(parts, fmt.Sprintf("%-8s %4dc remaining", shortFamily(family)+":", after[family]))
		}
	}

	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "  ")
}

func shortFamily(family compute.VMFamily) string {
	name := string(family)
	name = strings.TrimPrefix(name, "standard")
	name = strings.TrimSuffix(name, "Family")
	return name
}

// roleOf returns the role of the named pool, searching the given states in
// order (first match wins).
func roleOf(name string, states ...[]PoolState) compute.PoolRole {
	for _, state := range states {
		for _, p := range state {
			if p.Name == name {
				return p.Role
			}
		}
	}
	return ""
}

func sortedFamilies(budgets map[compute.VMFamily]int64) []compute.VMFamily {
	families := make([]compute.VMFamily, 0, len(budgets))
	for family := range budgets {
		families = append(families, family)
	}
	sort.Slice(families, func(i, j int) bool {
		return families[i] < families[j]
	})
	return families
}

func autoScaleTag(p PoolState) string {
	if p.AutoScalingEnabled {
		return "autoscaled"
	}
	return "frozen"
}

func actionDetail(a Action) string {
	switch v := a.(type) {
	case createAction:
		return fmt.Sprintf("min=%-3d max=%d", v.Pool.MinCount, v.Pool.MaxCount)
	case setScalingBoundsAction:
		return fmt.Sprintf("min=%-3d max=%d", v.MinCount, v.MaxCount)
	case unfreezeAction:
		return fmt.Sprintf("min=%-3d max=%d", v.MinCount, v.MaxCount)
	case freezeAction:
		return fmt.Sprintf("count=%d", v.Count)
	case reduceAction:
		return fmt.Sprintf("count=%d", v.Count)
	case deleteAction:
		return ""
	case reconcileAction:
		return "requires new observation"
	case waitAction:
		return fmt.Sprintf("observed transition; poll=%s", v.Delay)
	case updateConfigAction:
		return fmt.Sprintf("labels=%v taints=%v", v.Labels, v.Taints)
	default:
		return ""
	}
}
