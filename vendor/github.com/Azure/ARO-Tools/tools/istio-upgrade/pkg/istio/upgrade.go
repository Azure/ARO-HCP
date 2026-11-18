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

package istio

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/go-logr/logr"
)

var (
	ErrRetireRevisionWouldOrphanWorkloads = errors.New("retiring revision would orphan workloads: stale sidecar pods remain after restart retries")
	ErrControlPlaneUnhealthy              = errors.New("control plane unhealthy: one or more istiod pods are not ready")
	ErrIngressUnhealthy                   = errors.New("ingress gateway unhealthy")
)

func healthCheckError(phase string, health *CheckResult) error {
	var sentinels []error
	if health.CPUnhealthy {
		sentinels = append(sentinels, ErrControlPlaneUnhealthy)
	}
	if health.GWUnhealthy {
		sentinels = append(sentinels, ErrIngressUnhealthy)
	}
	if len(sentinels) == 0 {
		return fmt.Errorf("%s health check failed: %v", phase, health.Issues)
	}
	return fmt.Errorf("%s health check failed: %w: %v", phase, errors.Join(sentinels...), health.Issues)
}

var RevisionPattern = regexp.MustCompile(`^asm-\d+-\d+$`)

type StopAfter string

const (
	StopAfterCanaryStart StopAfter = "canary-start"
	StopAfterOrphanCheck StopAfter = "orphan-check"
)

func ValidateStopAfter(raw string) (StopAfter, error) {
	switch StopAfter(raw) {
	case StopAfterCanaryStart, StopAfterOrphanCheck:
		return StopAfter(raw), nil
	default:
		return "", fmt.Errorf("--stop-after must be one of: %s, %s", StopAfterCanaryStart, StopAfterOrphanCheck)
	}
}

type UpgradeOptions struct {
	ResourceGroup       string
	ClusterName         string
	Versions            string
	Tag                 string
	IngressIPName       string
	RegionRG            string
	DryRun              bool
	StopAfter           StopAfter
	RolloutTimeout      time.Duration
	RolloutPollInterval time.Duration
	OverallTimeout      time.Duration
	MaxOrphanRetries    int
}

func DefaultUpgradeOptions() UpgradeOptions {
	return UpgradeOptions{
		RolloutTimeout:      15 * time.Minute,
		RolloutPollInterval: 10 * time.Second,
		// EV2 shell step caps execution at PT1H; keep at or below 60m for graceful shutdown.
		OverallTimeout:   60 * time.Minute,
		MaxOrphanRetries: 3,
	}
}

