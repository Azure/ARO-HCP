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
	"math"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"text/tabwriter"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v6"

	"github.com/Azure/ARO-HCP/fleet/pkg/compute"
)

func TestNetworkConfigFromPools(t *testing.T) {
	tests := []struct {
		name     string
		pools    []armcontainerservice.AgentPool
		expected compute.NetworkConfig
	}{
		{
			name:     "no pools returns empty NetworkConfig",
			pools:    nil,
			expected: compute.NetworkConfig{},
		},
		{
			name: "no system pool returns empty NetworkConfig",
			pools: []armcontainerservice.AgentPool{
				makeAgentPool("user1", "Standard_D4s_v3", []string{"1"}, 100, 3, false, 0, 0, nil),
			},
			expected: compute.NetworkConfig{},
		},
		{
			name: "system pool with both subnets extracts both",
			pools: []armcontainerservice.AgentPool{
				{
					Name: ptr.To("system"),
					Properties: &armcontainerservice.ManagedClusterAgentPoolProfileProperties{
						Mode:         ptr.To(armcontainerservice.AgentPoolModeSystem),
						VnetSubnetID: ptr.To("/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.Network/virtualNetworks/vnet1/subnets/node-subnet"),
						PodSubnetID:  ptr.To("/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.Network/virtualNetworks/vnet1/subnets/pod-subnet"),
					},
				},
			},
			expected: compute.NetworkConfig{
				VnetSubnetID: "/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.Network/virtualNetworks/vnet1/subnets/node-subnet",
				PodSubnetID:  "/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.Network/virtualNetworks/vnet1/subnets/pod-subnet",
			},
		},
		{
			name: "system pool with nil properties is skipped",
			pools: []armcontainerservice.AgentPool{
				{
					Name:       ptr.To("system"),
					Properties: nil,
				},
			},
			expected: compute.NetworkConfig{},
		},
		{
			name: "system pool with nil subnets returns empty strings",
			pools: []armcontainerservice.AgentPool{
				{
					Name: ptr.To("system"),
					Properties: &armcontainerservice.ManagedClusterAgentPoolProfileProperties{
						Mode: ptr.To(armcontainerservice.AgentPoolModeSystem),
					},
				},
			},
			expected: compute.NetworkConfig{},
		},
		{
			name: "multiple pools and first system pool wins",
			pools: []armcontainerservice.AgentPool{
				makeAgentPool("user1", "Standard_D4s_v3", []string{"1"}, 100, 3, false, 0, 0, nil),
				{
					Name: ptr.To("system1"),
					Properties: &armcontainerservice.ManagedClusterAgentPoolProfileProperties{
						Mode:         ptr.To(armcontainerservice.AgentPoolModeSystem),
						VnetSubnetID: ptr.To("/subnets/first"),
						PodSubnetID:  ptr.To("/subnets/first-pod"),
					},
				},
				{
					Name: ptr.To("system2"),
					Properties: &armcontainerservice.ManagedClusterAgentPoolProfileProperties{
						Mode:         ptr.To(armcontainerservice.AgentPoolModeSystem),
						VnetSubnetID: ptr.To("/subnets/second"),
						PodSubnetID:  ptr.To("/subnets/second-pod"),
					},
				},
			},
			expected: compute.NetworkConfig{
				VnetSubnetID: "/subnets/first",
				PodSubnetID:  "/subnets/first-pod",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := networkConfigFromPools(test.pools)
			assert.Equal(t, test.expected, result)
		})
	}
}

var (
	specE32v6   = compute.VMSpec{Size: "Standard_E32ds_v6", Family: "standardEDSv6Family", VCPUs: 32, MemoryGiB: 256, SecondaryNICs: 7}
	specE16v6   = compute.VMSpec{Size: "Standard_E16ds_v6", Family: "standardEDSv6Family", VCPUs: 16, MemoryGiB: 128, SecondaryNICs: 7}
	specD4v3    = compute.VMSpec{Size: "Standard_D4s_v3", Family: "standardDSv3Family", VCPUs: 4, MemoryGiB: 16, SecondaryNICs: 1}
	specD8v6    = compute.VMSpec{Size: "Standard_D8ds_v6", Family: "standardDDSv6Family", VCPUs: 8, MemoryGiB: 64, SecondaryNICs: 3}
	specE8dsV5  = compute.VMSpec{Size: "Standard_E8ds_v5", Family: "standardEDSv5Family", VCPUs: 8, MemoryGiB: 64, SecondaryNICs: 3}
	specE16dsV5 = compute.VMSpec{Size: "Standard_E16ds_v5", Family: "standardEDSv5Family", VCPUs: 16, MemoryGiB: 128, SecondaryNICs: 7}
	specE32dsV5 = compute.VMSpec{Size: "Standard_E32ds_v5", Family: "standardEDSv5Family", VCPUs: 32, MemoryGiB: 256, SecondaryNICs: 7}
	specE32dsV4 = compute.VMSpec{Size: "Standard_E32ds_v4", Family: "standardEDSv4Family", VCPUs: 32, MemoryGiB: 256, SecondaryNICs: 7}
)

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

func pool(name string, spec compute.VMSpec, zone string, maxCount, osDiskSizeGB int32) compute.Pool {
	return compute.Pool{
		Role:              compute.PoolRoleWorker,
		Name:              name,
		Spec:              spec,
		AvailabilityZones: []string{zone},
		MaxCount:          maxCount,
		InitialMinCount:   1,
		OSDiskSizeGB:      osDiskSizeGB,
		MaxPods:           225,
		Labels:            map[string]string{compute.RoleLabel: string(compute.PoolRoleWorker)},
		EnableSwift:       true,
	}
}

func poolState(name string, spec compute.VMSpec, zone string, maxCount, osDiskSizeGB int32, autoScale bool, count int32) PoolState {
	return PoolState{
		Pool: compute.Pool{
			Role:              compute.PoolRoleWorker,
			Name:              name,
			Spec:              spec,
			AvailabilityZones: []string{zone},
			MaxCount:          maxCount,
			OSDiskSizeGB:      osDiskSizeGB,
			MaxPods:           225,
			EnableSwift:       true,
			Labels:            map[string]string{compute.RoleLabel: string(compute.PoolRoleWorker)},
		},
		AutoScalingEnabled: autoScale,
		Count:              count,
		ProvisioningState:  "Succeeded",
		ETag:               fmt.Sprintf("etag-%s", name),
		MinCount:           1,
	}
}

func systemPool(name string, spec compute.VMSpec, zone string, maxCount, osDiskSizeGB int32) compute.Pool {
	return compute.Pool{
		Role:              compute.PoolRoleSystem,
		Name:              name,
		Spec:              spec,
		AvailabilityZones: []string{zone},
		MaxCount:          maxCount,
		InitialMinCount:   1,
		OSDiskSizeGB:      osDiskSizeGB,
		MaxPods:           225,
		Labels:            map[string]string{compute.RoleLabel: string(compute.PoolRoleSystem)},
	}
}

func systemPoolState(name string, spec compute.VMSpec, zone string, maxCount, osDiskSizeGB int32, autoScale bool, count int32) PoolState {
	return PoolState{
		Pool: compute.Pool{
			Role:              compute.PoolRoleSystem,
			Name:              name,
			Spec:              spec,
			AvailabilityZones: []string{zone},
			MaxCount:          maxCount,
			OSDiskSizeGB:      osDiskSizeGB,
			MaxPods:           225,
			Labels:            map[string]string{compute.RoleLabel: string(compute.PoolRoleSystem)},
		},
		AutoScalingEnabled: autoScale,
		Count:              count,
		ProvisioningState:  "Succeeded",
		ETag:               fmt.Sprintf("etag-%s", name),
		MinCount:           1,
	}
}

func generousBudgets(desired []compute.Pool, current []PoolState) map[compute.VMFamily]int64 {
	budgets := make(map[compute.VMFamily]int64)
	for _, p := range desired {
		budgets[p.Spec.Family] = math.MaxInt64
	}
	for _, p := range current {
		budgets[p.Spec.Family] = math.MaxInt64
	}
	return budgets
}

func requireAction(t *testing.T, want Action, got Action) {
	t.Helper()
	if diff := cmp.Diff(want, got, cmp.Exporter(func(reflect.Type) bool { return true })); diff != "" {
		t.Errorf("action mismatch (-want +got):\n%s", diff)
	}
}

// ---------------------------------------------------------------------------
// Simulation + golden file infrastructure
// ---------------------------------------------------------------------------

