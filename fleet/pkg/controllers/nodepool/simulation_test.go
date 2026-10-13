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
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Azure/ARO-HCP/fleet/pkg/compute"
)

func TestSimulationTerminalOutcomes(t *testing.T) {
	desired := []compute.Pool{pool("worker", specE32v6, "1", 2, 512)}
	current := []PoolState{poolState("worker", specE32v6, "1", 2, 512, true, 1)}
	budgets := map[compute.VMFamily]int64{specE32v6.Family: 4 * specE32v6.VCPUs}

	t.Run("converged without an action budget", func(t *testing.T) {
		tr := requireSimulation(t, desired, current, budgets, true, 0)
		require.Equal(t, "converged", tr.Outcome)
		require.Empty(t, tr.Steps)
	})
	t.Run("quota blocked is not convergence", func(t *testing.T) {
		tr := requireSimulation(t, desired, nil, map[compute.VMFamily]int64{specE32v6.Family: 0}, true, 10)
		require.Equal(t, "blocked", tr.Outcome)
		require.Empty(t, tr.Steps)
		require.Empty(t, tr.finalState())
	})
	t.Run("bounded projection", func(t *testing.T) {
		pools := append([]compute.Pool{desired[0]}, pool("other", specE32v6, "2", 2, 512))
		tr := requireSimulation(t, pools, nil, budgets, true, 1)
		require.Equal(t, "step-limit", tr.Outcome)
		require.Len(t, tr.Steps, 1)
		require.Len(t, tr.finalState(), 1)
		require.Equal(t, "worker", tr.finalState()[0].Name)
	})
	t.Run("last permitted action can converge", func(t *testing.T) {
		tr := requireSimulation(t, desired, nil, budgets, true, 1)
		require.Equal(t, "converged", tr.Outcome)
		require.Len(t, tr.Steps, 1)
	})
	t.Run("observed transition precedes floor rejection", func(t *testing.T) {
		observed := []PoolState{current[0]}
		observed[0].MaxCount = 4
		observed[0].ProvisioningState = "Updating"
		tr := requireSimulation(t, desired, observed, budgets, false, 10)
		require.Equal(t, "waiting", tr.Outcome)
		require.NoError(t, tr.RejectedPlan)
		require.Equal(t, observed, tr.finalState())
		observed[0].ProvisioningState = "Succeeded"
		settled := requireSimulation(t, desired, observed, budgets, false, 10)
		require.Equal(t, "rejected", settled.Outcome)
		require.Error(t, settled.RejectedPlan)
	})
	for _, test := range []struct {
		state  string
		action actionType
	}{
		{state: "Updating", action: actionWait},
		{state: "Failed", action: actionReconcile},
	} {
		t.Run(test.state, func(t *testing.T) {
			observed := []PoolState{current[0]}
			observed[0].ProvisioningState = test.state
			tr := requireSimulation(t, desired, observed, budgets, true, 10)
			require.Equal(t, "waiting", tr.Outcome)
			require.Len(t, tr.Steps, 1)
			require.Equal(t, test.action, tr.Steps[0].Action.kind())
			require.Equal(t, observed, tr.finalState(), "projection cannot settle an observed operation or failed reconciliation")
			require.Equal(t, tr.Steps[0].HeadroomBefore, tr.Steps[0].HeadroomAfter)
			tr.Steps[0].Capacity[compute.PoolRoleWorker] = compute.RoleCapacity{}
			require.Equal(t, 2*specE32v6.VCPUs, tr.InitialCapacity[compute.PoolRoleWorker].VCPUs)
		})
	}
}

func TestSimulationCreateMinimumConsumesLiveQuota(t *testing.T) {
	first := pool("first", specE32v6, "1", 4, 512)
	first.MinCount = 3
	desired := []compute.Pool{first, pool("second", specE32v6, "2", 2, 512)}
	family, cores := specE32v6.Family, specE32v6.VCPUs
	budgets := map[compute.VMFamily]int64{family: 6 * cores}
	tr := requireSimulation(t, desired, nil, budgets, true, 2)
	require.Equal(t, "converged", tr.Outcome)
	require.Len(t, tr.Steps, 2)
	require.Equal(t, int32(3), tr.Steps[0].State[0].Count)
	require.Equal(t, int32(3), tr.Steps[0].State[0].MinCount)
	// Running minimum and unconsumed ceiling together reserve all four slots.
	require.Equal(t, 2*cores, tr.Steps[0].HeadroomAfter[family])
	require.Equal(t, int64(0), tr.Steps[1].HeadroomAfter[family])
	require.Equal(t, int32(1), tr.finalState()[1].Count)

	// A quota-clamped create must start at the same clamped minimum it advertises.
	clamped := requireSimulation(t, []compute.Pool{first}, nil, map[compute.VMFamily]int64{family: 2 * cores}, true, 10)
	require.Equal(t, "blocked", clamped.Outcome)
	require.Equal(t, int32(2), clamped.finalState()[0].Count)
	require.Equal(t, int32(2), clamped.finalState()[0].MinCount)
	require.Equal(t, int64(0), clamped.Steps[0].HeadroomAfter[family])
}

