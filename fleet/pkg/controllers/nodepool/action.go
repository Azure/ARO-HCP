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
	"context"
	"fmt"
	"maps"
	"slices"
	"sort"
	"time"

	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v6"

	"github.com/Azure/ARO-HCP/fleet/pkg/azure/agentpoolspec"
	"github.com/Azure/ARO-HCP/fleet/pkg/compute"
)

// Requeue delays after a mutating action, grouped by how long AKS typically
// takes to settle the change.
const (
	requeueAfterCreate         = 2 * time.Minute  // create/reconcile: pool needs time to start provisioning
	requeueAfterMaxCountChange = 30 * time.Second // maxCount/unfreeze/freeze: fast metadata-only updates
	requeueAfterDrainStep      = 1 * time.Minute  // reduce/delete/wait: poll cadence while a pool transitions
)

// beginCreateOrUpdate is the shared AKS agent pool PUT call used by every
// action that mutates a pool.
func beginCreateOrUpdate(
	ctx context.Context,
	execCtx ExecuteContext,
	poolName string,
	pool armcontainerservice.AgentPool,
	opts *armcontainerservice.AgentPoolsClientBeginCreateOrUpdateOptions,
) error {
	_, err := execCtx.Client.BeginCreateOrUpdate(
		ctx,
		execCtx.AKSResourceID.ResourceGroupName,
		execCtx.AKSResourceID.Name,
		poolName,
		pool,
		opts,
	)
	return err
}

// putWithIfMatch issues an optimistic-concurrency PUT (properties-only update,
// guarded by ETag) and wraps the result. It covers every action that mutates
// an existing pool in place; createAction (IfNoneMatch) and deleteAction
// (BeginDelete) have distinct call shapes and are not routed through it.
func putWithIfMatch(
	ctx context.Context,
	execCtx ExecuteContext,
	poolName string,
	properties *armcontainerservice.ManagedClusterAgentPoolProfileProperties,
	etag string,
	verb string,
	delay time.Duration,
) (*time.Duration, error) {
	pool := armcontainerservice.AgentPool{Properties: properties}

	err := beginCreateOrUpdate(ctx, execCtx, poolName, pool, &armcontainerservice.AgentPoolsClientBeginCreateOrUpdateOptions{
		IfMatch: &etag,
	})
	if err != nil {
		return nil, fmt.Errorf("%s pool %s: %w", verb, poolName, err)
	}
	return ptr.To(delay), nil
}

// networkConfigFromPools derives NetworkConfig from the system pool on the
// cluster, so new worker pools inherit the same subnets by construction. The
// system pool always exists at reconcile time (aks-cluster-create creates it
// first, and AKS forbids deleting the last system pool), so the no-system-pool
// fallthrough to an empty NetworkConfig is unreachable in practice. An empty
// result is also legitimate for a cluster whose system pool has no subnets.
func networkConfigFromPools(pools []armcontainerservice.AgentPool) compute.NetworkConfig {
	for _, pool := range pools {
		if pool.Properties == nil || pool.Properties.Mode == nil {
			continue
		}
		if *pool.Properties.Mode != armcontainerservice.AgentPoolModeSystem {
			continue
		}
		config := compute.NetworkConfig{}
		if pool.Properties.VnetSubnetID != nil {
			config.VnetSubnetID = *pool.Properties.VnetSubnetID
		}
		if pool.Properties.PodSubnetID != nil {
			config.PodSubnetID = *pool.Properties.PodSubnetID
		}
		return config
	}
	return compute.NetworkConfig{}
}

// ExecuteContext bundles the dependencies needed to execute an action against
// the AKS ARM API. Each action type reads the fields it needs.
type ExecuteContext struct {
	Client        *armcontainerservice.AgentPoolsClient
	AKSResourceID *azcorearm.ResourceID
}

// actionType identifies a single micro-operation the controller can take.
type actionType string