func applyAction(current []PoolState, action Action) []PoolState {
	switch a := action.(type) {
	case waitAction:
		return current

	case reconcileAction:
		for i, p := range current {
			if p.Name == a.poolName() {
				current[i].ProvisioningState = "Updating"
				current[i].ETag = fmt.Sprintf("etag-%s-reconciling", a.poolName())
				return current
			}
		}

	case createAction:
		return append(current, PoolState{
			Pool: a.Pool,
			// The seeded floor becomes the pool's live MinCount.
			MinCount:           a.Pool.InitialMinCount,
			AutoScalingEnabled: true,
			Count:              1,
			ProvisioningState:  "Succeeded",
			ETag:               fmt.Sprintf("etag-%s-new", a.poolName()),
		})

	case setScalingBoundsAction:
		for i, p := range current {
			if p.Name == a.poolName() {
				current[i].MaxCount = a.MaxCount
				current[i].MinCount = a.MinCount
				current[i].ETag = fmt.Sprintf("etag-%s-%d", a.poolName(), a.MaxCount)
				return current
			}
		}

	case unfreezeAction:
		for i, p := range current {
			if p.Name == a.poolName() {
				current[i].AutoScalingEnabled = true
				current[i].MaxCount = a.MaxCount
				current[i].MinCount = a.MinCount
				current[i].ETag = fmt.Sprintf("etag-%s-unfrozen", a.poolName())
				return current
			}
		}

	case freezeAction:
		for i, p := range current {
			if p.Name == a.poolName() {
				current[i].AutoScalingEnabled = false
				current[i].Count = a.Count
				current[i].MaxCount = a.Count
				current[i].ETag = fmt.Sprintf("etag-%s-frozen", a.poolName())
				return current
			}
		}

	case reduceAction:
		for i, p := range current {
			if p.Name == a.poolName() {
				current[i].Count = a.Count
				current[i].MaxCount = a.Count
				current[i].ETag = fmt.Sprintf("etag-%s-cnt%d", a.poolName(), a.Count)
				return current
			}
		}

	case updateConfigAction:
		for i, p := range current {
			if p.Name == a.poolName() {
				current[i].Labels = a.Labels
				current[i].Taints = a.Taints
				current[i].ETag = fmt.Sprintf("etag-%s-config", a.poolName())
				return current
			}
		}

	case deleteAction:
		result := make([]PoolState, 0, len(current)-1)
		for _, p := range current {
			if p.Name != a.poolName() {
				result = append(result, p)
			}
		}
		return result
	}
	return current
}

type traceStep struct {
	Action         Action
	State          []PoolState
	HeadroomBefore map[compute.VMFamily]int64
	HeadroomAfter  map[compute.VMFamily]int64
	Capacity       compute.CapacityByRole
}

type trace struct {
	Desired         []compute.Pool
	Initial         []PoolState
	FamilyBudgets   map[compute.VMFamily]int64
	Steps           []traceStep
	Baseline        compute.CapacityByRole
	CapacityFloor   compute.CapacityByRole
	FullyAllocated  bool
	InitialCapacity compute.CapacityByRole
	RejectedPlan    error
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

func simulateAndTrace(t *testing.T, desired []compute.Pool, initial []PoolState, familyBudgets map[compute.VMFamily]int64, baseline compute.CapacityByRole, fullyAllocated bool, maxCycles int) trace {
	t.Helper()
	state := make([]PoolState, len(initial))
	copy(state, initial)
	runInitial := runningVCPU(initial)

	tr := trace{Desired: desired, Initial: initial, FamilyBudgets: familyBudgets, Baseline: baseline, FullyAllocated: fullyAllocated, InitialCapacity: stateCapacity(t, initial)}
	desiredCapacity, err := compute.PoolCapacities(desired)
	require.NoError(t, err)
	// Match the controller's validation before selecting any ARM action.
	capacityFloor, err := desiredCapacity.TransitionFloor(baseline, fullyAllocated)
	if err != nil {
		tr.RejectedPlan = err
		return tr
	}

	tr.CapacityFloor = capacityFloor

	for range maxCycles {
		live := liveBudget(familyBudgets, runInitial, runningVCPU(state))
		action := findNextAction(desired, state, live, capacityFloor, compute.NetworkConfig{})
		if action == nil {
			break
		}
		if _, isWait := action.(waitAction); isWait {
			break
		}

		before := computeFamilyHeadroom(live, state)
		state = applyAction(state, action)
		snapshot := make([]PoolState, len(state))
		copy(snapshot, state)
		after := computeFamilyHeadroom(liveBudget(familyBudgets, runInitial, runningVCPU(snapshot)), snapshot)
		capacity := stateCapacity(t, snapshot)
		require.NoError(t, capacity.ValidateAgainstBaseline(capacityFloor), "every transition must preserve the accepted capacity floor")
		tr.Steps = append(tr.Steps, traceStep{Action: action, State: snapshot, HeadroomBefore: before, HeadroomAfter: after, Capacity: capacity})
	}

	return tr
}

func stateCapacity(t *testing.T, state []PoolState) compute.CapacityByRole {
	t.Helper()
	var pools []compute.Pool
	for _, cur := range state {
		p := cur.Pool
		p.MaxCount = int32(poolCeiling(cur))
		pools = append(pools, p)
	}
	capacity, err := compute.PoolCapacities(pools)
	require.NoError(t, err)
	return capacity
}

func (tr trace) finalState() []PoolState {
	if len(tr.Steps) == 0 {
		return tr.Initial
	}
	return tr.Steps[len(tr.Steps)-1].State
}

func assertConverged(t *testing.T, desired []compute.Pool, current []PoolState) {
	t.Helper()
	assert.True(t, configurationConverged(desired, current), "ARM pool configuration must match desired")
	desiredByName := make(map[string]compute.Pool, len(desired))
	for _, p := range desired {
		desiredByName[p.Name] = p
	}

	for _, p := range current {
		want, exists := desiredByName[p.Name]
		if !exists {
			t.Errorf("unexpected pool %s still present", p.Name)
			continue
		}
		assert.Equal(t, want.MaxCount, p.MaxCount, "pool %s maxCount", p.Name)
		assert.True(t, p.AutoScalingEnabled, "pool %s should have autoscaler enabled", p.Name)
		delete(desiredByName, p.Name)
	}
	for name := range desiredByName {
		t.Errorf("desired pool %s not created", name)
	}
}

func formatTrace(tr trace) string {
	var buf strings.Builder
	w := tabwriter.NewWriter(&buf, 0, 8, 2, ' ', 0)

	fmt.Fprintln(w, "initial:")
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
	fmt.Fprintln(w, "\nbaseline:")
	for _, role := range compute.CapacityRoles {
		fmt.Fprintf(w, "  %s:\t%s\n", role, formatCapacity(tr.Baseline[role]))
	}
	if tr.RejectedPlan == nil {
		fmt.Fprintln(w, "\ntransition floor:")
		for _, role := range compute.CapacityRoles {
			fmt.Fprintf(w, "  %s:\t%s\n", role, formatCapacity(tr.CapacityFloor[role]))
		}
	}
	fmt.Fprintln(w, "\ninitial capacity:")
	for _, role := range compute.CapacityRoles {
		fmt.Fprintf(w, "  %s:\t%s\n", role, formatCapacityAgainstBaseline(tr.InitialCapacity[role], tr.Baseline[role]))
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
		capacity := "capacity: " + formatCapacityAgainstBaseline(step.Capacity[role], tr.Baseline[role])
		delta := formatHeadroomDelta(step.HeadroomBefore, step.HeadroomAfter, families)
		fmt.Fprintln(w, strings.TrimRight(action+capacity+"\t"+delta, "\t"))
		prevState = step.State
	}
	if len(tr.Steps) == 0 {
		fmt.Fprintln(w, "  (none)")
	}

	fmt.Fprintln(w, "\nfinal:")
	final := tr.finalState()
	for _, p := range final {
		fmt.Fprintf(w, "  %s\t%s\t%s\tzone=%s\tmax=%d\tcount=%d\t%s\n",
			p.Name, p.Role, p.Spec.Size, p.ZoneString(), p.MaxCount, p.Count, autoScaleTag(p))
	}

	fmt.Fprintf(w, "\nsteps:\t%d\nconverged:\t%t\n", len(tr.Steps), tr.RejectedPlan == nil && configurationConverged(tr.Desired, final))
	_ = w.Flush() // strings.Builder writes cannot fail.
	lines := strings.Split(buf.String(), "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " ")
	}
	return strings.Join(lines, "\n")
}

func formatCapacity(capacity compute.RoleCapacity) string {
	return fmt.Sprintf("cpu=%d\tmemory=%dGiB\tswiftNICs=%d", capacity.VCPUs, capacity.MemoryGiB, capacity.SwiftNICs)
}

func formatCapacityAgainstBaseline(capacity, baseline compute.RoleCapacity) string {
	return fmt.Sprintf("cpu=%d\t(%+d)\tmemory=%dGiB\t(%+dGiB)\tswiftNICs=%d\t(%+d)",
		capacity.VCPUs, capacity.VCPUs-baseline.VCPUs,
		capacity.MemoryGiB, capacity.MemoryGiB-baseline.MemoryGiB,
		capacity.SwiftNICs, capacity.SwiftNICs-baseline.SwiftNICs)
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
		return fmt.Sprintf("min=%-3d max=%d", v.Pool.InitialMinCount, v.Pool.MaxCount)
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
		return ""
	default:
		return ""
	}
}