func RunUpgrade(ctx context.Context, opts UpgradeOptions, aksClient AKSClusterClient, kubeClient *KubeClient) error {
	if opts.OverallTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.OverallTimeout)
		defer cancel()
	}

	logger := logr.FromContextOrDiscard(ctx).WithName("istio-upgrade").WithValues(
		"cluster", opts.ClusterName,
		"versions", opts.Versions,
	)
	ctx = logr.NewContext(ctx, logger)

	target := strings.TrimSpace(opts.Versions)
	if target == "" {
		return fmt.Errorf("no versions specified in config")
	}
	if !RevisionPattern.MatchString(target) {
		return fmt.Errorf("invalid target version %q: must match %s", target, RevisionPattern.String())
	}

	clusterInfo, meshProfile, err := aksClient.GetClusterState(ctx, opts.ResourceGroup, opts.ClusterName)
	if err != nil {
		return fmt.Errorf("failed to get cluster state: %w", err)
	}
	upgradeInfo, err := aksClient.GetMeshUpgradeTargets(ctx, opts.ResourceGroup, opts.ClusterName)
	if err != nil {
		return fmt.Errorf("failed to get upgrade targets: %w", err)
	}

	logger.Info("Istio upgrade -- cluster state",
		"k8sVersion", clusterInfo.KubernetesVersion,
		"provisioningState", clusterInfo.ProvisioningState,
		"installedRevisions", meshProfile.Revisions,
		"availableUpgrades", upgradeInfo.AvailableUpgrades,
		"upgradeInProgress", upgradeInfo.UpgradeInProgress,
	)

	state := UpgradeState{
		ClusterName:            opts.ClusterName,
		MeshProfileRevisions:   meshProfile.Revisions,
		IstioAvailableUpgrades: upgradeInfo.AvailableUpgrades,
		KubernetesVersion:      clusterInfo.KubernetesVersion,
		ProvisioningState:      clusterInfo.ProvisioningState,
		IstioUpgradeInProgress: upgradeInfo.UpgradeInProgress,
	}

	action := Decide(logger, state, target)

	logMeshState(ctx, kubeClient, logger)

	if opts.DryRun {
		logger.Info("Istio upgrade -- [DRY-RUN] would execute", "action", action, "target", target)
		return nil
	}

	switch action {
	case ActionSkip:
		// True no-op — cluster not ready, target unavailable, or too many
		// revisions. Decide() logged the reason. Safe for EV2 retry.
		return nil

	case ActionReconcile:
		// Reconcile resource drift — ensures ConfigMap, tag webhook, and
		// ingress annotations are correct. When installed > target (AKS
		// default moved ahead of config), reconcile against the installed
		// revision so tags and configmaps match what's actually running.
		reconcileTarget := target
		if highest := slices.MaxFunc(meshProfile.Revisions, compareRevisions); highest != "" && compareRevisions(highest, target) > 0 {
			reconcileTarget = highest
		}
		return runReconcile(ctx, logger, aksClient, kubeClient, opts, reconcileTarget, meshProfile.Revisions)

	case ActionInstall:
		// No mesh installed. Enables mesh via ARM, creates the MISE ext-authz
		// ConfigMap, verifies CP readiness, flips the tag webhook to point at
		// the new istiod, and pins ingress gateway annotations.
		return runInitialInstall(ctx, logger, aksClient, kubeClient, opts, target)

	case ActionResume:
		// Mid-canary detected (target + old both installed). Re-enters the
		// post-install flow: verify CP, flip tag, restart workloads, wait for
		// rollout, health check, orphan guard, ARM complete, cleanup old
		// ConfigMap, final verification. Auto-rollback on failure keeps
		// workloads on the old sidecar between EV2 retries.
		return runCanaryPostInstall(ctx, logger, aksClient, kubeClient, opts, target, meshProfile.Revisions)

	case ActionUpgrade:
		// Single revision installed, target is available. Starts a canary via
		// ARM (installs target alongside current), then runs the full
		// post-install flow (same as ActionResume).
		return runCanaryUpgrade(ctx, logger, aksClient, kubeClient, opts, target, meshProfile.Revisions)

	case ActionCleanupAndUpgrade:
		// Two revisions from a prior failed canary, neither matches the new
		// target. Consolidates workloads onto the older stable revision,
		// ARM-completes to remove the stale one, then starts a fresh canary
		// to the target. Allows operators to skip a bad version by pointing
		// config at a newer one without manual cleanup.
		return runCleanupAndUpgrade(ctx, logger, aksClient, kubeClient, opts, target, meshProfile.Revisions)

	default:
		return fmt.Errorf("unhandled action %q", action)
	}
}

func runReconcile(ctx context.Context, logger logr.Logger, aksClient AKSClusterClient, kubeClient *KubeClient, opts UpgradeOptions, target string, currentRevisions []string) error {
	logger.Info("Reconciling expected resource state (no upgrade needed)", "target", target)
	if !slices.Contains(currentRevisions, target) {
		logger.Info("Installed revision does not match config target",
			"installed", currentRevisions,
			"expected", target,
		)
		return nil
	}
	if err := CreateRevisionConfigMap(ctx, kubeClient, target); err != nil {
		logger.Error(err, "Failed to ensure ConfigMap on reconcile (non-fatal)")
	}
	if opts.Tag != "" {
		if err := EnsureRevisionTag(ctx, kubeClient, opts.Tag, target); err != nil {
			logger.Error(err, "Failed to ensure tag webhook on reconcile (non-fatal)")
		}
	}
	if err := ensureIngress(ctx, kubeClient, opts); err != nil {
		logger.Error(err, "Failed to ensure ingress on reconcile (non-fatal)")
	}
	reconcileOrphanedGatewayLeases(ctx, logger, aksClient, kubeClient, opts, target)
	return nil
}

