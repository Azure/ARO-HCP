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

package swiftmesh

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/go-logr/logr"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/cache"

	"github.com/Azure/ARO-HCP/internal/utils"
)

// ControllerName is the single identifier used for this controller's logger
// field, context value, and metrics subsystem alignment.
const ControllerName = "swift-mesh"

// execOverhead is added to the per-probe curl timeout to bound the exec stream
// itself (connection setup to the API server) without racing curl's own -m.
const execOverhead = 5 * time.Second

// errMeshUnhealthy / errProbeUntrustworthy are sentinels used only to surface a
// mesh verdict at Error level (logr requires an error for Error()).
var (
	errMeshUnhealthy      = errors.New("swift mesh peer connectivity is broken")
	errProbeUntrustworthy = errors.New("swift mesh probe is broken: self/loopback edge unreachable")
)

// Controller periodically sweeps the SWIFT NIC mesh, logging every connection
// edge and exporting per-edge metrics. It is read-only: it mutates nothing in
// the cluster and only observes the data path.
type Controller struct {
	discoverer    Discoverer
	driver        Driver
	hasSynced     []cache.InformerSynced
	interval      time.Duration
	perProbe      time.Duration
	maxConcurrent int
	recorder      *metricsRecorder
}

// NewController builds the controller. probeTimeout is the per-edge curl -m
// value; the sweep interval is how often the full mesh is re-probed.
func NewController(discoverer Discoverer, driver Driver, hasSynced []cache.InformerSynced, interval, probeTimeout time.Duration, maxConcurrent int) *Controller {
	return &Controller{
		discoverer:    discoverer,
		driver:        driver,
		hasSynced:     hasSynced,
		interval:      interval,
		perProbe:      probeTimeout + execOverhead,
		maxConcurrent: maxConcurrent,
		recorder:      newMetricsRecorder(),
	}
}

func (c *Controller) Run(ctx context.Context) error {
	defer utilruntime.HandleCrash()

	ctx = utils.ContextWithControllerName(ctx, ControllerName)
	logger := utils.LoggerFromContext(ctx).WithValues(utils.LogValues{}.AddControllerName(ControllerName)...)
	ctx = utils.ContextWithLogger(ctx, logger)
	logger.Info("Starting controller")

	logger.Info("Waiting for informer caches to sync")
	if ok := cache.WaitForCacheSync(ctx.Done(), c.hasSynced...); !ok {
		return fmt.Errorf("failed to wait for caches to sync")
	}

	logger.Info("Started")
	// UntilWithContext runs the sweep immediately, then every interval, and
	// recovers panics internally, until the leadership context is cancelled.
	wait.UntilWithContext(ctx, c.sweep, c.interval)

	logger.Info("Shutting down")
	return nil
}

func (c *Controller) sweep(ctx context.Context) {
	logger := utils.LoggerFromContext(ctx)
	start := time.Now()

	meshes, err := c.discoverer.Discover(ctx)
	if err != nil {
		sweepErrorsTotal.Inc()
		// Leave the previous sweep's series in place (don't wipe good data on a
		// transient blip); last_successful_sweep_timestamp signals the staleness.
		utilruntime.HandleError(fmt.Errorf("swift mesh discovery: %w", err))
		return
	}

	budget := NewConcurrencyBudget(c.maxConcurrent)
	present := make(map[string]struct{}, len(meshes))

	for _, mesh := range meshes {
		present[mesh.Namespace] = struct{}{}
		rep := RunMesh(ctx, mesh, c.driver, budget, c.perProbe)
		c.recorder.record(rep)
		c.reportMesh(logger, rep)
	}
	// Drop series for HCPs that no longer exist, without disturbing live ones.
	c.recorder.pruneAbsent(present)

	lastSuccessfulSweepTimestamp.SetToCurrentTime()
	sweepDuration.Observe(time.Since(start).Seconds())

	if len(meshes) == 0 {
		logger.V(4).Info("No HCP router meshes discovered")
	}
}

// reportMesh logs one HCP mesh's result. Per-edge detail is V(2) to avoid
// flooding logs at steady state; broken edges are logged at Info so they surface,
// and the mesh verdict is Error when connectivity is broken or the probe itself
// is untrustworthy.
func (c *Controller) reportMesh(logger logr.Logger, rep MeshReport) {
	// Carry the HCP's Azure resource-ID fields (subscription/resource group/
	// resource/cluster name) plus the CP namespace on every line, matching the
	// Kusto index set used across the other controllers.
	meshLogger := logger.
		WithValues(utils.LogValues{}.AddLogValuesForResourceIDString(rep.ResourceID)...).
		WithValues("hcp_namespace", rep.Namespace)

	for _, r := range rep.Results {
		kv := []any{
			"from_pod", r.FromPod,
			"from_ip", r.FromIP,
			"to_ip", r.ToIP,
			"kind", string(r.Kind),
			"status", r.StatusCode,
			"connect_ms", r.ConnectTime.Milliseconds(),
			"timed_out", r.TimedOut,
		}
		if r.OK {
			meshLogger.V(2).Info("swift mesh edge reachable", kv...)
		} else {
			// A broken edge is the actionable signal; log it at Info.
			meshLogger.Info("swift mesh edge unreachable", kv...)
		}
		if r.Err != nil {
			meshLogger.Error(r.Err, "swift mesh edge error", kv...)
		}
	}

	summary := []any{
		"peer_ok", rep.PeerOK,
		"peer_total", rep.PeerTotal,
		"self_ok", rep.SelfOK,
		"self_total", rep.SelfTotal,
		"pass", rep.Pass,
		"fail", rep.Fail,
	}
	switch {
	case !rep.ProbeTrustworthy():
		// Self/loopback failed: this is a broken probe, not a data-path fault.
		// Do NOT raise a connectivity alarm; surface it as a distinct infra error.
		probeInfraFailuresTotal.Inc()
		meshLogger.Error(errProbeUntrustworthy, "swift mesh probe untrustworthy", summary...)
	case !rep.Healthy():
		meshLogger.Error(errMeshUnhealthy, "swift mesh unhealthy", summary...)
	default:
		meshLogger.Info("swift mesh healthy", summary...)
	}
}