func compareGolden(t *testing.T, got string) {
	t.Helper()
	golden := filepath.Join("testdata", t.Name()+".txt")

	if os.Getenv("UPDATE_GOLDEN") != "" {
		require.NoError(t, os.MkdirAll(filepath.Dir(golden), 0o755))
		require.NoError(t, os.WriteFile(golden, []byte(got), 0o644))
		return
	}

	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("golden file not found: %s (run with UPDATE_GOLDEN=1 to create)", golden)
	}

	if diff := cmp.Diff(string(want), got); diff != "" {
		t.Errorf("golden file mismatch (-want +got):\n%s", diff)
	}
}

// ---------------------------------------------------------------------------
// Scenario tests — convergence with golden file traces
// ---------------------------------------------------------------------------

func TestFreshStart(t *testing.T) {
	desired := []compute.Pool{
		pool("w1abc", specE32v6, "1", 6, 512),
		pool("w2abc", specE32v6, "2", 6, 512),
		pool("w3abc", specE32v6, "3", 6, 512),
	}
	budgets := map[compute.VMFamily]int64{specE32v6.Family: 3 * 6 * specE32v6.VCPUs}

	baseline := compute.CapacityByRole{compute.PoolRoleSystem: {}, compute.PoolRoleInfra: {}, compute.PoolRoleWorker: {}}
	tr := simulateAndTrace(t, desired, nil, budgets, baseline, true, 10)
	require.NoError(t, tr.RejectedPlan)
	assertConverged(t, desired, tr.finalState())
	compareGolden(t, formatTrace(tr))
}

func TestSteadyState(t *testing.T) {
	desired := []compute.Pool{
		pool("w1abc", specE32v6, "1", 6, 512),
		pool("w2abc", specE32v6, "2", 6, 512),
		pool("w3abc", specE32v6, "3", 6, 512),
	}
	current := []PoolState{
		poolState("w1abc", specE32v6, "1", 6, 512, true, 3),
		poolState("w2abc", specE32v6, "2", 6, 512, true, 3),
		poolState("w3abc", specE32v6, "3", 6, 512, true, 3),
	}

	baseline := compute.CapacityByRole{compute.PoolRoleSystem: {}, compute.PoolRoleInfra: {}, compute.PoolRoleWorker: {VCPUs: 576, MemoryGiB: 4608, SwiftNICs: 126}}
	tr := simulateAndTrace(t, desired, current, generousBudgets(desired, current), baseline, true, 10)
	assert.Empty(t, tr.Steps, "no actions needed")
}

func TestScaleUp(t *testing.T) {
	desired := []compute.Pool{
		pool("w1abc", specE32v6, "1", 10, 512),
		pool("w2abc", specE32v6, "2", 10, 512),
		pool("w3abc", specE32v6, "3", 10, 512),
	}
	current := []PoolState{
		poolState("w1abc", specE32v6, "1", 6, 512, true, 3),
		poolState("w2abc", specE32v6, "2", 6, 512, true, 3),
		poolState("w3abc", specE32v6, "3", 6, 512, true, 3),
	}
	budgets := map[compute.VMFamily]int64{specE32v6.Family: 3 * 10 * specE32v6.VCPUs}

	baseline := compute.CapacityByRole{compute.PoolRoleSystem: {}, compute.PoolRoleInfra: {}, compute.PoolRoleWorker: {VCPUs: 576, MemoryGiB: 4608, SwiftNICs: 126}}
	tr := simulateAndTrace(t, desired, current, budgets, baseline, true, 10)
	require.NoError(t, tr.RejectedPlan)
	assertConverged(t, desired, tr.finalState())
	compareGolden(t, formatTrace(tr))
}

func TestScaleDown(t *testing.T) {
	desired := []compute.Pool{
		pool("w1abc", specE32v6, "1", 6, 512),
		pool("w2abc", specE32v6, "2", 6, 512),
		pool("w3abc", specE32v6, "3", 6, 512),
	}
	current := []PoolState{
		poolState("w1abc", specE32v6, "1", 10, 512, true, 3),
		poolState("w2abc", specE32v6, "2", 10, 512, true, 3),
		poolState("w3abc", specE32v6, "3", 10, 512, true, 3),
	}
	budgets := map[compute.VMFamily]int64{specE32v6.Family: 3 * 10 * specE32v6.VCPUs}

	// The earlier six-node-per-pool baseline remains; the attempted growth to ten never converged.
	baseline := compute.CapacityByRole{compute.PoolRoleSystem: {}, compute.PoolRoleInfra: {}, compute.PoolRoleWorker: {VCPUs: 576, MemoryGiB: 4608, SwiftNICs: 126}}
	tr := simulateAndTrace(t, desired, current, budgets, baseline, true, 10)
	require.NoError(t, tr.RejectedPlan)
	assertConverged(t, desired, tr.finalState())
	compareGolden(t, formatTrace(tr))
}

func TestCrossFamilyReplace_Upsize(t *testing.T) {
	desired := []compute.Pool{
		pool("new1", specE32v6, "1", 6, 512),
		pool("new2", specE32v6, "2", 6, 512),
		pool("new3", specE32v6, "3", 6, 512),
	}
	current := []PoolState{
		poolState("old1", specD4v3, "1", 6, 100, true, 2),
		poolState("old2", specD4v3, "2", 6, 100, true, 2),
		poolState("old3", specD4v3, "3", 6, 100, true, 2),
	}
	budgets := map[compute.VMFamily]int64{
		specE32v6.Family: 3 * 6 * specE32v6.VCPUs,
		specD4v3.Family:  3 * 6 * specD4v3.VCPUs,
	}

	baseline := compute.CapacityByRole{compute.PoolRoleSystem: {}, compute.PoolRoleInfra: {}, compute.PoolRoleWorker: {VCPUs: 72, MemoryGiB: 288, SwiftNICs: 18}}
	tr := simulateAndTrace(t, desired, current, budgets, baseline, true, 60)
	require.NoError(t, tr.RejectedPlan)
	assertConverged(t, desired, tr.finalState())
	compareGolden(t, formatTrace(tr))
}

func TestCrossFamilyGrowBeforeSqueeze(t *testing.T) {
	desired := []compute.Pool{
		pool("new1", specE32v6, "1", 6, 512),
	}
	current := []PoolState{
		poolState("old1", specD4v3, "1", 6, 100, true, 2),
	}

	action := findNextAction(desired, current, generousBudgets(desired, current), compute.CapacityByRole{}, compute.NetworkConfig{})
	require.NotNil(t, action)
	create, ok := action.(createAction)
	require.True(t, ok, "cross-family: should create new pool immediately (independent headroom)")
	assert.Equal(t, "new1", create.poolName())
	assert.Equal(t, int32(6), create.Pool.MaxCount)
}

func TestSameFamilyGrowBeforeSqueeze(t *testing.T) {
	desired := []compute.Pool{
		pool("new1", specE32v6, "1", 6, 512),
	}
	current := []PoolState{
		poolState("old1", specE32v6, "1", 6, 100, true, 2),
	}

	action := findNextAction(desired, current, generousBudgets(desired, current), compute.CapacityByRole{}, compute.NetworkConfig{})
	require.NotNil(t, action)
	create, ok := action.(createAction)
	require.True(t, ok, "same-family with generous budget: should create new pool immediately")
	assert.Equal(t, "new1", create.poolName())
	assert.Equal(t, int32(6), create.Pool.MaxCount)
}

