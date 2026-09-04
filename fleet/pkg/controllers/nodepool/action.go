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
	"time"

	"github.com/go-logr/logr"

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

// Action describes a single operation the controller can take on an AKS agent
// pool. This includes mutations (Create, Delete, ...) and the no-op waitAction
// that signals an in-progress pool is blocking further work.
type Action interface {
	poolName() string
	vmSize() string
	zone() string
	kind() actionType
	execute(ctx context.Context, execCtx ExecuteContext) (*time.Duration, error)
	AddLoggerValues(logger logr.Logger) logr.Logger
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

func (b actionBase) AddLoggerValues(logger logr.Logger) logr.Logger {
	return logger.WithValues(
		"actionType", string(b.typ),
		"actionPool", b.name,
		"actionVMSize", b.size,
		"actionZone", b.azZone,
	)
}

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

func (a waitAction) AddLoggerValues(logger logr.Logger) logr.Logger {
	return a.actionBase.AddLoggerValues(logger).WithValues("waitDelay", a.Delay)
}

// reconcileAction sends a PUT with empty properties to trigger AKS
// reconciliation on a Failed pool. AKS treats a PUT with empty properties as a
// reconcile — it preserves all existing fields (labels, taints, VM size, etc.)
// and only re-runs the provisioning flow.
// This is verified AKS agent-pool behavior, not an ARM full-replace: empty properties do not clobber the pool spec.
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

func (a createAction) AddLoggerValues(logger logr.Logger) logr.Logger {
	return a.actionBase.AddLoggerValues(logger).WithValues("targetMaxCount", a.Pool.MaxCount)
}

// setScalingBoundsAction updates the autoscaler bounds (min and max) on an
// existing pool. It always writes both: lowering the ceiling below the pool's
// live floor would make min > max, which AKS rejects, so MinCount is co-written
// to keep the PUT valid. The controller does not own the floor — callers pass
// min(cur.MinCount, targetMax) so an existing floor is preserved and only
// lowered when the new ceiling forces it.
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

func (a setScalingBoundsAction) execute(ctx context.Context, execCtx ExecuteContext) (*time.Duration, error) {
	return putWithIfMatch(ctx, execCtx, a.poolName(), &armcontainerservice.ManagedClusterAgentPoolProfileProperties{
		MinCount: ptr.To(a.MinCount),
		MaxCount: ptr.To(a.MaxCount),
	}, a.ETag, "setting scaling bounds on", requeueAfterMaxCountChange)
}

func (a setScalingBoundsAction) AddLoggerValues(logger logr.Logger) logr.Logger {
	return a.actionBase.AddLoggerValues(logger).WithValues("targetMinCount", a.MinCount, "targetMaxCount", a.MaxCount)
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

func (a unfreezeAction) execute(ctx context.Context, execCtx ExecuteContext) (*time.Duration, error) {
	return putWithIfMatch(ctx, execCtx, a.poolName(), &armcontainerservice.ManagedClusterAgentPoolProfileProperties{
		EnableAutoScaling: ptr.To(true),
		MinCount:          ptr.To(a.MinCount),
		MaxCount:          ptr.To(a.MaxCount),
	}, a.ETag, "unfreezing", requeueAfterMaxCountChange)
}

func (a unfreezeAction) AddLoggerValues(logger logr.Logger) logr.Logger {
	return a.actionBase.AddLoggerValues(logger).WithValues("targetMinCount", a.MinCount, "targetMaxCount", a.MaxCount)
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

func (a freezeAction) AddLoggerValues(logger logr.Logger) logr.Logger {
	return a.actionBase.AddLoggerValues(logger).WithValues("frozenCount", a.Count)
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

func (a reduceAction) AddLoggerValues(logger logr.Logger) logr.Logger {
	return a.actionBase.AddLoggerValues(logger).WithValues("targetCount", a.Count)
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

func (a updateConfigAction) AddLoggerValues(logger logr.Logger) logr.Logger {
	return a.actionBase.AddLoggerValues(logger).WithValues("targetLabels", a.Labels, "targetTaints", a.Taints)
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
	// MinCount is the pool's live autoscaler floor. The controller does not own
	// it (another controller may adjust it); it is read only to keep min <= max
	// when a scaling-bounds PUT lowers the ceiling.
	MinCount int32 `json:"minCount"`
}