const (
	actionWait         actionType = "Wait"
	actionReconcile    actionType = "Reconcile"
	actionCreate       actionType = "Create"
	actionSetMaxCount  actionType = "SetMaxCount"
	actionUnfreeze     actionType = "Unfreeze"
	actionFreeze       actionType = "Freeze"
	actionReduce       actionType = "Reduce"
	actionDelete       actionType = "Delete"
	actionUpdateConfig actionType = "UpdateConfig"
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

// Action describes a single operation the controller can take on an AKS agent
// pool. This includes mutations (Create, Delete, ...) and the no-op waitAction
// that signals an in-progress pool is blocking further work.
type Action interface {
	poolName() string
	vmSize() string
	zone() string
	kind() actionType
	execute(ctx context.Context, execCtx ExecuteContext) (*time.Duration, error)
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
	_ Action = setMaxCountAction{}
	_ Action = unfreezeAction{}
	_ Action = freezeAction{}
	_ Action = reduceAction{}
	_ Action = deleteAction{}
	_ Action = updateConfigAction{}
)

// waitAction is a no-op action returned when a zone has an in-progress pool
// that blocks further mutations. It targets the blocking pool for
// observability and returns a requeue delay without making any API calls.
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

func (a waitAction) execute(_ context.Context, _ ExecuteContext) (*time.Duration, error) {
	return &a.Delay, nil
}

// reconcileAction sends a PUT with empty properties to trigger AKS
// reconciliation on a Failed pool. AKS treats a PUT with empty properties as a
// reconcile — it preserves all existing fields (labels, taints, VM size, etc.)
// and only re-runs the provisioning flow.
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

func (a reconcileAction) execute(ctx context.Context, execCtx ExecuteContext) (*time.Duration, error) {
	return putWithIfMatch(ctx, execCtx, a.poolName(), &armcontainerservice.ManagedClusterAgentPoolProfileProperties{}, a.ETag, "reconciling failed", requeueAfterCreate)
}

// createAction creates a new pool with autoscaling enabled.
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

func (a createAction) execute(ctx context.Context, execCtx ExecuteContext) (*time.Duration, error) {
	agentPool := armcontainerservice.AgentPool{
		Properties: agentpoolspec.Build(a.Pool, a.NetworkConfig),
	}

	err := beginCreateOrUpdate(ctx, execCtx, a.poolName(), agentPool, &armcontainerservice.AgentPoolsClientBeginCreateOrUpdateOptions{
		IfNoneMatch: ptr.To("*"),
	})
	if err != nil {
		return nil, fmt.Errorf("creating pool %s: %w", a.poolName(), err)
	}
	return ptr.To(requeueAfterCreate), nil
}

// setMaxCountAction updates the autoscaler ceiling on an existing pool.
type setMaxCountAction struct {
	actionBase
	ETag     string
	MaxCount int32
}

func newSetMaxCountAction(poolName, vmSize, zone, etag string, maxCount int32) setMaxCountAction {
	return setMaxCountAction{
		actionBase: actionBase{name: poolName, size: vmSize, azZone: zone, typ: actionSetMaxCount},
		ETag:       etag,
		MaxCount:   maxCount,
	}
}

func (a setMaxCountAction) execute(ctx context.Context, execCtx ExecuteContext) (*time.Duration, error) {
	return putWithIfMatch(ctx, execCtx, a.poolName(), &armcontainerservice.ManagedClusterAgentPoolProfileProperties{
		MaxCount: ptr.To(a.MaxCount),
	}, a.ETag, "setting maxCount on", requeueAfterMaxCountChange)
}

// unfreezeAction re-enables autoscaling on a pool that was being drained but
// reappeared in desired state.
type unfreezeAction struct {
	actionBase
	ETag     string
	MaxCount int32
}

func newUnfreezeAction(poolName, vmSize, zone, etag string, maxCount int32) unfreezeAction {
	return unfreezeAction{
		actionBase: actionBase{name: poolName, size: vmSize, azZone: zone, typ: actionUnfreeze},
		ETag:       etag,
		MaxCount:   maxCount,
	}
}

func (a unfreezeAction) execute(ctx context.Context, execCtx ExecuteContext) (*time.Duration, error) {
	return putWithIfMatch(ctx, execCtx, a.poolName(), &armcontainerservice.ManagedClusterAgentPoolProfileProperties{
		EnableAutoScaling: ptr.To(true),
		MinCount:          ptr.To[int32](1),
		MaxCount:          ptr.To(a.MaxCount),
	}, a.ETag, "unfreezing", requeueAfterMaxCountChange)
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

func (a freezeAction) execute(ctx context.Context, execCtx ExecuteContext) (*time.Duration, error) {
	return putWithIfMatch(ctx, execCtx, a.poolName(), &armcontainerservice.ManagedClusterAgentPoolProfileProperties{
		EnableAutoScaling: ptr.To(false),
		Count:             ptr.To(a.Count),
	}, a.ETag, "freezing", requeueAfterMaxCountChange)
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

func (a reduceAction) execute(ctx context.Context, execCtx ExecuteContext) (*time.Duration, error) {
	return putWithIfMatch(ctx, execCtx, a.poolName(), &armcontainerservice.ManagedClusterAgentPoolProfileProperties{
		Count: ptr.To(a.Count),
	}, a.ETag, "reducing", requeueAfterDrainStep)
}

// deleteAction removes a frozen pool with zero nodes.
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

func (a deleteAction) execute(ctx context.Context, execCtx ExecuteContext) (*time.Duration, error) {
	_, err := execCtx.Client.BeginDelete(
		ctx,
		execCtx.AKSResourceID.ResourceGroupName,
		execCtx.AKSResourceID.Name,
		a.poolName(),
		&armcontainerservice.AgentPoolsClientBeginDeleteOptions{
			IfMatch: &a.ETag,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("deleting pool %s: %w", a.poolName(), err)
	}
	return ptr.To(requeueAfterDrainStep), nil
}

// updateConfigAction reconciles a matched desired pool's mutable configuration
// (node labels and taints) when its live values have drifted from the desired
// spec. VMSize, OS disk, and zones are immutable in AKS and are never touched
// here. The controller owns NodeLabels/NodeTaints entirely: the payload is a
// full replacement (non-nil empty map/slice to clear), so out-of-band edits are
// corrected back to the desired set.
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

func (a updateConfigAction) execute(ctx context.Context, execCtx ExecuteContext) (*time.Duration, error) {
	labels := make(map[string]*string, len(a.Labels))
	for k, v := range a.Labels {
		labels[k] = ptr.To(v)
	}
	taints := make([]*string, 0, len(a.Taints))
	for _, t := range a.Taints {
		taints = append(taints, ptr.To(t))
	}
	return putWithIfMatch(ctx, execCtx, a.poolName(), &armcontainerservice.ManagedClusterAgentPoolProfileProperties{
		NodeLabels: labels,
		NodeTaints: taints,
	}, a.ETag, "updating config on", requeueAfterMaxCountChange)
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
}

// findNextAction determines the single next operation to advance current state
// toward desired state. It computes per-family vCPU headroom as the budget
// (quota limit minus live usage) minus the committed-but-not-running ceiling of
// current pools. Same-family transitions converge iteratively: grow (step 4)
// runs before shrink (step 5), and each shrink is guarded by the ceiling floor.
//
// Returns nil when converged. Returns waitAction when any pool is in progress.
func findNextAction(desired []compute.Pool, current []PoolState, familyBudgets map[compute.VMFamily]int64, networkConfig compute.NetworkConfig) Action {
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

	headroom := computeFamilyHeadroom(familyBudgets, current)

	// 1. Reconcile desired pools stuck in Failed state.
	if action, ok := findReconcileAction(desired, currentByName); ok {
		return action
	}

	// 2. Correct desired pools that are over-spec'd or need unfreezing.
	if action, ok := findCorrectDesiredAction(desired, currentByName, headroom); ok {
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
	//    Each shrink action is guarded: ceiling after the action must not
	//    drop below the desired ceiling (vCPU per family, NIC global).
	floor := computeCeilingFloor(desired)
	if action, ok := findShrinkAction(current, desiredByName, floor); ok {
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
func findCorrectDesiredAction(desired []compute.Pool, currentByName map[string]PoolState, headroom map[compute.VMFamily]int64) (Action, bool) {
	for _, pool := range desired {
		cur, exists := currentByName[pool.Name]
		if !exists {
			continue
		}

		if !cur.AutoScalingEnabled {
			if cur.Count <= pool.MaxCount {
				ceilingIncrease := int64(pool.MaxCount-cur.Count) * pool.Spec.VCPUs
				if ceilingIncrease <= headroom[pool.Spec.Family] {
					return newUnfreezeAction(pool.Name, pool.Spec.Size, pool.ZoneString(), cur.ETag, pool.MaxCount), true
				}
				continue
			}
			if cur.Count > 0 {
				// Deliberately not floor-gated, unlike the undesired-shrink path.
				// Draining a desired pool toward its own lowered maxCount is what
				// frees per-family budget for a same-family sibling to grow.
				// Gating this against the ceiling floor would deadlock a
				// same-family rebalance when the planner sizes desired total ==
				// budget (zero slack): the pool could not shrink and the sibling
				// could not grow. The brief capacity dip self-heals within a cycle.
				return newReduceAction(pool.Name, pool.Spec.Size, pool.ZoneString(), cur.ETag, cur.Count-1), true
			}
			continue
		}

		if cur.MaxCount > pool.MaxCount {
			if pool.MaxCount >= cur.Count {
				return newSetMaxCountAction(pool.Name, pool.Spec.Size, pool.ZoneString(), cur.ETag, pool.MaxCount), true
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
		return newSetMaxCountAction(pool.Name, pool.Spec.Size, pool.ZoneString(), cur.ETag, cur.MaxCount+int32(gap)), true
	}

	return nil, false
}

// roleFamily keys the vCPU ceiling floor. Capacity is not fungible across
// roles (taints keep system/infra/worker workloads apart) nor across VM
// families within a role (a same-role cross-family migration must be able to
// drain the old family without the new family masking the drop), so the floor
// is tracked per (role, family).
type roleFamily struct {
	role   compute.PoolRole
	family compute.VMFamily
}

// ceilingFloor defines the minimum ceiling the controller must maintain during
// transitions. Any shrink action whose result would drop below this floor is
// refused. Both dimensions must be satisfied: per-(role, family) vCPU ceiling
// and the global Swift NIC ceiling. Only worker pools consume Swift secondary
// NICs that matter here; system/infra NICs are irrelevant even when those pools
// are Swift-enabled.
type ceilingFloor struct {
	vcpus map[roleFamily]int64
	nics  int64
}

func computeCeilingFloor(desired []compute.Pool) ceilingFloor {
	vcpus := make(map[roleFamily]int64)
	var nics int64
	for _, pool := range desired {
		vcpus[roleFamily{pool.Role, pool.Spec.Family}] += int64(pool.MaxCount) * pool.Spec.VCPUs
		if pool.Role == compute.PoolRoleWorker {
			nics += int64(pool.MaxCount) * pool.Spec.SecondaryNICs
		}
	}
	return ceilingFloor{vcpus: vcpus, nics: nics}
}

func (f ceilingFloor) allowsShrink(current []PoolState, pool PoolState, newCeiling int64) bool {
	delta := poolCeiling(pool) - newCeiling
	key := roleFamily{pool.Role, pool.Spec.Family}

	var vcpus int64
	var totalNICs int64
	for _, cur := range current {
		ceiling := poolCeiling(cur)
		if cur.Role == compute.PoolRoleWorker {
			totalNICs += ceiling * cur.Spec.SecondaryNICs
		}
		if cur.Role == pool.Role && cur.Spec.Family == pool.Spec.Family {
			vcpus += ceiling * cur.Spec.VCPUs
		}
	}

	vcpusAfter := vcpus - delta*pool.Spec.VCPUs
	totalNICsAfter := totalNICs
	if pool.Role == compute.PoolRoleWorker {
		totalNICsAfter -= delta * pool.Spec.SecondaryNICs
	}

	return vcpusAfter >= f.vcpus[key] && totalNICsAfter >= f.nics
}

func findShrinkAction(current []PoolState, desiredByName map[string]compute.Pool, floor ceilingFloor) (Action, bool) {
	undesired := undesiredPools(current, desiredByName)
	if len(undesired) == 0 {
		return nil, false
	}

	// Squeeze: drop maxCount to count on autoscaled undesired pools where
	// max > count. Frees reserved-but-unused ceiling without evicting nodes, so
	// unlike the drain step below it is deliberately not floor-gated: gating it
	// would deadlock a zero-slack same-family replace (the desired sibling can't
	// grow because headroom is 0, and the undesired pool can't release its
	// unused ceiling because the floor guard blocks the squeeze). The squeeze
	// removes no running capacity; the brief ceiling dip self-heals as the
	// desired sibling grows into the freed budget within the same cycle.
	for _, cur := range undesired {
		if cur.AutoScalingEnabled && cur.MaxCount > cur.Count {
			return newSetMaxCountAction(cur.Name, cur.Spec.Size, cur.ZoneString(), cur.ETag, cur.Count), true
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
		if !cur.AutoScalingEnabled && cur.Count > 0 {
			if !floor.allowsShrink(current, cur, int64(cur.Count-1)) {
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

func computeFamilyHeadroom(familyBudgets map[compute.VMFamily]int64, current []PoolState) map[compute.VMFamily]int64 {
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

	headroom := make(map[compute.VMFamily]int64, len(familyBudgets)+len(used))
	for family, budget := range familyBudgets {
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