func TestSameFamilyConstrainedBudget(t *testing.T) {
	desired := []compute.Pool{
		pool("new1", specE32v6, "1", 6, 512),
	}
	current := []PoolState{
		poolState("old1", specE32v6, "1", 6, 100, true, 2),
	}

	tightBudget := map[compute.VMFamily]int64{
		specE32v6.Family: 6 * specE32v6.VCPUs,
	}
	// Headroom = budget - committed-not-running = 192 - (6-2)*32 = 64, so the
	// controller creates the replacement pool throttled to what currently fits
	// quota (max=2) instead of refusing all progress. old1's 2 running nodes are
	// accounted via the budget baseline, not double-counted in the ceiling.
	action := findNextAction(desired, current, tightBudget, compute.CapacityByRole{}, compute.NetworkConfig{})
	create, ok := action.(createAction)
	require.True(t, ok, "tight budget should still allow a throttled create")
	assert.Equal(t, "new1", create.poolName())
	assert.Equal(t, int32(2), create.Pool.MaxCount)
}

func TestInProgressPoolWaits(t *testing.T) {
	desired := []compute.Pool{
		pool("new1", specE32v6, "1", 6, 512),
	}
	current := []PoolState{
		{
			Pool:               pool("old1", specD4v3, "1", 6, 100),
			AutoScalingEnabled: true,
			Count:              2,
			ProvisioningState:  "Updating",
			ETag:               "etag-old1",
		},
	}

	action := findNextAction(desired, current, generousBudgets(desired, current), compute.CapacityByRole{}, compute.NetworkConfig{})
	require.NotNil(t, action)
	requireAction(t, newWaitAction("old1", "Standard_D4s_v3", "1", 1*time.Minute), action)
}

func TestFailedUndesiredPoolDoesNotBlock(t *testing.T) {
	desired := []compute.Pool{
		pool("new1", specE32v6, "1", 6, 512),
	}
	current := []PoolState{
		{
			Pool:               pool("old1", specD4v3, "1", 6, 100),
			AutoScalingEnabled: true,
			Count:              2,
			ProvisioningState:  "Failed",
			ETag:               "etag-old1",
		},
	}

	action := findNextAction(desired, current, generousBudgets(desired, current), compute.CapacityByRole{}, compute.NetworkConfig{})
	require.NotNil(t, action, "Failed undesired pool should not block")
	_, isWait := action.(waitAction)
	assert.False(t, isWait, "should not return waitAction for failed undesired pool")
}

// TestUndesiredSystemPoolDeletedDirectly is the regression test for the
// deadlock where an undesired AKS Mode=System pool could not be drained to zero
// (AKS enforces minCount 1), so the reduce-to-zero path failed on every
// reconcile. Such a pool must be deleted directly instead, while a desired
// system pool remains as the surviving system pool.
func TestUndesiredSystemPoolDeletedDirectly(t *testing.T) {
	desired := []compute.Pool{
		systemPool("sysnew", specD4v3, "1", 3, 128),
		pool("wrk1", specE32v6, "1", 2, 512),
	}
	current := []PoolState{
		systemPoolState("sysnew", specD4v3, "1", 3, 128, true, 1),
		poolState("wrk1", specE32v6, "1", 2, 512, true, 1),
		systemPoolState("s0old", specD4v3, "1", 1, 128, false, 1),
	}

	action := findNextAction(desired, current, generousBudgets(desired, current), compute.CapacityByRole{}, compute.NetworkConfig{})
	require.NotNil(t, action)
	requireAction(t, newDeleteAction("s0old", "Standard_D4s_v3", "1", "etag-s0old"), action)
}

// TestUndesiredSystemPoolPreservedWithoutSurvivor asserts the controller never
// removes the last system pool: with no system pool in the desired set, the
// undesired system pool is neither drained (which would 400 at count 0) nor
// deleted (AKS forbids deleting the last system pool). The controller idles.
func TestUndesiredSystemPoolPreservedWithoutSurvivor(t *testing.T) {
	desired := []compute.Pool{
		pool("wrk1", specE32v6, "1", 2, 512),
	}
	current := []PoolState{
		poolState("wrk1", specE32v6, "1", 2, 512, true, 1),
		systemPoolState("s0old", specD4v3, "1", 1, 128, false, 1),
	}

	action := findNextAction(desired, current, generousBudgets(desired, current), compute.CapacityByRole{}, compute.NetworkConfig{})
	assert.Nil(t, action, "must not drain or delete the last system pool")
}

// TestUndesiredSystemPoolUnblocksUserPools reproduces the head-of-line block
// from the incident: an undrainable system pool must not starve the deletion of
// undesired user pools. The whole set must converge to desired.
func TestUndesiredSystemPoolUnblocksUserPools(t *testing.T) {
	desired := []compute.Pool{
		systemPool("sysnew", specD4v3, "1", 3, 128),
		pool("wrk1", specE32v6, "1", 2, 512),
	}
	initial := []PoolState{
		systemPoolState("sysnew", specD4v3, "1", 3, 128, true, 1),
		poolState("wrk1", specE32v6, "1", 2, 512, true, 1),
		systemPoolState("s0old", specD4v3, "1", 1, 128, false, 1),
		poolState("u0old", specD4v3, "1", 1, 128, false, 1),
	}

	baseline := compute.CapacityByRole{compute.PoolRoleSystem: {VCPUs: 12, MemoryGiB: 48}, compute.PoolRoleInfra: {}, compute.PoolRoleWorker: {VCPUs: 64, MemoryGiB: 512, SwiftNICs: 14}}
	tr := simulateAndTrace(t, desired, initial, generousBudgets(desired, initial), baseline, true, 20)
	require.NoError(t, tr.RejectedPlan)
	assertConverged(t, desired, tr.finalState())
}

// TestCreateSeedsMinCountFromTier verifies the desired pool's seeded MinCount
// flows into the create action unchanged when headroom is ample.
func TestCreateSeedsMinCountFromTier(t *testing.T) {
	p := pool("w1", specE32v6, "1", 5, 512)
	p.InitialMinCount = 3
	desired := []compute.Pool{p}

	action := findNextAction(desired, nil, generousBudgets(desired, nil), compute.CapacityByRole{}, compute.NetworkConfig{})
	create, ok := action.(createAction)
	require.True(t, ok, "expected a create action")
	assert.Equal(t, int32(3), create.Pool.InitialMinCount)
	assert.Equal(t, int32(5), create.Pool.MaxCount)
}

// TestCreateReclampsMinToFitHeadroom verifies that when headroom forces the
// create ceiling below the seeded floor, MinCount is re-clamped so the create
// PUT keeps min <= max.
func TestCreateReclampsMinToFitHeadroom(t *testing.T) {
	p := pool("w1", specE32v6, "1", 5, 512)
	p.InitialMinCount = 3
	desired := []compute.Pool{p}
	// One node of headroom (specE32v6 is 32 vCPUs) forces MaxCount to 1.
	budgets := map[compute.VMFamily]int64{specE32v6.Family: 32}

	action := findNextAction(desired, nil, budgets, compute.CapacityByRole{}, compute.NetworkConfig{})
	create, ok := action.(createAction)
	require.True(t, ok, "expected a create action")
	assert.Equal(t, int32(1), create.Pool.MaxCount)
	assert.Equal(t, int32(1), create.Pool.InitialMinCount)
}

// TestUnfreezePreservesLiveMin verifies unfreeze restores the pool's live floor
// (owned by another controller, retained across the freeze), not the tier seed.
func TestUnfreezePreservesLiveMin(t *testing.T) {
	desired := []compute.Pool{
		pool("w1", specE32v6, "1", 6, 512),
	}
	cur := poolState("w1", specE32v6, "1", 6, 512, false, 2)
	cur.MinCount = 3
	current := []PoolState{cur}

	action := findNextAction(desired, current, generousBudgets(desired, current), compute.CapacityByRole{}, compute.NetworkConfig{})
	requireAction(t, newUnfreezeAction("w1", "Standard_E32ds_v6", "1", "etag-w1", 3, 6), action)
}

// TestSetScalingBoundsClampsMinToNewMax verifies a ceiling reduction that would
// leave the live floor above the new max clamps MinCount down to the new max.
func TestSetScalingBoundsClampsMinToNewMax(t *testing.T) {
	desired := []compute.Pool{
		pool("w1", specE32v6, "1", 2, 512),
	}
	cur := poolState("w1", specE32v6, "1", 10, 512, true, 2)
	cur.MinCount = 4
	current := []PoolState{cur}

	action := findNextAction(desired, current, generousBudgets(desired, current), compute.CapacityByRole{}, compute.NetworkConfig{})
	requireAction(t, newSetScalingBoundsAction("w1", "Standard_E32ds_v6", "1", "etag-w1", 2, 2), action)
}

