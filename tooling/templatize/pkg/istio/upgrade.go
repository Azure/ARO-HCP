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

	"k8s.io/client-go/kubernetes"
)

var (
	ErrRetireRevisionWouldOrphanWorkloads = errors.New("retiring revision would orphan workloads: stale sidecar pods remain after restart retries")
	ErrControlPlaneUnhealthy              = errors.New("control plane unhealthy: one or more istiod pods are not ready")
)

var revisionPattern = regexp.MustCompile(`^asm-\d+-\d+$`)

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
	KubeconfigPath      string
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
		OverallTimeout:      25 * time.Minute,
		MaxOrphanRetries:    3,
	}
}

func RunUpgrade(ctx context.Context, opts UpgradeOptions, aksClient AKSClusterClient, kubeClient kubernetes.Interface) error {
	if opts.OverallTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.OverallTimeout)
		defer cancel()
	}

	logger := logr.FromContextOrDiscard(ctx).WithName("istio-upgrade").WithValues(
		"cluster", opts.ClusterName,
		"versions", opts.Versions,
	)

	target := strings.TrimSpace(opts.Versions)
	if target == "" {
		return fmt.Errorf("no versions specified in config")
	}
	if !revisionPattern.MatchString(target) {
		return fmt.Errorf("invalid target version %q: must match %s", target, revisionPattern.String())
	}

	clusterInfo, meshProfile, err := aksClient.GetClusterState(ctx, opts.ResourceGroup, opts.ClusterName)
	if err != nil {
		return fmt.Errorf("failed to get cluster state: %w", err)
	}
	upgradeInfo, err := aksClient.GetMeshUpgradeTargets(ctx, opts.ResourceGroup, opts.ClusterName)
	if err != nil {
		return fmt.Errorf("failed to get upgrade targets: %w", err)
	}

	logger.Info("Istio upgrade — cluster state",
		"k8sVersion", clusterInfo.KubernetesVersion,
		"provisioningState", clusterInfo.ProvisioningState,
		"installedRevisions", meshProfile.Revisions,
		"availableUpgrades", upgradeInfo.AvailableUpgrades,
		"upgradeInProgress", upgradeInfo.UpgradeInProgress,
	)

	state := ClusterState{
		Name:              opts.ClusterName,
		Revisions:         meshProfile.Revisions,
		AvailableUpgrades: upgradeInfo.AvailableUpgrades,
		KubernetesVersion: clusterInfo.KubernetesVersion,
		ProvisioningState: clusterInfo.ProvisioningState,
		UpgradeInProgress: upgradeInfo.UpgradeInProgress,
	}

	decision := Decide(state, target)
	logger.Info("Istio upgrade — decision", "action", decision.Action, "reason", decision.Reason)

	logMeshState(ctx, kubeClient, logger)

	if opts.DryRun {
		logger.Info("Istio upgrade — [DRY-RUN] would execute", "action", decision.Action, "target", target)
		return nil
	}

	switch decision.Action {
	case ActionSkip:
		if !slices.Contains(meshProfile.Revisions, target) {
			logger.Info("Istio upgrade — installed revision does not match config target",
				"installed", meshProfile.Revisions,
				"expected", target,
			)
			return nil
		}
		if err := CreateRevisionConfigMap(ctx, kubeClient, target); err != nil {
			logger.Info("Istio upgrade — failed to ensure ConfigMap on skip (non-fatal)", "error", err)
		}
		if opts.Tag != "" {
			if err := EnsureRevisionTag(ctx, kubeClient, opts.Tag, target); err != nil {
				logger.Info("Istio upgrade — failed to ensure tag webhook on skip (non-fatal)", "error", err)
			}
		}
		if err := ensureIngress(ctx, kubeClient, opts); err != nil {
			logger.Info("Istio upgrade — failed to ensure ingress on skip (non-fatal)", "error", err)
		}
		return nil
	case ActionInstall:
		return runInitialInstall(ctx, logger, aksClient, kubeClient, opts, target)
	case ActionResume:
		return runCanaryPostInstall(ctx, logger, aksClient, kubeClient, opts, target, meshProfile.Revisions)
	case ActionUpgrade:
		return runCanaryUpgrade(ctx, logger, aksClient, kubeClient, opts, target, meshProfile.Revisions)
	case ActionCleanupAndUpgrade:
		return runCleanupAndUpgrade(ctx, logger, aksClient, kubeClient, opts, target, meshProfile.Revisions)
	default:
		return fmt.Errorf("unhandled action %q", decision.Action)
	}
}