func runInitialInstall(ctx context.Context, logger logr.Logger, aksClient AKSClusterClient, kubeClient *KubeClient, opts UpgradeOptions, target string) error {
	// Step 1: ARM enable — installs istiod and ingress gateway for the target revision
	logger.Info("Step 1/5: Enabling mesh via ARM", "revision", target)
	if err := aksClient.EnableMesh(ctx, opts.ResourceGroup, opts.ClusterName, target); err != nil {
		return fmt.Errorf("failed to enable mesh: %w", err)
	}

	// Step 2: MISE ext-authz ConfigMap — must exist before any workloads start sending traffic
	logger.Info("Step 2/5: Creating MISE ext-authz ConfigMap")
	if err := CreateRevisionConfigMap(ctx, kubeClient, target); err != nil {
		return fmt.Errorf("failed to create ConfigMap: %w", err)
	}

	// Step 3: verify CP is ready and flip the tag webhook to point at new istiod
	logger.Info("Step 3/5: Verifying control plane and flipping tag webhook")
	if err := verifyControlPlaneAndTag(ctx, kubeClient, opts.Tag, target); err != nil {
		return err
	}

	// Step 4: pin ingress gateway to the static PIP so external traffic routes correctly
	logger.Info("Step 4/5: Ensuring ingress gateway annotations")
	if err := ensureIngress(ctx, kubeClient, opts); err != nil {
		return err
	}

	// Step 5: verify CP readiness and ingress gateway health (external IP, healthy pods)
	logger.Info("Step 5/5: Running post-install health check")
	health, err := HealthCheck(ctx, kubeClient)
	if err != nil {
		return fmt.Errorf("failed to run post-install health check: %w", err)
	}
	if !health.Passed {
		return healthCheckError("initial-install", health)
	}

	namespaces, nsErr := kubeClient.GetMeshNamespaces(ctx)
	if nsErr == nil && len(namespaces) > 0 {
		logger.Info("WARNING: mesh namespaces already exist during initial install -- pods will not have sidecars until next upgrade cycle",
			"namespaces", len(namespaces))
	}

	logger.Info("Initial Istio install complete", "revision", target)
	return nil
}

func runCanaryUpgrade(ctx context.Context, logger logr.Logger, aksClient AKSClusterClient, kubeClient *KubeClient, opts UpgradeOptions, target string, currentRevisions []string) error {
	// ARM start-canary installs the target revision alongside the current one.
	// After this, the cluster has two control planes. No workloads move yet.
	logger.Info("Starting canary -- installing target alongside current")
	if err := aksClient.StartCanaryUpgrade(ctx, opts.ResourceGroup, opts.ClusterName, target); err != nil {
		return fmt.Errorf("failed to start canary: %w", err)
	}

	// --stop-after=canary-start halts here for staged rollouts or manual inspection
	if opts.StopAfter == StopAfterCanaryStart {
		logger.Info("Stopping after canary start as requested -- cluster has two revisions, re-run to resume")
		return nil
	}

	// Hand off to the post-install flow (same path ActionResume takes)
	return runCanaryPostInstall(ctx, logger, aksClient, kubeClient, opts, target, currentRevisions)
}