// TestMatchedPoolMinNotReset is the co-ownership guarantee: a matched desired
// pool whose live MinCount differs from the tier seed is left untouched. The
// controller never resets the floor another controller set.
func TestMatchedPoolMinNotReset(t *testing.T) {
	desired := []compute.Pool{
		pool("w1", specE32v6, "1", 6, 512),
	}
	cur := poolState("w1", specE32v6, "1", 6, 512, true, 3)
	cur.MinCount = 5
	current := []PoolState{cur}

	action := findNextAction(desired, current, generousBudgets(desired, current), compute.CapacityByRole{}, compute.NetworkConfig{})
	assert.Nil(t, action, "must not reset a live MinCount owned by another controller")
}

func TestFailedDesiredPoolGetsReconciled(t *testing.T) {
	desired := []compute.Pool{
		pool("w1abc", specE32v6, "1", 6, 512),
	}
	current := []PoolState{
		{
			Pool:               pool("w1abc", specE32v6, "1", 6, 512),
			AutoScalingEnabled: true,
			Count:              3,
			ProvisioningState:  "Failed",
			ETag:               "etag-w1abc",
		},
	}

	action := findNextAction(desired, current, generousBudgets(desired, current), compute.CapacityByRole{}, compute.NetworkConfig{})
	require.NotNil(t, action)
	requireAction(t, newReconcileAction("w1abc", "Standard_E32ds_v6", "1", "etag-w1abc"), action)
}

func TestFailedDesiredPoolReconcileBeforeMaxCountChange(t *testing.T) {
	desired := []compute.Pool{
		pool("w1abc", specE32v6, "1", 10, 512),
	}
	current := []PoolState{
		{
			Pool:               pool("w1abc", specE32v6, "1", 6, 512),
			AutoScalingEnabled: true,
			Count:              3,
			ProvisioningState:  "Failed",
			ETag:               "etag-w1abc",
		},
	}

	action := findNextAction(desired, current, generousBudgets(desired, current), compute.CapacityByRole{}, compute.NetworkConfig{})
	require.NotNil(t, action)
	requireAction(t, newReconcileAction("w1abc", "Standard_E32ds_v6", "1", "etag-w1abc"), action)
}

func TestInProgressPoolBlocksAllZones(t *testing.T) {
	desired := []compute.Pool{
		pool("new1", specE32v6, "1", 6, 512),
		pool("new2", specE32v6, "2", 6, 512),
	}
	current := []PoolState{
		{
			Pool:               pool("old1", specD4v3, "1", 6, 100),
			AutoScalingEnabled: true,
			Count:              2,
			ProvisioningState:  "Updating",
			ETag:               "etag-old1",
		},
	}

	action := findNextAction(desired, current, generousBudgets(desired, current), compute.CapacityByRole{}, compute.NetworkConfig{})
	require.NotNil(t, action)
	requireAction(t, newWaitAction("old1", "Standard_D4s_v3", "1", 1*time.Minute), action)
}

func TestOperatorReenablesAutoscaler(t *testing.T) {
	desired := []compute.Pool{
		pool("new1", specE32v6, "1", 6, 512),
	}
	current := []PoolState{
		poolState("old1", specE32v6, "1", 6, 100, true, 2),
		poolState("new1", specE32v6, "1", 6, 512, true, 1),
	}

	action := findNextAction(desired, current, generousBudgets(desired, current), compute.CapacityByRole{}, compute.NetworkConfig{})
	require.NotNil(t, action)
	setBounds, ok := action.(setScalingBoundsAction)
	require.True(t, ok, "should squeeze old1 max toward count")
	assert.Equal(t, "old1", setBounds.poolName())
	assert.Equal(t, int32(2), setBounds.MaxCount)
	assert.Equal(t, int32(1), setBounds.MinCount)
}

func TestConfigRollback(t *testing.T) {
	desired := []compute.Pool{
		pool("w1abc", specE32v6, "1", 6, 512),
	}
	current := []PoolState{
		poolState("w1abc", specE32v6, "1", 2, 512, false, 2),
	}

	action := findNextAction(desired, current, generousBudgets(desired, current), compute.CapacityByRole{}, compute.NetworkConfig{})
	require.NotNil(t, action)
	requireAction(t, newUnfreezeAction("w1abc", "Standard_E32ds_v6", "1", "etag-w1abc", 1, 6), action)
}

func TestCrossFamilyReplace_Downsize(t *testing.T) {
	desired := []compute.Pool{
		pool("d1", specD8v6, "1", 6, 512),
		pool("d2", specD8v6, "2", 6, 512),
		pool("d3", specD8v6, "3", 6, 512),
	}
	current := []PoolState{
		poolState("e1", specE32v6, "1", 6, 512, true, 2),
		poolState("e2", specE32v6, "2", 6, 512, true, 2),
		poolState("e3", specE32v6, "3", 6, 512, true, 2),
	}
	budgets := map[compute.VMFamily]int64{
		specD8v6.Family:  3 * 6 * specD8v6.VCPUs,
		specE32v6.Family: 3 * 6 * specE32v6.VCPUs,
	}

	// The larger pools are temporary capacity above this earlier established baseline.
	baseline := compute.CapacityByRole{compute.PoolRoleSystem: {}, compute.PoolRoleInfra: {}, compute.PoolRoleWorker: {VCPUs: 144, MemoryGiB: 1152, SwiftNICs: 54}}
	tr := simulateAndTrace(t, desired, current, budgets, baseline, true, 60)
	require.NoError(t, tr.RejectedPlan)
	assertConverged(t, desired, tr.finalState())
	compareGolden(t, formatTrace(tr))
}

func TestDesiredChangesMidReplace(t *testing.T) {
	current := []PoolState{
		poolState("old1", specE32v6, "1", 6, 100, true, 2),
	}

	desired := []compute.Pool{pool("new1", specE32v6, "1", 6, 512)}
	budgets := generousBudgets(desired, current)
	action := findNextAction(desired, current, budgets, compute.CapacityByRole{}, compute.NetworkConfig{})
	require.NotNil(t, action)
	create, ok := action.(createAction)
	require.True(t, ok, "same-family with generous budget: should create new pool immediately")
	assert.Equal(t, "new1", create.poolName())
	current = applyAction(current, action)

	desired = []compute.Pool{pool("new1", specE32v6, "1", 4, 512)}
	// The old pool was still growing from this baseline when replacement started.
	baseline := compute.CapacityByRole{compute.PoolRoleSystem: {}, compute.PoolRoleInfra: {}, compute.PoolRoleWorker: {VCPUs: 128, MemoryGiB: 1024, SwiftNICs: 28}}
	tr := simulateAndTrace(t, desired, current, generousBudgets(desired, current), baseline, true, 30)
	require.NoError(t, tr.RejectedPlan)
	assertConverged(t, desired, tr.finalState())
}

func TestNodeFailureDuringDrain(t *testing.T) {
	desired := []compute.Pool{
		pool("new1", specE32v6, "1", 6, 512),
	}
	current := []PoolState{
		poolState("old1", specE32v6, "1", 1, 100, false, 1),
		poolState("new1", specE32v6, "1", 6, 512, true, 1),
	}

	action := findNextAction(desired, current, generousBudgets(desired, current), compute.CapacityByRole{}, compute.NetworkConfig{})
	require.NotNil(t, action)
	reduce, ok := action.(reduceAction)
	require.True(t, ok, "should reduce remaining node")
	assert.Equal(t, int32(0), reduce.Count)
}

func TestMaxCountDecreaseBelowRunning(t *testing.T) {
	desired := []compute.Pool{
		pool("w1abc", specE32v6, "1", 6, 512),
	}
	current := []PoolState{
		poolState("w1abc", specE32v6, "1", 10, 512, true, 8),
	}
	budgets := map[compute.VMFamily]int64{specE32v6.Family: 10 * specE32v6.VCPUs}

	// The extra running nodes belong to an incomplete expansion above this baseline.
	baseline := compute.CapacityByRole{compute.PoolRoleSystem: {}, compute.PoolRoleInfra: {}, compute.PoolRoleWorker: {VCPUs: 192, MemoryGiB: 1536, SwiftNICs: 42}}
	tr := simulateAndTrace(t, desired, current, budgets, baseline, true, 10)
	require.NoError(t, tr.RejectedPlan)
	assertConverged(t, desired, tr.finalState())
	compareGolden(t, formatTrace(tr))
}