func runInitialInstall(ctx context.Context, logger logr.Logger, aksClient AKSClusterClient, kubeClient kubernetes.Interface, opts UpgradeOptions, target string) error {
	logger.Info("Enabling mesh on new cluster", "revision", target)
	if err := aksClient.EnableMesh(ctx, opts.ResourceGroup, opts.ClusterName, target); err != nil {
		return fmt.Errorf("failed to enable mesh: %w", err)
	}

	if err := CreateRevisionConfigMap(ctx, kubeClient, target); err != nil {
		return fmt.Errorf("failed to create ConfigMap: %w", err)
	}

	if err := verifyControlPlaneAndTag(ctx, kubeClient, opts.Tag, target); err != nil {
		return err
	}

	if err := ensureIngress(ctx, kubeClient, opts); err != nil {
		return err
	}

	logger.Info("Initial Istio install complete", "revision", target)
	return nil
}

func runCanaryUpgrade(ctx context.Context, logger logr.Logger, aksClient AKSClusterClient, kubeClient kubernetes.Interface, opts UpgradeOptions, target string, currentRevisions []string) error {
	logger.Info("Starting canary — installing target alongside current")
	if err := aksClient.StartCanaryUpgrade(ctx, opts.ResourceGroup, opts.ClusterName, target); err != nil {
		return fmt.Errorf("failed to start canary: %w", err)
	}

	if opts.StopAfter == StopAfterCanaryStart {
		logger.Info("Stopping after canary start as requested — cluster has two revisions, re-run to resume")
		return nil
	}

	return runCanaryPostInstall(ctx, logger, aksClient, kubeClient, opts, target, currentRevisions)
}

// runCleanupAndUpgrade handles clusters stuck with two revisions from a prior
// failed canary where neither revision matches the new target. Consolidates
// workloads onto the older stable revision, completes ARM to remove the stale
// one, then starts a fresh canary to the target.
func runCleanupAndUpgrade(ctx context.Context, logger logr.Logger, aksClient AKSClusterClient, kubeClient kubernetes.Interface, opts UpgradeOptions, target string, revisions []string) error {
	staleRevision := slices.MaxFunc(revisions, compareRevisions)
	oldRevision := stableRevisionFrom(revisions, staleRevision)
	if oldRevision == "" {
		return fmt.Errorf("cannot determine old revision to keep from %v", revisions)
	}

	logger.Info("Cleaning up stale canary before upgrading", "keeping", oldRevision, "removing", staleRevision, "target", target)

	if err := CreateRevisionConfigMap(ctx, kubeClient, oldRevision); err != nil {
		return fmt.Errorf("failed to ensure ConfigMap for old revision: %w", err)
	}

	if err := verifyControlPlaneAndTag(ctx, kubeClient, opts.Tag, oldRevision); err != nil {
		return fmt.Errorf("old revision control plane unhealthy during cleanup: %w", err)
	}

	if err := ensureIngress(ctx, kubeClient, opts); err != nil {
		return fmt.Errorf("failed to ensure ingress during cleanup: %w", err)
	}

	if err := migrateWorkloads(ctx, kubeClient, opts, oldRevision); err != nil {
		return fmt.Errorf("cleanup workload migration failed: %w", err)
	}

	health, err := HealthCheck(ctx, kubeClient)
	if err != nil {
		return fmt.Errorf("cleanup health check failed: %w", err)
	}
	if !health.Passed {
		return fmt.Errorf("cleanup health check failed: %w: %v", ErrControlPlaneUnhealthy, health.Issues)
	}

	if err := aksClient.CompleteCanaryUpgrade(ctx, opts.ResourceGroup, opts.ClusterName, oldRevision); err != nil {
		return fmt.Errorf("cleanup ARM completion failed: %w", err)
	}

	if err := DeleteRevisionConfigMap(ctx, kubeClient, staleRevision); err != nil {
		logger.Info("Failed to delete stale ConfigMap (non-fatal)", "revision", staleRevision, "error", err)
	}

	verification, err := VerifyUpgrade(ctx, kubeClient, oldRevision, opts.Tag)
	if err != nil {
		return fmt.Errorf("cleanup verification failed: %w", err)
	}
	if !verification.Passed {
		return fmt.Errorf("cleanup verification failed: %v", verification.Issues)
	}

	logger.Info("Stale canary cleaned up — starting fresh upgrade", "from", oldRevision, "to", target)
	return runCanaryUpgrade(ctx, logger, aksClient, kubeClient, opts, target, []string{oldRevision})
}

func rollbackAndReturn(ctx context.Context, logger logr.Logger, kubeClient kubernetes.Interface, opts UpgradeOptions, previousRevisions []string, target string, originalErr error) error {
	oldRevision := oldRevisionFrom(previousRevisions, target)
	if oldRevision != "" {
		logger.Info("Rolling back workloads to previous revision before returning error", "old", oldRevision)
		if rbErr := rollbackWorkloads(ctx, logger, kubeClient, opts, oldRevision); rbErr != nil {
			return errors.Join(originalErr, fmt.Errorf("workload rollback also failed: %w", rbErr))
		}
		logger.Info("Workloads rolled back — cluster still has two control planes, next run will retry via ActionResume")
	}
	return originalErr
}