// runCleanupAndUpgrade handles stale canary recovery. Primary use case: a mid-canary
// version is buggy and the operator wants to skip it by pointing config at a newer
// version (e.g. cluster has asm-1-28 + asm-1-29, config changes to asm-1-30).
//
// Stale revision = highest (the failed canary target). Old stable = lowest (last
// known-good). AKS supports n+2 version skips, so operators can skip a bad version
// entirely by changing svc.istio.versions — no flags or manual cleanup needed.
func runCleanupAndUpgrade(ctx context.Context, logger logr.Logger, aksClient AKSClusterClient, kubeClient *KubeClient, opts UpgradeOptions, target string, revisions []string) error {
	// Identify which revision to keep (older/stable) and which to remove (stale canary).
	// The stale revision is the highest — it was the failed canary target.
	staleRevision := slices.MaxFunc(revisions, compareRevisions)
	oldRevision := stableRevisionFrom(revisions, staleRevision)
	if oldRevision == "" {
		return fmt.Errorf("cannot determine old revision to keep from %v", revisions)
	}

	logger.Info("Cleaning up stale canary before upgrading", "keeping", oldRevision, "removing", staleRevision, "target", target)

	// Phase 1: Consolidate everything onto the old stable revision.
	// This is safe because the old revision was the last known-good state.
	// No auto-rollback here — there's nothing further to fall back to.
	logger.Info("Phase 1/3: Consolidating workloads to old stable revision", "keeping", oldRevision, "removing", staleRevision)

	// Step 1a: ensure MISE ext-authz ConfigMap exists for the old revision
	if err := CreateRevisionConfigMap(ctx, kubeClient, oldRevision); err != nil {
		return fmt.Errorf("failed to ensure ConfigMap for old revision: %w", err)
	}

	// Step 1b: verify old CP is healthy and flip tag webhook back to old istiod
	if err := verifyControlPlaneAndTag(ctx, kubeClient, opts.Tag, oldRevision); err != nil {
		return fmt.Errorf("old revision control plane unhealthy during cleanup: %w", err)
	}

	// Step 1c: pin ingress
	if err := ensureIngress(ctx, kubeClient, opts); err != nil {
		return fmt.Errorf("failed to ensure ingress during cleanup: %w", err)
	}

	// Step 1d: restart workloads to move all pods back to old sidecars
	if err := migrateWorkloads(ctx, kubeClient, opts, oldRevision); err != nil {
		return fmt.Errorf("cleanup workload migration failed: %w", err)
	}

	// Step 1e: health check before ARM complete — must pass before we remove the stale CP
	health, err := HealthCheck(ctx, kubeClient)
	if err != nil {
		return fmt.Errorf("cleanup health check failed: %w", err)
	}
	if !health.Passed {
		return healthCheckError("cleanup", health)
	}

	// Step 1f: orphan guard — verify no pods are still running stale sidecars before
	// removing the stale CP. Uses warn-and-continue on exhaustion because there is no
	// rollback target in the cleanup path.
	logger.Info("Checking for orphaned workloads before removing stale revision", "target", oldRevision, "retiring", staleRevision)
	if err := warnRetireOrphanedWorkloads(ctx, logger, kubeClient, oldRevision, []string{staleRevision}, opts); err != nil {
		return fmt.Errorf("cleanup orphan guard failed: %w", err)
	}

	// Phase 2: Remove the stale revision via ARM and clean up its resources.
	logger.Info("Phase 2/3: Removing stale revision via ARM", "staleRevision", staleRevision)

	// ARM complete — keeps old stable, removes stale canary. If AKS has deprecated
	// the old revision (removed from available list), this PUT may be rejected even
	// though the CP is still running. AKS distinguishes "installable" from "running"
	// and historically allows keeping deprecated versions, but this is not guaranteed.
	// If rejected, the cluster stays in a 2-revision state requiring manual intervention.
	if err := aksClient.CompleteCanaryUpgrade(ctx, opts.ResourceGroup, opts.ClusterName, oldRevision); err != nil {
		return fmt.Errorf("cleanup ARM completion failed: %w", err)
	}

	// Step 2b: delete stale revision's ConfigMap (best-effort)
	if err := DeleteRevisionConfigMap(ctx, kubeClient, staleRevision); err != nil {
		logger.Info("Failed to delete stale ConfigMap (non-fatal)", "revision", staleRevision, "error", err)
	}

	// Step 2c: verify we're back to a clean single-revision state
	verification, err := VerifyUpgrade(ctx, kubeClient, oldRevision, opts.Tag)
	if err != nil {
		return fmt.Errorf("cleanup verification failed: %w", err)
	}
	if !verification.Passed {
		return fmt.Errorf("cleanup verification failed: %v", verification.Issues)
	}

	reconcileOrphanedGatewayLeases(ctx, logger, aksClient, kubeClient, opts, oldRevision)

	// Phase 3: Start a fresh canary from old to target — this runs the full
	// post-install flow with health checks, orphan guard, and auto-rollback.
	logger.Info("Phase 3/3: Starting fresh canary to target", "from", oldRevision, "to", target)
	return runCanaryUpgrade(ctx, logger, aksClient, kubeClient, opts, target, []string{oldRevision})
}