func TestUnfreezeBlockedByHighCount(t *testing.T) {
	desired := []compute.Pool{
		pool("w1abc", specE32v6, "1", 6, 512),
	}
	current := []PoolState{
		poolState("w1abc", specE32v6, "1", 10, 512, false, 10),
	}
	budgets := map[compute.VMFamily]int64{specE32v6.Family: 10 * specE32v6.VCPUs}

	// The frozen pool contains temporary expansion capacity above this baseline.
	baseline := compute.CapacityByRole{compute.PoolRoleSystem: {}, compute.PoolRoleInfra: {}, compute.PoolRoleWorker: {VCPUs: 192, MemoryGiB: 1536, SwiftNICs: 42}}
	tr := simulateAndTrace(t, desired, current, budgets, baseline, true, 20)
	require.NoError(t, tr.RejectedPlan)
	assertConverged(t, desired, tr.finalState())
	compareGolden(t, formatTrace(tr))
}

func TestMultipleDesiredPoolsPerZone(t *testing.T) {
	desired := []compute.Pool{
		pool("e1", specE32v6, "1", 4, 512),
		pool("d1", specD8v6, "1", 2, 512),
		pool("e2", specE32v6, "2", 4, 512),
		pool("d2", specD8v6, "2", 2, 512),
		pool("e3", specE32v6, "3", 4, 512),
		pool("d3", specD8v6, "3", 2, 512),
	}
	budgets := map[compute.VMFamily]int64{
		specE32v6.Family: 3 * 4 * specE32v6.VCPUs,
		specD8v6.Family:  3 * 2 * specD8v6.VCPUs,
	}

	baseline := compute.CapacityByRole{compute.PoolRoleSystem: {}, compute.PoolRoleInfra: {}, compute.PoolRoleWorker: {}}
	tr := simulateAndTrace(t, desired, nil, budgets, baseline, true, 20)
	require.NoError(t, tr.RejectedPlan)
	assertConverged(t, desired, tr.finalState())
	compareGolden(t, formatTrace(tr))
}

func TestSameFamilyGrowBeforeShrink(t *testing.T) {
	desired := []compute.Pool{
		pool("new1", specE32v6, "1", 6, 512),
		pool("new2", specE32v6, "2", 6, 512),
		pool("new3", specE32v6, "3", 6, 512),
	}
	current := []PoolState{
		poolState("old1", specE32v6, "1", 6, 100, true, 4),
		poolState("old2", specE32v6, "2", 6, 100, true, 4),
		poolState("old3", specE32v6, "3", 6, 100, true, 4),
	}
	budgets := map[compute.VMFamily]int64{specE32v6.Family: 2 * 3 * 6 * specE32v6.VCPUs}

	baseline := compute.CapacityByRole{compute.PoolRoleSystem: {}, compute.PoolRoleInfra: {}, compute.PoolRoleWorker: {VCPUs: 576, MemoryGiB: 4608, SwiftNICs: 126}}
	tr := simulateAndTrace(t, desired, current, budgets, baseline, true, 100)
	require.NoError(t, tr.RejectedPlan)
	assertConverged(t, desired, tr.finalState())
	compareGolden(t, formatTrace(tr))
}

// TestSameFamilyRebalanceZeroSlack rebalances desired pools while preserving
// the established baseline. Desired capacity is a target, not a transition floor.
func TestSameFamilyRebalanceZeroSlack(t *testing.T) {
	desired := []compute.Pool{
		pool("a1", specE32v6, "1", 3, 512),
		pool("b1", specE32v6, "2", 6, 512),
	}
	current := []PoolState{
		poolState("a1", specE32v6, "1", 10, 512, true, 8),
		poolState("b1", specE32v6, "2", 1, 512, true, 1),
	}
	budgets := map[compute.VMFamily]int64{specE32v6.Family: (3 + 6) * specE32v6.VCPUs}

	// The incomplete rebalance may release capacity above the earlier eight-node baseline.
	baseline := compute.CapacityByRole{compute.PoolRoleSystem: {}, compute.PoolRoleInfra: {}, compute.PoolRoleWorker: {VCPUs: 256, MemoryGiB: 2048, SwiftNICs: 56}}
	tr := simulateAndTrace(t, desired, current, budgets, baseline, true, 100)
	require.NoError(t, tr.RejectedPlan)
	assertConverged(t, desired, tr.finalState())
	compareGolden(t, formatTrace(tr))
}

// TestSameFamilyReplace_ZeroSlack_UndesiredPool protects reserved capacity when
// quota permits a partial replacement but squeezing the old pool would cross
// the established baseline. The planner must stop with both pools preserved.
func TestSameFamilyReplace_ZeroSlack_UndesiredPool(t *testing.T) {
	desired := []compute.Pool{
		pool("new1", specE32v6, "1", 6, 512),
	}
	current := []PoolState{
		poolState("old1", specE32v6, "1", 6, 100, true, 2),
	}
	budgets := map[compute.VMFamily]int64{specE32v6.Family: 6 * specE32v6.VCPUs}

	baseline := compute.CapacityByRole{compute.PoolRoleSystem: {}, compute.PoolRoleInfra: {}, compute.PoolRoleWorker: {VCPUs: 192, MemoryGiB: 1536, SwiftNICs: 42}}
	tr := simulateAndTrace(t, desired, current, budgets, baseline, true, 100)
	require.NoError(t, tr.RejectedPlan)
	require.False(t, configurationConverged(desired, tr.finalState()))
	require.Len(t, tr.Steps, 1, "creation fits, but squeezing old1 would cross the baseline")
	compareGolden(t, formatTrace(tr))
}

func TestMixedState_MatchedMissingUndesired(t *testing.T) {
	desired := []compute.Pool{
		pool("a1", specE32v6, "1", 6, 512),
		pool("d1", specD8v6, "1", 4, 512),
	}
	current := []PoolState{
		poolState("a1", specE32v6, "1", 6, 512, true, 3),
		poolState("b1", specD4v3, "1", 6, 100, true, 2),
		poolState("c1", specD4v3, "2", 6, 100, true, 2),
	}
	budgets := map[compute.VMFamily]int64{
		specE32v6.Family: 6 * specE32v6.VCPUs,
		specD8v6.Family:  4 * specD8v6.VCPUs,
		specD4v3.Family:  2 * 6 * specD4v3.VCPUs,
	}

	// Only a1 belongs to the established baseline; b1 and c1 are temporary expansion.
	baseline := compute.CapacityByRole{compute.PoolRoleSystem: {}, compute.PoolRoleInfra: {}, compute.PoolRoleWorker: {VCPUs: 192, MemoryGiB: 1536, SwiftNICs: 42}}
	tr := simulateAndTrace(t, desired, current, budgets, baseline, true, 60)
	require.NoError(t, tr.RejectedPlan)
	assertConverged(t, desired, tr.finalState())
	compareGolden(t, formatTrace(tr))
}

func TestMultipleUndesiredAtDifferentStages(t *testing.T) {
	desired := []compute.Pool{
		pool("new1", specE32v6, "1", 6, 512),
	}
	current := []PoolState{
		poolState("new1", specE32v6, "1", 6, 512, true, 1),
		poolState("old_as", specE32v6, "1", 6, 100, true, 2),
		poolState("old_frozen", specE32v6, "2", 3, 100, false, 3),
		poolState("old_empty", specE32v6, "3", 0, 100, false, 0),
	}

	action := findNextAction(desired, current, generousBudgets(desired, current), compute.CapacityByRole{}, compute.NetworkConfig{})
	require.NotNil(t, action)
	setBounds, ok := action.(setScalingBoundsAction)
	require.True(t, ok, "should squeeze autoscaled undesired pool first")
	assert.Equal(t, "old_as", setBounds.poolName())
	assert.Equal(t, int32(2), setBounds.MaxCount)
}

func TestReconcileDesiredBeforeShrinkUndesired(t *testing.T) {
	desired := []compute.Pool{
		pool("new1", specE32v6, "1", 6, 512),
	}
	current := []PoolState{
		{
			Pool:               pool("new1", specE32v6, "1", 6, 512),
			AutoScalingEnabled: true,
			Count:              1,
			ProvisioningState:  "Failed",
			ETag:               "etag-new1",
		},
		poolState("old1", specE32v6, "1", 6, 100, true, 2),
	}

	action := findNextAction(desired, current, generousBudgets(desired, current), compute.CapacityByRole{}, compute.NetworkConfig{})
	require.NotNil(t, action)
	reconcile, ok := action.(reconcileAction)
	require.True(t, ok, "should reconcile failed desired pool before shrinking undesired")
	assert.Equal(t, "new1", reconcile.poolName())
}

