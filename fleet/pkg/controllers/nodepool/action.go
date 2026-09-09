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
	"time"

	"github.com/Azure/ARO-HCP/fleet/pkg/compute"
)

// Advisory poll cadence while an observed pool is transitioning.
const requeueAfterDrainStep = time.Minute

// actionType identifies a single proposed micro-operation.
type actionType string

const (
	actionWait             actionType = "Wait"
	actionReconcile        actionType = "Reconcile"
	actionCreate           actionType = "Create"
	actionSetScalingBounds actionType = "SetScalingBounds"
	actionUnfreeze         actionType = "Unfreeze"
	actionFreeze           actionType = "Freeze"
	actionReduce           actionType = "Reduce"
	actionDelete           actionType = "Delete"
	actionUpdateConfig     actionType = "UpdateConfig"
)

// AKS agent pool provisioning states relevant to action selection.
const (
	provisioningStateFailed    = "Failed"
	provisioningStateCreating  = "Creating"
	provisioningStateUpdating  = "Updating"
	provisioningStateScaling   = "Scaling"
	provisioningStateDeleting  = "Deleting"
	provisioningStateUpgrading = "Upgrading"
	provisioningStateMigrating = "Migrating"
)

// Action describes an advisory operation on an AKS agent pool. Descriptors have
// no execution path; only the shadow simulator applies their projected effects.
type Action interface {
	poolName() string
	vmSize() string
	zone() string
	kind() actionType
}

// actionBase holds fields common to all action types.
type actionBase struct {
	name   string
	size   string
	azZone string
	typ    actionType
}

func (b actionBase) poolName() string { return b.name }
func (b actionBase) vmSize() string   { return b.size }
func (b actionBase) zone() string     { return b.azZone }
func (b actionBase) kind() actionType { return b.typ }

var (
	_ Action = waitAction{}
	_ Action = reconcileAction{}
	_ Action = createAction{}
	_ Action = setScalingBoundsAction{}
	_ Action = unfreezeAction{}
	_ Action = freezeAction{}
	_ Action = reduceAction{}
	_ Action = deleteAction{}
	_ Action = updateConfigAction{}
)

// waitAction identifies an observed in-progress pool blocking further planning.
type waitAction struct {
	actionBase
	Delay time.Duration
}

func newWaitAction(poolName, vmSize, zone string, delay time.Duration) waitAction {
	return waitAction{
		actionBase: actionBase{name: poolName, size: vmSize, azZone: zone, typ: actionWait},
		Delay:      delay,
	}
}

// reconcileAction proposes reconciling a failed pool. Its outcome cannot be
// projected: the simulator records this proposal and waits for a new observation.
type reconcileAction struct {
	actionBase
	ETag string
}

func newReconcileAction(poolName, vmSize, zone, etag string) reconcileAction {
	return reconcileAction{
		actionBase: actionBase{name: poolName, size: vmSize, azZone: zone, typ: actionReconcile},
		ETag:       etag,
	}
}

// createAction proposes a new pool with autoscaling enabled.
type createAction struct {
	actionBase
	Pool          compute.Pool
	NetworkConfig compute.NetworkConfig
}

func newCreateAction(pool compute.Pool, networkConfig compute.NetworkConfig) createAction {
	return createAction{
		actionBase:    actionBase{name: pool.Name, size: pool.Spec.Size, azZone: pool.ZoneString(), typ: actionCreate},
		Pool:          pool,
		NetworkConfig: networkConfig,
	}
}

// setScalingBoundsAction proposes autoscaler bounds, preserving the live floor
// unless the new ceiling requires lowering it to keep min <= max.
type setScalingBoundsAction struct {
	actionBase
	ETag     string
	MinCount int32
	MaxCount int32
}

func newSetScalingBoundsAction(poolName, vmSize, zone, etag string, minCount, maxCount int32) setScalingBoundsAction {
	return setScalingBoundsAction{
		actionBase: actionBase{name: poolName, size: vmSize, azZone: zone, typ: actionSetScalingBounds},
		ETag:       etag,
		MinCount:   minCount,
		MaxCount:   maxCount,
	}
}

// unfreezeAction re-enables autoscaling on a pool that was being drained but
// reappeared in desired state.
type unfreezeAction struct {
	actionBase
	ETag     string
	MinCount int32
	MaxCount int32
}

func newUnfreezeAction(poolName, vmSize, zone, etag string, minCount, maxCount int32) unfreezeAction {
	return unfreezeAction{
		actionBase: actionBase{name: poolName, size: vmSize, azZone: zone, typ: actionUnfreeze},
		ETag:       etag,
		MinCount:   minCount,
		MaxCount:   maxCount,
	}
}

// freezeAction disables autoscaling on an undesired pool, pinning it at its
// current node count so it can be drained safely.
type freezeAction struct {
	actionBase
	ETag  string
	Count int32
}

func newFreezeAction(poolName, vmSize, zone, etag string, count int32) freezeAction {
	return freezeAction{
		actionBase: actionBase{name: poolName, size: vmSize, azZone: zone, typ: actionFreeze},
		ETag:       etag,
		Count:      count,
	}
}

// reduceAction decrements the node count by one on a frozen pool.
type reduceAction struct {
	actionBase
	ETag  string
	Count int32
}

func newReduceAction(poolName, vmSize, zone, etag string, count int32) reduceAction {
	return reduceAction{
		actionBase: actionBase{name: poolName, size: vmSize, azZone: zone, typ: actionReduce},
		ETag:       etag,
		Count:      count,
	}
}

// deleteAction proposes removing a pool after the planner's capacity checks.
type deleteAction struct {
	actionBase
	ETag string
}

func newDeleteAction(poolName, vmSize, zone, etag string) deleteAction {
	return deleteAction{
		actionBase: actionBase{name: poolName, size: vmSize, azZone: zone, typ: actionDelete},
		ETag:       etag,
	}
}

// updateConfigAction proposes replacing a matched pool's mutable labels and
// taints with the desired configuration. Immutable fields remain unchanged.
type updateConfigAction struct {
	actionBase
	ETag   string
	Labels map[string]string
	Taints []string
}

func newUpdateConfigAction(pool compute.Pool, etag string) updateConfigAction {
	return updateConfigAction{
		actionBase: actionBase{name: pool.Name, size: pool.Spec.Size, azZone: pool.ZoneString(), typ: actionUpdateConfig},
		ETag:       etag,
		Labels:     pool.Labels,
		Taints:     pool.Taints,
	}
}

// PoolState is the observed state of a single AKS worker pool, projected from
// the AKS agent pool API response. It carries both the spec fields (for
// comparison with desired state) and operational fields (for action selection).
type PoolState struct {
	compute.Pool
	ETag               string `json:"etag"`
	ProvisioningState  string `json:"provisioningState"`
	AutoScalingEnabled bool   `json:"autoScalingEnabled"`
	Count              int32  `json:"count"`
	// MinCount is the observed floor, kept separate from the embedded desired
	// specification so planning preserves another controller's live setting.
	MinCount int32 `json:"minCount"`
}