// rollbackAndReturn is the auto-rollback safety net during canary upgrades. Works
// only while both CPs coexist (before CompleteCanaryUpgrade). Flips the tag webhook
// back to the old istiod and restarts workloads to re-inject old sidecars. Both CPs
// stay installed so the next EV2 retry enters ActionResume and re-attempts.
func rollbackAndReturn(ctx context.Context, logger logr.Logger, kubeClient *KubeClient, opts UpgradeOptions, previousRevisions []string, target string, originalErr error) error {
	oldRevision := oldRevisionFrom(previousRevisions, target)
	if oldRevision != "" {
		logger.Info("Rolling back workloads to previous revision before returning error", "old", oldRevision)
		if rbErr := rollbackWorkloads(ctx, kubeClient, opts, oldRevision); rbErr != nil {
			return errors.Join(originalErr, fmt.Errorf("workload rollback also failed: %w", rbErr))
		}
		logger.Info("Workloads rolled back -- cluster still has two control planes, next run will retry via ActionResume")
	}
	return originalErr
}

func rollbackWorkloads(ctx context.Context, kubeClient *KubeClient, opts UpgradeOptions, oldRevision string) error {
	return migrateWorkloads(ctx, kubeClient, opts, oldRevision)
}

func pickRevision(revisions []string, exclude string, cmp func(a, b string) int) string {
	var candidates []string
	for _, rev := range revisions {
		if rev != exclude {
			candidates = append(candidates, rev)
		}
	}
	if len(candidates) == 0 {
		return ""
	}
	return slices.MaxFunc(candidates, cmp)
}

func oldRevisionFrom(revisions []string, target string) string {
	return pickRevision(revisions, target, compareRevisions)
}

func stableRevisionFrom(revisions []string, exclude string) string {
	return pickRevision(revisions, exclude, func(a, b string) int {
		return compareRevisions(b, a)
	})
}