func rollbackWorkloads(ctx context.Context, logger logr.Logger, kubeClient kubernetes.Interface, opts UpgradeOptions, oldRevision string) error {
	logger.Info("Rolling back workloads to previous revision", "revision", oldRevision)
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

func runCanaryPostInstall(ctx context.Context, logger logr.Logger, aksClient AKSClusterClient, kubeClient kubernetes.Interface, opts UpgradeOptions, target string, previousRevisions []string) error {
	if err := CreateRevisionConfigMap(ctx, kubeClient, target); err != nil {
		return fmt.Errorf("failed to ensure ConfigMap on resume: %w", err)
	}

	if opts.Tag == "" {
		hasTaggedNamespaces, err := hasTagBasedNamespaces(ctx, kubeClient, target)
		if err != nil {
			return fmt.Errorf("failed to check namespace labels: %w", err)
		}
		if hasTaggedNamespaces {
			return fmt.Errorf("namespaces use tag-based injection labels but no tag is configured — " +
				"set svc.istio.tag in config or pass --tag to enable webhook flipping")
		}
	}

	if err := verifyControlPlaneAndTag(ctx, kubeClient, opts.Tag, target); err != nil {
		return rollbackAndReturn(ctx, logger, kubeClient, opts, previousRevisions, target, err)
	}

	if err := ensureIngress(ctx, kubeClient, opts); err != nil {
		return err
	}

	if err := migrateWorkloads(ctx, kubeClient, opts, target); err != nil {
		return rollbackAndReturn(ctx, logger, kubeClient, opts, previousRevisions, target, err)
	}

	health, err := HealthCheck(ctx, kubeClient)
	if err != nil {
		return fmt.Errorf("health check failed: %w", err)
	}
	if !health.Passed {
		healthErr := fmt.Errorf("post-upgrade health check failed: %w: %v", ErrControlPlaneUnhealthy, health.Issues)
		return rollbackAndReturn(ctx, logger, kubeClient, opts, previousRevisions, target, healthErr)
	}
	logger.Info("Health check passed — checking for orphaned workloads before completing canary")

	if err := retireOrphanedWorkloads(ctx, logger, kubeClient, target, previousRevisions, opts); err != nil {
		if errors.Is(err, ErrRetireRevisionWouldOrphanWorkloads) {
			return rollbackAndReturn(ctx, logger, kubeClient, opts, previousRevisions, target, err)
		}
		return err
	}

	logger.Info("No orphaned workloads — completing canary")

	if opts.StopAfter == StopAfterOrphanCheck {
		logger.Info("Stopping after orphan check as requested — workloads migrated and verified, re-run to complete canary")
		return nil
	}

	if err := aksClient.CompleteCanaryUpgrade(ctx, opts.ResourceGroup, opts.ClusterName, target); err != nil {
		return fmt.Errorf("failed to complete canary: %w", err)
	}

	for _, oldRev := range previousRevisions {
		if oldRev != target {
			if err := DeleteRevisionConfigMap(ctx, kubeClient, oldRev); err != nil {
				logger.Info("Failed to delete old ConfigMap (non-fatal)", "revision", oldRev, "error", err)
			}
		}
	}

	verification, err := VerifyUpgrade(ctx, kubeClient, target, opts.Tag)
	if err != nil {
		return fmt.Errorf("upgrade verification failed: %w", err)
	}
	if !verification.Passed {
		return fmt.Errorf("post-upgrade verification failed: %v", verification.Issues)
	}

	logger.Info("Istio upgrade complete and verified", "target", target)
	return nil
}

func ensureIngress(ctx context.Context, kubeClient kubernetes.Interface, opts UpgradeOptions) error {
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

// AKS-managed Istio revisions follow the pattern asm-{major}-{minor}.
var directRevisionPattern = regexp.MustCompile(`^asm-\d+-\d+$`)

func isDirectRevision(label string) bool {
	return directRevisionPattern.MatchString(label)
}

func hasTagBasedNamespaces(ctx context.Context, kubeClient kubernetes.Interface, target string) (bool, error) {
	namespaces, err := GetMeshNamespaces(ctx, kubeClient)
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

func retireOrphanedWorkloads(ctx context.Context, logger logr.Logger, kubeClient kubernetes.Interface, target string, previousRevisions []string, opts UpgradeOptions) error {
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
		logger.Info("Orphaned workloads found — restarting stale pods",
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

func verifyControlPlaneAndTag(ctx context.Context, kubeClient kubernetes.Interface, tag, target string) error {
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
		return fmt.Errorf("target revision %s control plane not found — upgrade may be targeting a different revision: %w", target, ErrControlPlaneUnhealthy)
	}

	if tag != "" {
		if err := EnsureRevisionTag(ctx, kubeClient, tag, target); err != nil {
			return fmt.Errorf("failed to ensure revision tag %s → %s: %w", tag, target, err)
		}
	}

	return nil
}