func TestSimulationRunningAboveCeilingDoesNotReleaseQuota(t *testing.T) {
	desired := []compute.Pool{
		pool("existing", specD4v3, "1", 2, 32),
		pool("new", specD4v3, "2", 1, 32),
	}
	current := []PoolState{poolState("existing", specD4v3, "1", 2, 32, true, 5)}
	// All 20 vCPUs are still running after an external maximum reduction.
	budgets := map[compute.VMFamily]int64{specD4v3.Family: 0}
	blocked := requireSimulation(t, desired, current, budgets, true, 10)
	require.Equal(t, "blocked", blocked.Outcome)
	require.Empty(t, blocked.Steps, "lowering the ceiling must not fund a new node before the old nodes are removed")

	// A later observation confirms scale-down and the corresponding quota release.
	current[0].Count = 2
	budgets[specD4v3.Family] = 3 * specD4v3.VCPUs
	converged := requireSimulation(t, desired, current, budgets, true, 10)
	require.Equal(t, "converged", converged.Outcome)
	require.Len(t, converged.Steps, 1)
	require.Equal(t, actionCreate, converged.Steps[0].Action.kind())
	require.Equal(t, "new", converged.Steps[0].Action.poolName())
}

func TestSimulationOwnsInputsAndSnapshots(t *testing.T) {
	desired := []compute.Pool{pool("existing", specE32v6, "1", 2, 512), pool("new", specE32v6, "2", 2, 512)}
	desired[0].Labels["configuration"] = "desired"
	desired[0].Taints = []string{"dedicated=worker:NoSchedule"}
	current := []PoolState{poolState("existing", specE32v6, "1", 2, 512, true, 1)}
	current[0].Labels["configuration"] = "old"
	current[0].Taints = []string{"old=true:NoSchedule"}
	budgets := map[compute.VMFamily]int64{specE32v6.Family: 5 * specE32v6.VCPUs}
	encode := func(value any) []byte {
		t.Helper()
		data, err := json.Marshal(value)
		require.NoError(t, err)
		return data
	}
	beforeDesired, beforeCurrent, beforeBudgets := encode(desired), encode(current), encode(budgets)
	tr := requireSimulation(t, desired, current, budgets, true, 10)
	require.Equal(t, "converged", tr.Outcome)
	require.Len(t, tr.Steps, 2)
	require.Equal(t, beforeDesired, encode(desired))
	require.Equal(t, beforeCurrent, encode(current))
	require.Equal(t, beforeBudgets, encode(budgets))
	beforeTrace := formatTrace(tr)
	desired[0].Labels["configuration"] = "external"
	desired[0].Taints[0] = "external"
	desired[0].AvailabilityZones[0] = "3"
	current[0].Labels["configuration"] = "external"
	current[0].Taints[0] = "external"
	current[0].AvailabilityZones[0] = "3"
	budgets[specE32v6.Family] = 0
	require.Equal(t, beforeTrace, formatTrace(tr))
	require.Equal(t, "old", tr.Initial[0].Labels["configuration"])
	require.Equal(t, "old=true:NoSchedule", tr.Initial[0].Taints[0])
	require.Equal(t, "desired", tr.Desired[0].Labels["configuration"])
	require.Equal(t, "dedicated=worker:NoSchedule", tr.Desired[0].Taints[0])
	tr.Steps[0].State[0].Labels["configuration"] = "changed snapshot"
	tr.Steps[0].State[0].Taints[0] = "changed snapshot"
	tr.Steps[0].State[0].AvailabilityZones[0] = "3"
	require.Equal(t, "desired", tr.finalState()[0].Labels["configuration"])
	require.Equal(t, "dedicated=worker:NoSchedule", tr.finalState()[0].Taints[0])
	require.Equal(t, []string{"1"}, tr.finalState()[0].AvailabilityZones)
	update := tr.Steps[0].Action.(updateConfigAction)
	update.Labels["configuration"] = "changed action"
	update.Taints[0] = "changed action"
	require.Equal(t, "desired", tr.Desired[0].Labels["configuration"])
	require.Equal(t, "desired", tr.finalState()[0].Labels["configuration"])
	create := tr.Steps[1].Action.(createAction)
	create.Pool.Labels[compute.RoleLabel] = "changed action"
	create.Pool.AvailabilityZones[0] = "3"
	require.Equal(t, string(compute.PoolRoleWorker), tr.finalState()[1].Labels[compute.RoleLabel])
	require.Equal(t, []string{"2"}, tr.finalState()[1].AvailabilityZones)
}