// runCanaryPostInstall is the core post-install sequence shared by ActionUpgrade and
// ActionResume. Every step from here through ARM completion has auto-rollback: on
// failure, workloads are flipped back to the old sidecar so the cluster stays healthy
// between EV2 retries. AKS does not support rollback after CompleteCanaryUpgrade,
// which is why all health checks run before that step.
//
// Steps: ConfigMap → tag safety check → CP verify + tag flip → ingress → workload
// migration → health check → orphan guard → ARM complete (point of no return) →
// cleanup old ConfigMap + final verification.
func runCanaryPostInstall(ctx context.Context, logger logr.Logger, aksClient AKSClusterClient, kubeClient *KubeClient, opts UpgradeOptions, target string, previousRevisions []string) error {
	logger.Info("Step 1/9: Ensuring MISE ext-authz ConfigMap")
	if err := CreateRevisionConfigMap(ctx, kubeClient, target); err != nil {
		return fmt.Errorf("failed to ensure ConfigMap on resume: %w", err)
	}

	// Safety check: if namespaces use tag-based labels (e.g. prod-stable) but no
	// tag is configured, fail early — without a tag the webhook can't be flipped
	logger.Info("Step 2/9: Checking tag-based namespace safety")
	if opts.Tag == "" {
		hasTaggedNamespaces, err := hasTagBasedNamespaces(ctx, kubeClient, target)
		if err != nil {
			return fmt.Errorf("failed to check namespace labels: %w", err)
		}
		if hasTaggedNamespaces {
			return fmt.Errorf("namespaces use tag-based injection labels but no tag is configured -- " +
				"set svc.istio.tag in config or pass --tag to enable webhook flipping")
		}
	}

	// Verify both control planes are healthy, then flip the tag webhook to route
	// injection requests to the new istiod. This is what makes new pods get the
	// new sidecar — namespace labels stay unchanged.
	logger.Info("Step 3/9: Verifying control plane health and flipping tag webhook")
	if err := verifyControlPlaneAndTag(ctx, kubeClient, opts.Tag, target); err != nil {
		return rollbackAndReturn(ctx, logger, kubeClient, opts, previousRevisions, target, err)
	}

	logger.Info("Step 4/9: Ensuring ingress gateway annotations")
	if err := ensureIngress(ctx, kubeClient, opts); err != nil {
		return rollbackAndReturn(ctx, logger, kubeClient, opts, previousRevisions, target, err)
	}

	// Rolling-restart all mesh workloads to pick up the new sidecar,
	// then wait for rollout convergence in each namespace.
	logger.Info("Step 5/9: Migrating workloads to target revision")
	if err := migrateWorkloads(ctx, kubeClient, opts, target); err != nil {
		return rollbackAndReturn(ctx, logger, kubeClient, opts, previousRevisions, target, err)
	}

	// Health check — CP readiness, ingress gateway, namespace coverage.
	// Must pass before we remove the old control plane (irreversible).
	logger.Info("Step 6/9: Running health check")
	health, err := HealthCheck(ctx, kubeClient)
	if err != nil {
		healthErr := fmt.Errorf("health check failed: %w", err)
		return rollbackAndReturn(ctx, logger, kubeClient, opts, previousRevisions, target, healthErr)
	}
	if !health.Passed {
		return rollbackAndReturn(ctx, logger, kubeClient, opts, previousRevisions, target, healthCheckError("post-upgrade", health))
	}
	logger.Info("Health check passed -- checking for orphaned workloads before completing canary")

	// Orphan guard — verify no pods are still running old sidecars. Retries up to
	// MaxOrphanRetries because pods can lag behind the rolling restart. Must pass
	// before ARM complete because removing the old CP orphans any pods still on
	// old sidecars (mTLS certs expire).
	logger.Info("Step 7/9: Running orphan guard")
	if err := retireOrphanedWorkloads(ctx, logger, kubeClient, target, previousRevisions, opts); err != nil {
		return rollbackAndReturn(ctx, logger, kubeClient, opts, previousRevisions, target, err)
	}

	logger.Info("No orphaned workloads -- completing canary")

	if opts.StopAfter == StopAfterOrphanCheck {
		logger.Info("Stopping after orphan check as requested -- workloads migrated and verified, re-run to complete canary")
		return nil
	}

	// Step 8: ARM complete — point of no return. After this, the old CP is gone and
	// auto-rollback is impossible. All safety checks above must pass first.
	logger.Info("Step 8/9: Completing canary via ARM (point of no return)")
	if err := aksClient.CompleteCanaryUpgrade(ctx, opts.ResourceGroup, opts.ClusterName, target); err != nil {
		return fmt.Errorf("failed to complete canary: %w", err)
	}

	logger.Info("Step 9/9: Cleaning up old ConfigMap and running final verification")
	for _, oldRev := range previousRevisions {
		if oldRev != target {
			if err := DeleteRevisionConfigMap(ctx, kubeClient, oldRev); err != nil {
				logger.Info("Failed to delete old ConfigMap (non-fatal)", "revision", oldRev, "error", err)
			}
		}
	}

	// Final verification — confirms single revision, correct tag, all pods on target.
	verification, err := VerifyUpgrade(ctx, kubeClient, target, opts.Tag)
	if err != nil {
		return fmt.Errorf("upgrade verification failed: %w", err)
	}
	if !verification.Passed {
		return fmt.Errorf("post-upgrade verification failed: %v", verification.Issues)
	}

	reconcileOrphanedGatewayLeases(ctx, logger, aksClient, kubeClient, opts, target)

	logger.Info("Istio upgrade complete and verified", "target", target)
	return nil
}