func TestUndesiredPoolCountExceedsMax(t *testing.T) {
	desired := []compute.Pool{
		pool("new1", specE32v6, "1", 6, 512),
	}
	current := []PoolState{
		poolState("new1", specE32v6, "1", 6, 512, true, 1),
		{
			Pool:               pool("old1", specE32v6, "1", 5, 100),
			AutoScalingEnabled: true,
			Count:              7,
			ProvisioningState:  "Succeeded",
			ETag:               "etag-old1",
		},
	}

	action := findNextAction(desired, current, generousBudgets(desired, current), compute.CapacityByRole{}, compute.NetworkConfig{})
	require.NotNil(t, action, "should not be stuck — undesired pool needs freezing")
	freeze, ok := action.(freezeAction)
	require.True(t, ok, "should freeze undesired pool where count > max")
	assert.Equal(t, "old1", freeze.poolName())
	assert.Equal(t, int32(7), freeze.Count)
}

func TestSameFamilyConvergenceStepCount(t *testing.T) {
	desired := []compute.Pool{
		pool("new1", specE32v6, "1", 6, 512),
		pool("new2", specE32v6, "2", 6, 512),
		pool("new3", specE32v6, "3", 6, 512),
	}
	current := []PoolState{
		poolState("old1", specE32v6, "1", 6, 100, true, 2),
		poolState("old2", specE32v6, "2", 6, 100, true, 2),
		poolState("old3", specE32v6, "3", 6, 100, true, 2),
	}
	budgets := map[compute.VMFamily]int64{specE32v6.Family: 2 * 3 * 6 * specE32v6.VCPUs}

	baseline := compute.CapacityByRole{compute.PoolRoleSystem: {}, compute.PoolRoleInfra: {}, compute.PoolRoleWorker: {VCPUs: 576, MemoryGiB: 4608, SwiftNICs: 126}}
	tr := simulateAndTrace(t, desired, current, budgets, baseline, true, 200)
	require.NoError(t, tr.RejectedPlan)
	assertConverged(t, desired, tr.finalState())
	assert.Less(t, len(tr.Steps), 80, "convergence should be O(total slots), got %d steps", len(tr.Steps))
	compareGolden(t, formatTrace(tr))
}

func TestDeterministicActionSelection(t *testing.T) {
	desired := []compute.Pool{
		pool("new1", specE32v6, "1", 6, 512),
		pool("new2", specE32v6, "2", 6, 512),
	}
	current := []PoolState{
		poolState("old1", specE32v6, "1", 6, 100, true, 3),
		poolState("old2", specE32v6, "2", 4, 100, true, 2),
		poolState("old3", specE32v6, "3", 2, 100, false, 1),
	}

	first := findNextAction(desired, current, generousBudgets(desired, current), compute.CapacityByRole{}, compute.NetworkConfig{})
	for range 20 {
		again := findNextAction(desired, current, generousBudgets(desired, current), compute.CapacityByRole{}, compute.NetworkConfig{})
		assert.Equal(t, first.poolName(), again.poolName(), "action pool should be deterministic")
		assert.Equal(t, first.kind(), again.kind(), "action type should be deterministic")
	}
}

func TestCompleteTeardownPreservesExisting(t *testing.T) {
	current := []PoolState{
		poolState("a1", specD4v3, "1", 6, 100, true, 3),
		poolState("a2", specD4v3, "2", 6, 100, true, 3),
	}

	action := findNextAction(nil, current, generousBudgets(nil, current), compute.CapacityByRole{}, compute.NetworkConfig{})
	assert.Nil(t, action, "should preserve existing pools when desired is empty")
}

// ---------------------------------------------------------------------------
// Quota-constrained interleave — a tight per-family vCPU budget forces the
// planner to interleave: create a throttled replacement, shrink an undesired
// pool to free budget, then grow the replacement one slot at a time.
// ---------------------------------------------------------------------------

func TestQuotaConstrained_Interleave(t *testing.T) {
	desired := []compute.Pool{
		pool("e32z1", specE32v6, "1", 3, 512),
		pool("e32z2", specE32v6, "2", 3, 512),
		pool("e32z3", specE32v6, "3", 3, 512),
	}
	current := []PoolState{
		poolState("e16az1", specE16v6, "1", 3, 512, true, 1),
		poolState("e16bz1", specE16v6, "1", 3, 512, true, 1),
		poolState("e16az2", specE16v6, "2", 3, 512, true, 1),
		poolState("e16bz2", specE16v6, "2", 3, 512, true, 1),
		poolState("e16az3", specE16v6, "3", 3, 512, true, 1),
		poolState("e16bz3", specE16v6, "3", 3, 512, true, 1),
	}
	budgets := map[compute.VMFamily]int64{specE16v6.Family: 3 * 3 * specE32v6.VCPUs}

	// The E16 pools are midway through expansion from this nine-node baseline.
	baseline := compute.CapacityByRole{compute.PoolRoleSystem: {}, compute.PoolRoleInfra: {}, compute.PoolRoleWorker: {VCPUs: 144, MemoryGiB: 1152, SwiftNICs: 63}}
	tr := simulateAndTrace(t, desired, current, budgets, baseline, true, 60)
	require.NoError(t, tr.RejectedPlan)
	assertConverged(t, desired, tr.finalState())
	compareGolden(t, formatTrace(tr))
}

func TestComputeFamilyHeadroom_ExcludesUnresolvedSKU(t *testing.T) {
	budgets := map[compute.VMFamily]int64{specE16v6.Family: 100}
	current := []PoolState{
		poolState("e16az1", specE16v6, "1", 3, 128, true, 1), // committed-not-running (3-1) * 16 vCPUs = 32 used
		{
			// SKU metadata did not resolve: VCPUs projected as 0, family empty.
			Pool: compute.Pool{
				Name:     "legacy",
				Spec:     compute.VMSpec{Size: "Standard_Legacy_v0", Family: "", VCPUs: 0, MemoryGiB: 0, SecondaryNICs: 0},
				MaxCount: 5,
			},
			AutoScalingEnabled: true,
			Count:              2,
			ProvisioningState:  "Succeeded",
		},
	}

	headroom := computeFamilyHeadroom(budgets, current)

	assert.Equal(t, int64(100-32), headroom[specE16v6.Family], "resolved family headroom must exclude the unresolved pool")
	_, hasEmptyFamily := headroom[""]
	assert.False(t, hasEmptyFamily, "unresolved pool must not create a family \"\" headroom entry")
}

func TestTaintsEqual(t *testing.T) {
	tests := []struct {
		name string
		a    []string
		b    []string
		want bool
	}{
		{"both empty", nil, nil, true},
		{"same order", []string{"x=1:NoSchedule", "y=2:NoSchedule"}, []string{"x=1:NoSchedule", "y=2:NoSchedule"}, true},
		{"different order", []string{"x=1:NoSchedule", "y=2:NoSchedule"}, []string{"y=2:NoSchedule", "x=1:NoSchedule"}, true},
		{"different value", []string{"x=1:NoSchedule"}, []string{"x=2:NoSchedule"}, false},
		{"different length", []string{"x=1:NoSchedule"}, []string{"x=1:NoSchedule", "y=2:NoSchedule"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, taintsEqual(tt.a, tt.b))
		})
	}
}

func TestFindDriftAction(t *testing.T) {
	desiredPool := func(labels map[string]string, taints []string) compute.Pool {
		return compute.Pool{
			Name:              "e16az1",
			Spec:              specE16v6,
			AvailabilityZones: []string{"1"},
			MaxCount:          3,
			OSDiskSizeGB:      128,
			Labels:            labels,
			Taints:            taints,
		}
	}
	currentPool := func(labels map[string]string, taints []string) PoolState {
		return PoolState{
			Pool: compute.Pool{
				Name:              "e16az1",
				Spec:              specE16v6,
				AvailabilityZones: []string{"1"},
				MaxCount:          3,
				OSDiskSizeGB:      128,
				Labels:            labels,
				Taints:            taints,
			},
			AutoScalingEnabled: true,
			Count:              1,
			ProvisioningState:  "Succeeded",
			ETag:               "etag-e16az1",
		}
	}
	roleWorker := map[string]string{compute.RoleLabel: "worker"}

	tests := []struct {
		name    string
		desired compute.Pool
		current PoolState
		want    Action // nil means no drift action expected
	}{
		{
			name:    "identical labels and taints: no action",
			desired: desiredPool(roleWorker, nil),
			current: currentPool(roleWorker, nil),
			want:    nil,
		},
		{
			name:    "label value drift: update config",
			desired: desiredPool(map[string]string{"tier": "gold"}, nil),
			current: currentPool(map[string]string{"tier": "silver"}, nil),
			want:    newUpdateConfigAction(desiredPool(map[string]string{"tier": "gold"}, nil), "etag-e16az1"),
		},
		{
			name:    "extra label on live pool removed: update config",
			desired: desiredPool(roleWorker, nil),
			current: currentPool(map[string]string{compute.RoleLabel: "worker", "stale": "x"}, nil),
			want:    newUpdateConfigAction(desiredPool(roleWorker, nil), "etag-e16az1"),
		},
		{
			name:    "taints reordered only: no action",
			desired: desiredPool(roleWorker, []string{"a=1:NoSchedule", "b=2:NoSchedule"}),
			current: currentPool(roleWorker, []string{"b=2:NoSchedule", "a=1:NoSchedule"}),
			want:    nil,
		},
		{
			name:    "taint changed: update config",
			desired: desiredPool(roleWorker, []string{"a=1:NoSchedule"}),
			current: currentPool(roleWorker, []string{"a=2:NoSchedule"}),
			want:    newUpdateConfigAction(desiredPool(roleWorker, []string{"a=1:NoSchedule"}), "etag-e16az1"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			currentByName := map[string]PoolState{tt.current.Name: tt.current}
			got, ok := findDriftAction([]compute.Pool{tt.desired}, currentByName)
			if tt.want == nil {
				assert.False(t, ok, "expected no drift action")
				return
			}
			assert.True(t, ok, "expected a drift action")
			requireAction(t, tt.want, got)
		})
	}
}