func ensureIngress(ctx context.Context, kubeClient *KubeClient, opts UpgradeOptions) error {
	if opts.IngressIPName == "" && opts.RegionRG == "" {
		return nil
	}
	if opts.IngressIPName == "" || opts.RegionRG == "" {
		return fmt.Errorf("ingress config is incomplete: both IngressIPName and RegionRG must be set (got IngressIPName=%q, RegionRG=%q)", opts.IngressIPName, opts.RegionRG)
	}
	if _, err := EnsureIngressAnnotations(ctx, kubeClient, opts.RegionRG, map[string]string{
		"aks-istio-ingressgateway-external": opts.IngressIPName,
	}); err != nil {
		return fmt.Errorf("failed to ensure ingress annotations: %w", err)
	}
	return nil
}

func isDirectRevision(label string) bool {
	return RevisionPattern.MatchString(label)
}

func hasTagBasedNamespaces(ctx context.Context, kubeClient *KubeClient, target string) (bool, error) {
	namespaces, err := kubeClient.GetMeshNamespaces(ctx)
	if err != nil {
		return false, err
	}
	for _, ns := range namespaces {
		if ns.RevisionLabel != "" && ns.RevisionLabel != target && !isDirectRevision(ns.RevisionLabel) {
			return true, nil
		}
	}
	return false, nil
}

// retireOrphanedWorkloads prevents mTLS cert expiry: if the old CP is removed while
// pods still use its sidecars, those sidecars lose cert rotation and mTLS connections
// fail. Retries up to MaxOrphanRetries because pods can lag behind rolling restarts.
func retireOrphanedWorkloads(ctx context.Context, logger logr.Logger, kubeClient *KubeClient, target string, previousRevisions []string, opts UpgradeOptions) error {
	for attempt := 1; ; attempt++ {
		orphaned, err := CheckOrphanedWorkloads(ctx, kubeClient, target, previousRevisions)
		if err != nil {
			return fmt.Errorf("orphan guard check failed: %w", err)
		}
		if len(orphaned) == 0 {
			return nil
		}
		if attempt > opts.MaxOrphanRetries {
			return fmt.Errorf("%d pod(s) still on old revision after %d restart attempts: %v: %w",
				len(orphaned), opts.MaxOrphanRetries, orphaned, ErrRetireRevisionWouldOrphanWorkloads)
		}
		logger.Info("Orphaned workloads found -- restarting stale pods",
			"attempt", attempt,
			"orphaned", len(orphaned),
			"pods", orphaned,
		)
		if _, err := ExecuteRestartAllNamespaces(ctx, kubeClient, target); err != nil {
			return fmt.Errorf("orphan restart failed: %w", err)
		}
		if err := WaitForRolloutAllNamespaces(ctx, kubeClient, opts.RolloutTimeout, opts.RolloutPollInterval); err != nil {
			return fmt.Errorf("orphan restart rollout failed: %w", err)
		}
	}
}