func TestFindDriftAction_PoolNotCurrentIsSkipped(t *testing.T) {
	desired := []compute.Pool{pool("e16az1", specE16v6, "1", 3, 128)}
	_, ok := findDriftAction(desired, map[string]PoolState{})
	assert.False(t, ok, "a desired pool with no matching current pool must not produce a drift action")
}

func TestFindNextAction_InProgressGatesDrift(t *testing.T) {
	desired := []compute.Pool{{
		Name:              "e16az1",
		Spec:              specE16v6,
		AvailabilityZones: []string{"1"},
		MaxCount:          3,
		OSDiskSizeGB:      128,
		Labels:            map[string]string{"tier": "desired"},
	}}
	drifted := func(state string) []PoolState {
		return []PoolState{{
			Pool: compute.Pool{
				Name:              "e16az1",
				Spec:              specE16v6,
				AvailabilityZones: []string{"1"},
				MaxCount:          3,
				OSDiskSizeGB:      128,
				Labels:            map[string]string{"tier": "live"},
			},
			AutoScalingEnabled: true,
			Count:              1,
			ProvisioningState:  state,
			ETag:               "etag-e16az1",
		}}
	}
	budgets := generousBudgets(desired, drifted("Succeeded"))

	t.Run("in-progress pool gates drift", func(t *testing.T) {
		action := findNextAction(desired, drifted(provisioningStateUpdating), budgets, compute.CapacityByRole{}, compute.NetworkConfig{})
		_, isWait := action.(waitAction)
		assert.True(t, isWait, "drift correction must wait while the pool is in progress")
	})

	t.Run("settled pool gets drift correction", func(t *testing.T) {
		action := findNextAction(desired, drifted("Succeeded"), budgets, compute.CapacityByRole{}, compute.NetworkConfig{})
		_, isUpdate := action.(updateConfigAction)
		assert.True(t, isUpdate, "settled drift must produce an update config action")
	})
}

// TestProductionScenario_MigrationToDesired retains the real production snapshot
// as a rejected plan and exercises a synthetic capacity-preserving migration
// from the same initial pools. The synthetic case increases desired ceilings
// and quota; it does not represent the current production profile or quota.
func TestProductionScenario_MigrationToDesired(t *testing.T) {
	tests := []struct {
		name           string
		infraMax       int32
		workerE32Max   int32
		availableEDSv5 int64
		fullyAllocated bool
		wantErr        string
	}{
		{
			name:     "real_snapshot_rejected",
			infraMax: 1, workerE32Max: 5, availableEDSv5: 856,
			wantErr: "infra capacity",
		},
		{
			name: "capacity_preserving_migration",
			// Desired EDSv5 capacity is 1,568 vCPUs. A hypothetical quota of
			// 1,824 leaves 256 for overlap; initial live usage is 584 vCPUs.
			infraMax: 3, workerE32Max: 12, availableEDSv5: 1240, fullyAllocated: true,
		},
		{
			name: "fully_allocated_downsize",
			// A new configuration explicitly targets the smaller pool set.
			infraMax: 1, workerE32Max: 5, availableEDSv5: 856, fullyAllocated: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// pool()/poolState() default to the worker role; withRole/withStateRole
			// stamp the system and infra pools so the baseline checks see the real role mix.
			withRole := func(role compute.PoolRole, p compute.Pool) compute.Pool {
				p.Role = role
				p.Labels = map[string]string{compute.RoleLabel: string(role)}
				return p
			}
			withStateRole := func(role compute.PoolRole, s PoolState) PoolState {
				s.Role = role
				s.Labels = map[string]string{compute.RoleLabel: string(role)}
				return s
			}

			// Names mirror the planner's poolName scheme (prefix s/i/w + zone digit;
			// spanzones system uses zone 0), with a readable SKU suffix in place of the
			// real hash.
			desired := []compute.Pool{
				{
					Role:              compute.PoolRoleSystem,
					Name:              "s0-e8",
					Spec:              specE8dsV5,
					AvailabilityZones: []string{"1", "2", "3"},
					MaxCount:          4,
					OSDiskSizeGB:      128,
					MaxPods:           100,
					Labels:            map[string]string{compute.RoleLabel: string(compute.PoolRoleSystem)},
				},
				// infra caps at 2 zones (production profile PoolCount: 2).
				withRole(compute.PoolRoleInfra, pool("i1-e32", specE32dsV5, "1", test.infraMax, 128)),
				withRole(compute.PoolRoleInfra, pool("i2-e32", specE32dsV5, "2", test.infraMax, 128)),
				pool("w1-e16", specE16dsV5, "1", 4, 256),
				pool("w2-e16", specE16dsV5, "2", 4, 256),
				pool("w3-e16", specE16dsV5, "3", 4, 256),
				pool("w1-e32", specE32dsV5, "1", test.workerE32Max, 512),
				pool("w2-e32", specE32dsV5, "2", test.workerE32Max, 512),
				pool("w3-e32", specE32dsV5, "3", test.workerE32Max, 512),
			}

			current := []PoolState{
				{
					Pool: compute.Pool{
						Role:              compute.PoolRoleSystem,
						Name:              "system",
						Spec:              specE8dsV5,
						AvailabilityZones: []string{"1", "2", "3"},
						MaxCount:          4,
						OSDiskSizeGB:      128,
					},
					AutoScalingEnabled: true,
					Count:              1,
					ProvisioningState:  "Succeeded",
					ETag:               "etag-system",
				},
				withStateRole(compute.PoolRoleInfra, poolState("infra1", specE32dsV4, "1", 3, 32, true, 1)),
				withStateRole(compute.PoolRoleInfra, poolState("infra2", specE32dsV4, "2", 3, 32, true, 1)),
				poolState("userswft1", specE32dsV5, "1", 14, 512, true, 6),
				poolState("userswft2", specE32dsV5, "2", 14, 512, true, 6),
				poolState("userswft3", specE32dsV5, "3", 14, 512, true, 6),
			}

			// Available quota is limit minus live usage in both cases.
			// EDSv4 is absent (not a production-profile family); its undesired pools
			// drain out identity-based, without a budget.
			budgets := map[compute.VMFamily]int64{
				"standardEDSv5Family": test.availableEDSv5,
				"standardESv3Family":  100,
			}

			// Adoption protects the complete initial configuration, including unused autoscaler capacity.
			baseline := compute.CapacityByRole{compute.PoolRoleSystem: {VCPUs: 32, MemoryGiB: 256}, compute.PoolRoleInfra: {VCPUs: 192, MemoryGiB: 1536}, compute.PoolRoleWorker: {VCPUs: 1344, MemoryGiB: 10752, SwiftNICs: 294}}
			tr := simulateAndTrace(t, desired, current, budgets, baseline, test.fullyAllocated, 200)
			if len(test.wantErr) > 0 {
				require.ErrorContains(t, tr.RejectedPlan, test.wantErr)
				require.Empty(t, tr.Steps)
				require.Equal(t, current, tr.finalState())
			} else {
				require.NoError(t, tr.RejectedPlan)
				assertConverged(t, desired, tr.finalState())
				require.NotEmpty(t, tr.Steps)
			}
			compareGolden(t, formatTrace(tr))
		})
	}
}