// warnRetireOrphanedWorkloads runs the same retry loop as retireOrphanedWorkloads but
// logs a warning and continues on exhaustion instead of failing. Used in cleanup paths
// where there is no rollback target — proceeding with a small number of orphans (which
// retain valid mTLS certs until expiry) is better than blocking the entire upgrade.
func warnRetireOrphanedWorkloads(ctx context.Context, logger logr.Logger, kubeClient *KubeClient, target string, retiringRevisions []string, opts UpgradeOptions) error {
	for attempt := 1; ; attempt++ {
		orphaned, err := CheckOrphanedWorkloads(ctx, kubeClient, target, retiringRevisions)
		if err != nil {
			return fmt.Errorf("cleanup orphan check failed: %w", err)
		}
		if len(orphaned) == 0 {
			return nil
		}
		if attempt > opts.MaxOrphanRetries {
			logger.Info("WARNING: orphaned workloads remain after retries -- proceeding with cleanup, pods may lose mTLS cert rotation",
				"orphaned", len(orphaned),
				"pods", orphaned,
			)
			return nil
		}
		logger.Info("Orphaned workloads found during cleanup -- restarting stale pods",
			"attempt", attempt,
			"orphaned", len(orphaned),
			"pods", orphaned,
		)
		if _, err := ExecuteRestartAllNamespaces(ctx, kubeClient, target); err != nil {
			return fmt.Errorf("cleanup orphan restart failed: %w", err)
		}
		if err := WaitForRolloutAllNamespaces(ctx, kubeClient, opts.RolloutTimeout, opts.RolloutPollInterval); err != nil {
			return fmt.Errorf("cleanup orphan rollout failed: %w", err)
		}
	}
}

func verifyControlPlaneAndTag(ctx context.Context, kubeClient *KubeClient, tag, target string) error {
	cpStatus, err := GetControlPlaneStatus(ctx, kubeClient)
	if err != nil {
		return fmt.Errorf("failed to get control plane status: %w", err)
	}
	targetFound := false
	for _, cp := range cpStatus {
		if cp.Revision == target {
			targetFound = true
		}
		if !cp.Ready {
			return fmt.Errorf("istiod-%s not ready (%d/%d available): %w", cp.Revision, cp.Available, cp.Replicas, ErrControlPlaneUnhealthy)
		}
	}
	if !targetFound {
		return fmt.Errorf("target revision %s control plane not found -- upgrade may be targeting a different revision: %w", target, ErrControlPlaneUnhealthy)
	}

	if tag != "" {
		if err := EnsureRevisionTag(ctx, kubeClient, tag, target); err != nil {
			return fmt.Errorf("failed to ensure revision tag %s -> %s: %w", tag, target, err)
		}
	}

	return nil
}

func reconcileOrphanedGatewayLeases(
	ctx context.Context,
	logger logr.Logger,
	aksClient AKSClusterClient,
	kubeClient *KubeClient,
	opts UpgradeOptions,
	target string,
) {
	clusterInfo, meshProfile, err := aksClient.GetClusterState(
		ctx,
		opts.ResourceGroup,
		opts.ClusterName,
	)
	if err != nil {
		logger.Error(err, "Failed to get mesh state before orphaned lease reconciliation (non-fatal)")
		return
	}

	upgradeInfo, err := aksClient.GetMeshUpgradeTargets(
		ctx,
		opts.ResourceGroup,
		opts.ClusterName,
	)
	if err != nil {
		logger.Error(err, "Failed to get Istio upgrade state before orphaned lease reconciliation (non-fatal)")
		return
	}

	// Do not touch leases while AKS is adding/removing revisions, or while
	// a rollback can still reactivate the previous control plane.
	if clusterInfo.ProvisioningState != "Succeeded" ||
		upgradeInfo.UpgradeInProgress ||
		len(meshProfile.Revisions) != 1 ||
		meshProfile.Revisions[0] != target {
		logger.Info(
			"Skipping orphaned Istio gateway lease reconciliation until mesh is stable",
			"provisioningState", clusterInfo.ProvisioningState,
			"upgradeInProgress", upgradeInfo.UpgradeInProgress,
			"installedRevisions", meshProfile.Revisions,
			"target", target,
		)
		return
	}

	logger.Info("Reconciling orphaned Istio gateway leases")
	if err := ReconcileOrphanedGatewayLeases(
		ctx,
		logger,
		kubeClient,
		meshProfile.Revisions,
	); err != nil {
		logger.Error(err, "Failed to reconcile orphaned Istio gateway leases (non-fatal)")
		return
	}
	logger.Info("Orphaned Istio gateway leases reconciled")
}
