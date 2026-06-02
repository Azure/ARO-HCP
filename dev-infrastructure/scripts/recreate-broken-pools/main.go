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
//
// recreate-broken-pools: detection-gated self-healing for the NRP KVS
// corruption that breaks the AKS `system` pool VMSS.
//
// Background
// ----------
// A corrupted NRP KVS entity for the `system` pool's VMSS causes every
// Microsoft.Compute/virtualMachineScaleSets/write to fail with
// NetworkingInternalOperationError on a continuous retry chain. Fresh
// VM instances come up but never get a Swift NIC, so kubelet never
// registers. The corruption is bound to the VMSS ARM resource id;
// per-instance delete does not fix it. Deleting the pool destroys the
// VMSS and frees the KVS entity; re-creating the pool gets a fresh
// VMSS name and a clean KVS entity.
//
// Manually applied at INT on 2026-05-24 (AROSLSRE-924 + AROSLSRE-925).
// This binary automates the same recipe behind a tight detection gate.
//
// Tracked upstream in ICM 798003653. Once NRP ships the proper fix
// (ModifyKeyValueItem scoped to every update group), the detection
// guards never fire and this binary becomes a no-op.
//
// Inputs (env vars, set by mgmt-pipeline.yaml step)
// -------------------------------------------------
//   CLUSTER_NAME                AKS cluster name (e.g. int-uksouth-mgmt-1)
//   RESOURCE_GROUP              Resource group containing the AKS cluster
//   SUBSCRIPTION_ID             Azure subscription ID containing the AKS cluster
//   NODEPOOL_TAG                aro-hcp.azure.com/role tag value selecting which
//                               pools this run targets (e.g. "system", "user")
//   NRP_FAIL_THRESHOLD          Failed-event count threshold (default 5)
//   NRP_FAIL_WINDOW_MIN         Activity-log lookback window in min (default 15)
//   FORCED_EVIDENCE_TIMEOUT_MIN Max minutes to wait for evidence after the
//                               forced reconcile trigger (default 20)
//   FORCED_EVIDENCE_THRESHOLD   Distinct NRP-KVS hits required during the
//                               forced-evidence probe to confirm the wedge
//                               (default 3)
//   DRY_RUN                     "true" to print intended actions but make no writes
//   SKIP_GUARDS                 "true" to bypass detection guards (proceeds even
//                               when NRP-KVS storm check fails, operator override).
//                               Has no effect when the confirmed target list is
//                               empty: the run still exits no-op so that Step 2
//                               (maybeAbortLRO) and Step 8 (reconcileTagPut) never
//                               execute without a pool to recreate.
//   LOG_VERBOSITY               integer logr verbosity for slog handler (default 0)
//
// Detection checks (ALL must pass; otherwise exit 0 no-op)
// --------------------------------------------------------
// Names below are the check labels used in log lines and reason
// strings throughout this binary. Checks run in the order listed
// (cluster state -> non-system pools -> system wedge -> NRP-KVS storm)
// so the cheap ARM checks short-circuit before we query Activity Log.
//
//   [cluster state]      cluster provisioningState is recoverable:
//                        Succeeded, Canceled, Failed (settled) OR
//                        Updating, Upgrading (mid-LRO — the NRP-KVS
//                        wedge signature itself; step 2 decides
//                        whether to abort the LRO). Creating and
//                        Deleting are rejected; unknown states are
//                        rejected conservatively.
//   [non-system pools]   every non-system pool has count > 0.
//   [system wedge]       system pool provisioningState is NOT
//                        Succeeded — positive confirmation that this
//                        specific pool is wedged. Accepts Failed,
//                        Canceled, Updating, Upgrading (an NRP-KVS
//                        wedge typically leaves the pool in Updating
//                        while its parent cluster LRO retries forever
//                        — AROSLSRE-880 — or in Failed/Canceled once
//                        that LRO finally times out or is aborted).
//                        Rejects Succeeded (no wedge) and
//                        Creating/Deleting/unknown.
//   [NRP-KVS storm]      >= NRP_FAIL_THRESHOLD Failed VMSS-write
//                        events on the system pool's VMSS in the
//                        last NRP_FAIL_WINDOW_MIN whose inner error
//                        code is NetworkingInternalOperationError.
//                        Other failure modes — quota/capacity/policy
//                        / image pull / etc — never satisfy this
//                        check, so they cannot trigger a destructive
//                        pool recreation that would not address their
//                        actual root cause.
//
// Action (only when all guards pass) — applied to every confirmed target pool
// ----------------------------------------------------------------------------
//   1. Snapshot the target pool ARM JSON (raw).
//   2. Abort cluster LRO if one is active and older than 30 min. The
//      AROSLSRE-880 / NRP-KVS incident at INT (2026-05-16..18) left the
//      cluster stuck in Updating for days because the parent upgrade
//      LRO retried forever; aborting frees the cluster to accept fresh
//      PUTs. Aborts move the cluster from Updating to Canceled. If the
//      latest LRO is younger than 30 min, we no-op exit 0 instead of
//      racing a potentially-healthy in-progress operation.
//   3. Add a throwaway temp pool (deterministic name derived from the
//      target) inheriting Mode/VMSize/subnet/taints/labels from the live
//      snapshot. Wait for the ARM LRO to succeed AND the new k8s node to
//      become Ready before proceeding. The original pool is never
//      touched until the temp pool is healthy in the cluster.
//   4. Cordon + drain existing target-pool nodes (client-go drain helper).
//   5. Delete the broken target pool.
//   6. Re-create the target pool via SDK CreateOrUpdate with the
//      sanitized AgentPool struct from the snapshot.
//   7. Cordon + drain + delete the temp pool.
//   8. No-op reconcile via tag update (kicks cluster back to Succeeded).

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/kubectl/pkg/drain"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	armcs "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v6"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/monitor/armmonitor"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"

	mcclient "github.com/Azure/ARO-HCP/sessiongate/pkg/mc"
)

const (
	defaultThreshold = 5
	defaultWindowMin = 15
	lroAbortAgeMin   = 30
	lroLookupWindow  = "14d"
	tempReadyTOMin   = 8
	poolReadyTOMin   = 15
	pollIntervalSec  = 30
	// overallTimeoutMin caps the whole binary. The EV2 ShellExtension host
	// honors the `timeout` field set on the corresponding Shell step in
	// mgmt-pipeline.yaml (currently `150m`, which leaves headroom for the
	// deferred forced-evidence cleanup contexts that are rooted in
	// context.Background and can run for ~20m past the parent ctx).
	overallTimeoutMin = 120

	// The NRP-KVS storm check requires this error code so other failure
	// modes (quota / capacity / policy / etc) cannot trip the threshold.
	nrpKVSErrorCode    = "NetworkingInternalOperationError"
	vmssWriteOperation = "Microsoft.Compute/virtualMachineScaleSets/write"

	// tempPoolPurposeTag / tempPoolPurposeValue mark agent pools created
	// by this binary as throwaway replacements. Detection skips any pool
	// carrying this tag so the temp pool itself is never picked as a
	// remediation target, and adoptLeftoverTempPool uses the deterministic
	// per-target name from tempPoolName to detect (and either adopt or
	// fail closed on) leftover temp pools from previous crashed runs.
	tempPoolPurposeTag   = "purpose"
	tempPoolPurposeValue = "temp-recreate-broken-pools"

	// tempPoolSourceTag carries the full Azure resource ID of the source
	// agent pool (e.g.
	//   /subscriptions/<sub>/resourceGroups/<rg>/providers/
	//   Microsoft.ContainerService/managedClusters/<cluster>/agentPools/<pool>
	// ). The full ID — not the short pool name — is stored so leftover
	// temp pools can always be matched back to a unique source even
	// when multiple clusters in the same subscription share pool names
	// (e.g. every mgmt cluster has a "system" pool), and so the
	// human reading the tag can navigate directly to the source via
	// the portal / az CLI.
	tempPoolSourceTag = "source"

	// tempPoolNamePrefix is the leading 3 chars of every temp pool name.
	// AKS Linux agent-pool names must start with a letter, be 1..12
	// chars, and contain only lowercase alphanumerics. With a base36
	// fnv32a hash appended, the resulting name is at most 10 chars.
	tempPoolNamePrefix = "tmp"

	activityLogAuthRetryTimeoutMin = 5
	activityLogAuthRetryInitialSec = 10
	activityLogAuthRetryMaxSec     = 60

	// forcedEvidence forces an AKS RP reconcile of a suspected pool
	// when the cluster-state / non-system-pools / system-wedge checks
	// PASS but the NRP-KVS-storm check has no recent NRP-KVS events in
	// the configured lookback window. The trigger gives the wedge a
	// chance to produce fresh evidence (or to prove the wedge is not
	// NRP-KVS). Times are short relative to the AKS RP retry cadence
	// (~3 min) so threshold-many retries can accumulate during the wait
	// window.
	defaultForcedEvidenceTimeoutMin = 20 // wait at most this long for evidence
	defaultForcedEvidenceThreshold  = 3  // distinct NRP-KVS hits to confirm the wedge during the probe
	forcedEvidencePollIntervalSec   = 60 // re-query activity log every poll
	forcedEvidenceWindowMin         = 30 // activity-log lookback for the wait loop (covers the probe window plus a small margin)

	// forcedEvidenceAbortTimeoutMin caps the wait for cleanup of the LRO
	// that forcedEvidencePath itself triggered. The abort runs with a
	// fresh context derived from context.Background so it executes even
	// when the parent run context has already expired (overall script
	// timeout or pollForNRPEvidence consuming the full forced-evidence
	// timeout budget). Without this, a cancelled parent context would
	// silently skip the abort and leave the AKS RP retrying the wedged
	// write.
	forcedEvidenceAbortTimeoutMin = 8

	// forcedEvidenceRestoreTimeoutMin caps the wait for the restorePoolSpec
	// PUT + PollUntilDone that reverses the forced-evidence scale-up. ARM
	// pool PUTs against AKS can legitimately take 5-10m, so this runs on
	// its own context (also derived from context.Background) rather than
	// sharing the shorter abort budget. Without a dedicated context, an
	// abort that consumed most of forcedEvidenceAbortTimeoutMin would
	// silently skip the restore and leave the pool one node larger than
	// the original snapshot.
	forcedEvidenceRestoreTimeoutMin = 15
)

const nodePoolRoleLabel = "aro-hcp.azure.com/role"

type nodePoolTarget struct {
	name        string
	vmssPrefix  string
	nrpFailures int
	suspected   bool
}

func poolVMSSPrefix(poolName string) string {
	return "aks-" + poolName + "-"
}

func poolRoleTag(p *armcs.AgentPool) string {
	if p == nil || p.Properties == nil {
		return ""
	}
	if p.Properties.NodeLabels != nil {
		if role := p.Properties.NodeLabels[nodePoolRoleLabel]; role != nil {
			return *role
		}
	}
	if p.Properties.Tags != nil {
		if role := p.Properties.Tags[nodePoolRoleLabel]; role != nil {
			return *role
		}
	}
	return ""
}

// tempPoolName returns the deterministic temp-pool name for the given
// source pool. AKS Linux agent-pool names must be 1..12 lowercase
// alphanumeric chars starting with a letter; "tmp" + fnv32a(source)
// in base36 is at most 10 chars and always satisfies that rule.
//
// The full Azure resource ID of the source pool is stored in the
// tempPoolSourceTag on the temp pool, so the name itself does not
// need to carry any of the source identity.
func tempPoolName(source string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(source))
	return tempPoolNamePrefix + strconv.FormatUint(uint64(h.Sum32()), 36)
}

// isTempPool reports whether p is a throwaway pool previously created by
// this binary. Detection and target selection skip such pools.
func isTempPool(p *armcs.AgentPool) bool {
	if p == nil || p.Properties == nil || p.Properties.Tags == nil {
		return false
	}
	v := p.Properties.Tags[tempPoolPurposeTag]
	return v != nil && *v == tempPoolPurposeValue
}

func main() {
	// JSON logs to stderr so the Geneva collector ships them to Kusto in
	// the same shape as frontend / backend / admin-server. Use the
	// LOG_VERBOSITY env var (logr convention: 0 = INFO, 1+ = more verbose).
	verbosity := 0
	if v := os.Getenv("LOG_VERBOSITY"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			verbosity = n
		}
	}
	handler := slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
		Level:     slog.Level(verbosity * -1),
		AddSource: false,
	})
	slog.SetDefault(slog.New(handler).With("component", "recreate-broken-pools"))

	if err := run(); err != nil {
		slog.Error("run failed", "error", err.Error())
		os.Exit(1)
	}
}

type orchestrator interface {
	ensureCluster(ctx context.Context) (armcs.ManagedCluster, bool, error)
	bootstrapKube(ctx context.Context, mc armcs.ManagedCluster) error
	detect(ctx context.Context) ([]nodePoolTarget, string, error)
	dumpPreflight(ctx context.Context) error
	dumpPostflight(ctx context.Context) error
	adoptLeftoverTempPool(ctx context.Context, target nodePoolTarget) error
	snapshotPool(ctx context.Context, poolName string) (*armcs.AgentPool, error)
	maybeAbortLRO(ctx context.Context) (bool, error)
	addTempPool(ctx context.Context, target nodePoolTarget, live *armcs.AgentPool) error
	drainPool(ctx context.Context, pool string, timeout time.Duration) error
	deletePool(ctx context.Context, pool string) error
	recreatePool(ctx context.Context, poolName string, live *armcs.AgentPool) error
	reconcileTagPut(ctx context.Context) error
	triggerPoolReconcile(ctx context.Context, target nodePoolTarget, live *armcs.AgentPool) error
	pollForNRPEvidence(ctx context.Context, target nodePoolTarget, timeout time.Duration, pollInterval time.Duration, windowMin int, threshold int) (int, error)
	abortPoolReconcile(ctx context.Context, poolName string) error
	restorePoolSpec(ctx context.Context, target nodePoolTarget, live *armcs.AgentPool) error
}

func run() error {
	ctx, cancel := context.WithTimeout(context.Background(), overallTimeoutMin*time.Minute)
	defer cancel()

	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	logBanner("STARTUP")
	cfg.logEnv()

	clients, err := newAzureClients(cfg)
	if err != nil {
		return fmt.Errorf("init azure clients: %w", err)
	}

	return runWith(ctx, cfg, clients)
}

func runWith(ctx context.Context, cfg *config, orch orchestrator) error {
	logBanner("CLUSTER EXISTENCE CHECK")
	mc, exists, err := orch.ensureCluster(ctx)
	if err != nil {
		return fmt.Errorf("ensure cluster: %w", err)
	}
	if !exists {
		logf("cluster %s/%s does not exist yet (greenfield rollout). Exiting no-op.", cfg.resourceGroup, cfg.clusterName)
		return nil
	}
	logf("cluster found: provisioning fields nodeResourceGroup=%q currentKubernetesVersion=%q", cfg.nodeRG, cfg.cpVersion)

	logBanner("PRE-FLIGHT ARM STATE")
	if err := orch.dumpPreflight(ctx); err != nil {
		logf("WARN: pre-flight dump partial: %v", err)
	}

	logBanner("DETECTION GUARDS")
	if cfg.skipGuards {
		logf("SKIP_GUARDS=true — bypassing detection guards")
	}
	targets, reason, err := orch.detect(ctx)
	if err != nil {
		return fmt.Errorf("detection: %w", err)
	}

	var confirmed, suspected []nodePoolTarget
	for _, target := range targets {
		if target.suspected {
			suspected = append(suspected, target)
		} else {
			confirmed = append(confirmed, target)
		}
	}
	if len(confirmed) == 0 && len(suspected) > 0 && !cfg.skipGuards && !cfg.dryRun {
		for _, target := range suspected {
			logBanner(fmt.Sprintf("FORCED EVIDENCE :: pool %s", target.name))
			confirmedTarget, err := forcedEvidencePath(ctx, cfg, orch, target)
			if err != nil {
				return err
			}
			if confirmedTarget != nil {
				confirmed = append(confirmed, *confirmedTarget)
			}
		}
	}
	targets = confirmed
	// Always exit no-op when no pools are confirmed broken, even if
	// SKIP_GUARDS=true. With no targets there is nothing to recreate, so
	// continuing past this point would only run Step 2 (maybeAbortLRO)
	// and Step 8 (reconcileTagPut) without acting on any pool, which can
	// abort an unrelated in-progress cluster operation or issue a
	// cluster PUT that the operator did not intend.
	if len(targets) == 0 {
		if cfg.skipGuards {
			logf("no selected node pools confirmed broken (%s) and SKIP_GUARDS=true; nothing to recreate. Exiting no-op.", reason)
		} else {
			logf("no selected node pools confirmed broken: %s. Exiting no-op.", reason)
		}
		return nil
	}
	logf("SELECTED BROKEN NODE POOLS: %d pool(s) confirmed", len(targets))
	for _, target := range targets {
		logf("  pool=%s role=%s nrpFailures=%d", target.name, cfg.nodePoolTag, target.nrpFailures)
	}

	if cfg.dryRun {
		logf("DRY_RUN=true — would remediate %d pool(s). Exiting no-op.", len(targets))
		return nil
	}
	if cfg.cpVersion == "" {
		return errors.New("currentKubernetesVersion empty after guards passed; refusing to act")
	}

	logBanner("KUBECONFIG BOOTSTRAP")
	if err := orch.bootstrapKube(ctx, mc); err != nil {
		return fmt.Errorf("bootstrap kube client: %w", err)
	}

	logBanner("PRE-ACTION STATE")
	if err := orch.dumpPreflight(ctx); err != nil {
		logf("WARN: pre-action dump partial: %v", err)
	}

	logBanner("STEP 1 :: snapshot selected node pools")
	for _, target := range targets {
		if _, err := orch.snapshotPool(ctx, target.name); err != nil {
			return fmt.Errorf("snapshot %s: %w", target.name, err)
		}
	}

	logBanner("STEP 2 :: abort long-stuck cluster LRO if any")
	proceed, err := orch.maybeAbortLRO(ctx)
	if err != nil {
		return fmt.Errorf("abort LRO: %w", err)
	}
	if !proceed {
		logf("active LRO is younger than %dm; refusing to fight an in-progress op. Exiting no-op.", lroAbortAgeMin)
		return nil
	}

	logBanner("STEP 2b :: re-check detection guards after LRO handling")
	targets, reason, err = orch.detect(ctx)
	if err != nil {
		return fmt.Errorf("post-LRO detection: %w", err)
	}
	var postConfirmed []nodePoolTarget
	for _, target := range targets {
		if !target.suspected {
			postConfirmed = append(postConfirmed, target)
		}
	}
	targets = postConfirmed
	// Same rationale as the pre-LRO recheck: if nothing is confirmed
	// broken after the LRO handling, there is no pool to recreate and we
	// must not fall through to Step 8 reconcileTagPut. Apply the no-op
	// exit regardless of SKIP_GUARDS.
	if len(targets) == 0 {
		if cfg.skipGuards {
			logf("no selected node pools confirmed broken after LRO handling (%s) and SKIP_GUARDS=true; nothing to recreate. Exiting no-op.", reason)
		} else {
			logf("no selected node pools confirmed broken after LRO handling: %s. Exiting no-op.", reason)
		}
		return nil
	}
	logf("selected node pools still confirmed broken after LRO handling: %d", len(targets))

	for i, target := range targets {
		logBanner(fmt.Sprintf("REMEDIATE POOL %d/%d :: %s", i+1, len(targets), target.name))
		// Per-pool atomicity: any leftover temp pool for this target
		// from a previous crashed run must be adopted (deleted or fail
		// closed) BEFORE we touch the source pool, and the entire
		// adopt + snapshot + recreate sequence below must complete
		// for this target before we move on to the next one in the
		// loop. The orchestrator primitives below are all synchronous
		// (LRO pollers wait via PollUntilDone), so the loop is
		// strictly sequential per target by construction.
		if err := orch.adoptLeftoverTempPool(ctx, target); err != nil {
			return err
		}
		live, err := orch.snapshotPool(ctx, target.name)
		if err != nil {
			return fmt.Errorf("post-LRO snapshot %s: %w", target.name, err)
		}
		if err := remediatePool(ctx, orch, target, live); err != nil {
			return err
		}
	}

	logBanner("STEP 8 :: no-op reconcile via tag update")
	if err := orch.reconcileTagPut(ctx); err != nil {
		return fmt.Errorf("tag reconcile: %w", err)
	}

	logBanner("DONE")
	if err := orch.dumpPostflight(ctx); err != nil {
		logf("WARN: post-flight dump partial: %v", err)
	}
	return nil
}

// forcedEvidencePath triggers a temporary scale-up on the live target
// pool so the AKS RP attempts a fresh VMSS write. It then polls the
// activity log for NRP-KVS Failed events, aborts the LRO it started,
// and restores the original pool spec before deciding whether to act.
//
// Returns (*nodePoolTarget, error). A non-nil target means evidence
// reached the threshold and the caller should proceed with the
// recreate flow on the returned (now-confirmed) target. A nil target
// with no error means the trigger was inconclusive (the cluster is
// wedged for a different reason or the trigger itself failed) and
// the caller should treat this pool as a no-op.
func forcedEvidencePath(ctx context.Context, cfg *config, orch orchestrator, target nodePoolTarget) (*nodePoolTarget, error) {
	logf("initial NRP-KVS storm check saw no evidence for pool %s in last %dm", target.name, cfg.windowMin)

	// triggerPoolReconcile builds a PUT body via agentPoolForScaleUpTrigger,
	// which pins OrchestratorVersion to cfg.cpVersion. An empty cpVersion
	// would either be rejected by AKS or, worse, silently sent as "" — and
	// the main flow already refuses to act on an empty cpVersion further
	// down. Treat empty cpVersion as inconclusive here so we do not issue
	// an unnecessary (and potentially invalid) write during the probe.
	if cfg.cpVersion == "" {
		logf("WARN: cpVersion empty; skipping forced-evidence trigger for %s (cannot build a safe scale-up PUT)", target.name)
		return nil, nil
	}

	live, err := orch.snapshotPool(ctx, target.name)
	if err != nil {
		return nil, fmt.Errorf("forced evidence snapshot %s: %w", target.name, err)
	}

	if err := orch.triggerPoolReconcile(ctx, target, live); err != nil {
		logf("WARN: triggerPoolReconcile for %s failed: %v; treating as no-op", target.name, err)
		return nil, nil
	}

	logf("triggered pool %s reconcile; polling activity log every %ds for up to %dm", target.name, forcedEvidencePollIntervalSec, cfg.forcedEvidenceTimeoutMin)
	hits, pollErr := orch.pollForNRPEvidence(
		ctx,
		target,
		time.Duration(cfg.forcedEvidenceTimeoutMin)*time.Minute,
		forcedEvidencePollIntervalSec*time.Second,
		forcedEvidenceWindowMin,
		cfg.forcedEvidenceThreshold,
	)

	abortCtx, abortCancel := context.WithTimeout(context.Background(), forcedEvidenceAbortTimeoutMin*time.Minute)
	defer abortCancel()
	if abortErr := orch.abortPoolReconcile(abortCtx, target.name); abortErr != nil {
		logf("WARN: abortPoolReconcile for %s failed: %v", target.name, abortErr)
	}
	// Restore runs on its own longer context: an AKS pool PUT + LRO can
	// legitimately take 5-10m, so sharing the abort budget would risk
	// skipping the restore when the abort consumed most of those 8m and
	// leaving the pool one node larger than the original snapshot.
	restoreCtx, restoreCancel := context.WithTimeout(context.Background(), forcedEvidenceRestoreTimeoutMin*time.Minute)
	defer restoreCancel()
	if restoreErr := orch.restorePoolSpec(restoreCtx, target, live); restoreErr != nil {
		logf("WARN: restorePoolSpec for %s failed: %v", target.name, restoreErr)
	}

	if pollErr != nil {
		return nil, fmt.Errorf("poll for NRP evidence on %s: %w", target.name, pollErr)
	}
	if hits < cfg.forcedEvidenceThreshold {
		logf("forced evidence inconclusive for %s: only %d NRP failures < %d after %dm", target.name, hits, cfg.forcedEvidenceThreshold, cfg.forcedEvidenceTimeoutMin)
		return nil, nil
	}
	logf("forced evidence confirmed NRP-KVS for %s (%d hits >= threshold %d)", target.name, hits, cfg.forcedEvidenceThreshold)
	target.nrpFailures = hits
	target.suspected = false
	return &target, nil
}

// remediatePool is the single remediation flow applied to every confirmed
// target pool, regardless of AKS Mode (System/User). It always brings up
// a deterministic per-target temp pool first — waiting for the ARM LRO
// AND the new k8s node to be Ready — before touching the broken pool,
// so the cluster never loses the target pool's capacity "on faith".
func remediatePool(ctx context.Context, orch orchestrator, target nodePoolTarget, live *armcs.AgentPool) error {
	tmpName := tempPoolName(target.name)
	logf("pool %s: creating temp %s, then drain+delete+recreate", target.name, tmpName)
	if err := orch.addTempPool(ctx, target, live); err != nil {
		return fmt.Errorf("add temp pool %s for %s: %w", tmpName, target.name, err)
	}
	if err := orch.drainPool(ctx, target.name, 5*time.Minute); err != nil {
		return fmt.Errorf("drain %s: %w", target.name, err)
	}
	if err := orch.deletePool(ctx, target.name); err != nil {
		return fmt.Errorf("delete %s: %w", target.name, err)
	}
	if err := orch.recreatePool(ctx, target.name, live); err != nil {
		return fmt.Errorf("recreate %s: %w", target.name, err)
	}
	if err := orch.drainPool(ctx, tmpName, 3*time.Minute); err != nil {
		logf("WARN: temp pool %s drain returned: %v (continuing to delete)", tmpName, err)
	}
	if err := orch.deletePool(ctx, tmpName); err != nil {
		return fmt.Errorf("delete temp pool %s: %w", tmpName, err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// config
// ---------------------------------------------------------------------------

type config struct {
	clusterName              string
	resourceGroup            string
	subscriptionID           string
	nodeRG                   string
	cpVersion                string
	nodePoolTag              string
	threshold                int
	windowMin                int
	forcedEvidenceTimeoutMin int
	forcedEvidenceThreshold  int
	dryRun                   bool
	skipGuards               bool
}

// parseEnvConfig builds a config from environment variables only. It does
// not call any external tools or APIs, which makes it safe to unit-test.
func parseEnvConfig(env func(string) string) (*config, error) {
	c := &config{
		clusterName:              env("CLUSTER_NAME"),
		resourceGroup:            env("RESOURCE_GROUP"),
		subscriptionID:           env("SUBSCRIPTION_ID"),
		nodePoolTag:              env("NODEPOOL_TAG"),
		threshold:                defaultThreshold,
		windowMin:                defaultWindowMin,
		forcedEvidenceTimeoutMin: defaultForcedEvidenceTimeoutMin,
		forcedEvidenceThreshold:  defaultForcedEvidenceThreshold,
	}
	if c.clusterName == "" {
		return nil, errors.New("CLUSTER_NAME is required")
	}
	if c.resourceGroup == "" {
		return nil, errors.New("RESOURCE_GROUP is required")
	}
	if c.subscriptionID == "" {
		return nil, errors.New("SUBSCRIPTION_ID is required")
	}
	if c.nodePoolTag == "" {
		return nil, errors.New("NODEPOOL_TAG is required")
	}
	if v := env("NRP_FAIL_THRESHOLD"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return nil, fmt.Errorf("NRP_FAIL_THRESHOLD: %w", err)
		}
		if n <= 0 {
			return nil, fmt.Errorf("NRP_FAIL_THRESHOLD must be > 0, got %d", n)
		}
		c.threshold = n
	}
	if v := env("NRP_FAIL_WINDOW_MIN"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return nil, fmt.Errorf("NRP_FAIL_WINDOW_MIN: %w", err)
		}
		if n <= 0 {
			return nil, fmt.Errorf("NRP_FAIL_WINDOW_MIN must be > 0, got %d", n)
		}
		c.windowMin = n
	}
	if v := env("FORCED_EVIDENCE_TIMEOUT_MIN"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return nil, fmt.Errorf("FORCED_EVIDENCE_TIMEOUT_MIN: %w", err)
		}
		if n <= 0 {
			return nil, fmt.Errorf("FORCED_EVIDENCE_TIMEOUT_MIN must be > 0, got %d", n)
		}
		c.forcedEvidenceTimeoutMin = n
	}
	if v := env("FORCED_EVIDENCE_THRESHOLD"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return nil, fmt.Errorf("FORCED_EVIDENCE_THRESHOLD: %w", err)
		}
		if n <= 0 {
			return nil, fmt.Errorf("FORCED_EVIDENCE_THRESHOLD must be > 0, got %d", n)
		}
		c.forcedEvidenceThreshold = n
	}
	if v := strings.ToLower(strings.TrimSpace(env("DRY_RUN"))); v == "true" || v == "1" || v == "yes" {
		c.dryRun = true
	}
	if v := strings.ToLower(strings.TrimSpace(env("SKIP_GUARDS"))); v == "true" || v == "1" || v == "yes" {
		c.skipGuards = true
	}
	return c, nil
}

func loadConfig() (*config, error) {
	return parseEnvConfig(os.Getenv)
}

func (c *config) logEnv() {
	logf("CLUSTER_NAME=%s", c.clusterName)
	logf("RESOURCE_GROUP=%s", c.resourceGroup)
	logf("SUBSCRIPTION_ID=%s", c.subscriptionID)
	logf("NODEPOOL_TAG=%s", c.nodePoolTag)
	logf("NRP_FAIL_THRESHOLD=%d", c.threshold)
	logf("NRP_FAIL_WINDOW_MIN=%d", c.windowMin)
	logf("FORCED_EVIDENCE_TIMEOUT_MIN=%d", c.forcedEvidenceTimeoutMin)
	logf("FORCED_EVIDENCE_THRESHOLD=%d", c.forcedEvidenceThreshold)
	logf("DRY_RUN=%t", c.dryRun)
	logf("SKIP_GUARDS=%t", c.skipGuards)
}

// ---------------------------------------------------------------------------
// clients
// ---------------------------------------------------------------------------

type clients struct {
	cfg          *config
	cred         azcore.TokenCredential
	pools        *armcs.AgentPoolsClient
	cluster      *armcs.ManagedClustersClient
	activityLogs *armmonitor.ActivityLogsClient
	tags         *armresources.TagsClient
	kube         kubernetes.Interface
}

// newAzureClients sets up Azure SDK clients only. Kubernetes client is
// deferred until we have confirmed the cluster exists (see bootstrapKube),
// because on a greenfield rollout there is nothing to talk to yet.
func newAzureClients(cfg *config) (*clients, error) {
	// RequireAzureTokenCredentials: true restricts the chain to MSI /
	// SP / workload-identity credentials, matching the convention used
	// by backend, sessiongate and admin-client. We never want this
	// binary to fall back to interactive sign-in inside EV2.
	cred, err := azidentity.NewDefaultAzureCredential(&azidentity.DefaultAzureCredentialOptions{
		RequireAzureTokenCredentials: true,
	})
	if err != nil {
		return nil, fmt.Errorf("azidentity: %w", err)
	}
	clientFactory, err := armcs.NewClientFactory(cfg.subscriptionID, cred, nil)
	if err != nil {
		return nil, fmt.Errorf("arm containerservice factory: %w", err)
	}
	activityLogs, err := armmonitor.NewActivityLogsClient(cfg.subscriptionID, cred, nil)
	if err != nil {
		return nil, fmt.Errorf("arm monitor activity logs client: %w", err)
	}
	tags, err := armresources.NewTagsClient(cfg.subscriptionID, cred, nil)
	if err != nil {
		return nil, fmt.Errorf("arm resources tags client: %w", err)
	}
	return &clients{
		cfg:          cfg,
		cred:         cred,
		pools:        clientFactory.NewAgentPoolsClient(),
		cluster:      clientFactory.NewManagedClustersClient(),
		activityLogs: activityLogs,
		tags:         tags,
	}, nil
}

// ensureCluster does an ARM Get on the managed cluster. If the cluster
// does not exist (HTTP 404), returns (zero, false, nil) so the caller
// can no-op exit cleanly. On any other error returns (zero, false, error).
// On success, it records nodeRG and cpVersion when ARM has populated them,
// but deliberately does not require them yet: partially-created clusters can
// be returned without these fields, and evalClusterState should reject Creating as
// a no-op guard failure instead of failing the Shell step.
func (c *clients) ensureCluster(ctx context.Context) (armcs.ManagedCluster, bool, error) {
	resp, err := c.cluster.Get(ctx, c.cfg.resourceGroup, c.cfg.clusterName, nil)
	if err != nil {
		if isNotFoundErr(err) {
			return armcs.ManagedCluster{}, false, nil
		}
		return armcs.ManagedCluster{}, false, fmt.Errorf("cluster get: %w", err)
	}
	mc := resp.ManagedCluster
	if mc.Properties != nil {
		if mc.Properties.NodeResourceGroup != nil {
			c.cfg.nodeRG = *mc.Properties.NodeResourceGroup
		}
		if mc.Properties.CurrentKubernetesVersion != nil {
			c.cfg.cpVersion = *mc.Properties.CurrentKubernetesVersion
		}
	}
	return mc, true, nil
}

// bootstrapKube builds a Kubernetes client using the shared sessiongate AKS
// REST config helper. That helper injects an Azure token per request, relying
// on the Azure credential's internal cache/refresh behavior, so long-running
// runs don't depend on a single static bearer token.
func (c *clients) bootstrapKube(ctx context.Context, mc armcs.ManagedCluster) error {
	if mc.ID == nil || *mc.ID == "" {
		return errors.New("cluster ARM ID empty; cannot bootstrap kube client")
	}
	restCfg, err := mcclient.GetAKSRESTConfig(ctx, *mc.ID, c.cred)
	if err != nil {
		return fmt.Errorf("AKS REST config: %w", err)
	}
	kc, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		return fmt.Errorf("kubernetes client: %w", err)
	}
	c.kube = kc
	return nil
}

// isNotFoundErr reports whether err is an azcore HTTP 404 ResponseError
// or wraps one. Used to distinguish "cluster does not exist yet" from
// genuine ARM failures (auth, throttling, transient 500s).
func isNotFoundErr(err error) bool {
	if err == nil {
		return false
	}
	var re *azcore.ResponseError
	if errors.As(err, &re) {
		return re.StatusCode == http.StatusNotFound
	}
	return false
}

// ---------------------------------------------------------------------------
// pre/post-flight dumps
// ---------------------------------------------------------------------------

func (c *clients) dumpPreflight(ctx context.Context) error {
	logf("--- nodepools ---")
	if err := c.dumpNodePools(ctx); err != nil {
		return err
	}
	logf("--- cluster ---")
	if err := c.dumpCluster(ctx); err != nil {
		return err
	}
	logf("--- k8s nodes (all) ---")
	c.dumpKubeNodes(ctx, "")
	return nil
}

func (c *clients) dumpPostflight(ctx context.Context) error {
	logf("--- final nodepools ---")
	if err := c.dumpNodePools(ctx); err != nil {
		logf("WARN: final nodepools dump failed: %v", err)
	}
	logf("--- final cluster ---")
	if err := c.dumpCluster(ctx); err != nil {
		logf("WARN: final cluster dump failed: %v", err)
	}
	logf("--- final k8s nodes (all) ---")
	c.dumpKubeNodes(ctx, "")
	logf("--- post-flight: residual NRP failures (informational) ---")
	out, err := c.activityLogJSON(ctx, c.cfg.nodeRG, "10m")
	if err == nil {
		hits, parseErr := countNRPFailures(out, "")
		if parseErr != nil {
			logf("WARN: failed to parse post-flight activity log: %v", parseErr)
			return nil
		}
		logf("Failed VMSS-write events on %s in last 10m: %d", c.cfg.nodeRG, hits)
		if hits > 0 {
			ids, parseErr := nrpResourceIDs(out)
			if parseErr != nil {
				logf("WARN: failed to parse post-flight NRP resource IDs: %v", parseErr)
				return nil
			}
			for _, id := range ids {
				logf("    %s", id)
			}
		}
	} else {
		logf("WARN: failed to query post-flight activity log: %v", err)
	}
	return nil
}

func (c *clients) dumpNodePools(ctx context.Context) error {
	pager := c.pools.NewListPager(c.cfg.resourceGroup, c.cfg.clusterName, nil)
	seen := 0
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return fmt.Errorf("list nodepools: %w", err)
		}
		for _, p := range page.Value {
			seen++
			name := ""
			if p != nil {
				name = strDeref(p.Name)
			}
			if p == nil || p.Properties == nil {
				logf("nodepool name=%s properties=<nil>", name)
				continue
			}
			props := p.Properties
			logf("nodepool name=%s mode=%s state=%s count=%s min=%s max=%s k8s=%s vmSize=%s",
				name, ptrValue(props.Mode), strDeref(props.ProvisioningState), ptrValue(props.Count),
				ptrValue(props.MinCount), ptrValue(props.MaxCount), strDeref(props.CurrentOrchestratorVersion), strDeref(props.VMSize))
		}
	}
	if seen == 0 {
		logf("nodepools: none returned")
	}
	return nil
}

func (c *clients) dumpCluster(ctx context.Context) error {
	resp, err := c.cluster.Get(ctx, c.cfg.resourceGroup, c.cfg.clusterName, nil)
	if err != nil {
		return fmt.Errorf("cluster get: %w", err)
	}
	if resp.Properties == nil {
		logf("cluster properties=<nil>")
		return nil
	}
	props := resp.Properties
	power := ""
	if props.PowerState != nil {
		power = ptrValue(props.PowerState.Code)
	}
	logf("cluster prov=%s power=%s cpVer=%s target=%s nodeRG=%s",
		strDeref(props.ProvisioningState), power, strDeref(props.CurrentKubernetesVersion),
		strDeref(props.KubernetesVersion), strDeref(props.NodeResourceGroup))
	return nil
}

func (c *clients) dumpKubeNodes(ctx context.Context, pool string) {
	if c.kube == nil {
		logf("WARN: kube client not bootstrapped; skipping k8s node dump")
		return
	}
	selector := ""
	if pool != "" {
		selector = "agentpool=" + pool
	}
	nodes, err := c.kube.CoreV1().Nodes().List(ctx, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		logf("WARN: list k8s nodes selector=%q: %v", selector, err)
		return
	}
	if len(nodes.Items) == 0 {
		logf("k8s nodes selector=%q: none returned", selector)
		return
	}
	for _, n := range nodes.Items {
		logf("node name=%s agentpool=%s ready=%t schedulableReady=%t unschedulable=%t deleting=%t kubelet=%s internalIP=%s",
			n.Name, n.Labels["agentpool"], isNodeReady(&n), isNodeSchedulableReady(&n), n.Spec.Unschedulable,
			n.DeletionTimestamp != nil, n.Status.NodeInfo.KubeletVersion, nodeInternalIP(&n))
	}
}

func nodeInternalIP(n *corev1.Node) string {
	if n == nil {
		return ""
	}
	for _, a := range n.Status.Addresses {
		if a.Type == corev1.NodeInternalIP {
			return a.Address
		}
	}
	return ""
}

// ---------------------------------------------------------------------------
// detection
// ---------------------------------------------------------------------------

// evalNRPStorm reports whether NRP-KVS failure count exceeds the threshold.
func evalNRPStorm(failures, threshold int) (bool, string) {
	if threshold <= 0 {
		return false, fmt.Sprintf("NRP-KVS storm FAIL: threshold=%d (invalid)", threshold)
	}
	if failures < threshold {
		return false, fmt.Sprintf("NRP-KVS storm FAIL: only %d NRP failures < %d", failures, threshold)
	}
	return true, ""
}

// evalClusterState reports whether the cluster is in a state where we can act.
//
// Acceptable:
//   - Succeeded / Canceled / Failed: settled, no LRO to fight, free to PUT.
//   - Updating / Upgrading: an LRO is running. This is the NRP-KVS wedge
//     signature (AROSLSRE-880, INT 2026-05-16..18) — the upgrade LRO
//     retries forever and the cluster sits in Updating for days. We
//     accept this state at the guard level and let step 2
//     (maybeAbortLRO) decide whether to abort (>= 30 min old) or
//     no-op exit (younger LRO, might still be healthy).
//
// Rejected:
//   - Creating: the cluster isn't fully provisioned yet; the system
//     pool we'd want to recreate doesn't exist in a stable form.
//   - Deleting: someone is tearing the cluster down; do not interfere.
//   - empty / unknown future states: be conservative.
func evalClusterState(provisioningState string) (bool, string) {
	switch provisioningState {
	case "Succeeded", "Canceled", "Failed", "Updating", "Upgrading":
		return true, ""
	case "Creating":
		return false, "cluster state FAIL: provisioningState=\"Creating\" (cluster not fully provisioned)"
	case "Deleting":
		return false, "cluster state FAIL: provisioningState=\"Deleting\" (cluster is being torn down)"
	case "":
		return false, "cluster state FAIL: provisioningState is empty"
	}
	return false, fmt.Sprintf("cluster state FAIL: provisioningState=%q is not a recognized recoverable state", provisioningState)
}

// evalNonTargetPoolsHealthy reports whether every non-temp, non-wedged
// pool has count > 0. Wedge-state pools are skipped because the wedge
// itself can legitimately drive count to zero — it is precisely the
// remediation target we expect to act on. Temp pools created by this
// binary (purpose=temp-recreate-broken-pools) are also skipped.
func evalNonTargetPoolsHealthy(pools []*armcs.AgentPool) (bool, string) {
	for _, p := range pools {
		if p == nil || p.Name == nil || p.Properties == nil {
			continue
		}
		if isTempPool(p) {
			continue
		}
		provState := strDeref(p.Properties.ProvisioningState)
		if pass, _ := evalPoolWedge(provState); pass {
			// Pool is itself in a wedge-compatible state; skip the
			// healthy-count check for it.
			continue
		}
		cnt := int32(0)
		if p.Properties.Count != nil {
			cnt = *p.Properties.Count
		}
		if cnt == 0 {
			return false, fmt.Sprintf("cluster pools FAIL: pool %q has count=0 and is not in a wedge state", *p.Name)
		}
	}
	return true, ""
}

// evalPoolWedge reports whether a single pool's provisioningState is in
// a wedge-compatible state — a positive per-pool signal that refines
// the cluster-wide NRP-KVS storm check.
//
// Accepts:
//   - Failed   — RP gave up retrying the VMSS write chain.
//   - Canceled — operator already aborted the parent LRO.
//   - Updating / Upgrading — the cluster LRO is still retrying the
//     pool update forever (AROSLSRE-880 / INT
//     2026-05-16..18 signature). Guard 1 confirms
//     that the retries are NRP errors and not a
//     healthy upgrade.
//
// Rejects:
//   - Succeeded — pool is healthy; no wedge.
//   - Creating  — pool not fully created yet; do not interfere.
//   - Deleting  — pool being torn down; do not interfere.
//   - empty / unknown — fail conservatively.
func evalPoolWedge(provState string) (bool, string) {
	switch provState {
	case "Failed", "Canceled", "Updating", "Upgrading":
		return true, ""
	case "Succeeded":
		return false, "pool wedge FAIL: provisioningState=\"Succeeded\" (no wedge)"
	case "Creating":
		return false, "pool wedge FAIL: provisioningState=\"Creating\" (not fully created)"
	case "Deleting":
		return false, "pool wedge FAIL: provisioningState=\"Deleting\" (being torn down)"
	case "":
		return false, "pool wedge FAIL: provisioningState is empty"
	}
	return false, fmt.Sprintf("pool wedge FAIL: provisioningState=%q is not a recognized wedge-compatible state", provState)
}

func (c *clients) detect(ctx context.Context) ([]nodePoolTarget, string, error) {
	mc, err := c.cluster.Get(ctx, c.cfg.resourceGroup, c.cfg.clusterName, nil)
	if err != nil {
		return nil, "", fmt.Errorf("cluster state get: %w", err)
	}
	cs := ""
	if mc.Properties != nil && mc.Properties.ProvisioningState != nil {
		cs = *mc.Properties.ProvisioningState
	}
	logf("cluster state :: provisioningState=%s (accept: Succeeded/Canceled/Failed/Updating/Upgrading; reject: Creating/Deleting/unknown)", cs)
	if pass, reason := evalClusterState(cs); !pass {
		return nil, reason, nil
	}
	logf("cluster state PASS")

	pager := c.pools.NewListPager(c.cfg.resourceGroup, c.cfg.clusterName, nil)
	var allPools []*armcs.AgentPool
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, "", fmt.Errorf("list pools: %w", err)
		}
		allPools = append(allPools, page.Value...)
	}

	pass, reason := evalNonTargetPoolsHealthy(allPools)
	if !pass {
		return nil, reason, nil
	}
	logf("cluster safety PASS")

	// First pass: classify pools by role match and wedge state without
	// touching Activity Log. This honors the file-header promise to
	// short-circuit on cheap ARM checks before issuing the activity-log
	// query (which costs an ARM List call and a throttling slot per run).
	type wedgedPool struct {
		target    nodePoolTarget
		provState string
	}
	var selected []nodePoolTarget
	var wedged []wedgedPool
	for _, p := range allPools {
		if p == nil || p.Name == nil || p.Properties == nil {
			continue
		}
		if isTempPool(p) {
			continue
		}
		if poolRoleTag(p) != c.cfg.nodePoolTag {
			continue
		}
		name := *p.Name
		vmssPrefix := poolVMSSPrefix(name)
		selected = append(selected, nodePoolTarget{name: name, vmssPrefix: vmssPrefix})
		provState := strDeref(p.Properties.ProvisioningState)
		if ok, _ := evalPoolWedge(provState); !ok {
			continue
		}
		wedged = append(wedged, wedgedPool{
			target:    nodePoolTarget{name: name, vmssPrefix: vmssPrefix},
			provState: provState,
		})
	}
	if len(selected) == 0 {
		return nil, fmt.Sprintf("no node pools found with %s=%q", nodePoolRoleLabel, c.cfg.nodePoolTag), nil
	}
	if len(wedged) == 0 {
		return nil, fmt.Sprintf("no selected node pools with %s=%q are in wedge-compatible state", nodePoolRoleLabel, c.cfg.nodePoolTag), nil
	}

	// Only now do we pay for the Activity Log list.
	out, err := c.activityLogJSON(ctx, c.cfg.nodeRG, fmt.Sprintf("%dm", c.cfg.windowMin))
	if err != nil {
		return nil, "", fmt.Errorf("NRP-KVS storm activity-log query failed: %w", err)
	}

	var targets []nodePoolTarget
	var confirmedCount int
	for _, w := range wedged {
		logf("pool %s: role=%s provisioningState=%s — wedge-compatible", w.target.name, c.cfg.nodePoolTag, w.provState)
		hits, err := countNRPFailures(out, w.target.vmssPrefix)
		if err != nil {
			return nil, "", fmt.Errorf("NRP failure count for pool %s: %w", w.target.name, err)
		}
		logf("pool %s: NRP-KVS failures on %s*: %d (threshold %d)", w.target.name, w.target.vmssPrefix, hits, c.cfg.threshold)
		target := w.target
		target.nrpFailures = hits
		if hits < c.cfg.threshold && !c.cfg.skipGuards {
			target.suspected = true
		} else {
			confirmedCount++
		}
		targets = append(targets, target)
	}
	// When every candidate is below threshold the caller will either route
	// through the forced-evidence path or log a no-op exit. Surface a
	// non-empty reason so the operator log line is actionable rather than
	// trailing an empty period ("...confirmed broken: .").
	if confirmedCount == 0 {
		return targets, fmt.Sprintf("%d candidate pool(s) all below NRP failure threshold %d in last %dm", len(targets), c.cfg.threshold, c.cfg.windowMin), nil
	}
	return targets, "", nil
}

// tempPoolSourceID returns the value of the tempPoolSourceTag on p, or
// "" if the tag is absent. The tag is always set by buildTempAgentPool
// for pools created by this binary; an empty value on a pool that
// otherwise carries tempPoolPurposeTag is treated by
// adoptLeftoverTempPool as a defensive failure.
func tempPoolSourceID(p *armcs.AgentPool) string {
	if p == nil || p.Properties == nil || p.Properties.Tags == nil {
		return ""
	}
	v := p.Properties.Tags[tempPoolSourceTag]
	if v == nil {
		return ""
	}
	return *v
}

// poolNameFromARMID parses the trailing agent-pool name segment from
// an AKS agent pool ARM resource ID, e.g.
//
//	/subscriptions/<sub>/resourceGroups/<rg>/providers/
//	  Microsoft.ContainerService/managedClusters/<cluster>/agentPools/<pool>
//
// Returns an error if the input does not contain a single
// "/agentPools/<name>" segment with a non-empty name and no trailing
// path segments. adoptLeftoverTempPool surfaces that error verbatim
// to the operator rather than guessing at recovery.
func poolNameFromARMID(id string) (string, error) {
	const sep = "/agentPools/"
	i := strings.LastIndex(id, sep)
	if i < 0 {
		return "", fmt.Errorf("missing %q segment", sep)
	}
	name := id[i+len(sep):]
	if name == "" {
		return "", errors.New("trailing pool name segment empty")
	}
	if strings.Contains(name, "/") {
		return "", fmt.Errorf("trailing pool name segment %q contains a path separator", name)
	}
	return name, nil
}

// adoptLeftoverTempPool handles a leftover temp pool from a previous
// crashed or aborted run of this binary for the given target. It is
// called per-target from runWith's remediation loop and runs to
// completion (including the temp-pool Delete LRO via PollUntilDone)
// before the loop touches the source pool, so each target is processed
// atomically: nothing in the next iteration can run until this
// target's leftover (if any) is fully cleaned up.
//
// The temp pool name is deterministic (tempPoolName(target.name)), so
// a single Get suffices to detect a leftover. The decision matrix
// uses the tempPoolSourceTag (full ARM resource ID of the source pool)
// recorded by buildTempAgentPool:
//
//	no leftover (Get returns 404)
//	    -> nothing to do.
//	leftover present but missing the purpose tag
//	    -> fail closed (something else owns this name).
//	leftover present but no/malformed source tag
//	    -> fail closed (defensive).
//	leftover's source tag points to a pool other than target.name
//	    -> fail closed (rename/collision; manual review).
//	source pool exists and ProvisioningState == "Succeeded"
//	    -> delete the temp pool. The previous run completed the
//	       recreate but crashed before deleting the temp.
//	source pool exists and ProvisioningState in {Failed, Canceled}
//	    -> delete the temp pool. The target is wedged (likely an
//	       NRP-KVS re-wedge or a crash during recreate) and is being
//	       remediated this run; the temp will be created fresh by the
//	       upcoming addTempPool. Failing closed here would block the
//	       binary's primary job (auto-heal a wedged pool).
//	source pool exists and ProvisioningState in {Updating, Upgrading}
//	    -> fail closed. An active LRO is in flight on the source;
//	       continuing would race the in-progress operation. Re-run
//	       after the LRO settles.
//	source pool missing
//	    -> fail closed (CRITICAL). The previous run deleted the
//	       source but never recreated it; manual recovery required.
//	source pool in any other ProvisioningState
//	    -> fail closed (defensive).
//
// adoptLeftoverTempPool never resumes a crashed mid-flight
// remediation. Remediation requires the live source pool snapshot
// (VMSize, OSDiskSizeGB, etc.), which is in-memory only and is lost
// on crash; reconstructing it from the temp pool's tags would risk
// picking wrong compute sizing across stg/prod.
func (c *clients) adoptLeftoverTempPool(ctx context.Context, target nodePoolTarget) error {
	tmpName := tempPoolName(target.name)
	tempResp, err := c.pools.Get(ctx, c.cfg.resourceGroup, c.cfg.clusterName, tmpName, nil)
	if isNotFoundErr(err) {
		logf("adopt: no leftover temp pool %q for target %q", tmpName, target.name)
		return nil
	}
	if err != nil {
		return fmt.Errorf("adopt: get temp pool %q for target %q: %w", tmpName, target.name, err)
	}
	temp := &tempResp.AgentPool

	if !isTempPool(temp) {
		return fmt.Errorf("adopt: pool %q exists at the deterministic temp name for target %q but lacks the %q=%q tag; refusing to act, please inspect and clean up manually then re-run", tmpName, target.name, tempPoolPurposeTag, tempPoolPurposeValue)
	}
	srcID := tempPoolSourceID(temp)
	if srcID == "" {
		return fmt.Errorf("adopt: leftover temp pool %q for target %q has no %q tag; refusing to act, please clean up manually then re-run", tmpName, target.name, tempPoolSourceTag)
	}
	srcName, err := poolNameFromARMID(srcID)
	if err != nil {
		return fmt.Errorf("adopt: leftover temp pool %q for target %q has malformed %q tag %q: %w; manual cleanup required", tmpName, target.name, tempPoolSourceTag, srcID, err)
	}
	if srcName != target.name {
		return fmt.Errorf("adopt: leftover temp pool %q embeds source pool %q but the current target with the matching deterministic name is %q (rename or hash collision?); refusing to act, manual review required", tmpName, srcName, target.name)
	}

	srcResp, err := c.pools.Get(ctx, c.cfg.resourceGroup, c.cfg.clusterName, srcName, nil)
	if isNotFoundErr(err) {
		return fmt.Errorf("adopt: leftover temp pool %q references source pool %q which no longer exists; CRITICAL: previous run deleted the source but never recreated it, manual recovery required (re-create source pool or delete temp pool) then re-run", tmpName, srcName)
	}
	if err != nil {
		return fmt.Errorf("adopt: get source pool %q for leftover temp %q: %w", srcName, tmpName, err)
	}
	srcState := ""
	if srcResp.Properties != nil {
		srcState = strDeref(srcResp.Properties.ProvisioningState)
	}

	switch srcState {
	case "Succeeded":
		logf("adopt: leftover temp pool %q source %q is Succeeded; previous run finished the recreate but crashed before cleanup. Deleting temp pool.", tmpName, srcName)
	case "Failed", "Canceled":
		logf("adopt: leftover temp pool %q source %q is %q; target is wedged and will be remediated this run. Deleting old temp pool so the upcoming addTempPool starts clean.", tmpName, srcName, srcState)
	case "Updating", "Upgrading":
		return fmt.Errorf("adopt: leftover temp pool %q source %q is in provisioning state %q (active LRO in flight); refusing to race, please re-run after the LRO settles", tmpName, srcName, srcState)
	default:
		return fmt.Errorf("adopt: leftover temp pool %q source %q is in unexpected provisioning state %q; refusing to auto-adopt, please inspect manually then re-run", tmpName, srcName, srcState)
	}

	if err := c.deletePool(ctx, tmpName); err != nil {
		return fmt.Errorf("adopt: delete leftover temp pool %q (source %q): %w", tmpName, srcName, err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// step 1 :: snapshot
// ---------------------------------------------------------------------------

func (c *clients) snapshotPool(ctx context.Context, poolName string) (*armcs.AgentPool, error) {
	resp, err := c.pools.Get(ctx, c.cfg.resourceGroup, c.cfg.clusterName, poolName, nil)
	if err != nil {
		return nil, err
	}
	live := resp.AgentPool
	if live.Properties == nil {
		return nil, fmt.Errorf("pool %s has nil properties", poolName)
	}
	if live.Properties.VMSize == nil || *live.Properties.VMSize == "" {
		return nil, fmt.Errorf("pool %s has no VMSize; refusing to act", poolName)
	}
	if live.Properties.VnetSubnetID == nil || *live.Properties.VnetSubnetID == "" {
		return nil, fmt.Errorf("pool %s has no VnetSubnetID; refusing to act", poolName)
	}
	return &live, nil
}

// agentPoolForCreate produces a deep-copy of the snapshotted AgentPool with
// read-only fields and AKS-managed tags stripped, ready to feed into
// CreateOrUpdate. The input is never mutated.
//
// Read-only fields stripped (RP rejects user-supplied values):
//   - top-level: id, name, type
//   - properties: provisioningState, currentOrchestratorVersion,
//     nodeImageVersion, powerState, creationData, ETag
//
// orchestratorVersion is overwritten with the live cluster control-plane
// version to guarantee we never request a version downgrade.
//
// Tags prefixed `aks-managed-` are stripped (RP rejects user PUTs that
// contain them; they will be re-added by AKS).
func agentPoolForCreate(live *armcs.AgentPool, cpVersion string) (*armcs.AgentPool, error) {
	if live == nil {
		return nil, errors.New("agentPoolForCreate: nil input")
	}
	// Deep-copy via JSON round-trip so we never mutate the snapshot the
	// caller still holds. Slower than reflect-based copy, but bullet-proof
	// against future SDK shape changes.
	raw, err := json.Marshal(live)
	if err != nil {
		return nil, fmt.Errorf("agentPoolForCreate: marshal: %w", err)
	}
	var out armcs.AgentPool
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("agentPoolForCreate: unmarshal: %w", err)
	}

	out.ID = nil
	out.Name = nil
	out.Type = nil

	if out.Properties == nil {
		return nil, errors.New("agentPoolForCreate: nil properties after copy")
	}
	out.Properties.ProvisioningState = nil
	out.Properties.CurrentOrchestratorVersion = nil
	out.Properties.NodeImageVersion = nil
	out.Properties.PowerState = nil
	out.Properties.CreationData = nil
	out.Properties.ETag = nil
	// Pin to the live CP version so we never request a downgrade.
	v := cpVersion
	out.Properties.OrchestratorVersion = &v
	// Strip AKS-managed tags.
	if out.Properties.Tags != nil {
		out.Properties.Tags = cloneStringPtrMapWithoutPrefix(out.Properties.Tags, "aks-managed-")
	}
	return &out, nil
}

// agentPoolForScaleUpTrigger returns a sanitized PUT body that increments
// the target pool's Count (and MinCount / MaxCount when autoscale is
// enabled) by 1. Used by triggerPoolReconcile to force the AKS RP to
// schedule a fresh VMSS write — a no-op CreateOrUpdate with the same
// Count is rejected by AKS as "no changes detected".
func agentPoolForScaleUpTrigger(live *armcs.AgentPool, cpVersion string) (*armcs.AgentPool, error) {
	body, err := agentPoolForCreate(live, cpVersion)
	if err != nil {
		return nil, fmt.Errorf("agentPoolForScaleUpTrigger: %w", err)
	}
	if body.Properties.Count != nil {
		(*body.Properties.Count)++
	} else {
		body.Properties.Count = ptr(int32(2))
	}
	if body.Properties.EnableAutoScaling != nil && *body.Properties.EnableAutoScaling && body.Properties.MinCount != nil {
		(*body.Properties.MinCount)++
		if body.Properties.MaxCount != nil && *body.Properties.MinCount > *body.Properties.MaxCount {
			*body.Properties.MaxCount = *body.Properties.MinCount
		}
	}
	return body, nil
}

// ---------------------------------------------------------------------------
// step 2 :: maybe abort LRO
// ---------------------------------------------------------------------------

func isActiveClusterState(state string) bool {
	return state == "Updating" || state == "Upgrading"
}

// maybeAbortLRO age-gates and aborts a stuck cluster LRO without relying
// on `az aks operation show-latest` (that command requires the aks-preview
// extension, which is not available in EV2 runners).
//
// If the cluster is not in an active LRO state, there is nothing to abort.
// If it is active (Updating/Upgrading), we derive LRO age from the latest
// management-cluster write Started event in Activity Log. Once the event is
// at least 30 minutes old, the NRP-KVS retry storm has outlived healthy
// upgrade/scale behavior, so we abort via the typed AKS SDK
// BeginAbortLatestOperation. If the latest start is younger, return
// proceed=false so the caller exits 0 without racing a healthy operation.
func (c *clients) maybeAbortLRO(ctx context.Context) (bool, error) {
	clusterState := ""
	mc, err := c.cluster.Get(ctx, c.cfg.resourceGroup, c.cfg.clusterName, nil)
	if err != nil {
		return false, fmt.Errorf("cluster get before LRO inspection: %w", err)
	}
	if mc.Properties != nil && mc.Properties.ProvisioningState != nil {
		clusterState = *mc.Properties.ProvisioningState
	}
	if !isActiveClusterState(clusterState) {
		logf("cluster provisioningState=%s; no active LRO to abort", clusterState)
		return true, nil
	}

	clusterID := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.ContainerService/managedClusters/%s",
		c.cfg.subscriptionID, c.cfg.resourceGroup, c.cfg.clusterName)
	logf("cluster provisioningState=%s; locating latest managedClusters/write Started event in last %s", clusterState, lroLookupWindow)
	out, err := c.activityLogJSON(ctx, c.cfg.resourceGroup, lroLookupWindow)
	if err != nil {
		return false, fmt.Errorf("activity-log query for active cluster LRO: %w", err)
	}
	start, correlationID, err := latestClusterWriteStart(out, clusterID)
	if err != nil {
		return false, fmt.Errorf("determine active cluster LRO age: %w", err)
	}
	age := time.Since(start)
	logf("latest cluster write LRO: started=%s age=%s correlationID=%s", start.UTC().Format(time.RFC3339), age.Round(time.Minute), correlationID)
	if age < lroAbortAgeMin*time.Minute {
		return false, nil
	}

	logf("aborting latest cluster LRO via SDK (age >= %dm)", lroAbortAgeMin)
	poller, err := c.cluster.BeginAbortLatestOperation(ctx, c.cfg.resourceGroup, c.cfg.clusterName, nil)
	if err != nil {
		return false, fmt.Errorf("begin abort latest cluster operation: %w", err)
	}
	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		return false, fmt.Errorf("poll abort latest cluster operation: %w", err)
	}
	time.Sleep(15 * time.Second)
	return true, nil
}

// ---------------------------------------------------------------------------
// step 3 :: temp pool
// ---------------------------------------------------------------------------

// buildTempAgentPool constructs a throwaway agent-pool body from a live
// snapshot of the target pool. Extracted from addTempPool for unit
// testing.
//
// All compute-sizing fields (VMSize, OSDiskSizeGB, OSDiskType, OSType,
// OSSKU) and Mode are inherited from the live snapshot — hard-coding
// these is unsafe because management clusters across stg/prod use
// different VM SKUs and disk sizes (see config/config.yaml entries
// across environments), and AKS rejects an all-User cluster (the
// existing System pool, if any, must be replaced by another System
// pool). The temporary pool overrides Count=1 and tags itself with
// (1) the purpose marker so detection can skip it and (2) the full
// Azure resource ID of the source pool (read from live.ID) so a
// leftover temp pool can always be matched back to a unique source —
// even when multiple clusters in the same subscription share pool
// names like "system" and would therefore collide on the short
// 12-char tempPoolName.
func buildTempAgentPool(live *armcs.AgentPool, cpVersion string) (*armcs.AgentPool, error) {
	if live == nil || live.ID == nil || *live.ID == "" {
		return nil, errors.New("buildTempAgentPool: live snapshot has no resource ID")
	}
	body, err := agentPoolForCreate(live, cpVersion)
	if err != nil {
		return nil, fmt.Errorf("buildTempAgentPool: %w", err)
	}
	if body.Properties.VMSize == nil || *body.Properties.VMSize == "" {
		return nil, errors.New("buildTempAgentPool: live snapshot has no VMSize")
	}
	if body.Properties.OSDiskSizeGB == nil || *body.Properties.OSDiskSizeGB <= 0 {
		return nil, errors.New("buildTempAgentPool: live snapshot has no OSDiskSizeGB")
	}
	if cpVersion == "" {
		return nil, errors.New("buildTempAgentPool: empty cpVersion")
	}
	cnt := int32(1)
	body.Properties.Count = &cnt
	body.Properties.MinCount = nil
	body.Properties.MaxCount = nil
	body.Properties.EnableAutoScaling = nil
	if body.Properties.Tags == nil {
		body.Properties.Tags = map[string]*string{}
	}
	body.Properties.Tags[tempPoolPurposeTag] = ptr(tempPoolPurposeValue)
	body.Properties.Tags[tempPoolSourceTag] = ptr(*live.ID)
	return body, nil
}

func (c *clients) addTempPool(ctx context.Context, target nodePoolTarget, live *armcs.AgentPool) error {
	body, err := buildTempAgentPool(live, c.cfg.cpVersion)
	if err != nil {
		return err
	}
	tmpName := tempPoolName(target.name)
	logf("creating temp pool %s for %s (vmSize=%s, mode=%v, 1 node, k8s=%s, inherited taints)",
		tmpName, target.name, strDeref(live.Properties.VMSize), ptrValue(live.Properties.Mode), c.cfg.cpVersion)
	poller, err := c.pools.BeginCreateOrUpdate(ctx, c.cfg.resourceGroup, c.cfg.clusterName, tmpName, *body, nil)
	if err != nil {
		return fmt.Errorf("begin create temp pool %s: %w", tmpName, err)
	}
	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		return fmt.Errorf("poll create temp pool %s: %w", tmpName, err)
	}
	logf("temp pool %s created; waiting for k8s node Ready", tmpName)
	return c.waitForReadyNodes(ctx, tmpName, 1, tempReadyTOMin*time.Minute)
}

// ---------------------------------------------------------------------------
// step 4/7 :: drain (client-go drain helper)
// ---------------------------------------------------------------------------

// drainPool cordons each node before inspecting/deleting pods. Cordon failure is fatal:
// if new pods can still land on the node, the graceful-drain phase is not reliable.
// Force=true matches the later authoritative nodepool deletion and lets drain remove
// unmanaged pods instead of getting stuck before the pool delete.
func (c *clients) drainPool(ctx context.Context, pool string, timeout time.Duration) error {
	nodes, err := c.kube.CoreV1().Nodes().List(ctx, metav1.ListOptions{
		LabelSelector: "agentpool=" + pool,
	})
	if err != nil {
		return fmt.Errorf("list nodes for pool %s: %w", pool, err)
	}
	if len(nodes.Items) == 0 {
		logf("no nodes to drain in pool %s", pool)
		return nil
	}

	var out, errOut bytes.Buffer
	drainer := &drain.Helper{
		Ctx:                 ctx,
		Client:              c.kube,
		Force:               true,
		GracePeriodSeconds:  -1,
		IgnoreAllDaemonSets: true,
		DeleteEmptyDirData:  true,
		Timeout:             timeout,
		Out:                 &out,
		ErrOut:              &errOut,
	}
	for _, n := range nodes.Items {
		name := n.Name
		logf(">>> cordoning %s", name)
		if err := drain.RunCordonOrUncordon(drainer, n.DeepCopy(), true); err != nil {
			return fmt.Errorf("cordon %s: %w", name, err)
		}
		logf(">>> draining %s (timeout=%s)", name, timeout)
		podList, errs := drainer.GetPodsForDeletion(name)
		for _, err := range errs {
			logf("WARN: inspect pods for %s: %v (continuing)", name, err)
		}
		if podList == nil {
			continue
		}
		if warnings := podList.Warnings(); warnings != "" {
			logf("WARN: drain warnings for %s: %s", name, warnings)
		}
		if err := drainer.DeleteOrEvictPods(podList.Pods()); err != nil {
			// Don't fail the whole script on drain hiccups; delete-pool will force-evict.
			logf("WARN: drain %s returned: %v (continuing)", name, err)
		}
	}
	logBuffer("drain stdout", out.String())
	logBuffer("drain stderr", errOut.String())
	return nil
}

// ---------------------------------------------------------------------------
// step 5 :: delete pool
// ---------------------------------------------------------------------------

func (c *clients) deletePool(ctx context.Context, pool string) error {
	logf("deleting pool %s", pool)
	poller, err := c.pools.BeginDelete(ctx, c.cfg.resourceGroup, c.cfg.clusterName, pool, nil)
	if err != nil {
		return fmt.Errorf("begin delete %s: %w", pool, err)
	}
	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		return fmt.Errorf("poll delete %s: %w", pool, err)
	}
	logf("pool %s deleted", pool)
	return nil
}

// ---------------------------------------------------------------------------
// step 6 :: re-create system via SDK CreateOrUpdate
// ---------------------------------------------------------------------------

func (c *clients) recreatePool(ctx context.Context, poolName string, live *armcs.AgentPool) error {
	body, err := agentPoolForCreate(live, c.cfg.cpVersion)
	if err != nil {
		return fmt.Errorf("agent pool clone for %s: %w", poolName, err)
	}
	if b, err := json.MarshalIndent(body, "", "  "); err == nil {
		logf("--- sanitized PUT body for %s ---\n%s", poolName, string(b))
	}
	logf("BeginCreateOrUpdate pool %s", poolName)
	poller, err := c.pools.BeginCreateOrUpdate(ctx, c.cfg.resourceGroup, c.cfg.clusterName, poolName, *body, nil)
	if err != nil {
		return fmt.Errorf("begin recreate %s: %w", poolName, err)
	}
	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		return fmt.Errorf("poll recreate %s: %w", poolName, err)
	}
	expected := int32(1)
	if body.Properties != nil {
		if body.Properties.MinCount != nil {
			expected = *body.Properties.MinCount
		} else if body.Properties.Count != nil {
			expected = *body.Properties.Count
		}
	}
	logf("pool %s ARM-Succeeded; waiting for %d Ready k8s node(s)", poolName, expected)
	return c.waitForReadyNodes(ctx, poolName, int(expected), poolReadyTOMin*time.Minute)
}

// ---------------------------------------------------------------------------
// step 8 :: no-op tag reconcile via SDK tag PATCH
// ---------------------------------------------------------------------------

func (c *clients) reconcileTagPut(ctx context.Context) error {
	// Use nanosecond precision so repeated invocations within the same
	// minute produce different values (forcing ARM to see a real change).
	ts := time.Now().UTC().Format(time.RFC3339Nano)
	id := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.ContainerService/managedClusters/%s",
		c.cfg.subscriptionID, c.cfg.resourceGroup, c.cfg.clusterName)
	operation := armresources.TagsPatchOperationMerge
	_, err := c.tags.UpdateAtScope(ctx, id, armresources.TagsPatchResource{
		Operation: &operation,
		Properties: &armresources.Tags{
			Tags: map[string]*string{
				"recreate-broken-pools": &ts,
			},
		},
	}, nil)
	return err
}

// ---------------------------------------------------------------------------
// forced-evidence trigger (used only when cluster-state, non-system-pools,
// and system-wedge checks PASS and the NRP-KVS-storm check FAILs
// because the activity log has no recent NRP-KVS events)
// ---------------------------------------------------------------------------

// triggerPoolReconcile starts an AKS RP scale-up of the live target
// pool by issuing a sanitized CreateOrUpdate with the snapshot spec plus
// one node. It does not wait for the LRO to finish — the caller polls the
// activity log for NRP-KVS evidence in parallel, aborts the LRO, and then
// restores the original pool spec.
func (c *clients) triggerPoolReconcile(ctx context.Context, target nodePoolTarget, live *armcs.AgentPool) error {
	body, err := agentPoolForScaleUpTrigger(live, c.cfg.cpVersion)
	if err != nil {
		return fmt.Errorf("triggerPoolReconcile %s: %w", target.name, err)
	}
	logf("triggering scale-up on %q (count=%d) to force VMSS write", target.name, *body.Properties.Count)
	if _, err := c.pools.BeginCreateOrUpdate(ctx, c.cfg.resourceGroup, c.cfg.clusterName, target.name, *body, nil); err != nil {
		return fmt.Errorf("begin trigger pool scale-up %s: %w", target.name, err)
	}
	return nil
}

// pollForNRPEvidence re-queries the activity log on a fixed interval
// until the NRP-KVS Failed-event count reaches threshold or the timeout
// elapses. windowMin controls how far back each poll looks (a window
// equal to the timeout makes every poll see all events since the
// trigger).
func (c *clients) pollForNRPEvidence(ctx context.Context, target nodePoolTarget, timeout time.Duration, pollInterval time.Duration, windowMin int, threshold int) (int, error) {
	if pollInterval <= 0 {
		pollInterval = 60 * time.Second
	}
	deadline := time.Now().Add(timeout)
	last := 0
	for {
		resp, err := c.pools.Get(ctx, c.cfg.resourceGroup, c.cfg.clusterName, target.name, nil)
		if err != nil {
			logf("WARN: forced-evidence pool state check for %s failed: %v (continuing)", target.name, err)
		} else if resp.Properties != nil && resp.Properties.ProvisioningState != nil && *resp.Properties.ProvisioningState == "Succeeded" {
			logf("forced evidence: pool %s provisioningState=Succeeded; wedge resolved, returning early", target.name)
			return 0, nil
		}

		out, err := c.activityLogJSON(ctx, c.cfg.nodeRG, fmt.Sprintf("%dm", windowMin))
		if err != nil {
			return last, fmt.Errorf("forced-evidence activity-log query for %s: %w", target.name, err)
		}
		hits, parseErr := countNRPFailures(out, target.vmssPrefix)
		if parseErr != nil {
			return last, fmt.Errorf("forced-evidence activity-log parse for %s: %w", target.name, parseErr)
		}
		last = hits
		logf("forced evidence poll for %s: NRP-KVS hits=%d threshold=%d (window=%dm)", target.name, hits, threshold, windowMin)
		if hits >= threshold {
			return hits, nil
		}
		if !time.Now().Before(deadline) {
			return hits, nil
		}
		sleep := pollInterval
		if remaining := time.Until(deadline); remaining < sleep {
			sleep = remaining
		}
		select {
		case <-ctx.Done():
			return last, ctx.Err()
		case <-time.After(sleep):
		}
	}
}

// abortPoolReconcile aborts the latest LRO on the named agent pool,
// which (when the forced-evidence trigger started one) cancels the
// in-flight CreateOrUpdate. Used for both system and user pools.
// Best-effort: failures here are logged by the caller but not propagated.
func (c *clients) abortPoolReconcile(ctx context.Context, poolName string) error {
	poller, err := c.pools.BeginAbortLatestOperation(ctx, c.cfg.resourceGroup, c.cfg.clusterName, poolName, nil)
	if err != nil {
		return fmt.Errorf("begin abort pool reconcile %s: %w", poolName, err)
	}
	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		return fmt.Errorf("poll abort pool reconcile %s: %w", poolName, err)
	}
	return nil
}

// restorePoolSpec re-PUTs the target pool with the snapshotted spec to
// reverse the temporary scale-up issued by triggerPoolReconcile. Used
// only on the forced-evidence path so the pool is restored to its
// original size even when forced evidence ends inconclusive.
func (c *clients) restorePoolSpec(ctx context.Context, target nodePoolTarget, live *armcs.AgentPool) error {
	body, err := agentPoolForCreate(live, c.cfg.cpVersion)
	if err != nil {
		return fmt.Errorf("restorePoolSpec %s: %w", target.name, err)
	}
	logf("restoring original spec for pool %s", target.name)
	poller, err := c.pools.BeginCreateOrUpdate(ctx, c.cfg.resourceGroup, c.cfg.clusterName, target.name, *body, nil)
	if err != nil {
		return fmt.Errorf("begin restore pool spec %s: %w", target.name, err)
	}
	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		return fmt.Errorf("poll restore pool spec %s: %w", target.name, err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func (c *clients) waitForReadyNodes(ctx context.Context, pool string, want int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastListErr error
	for {
		nodes, err := c.kube.CoreV1().Nodes().List(ctx, metav1.ListOptions{
			LabelSelector: "agentpool=" + pool,
		})
		if err != nil {
			lastListErr = fmt.Errorf("list nodes for pool %s: %w", pool, err)
			logf("WARN: %v; retrying", lastListErr)
		} else {
			lastListErr = nil
			ready := 0
			for _, n := range nodes.Items {
				if isNodeSchedulableReady(&n) {
					ready++
				}
			}
			logf("  pool=%s ready=%d/%d", pool, ready, want)
			if ready >= want {
				return nil
			}
		}
		if time.Now().After(deadline) {
			if lastListErr != nil {
				return fmt.Errorf("pool %s did not reach %d ready nodes within %s; last list error: %w", pool, want, timeout, lastListErr)
			}
			return fmt.Errorf("pool %s did not reach %d ready nodes within %s", pool, want, timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pollIntervalSec * time.Second):
		}
	}
}

func isNodeReady(n *corev1.Node) bool {
	if n == nil {
		return false
	}
	for _, c := range n.Status.Conditions {
		if c.Type == corev1.NodeReady {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}

func isNodeSchedulableReady(n *corev1.Node) bool {
	if !isNodeReady(n) {
		return false
	}
	if n.Spec.Unschedulable {
		return false
	}
	return n.DeletionTimestamp == nil
}

// activityLogJSON returns Activity Log events in the compact JSON shape used
// by the pure parsing helpers and tests. Keeping this conversion boundary lets
// detection use the Azure Monitor SDK while unit tests remain simple.
func (c *clients) activityLogJSON(ctx context.Context, resourceGroup string, offset string) ([]byte, error) {
	start, end, err := activityLogWindow(offset)
	if err != nil {
		return nil, err
	}
	filter := fmt.Sprintf("eventTimestamp ge '%s' and eventTimestamp le '%s' and resourceGroupName eq '%s'",
		start.UTC().Format(time.RFC3339), end.UTC().Format(time.RFC3339), resourceGroup)
	logf("querying activity logs: %s", filter)
	return c.activityLogJSONForFilter(ctx, filter)
}

func (c *clients) activityLogJSONForFilter(ctx context.Context, filter string) ([]byte, error) {
	timeout := time.Duration(activityLogAuthRetryTimeoutMin) * time.Minute
	deadline := time.Now().Add(timeout)
	delay := time.Duration(activityLogAuthRetryInitialSec) * time.Second
	maxDelay := time.Duration(activityLogAuthRetryMaxSec) * time.Second

	for attempt := 1; ; attempt++ {
		events, err := c.activityLogJSONOnce(ctx, filter)
		if err == nil {
			return json.Marshal(events)
		}
		if !isActivityLogAuthorizationError(err) {
			return nil, err
		}
		if !time.Now().Before(deadline) {
			return nil, fmt.Errorf("activity-log authorization failed after retrying for %s: %w", timeout, err)
		}

		sleep := delay
		if remaining := time.Until(deadline); remaining < sleep {
			sleep = remaining
		}
		logf("WARN: activity-log authorization failed; retrying in %s (attempt=%d): %v", sleep, attempt, err)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(sleep):
		}
		if delay < maxDelay {
			delay *= 2
			if delay > maxDelay {
				delay = maxDelay
			}
		}
	}
}

func (c *clients) activityLogJSONOnce(ctx context.Context, filter string) ([]activityEvent, error) {
	pager := c.activityLogs.NewListPager(filter, nil)
	var events []activityEvent
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, e := range page.Value {
			if e == nil {
				continue
			}
			events = append(events, activityEventFromSDK(e))
		}
	}
	return events, nil
}

func isActivityLogAuthorizationError(err error) bool {
	var respErr *azcore.ResponseError
	if !errors.As(err, &respErr) {
		return false
	}
	if respErr.StatusCode != http.StatusForbidden {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(respErr.ErrorCode), "AuthorizationFailed") ||
		strings.EqualFold(strings.TrimSpace(respErr.ErrorCode), "LinkedAuthorizationFailed")
}

func activityLogWindow(offset string) (time.Time, time.Time, error) {
	end := time.Now().UTC()
	if offset == "" {
		return time.Time{}, time.Time{}, errors.New("activity-log offset is required")
	}
	unit := offset[len(offset)-1]
	value, err := strconv.Atoi(offset[:len(offset)-1])
	if err != nil || value <= 0 {
		return time.Time{}, time.Time{}, fmt.Errorf("invalid activity-log offset %q", offset)
	}
	var d time.Duration
	switch unit {
	case 'm':
		d = time.Duration(value) * time.Minute
	case 'h':
		d = time.Duration(value) * time.Hour
	case 'd':
		d = time.Duration(value) * 24 * time.Hour
	default:
		return time.Time{}, time.Time{}, fmt.Errorf("invalid activity-log offset unit %q in %q", string(unit), offset)
	}
	return end.Add(-d), end, nil
}

func activityEventFromSDK(e *armmonitor.EventData) activityEvent {
	out := activityEvent{}
	if e.Status != nil && e.Status.Value != nil {
		out.Status.Value = *e.Status.Value
	}
	if e.OperationName != nil && e.OperationName.Value != nil {
		out.OperationName.Value = *e.OperationName.Value
	}
	if e.ResourceID != nil {
		out.ResourceID = *e.ResourceID
	}
	if e.CorrelationID != nil {
		out.CorrelationID = *e.CorrelationID
	}
	if e.EventTimestamp != nil {
		out.EventTime = e.EventTimestamp.UTC().Format(time.RFC3339)
	}
	if e.Properties != nil {
		if msg := e.Properties["statusMessage"]; msg != nil {
			out.Properties.StatusMessage = *msg
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// activity-log parsing
// ---------------------------------------------------------------------------

type activityEvent struct {
	Status        struct{ Value string } `json:"status"`
	OperationName struct{ Value string } `json:"operationName"`
	ResourceID    string                 `json:"resourceId"`
	CorrelationID string                 `json:"correlationId"`
	EventTime     string                 `json:"eventTimestamp"`
	Properties    struct {
		// StatusMessage is the inner ARM error body as an embedded
		// JSON string, e.g.
		// `{"error":{"code":"NetworkingInternalOperationError",...}}`.
		// Activity-log events deliver it as a string for backward
		// compatibility with classic-portal consumers.
		StatusMessage string `json:"statusMessage"`
	} `json:"properties"`
}

// hasNRPKVSSignature reports whether an activity-log event's inner ARM
// error body carries the NetworkingInternalOperationError code. Returns
// false on any parse error or missing field — the NRP-KVS-storm
// check must fail closed rather than over-count.
func hasNRPKVSSignature(e activityEvent) bool {
	msg := strings.TrimSpace(e.Properties.StatusMessage)
	if msg == "" {
		return false
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(msg), &body); err != nil {
		return false
	}
	return body.Error.Code == nrpKVSErrorCode
}

func isVMSSWriteOperation(operation string) bool {
	return strings.EqualFold(strings.TrimSpace(operation), vmssWriteOperation)
}

func countNRPFailures(raw []byte, vmssPrefix string) (int, error) {
	events, err := parseActivityEvents(raw)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, e := range events {
		if e.Status.Value != "Failed" {
			continue
		}
		if !isVMSSWriteOperation(e.OperationName.Value) {
			continue
		}
		if !hasNRPKVSSignature(e) {
			continue
		}
		if vmssPrefix == "" {
			n++
			continue
		}
		if strings.Contains(strings.ToLower(e.ResourceID), strings.ToLower("/virtualMachineScaleSets/"+vmssPrefix)) {
			n++
		}
	}
	return n, nil
}

func nrpResourceIDs(raw []byte) ([]string, error) {
	events, err := parseActivityEvents(raw)
	if err != nil {
		return nil, err
	}
	seen := map[string]struct{}{}
	var out []string
	for _, e := range events {
		if e.Status.Value != "Failed" {
			continue
		}
		if !isVMSSWriteOperation(e.OperationName.Value) {
			continue
		}
		if !hasNRPKVSSignature(e) {
			continue
		}
		if _, ok := seen[e.ResourceID]; ok {
			continue
		}
		seen[e.ResourceID] = struct{}{}
		out = append(out, e.ResourceID)
	}
	return out, nil
}

func latestClusterWriteStart(raw []byte, clusterResourceID string) (time.Time, string, error) {
	events, err := parseActivityEvents(raw)
	if err != nil {
		return time.Time{}, "", err
	}
	clusterResourceID = strings.ToLower(clusterResourceID)
	var latest time.Time
	var correlationID string
	for _, e := range events {
		if e.Status.Value != "Started" {
			continue
		}
		if !strings.EqualFold(e.ResourceID, clusterResourceID) {
			continue
		}
		if !strings.Contains(strings.ToLower(e.OperationName.Value), "managedclusters/write") {
			continue
		}
		if e.EventTime == "" {
			continue
		}
		t, err := time.Parse(time.RFC3339, e.EventTime)
		if err != nil {
			return time.Time{}, "", fmt.Errorf("parse cluster write eventTimestamp %q: %w", e.EventTime, err)
		}
		if latest.IsZero() || t.After(latest) {
			latest = t
			correlationID = e.CorrelationID
		}
	}
	if latest.IsZero() {
		return time.Time{}, "", fmt.Errorf("no Started Microsoft.ContainerService/managedClusters/write event found for %s in activity log", clusterResourceID)
	}
	return latest, correlationID, nil
}

func parseActivityEvents(raw []byte) ([]activityEvent, error) {
	var events []activityEvent
	if err := json.Unmarshal(raw, &events); err != nil {
		return nil, fmt.Errorf("parse activity log JSON: %w", err)
	}
	return events, nil
}

// ---------------------------------------------------------------------------
// logging
// ---------------------------------------------------------------------------

// logf is a thin wrapper around slog.Info that preserves the printf-style
// call sites peppered through this binary. WARN-prefixed messages are
// promoted to slog.Warn so log filters in Kusto see them with the right
// severity; everything else is INFO.
func logf(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	switch {
	case strings.HasPrefix(msg, "WARN:"):
		slog.Warn(strings.TrimSpace(strings.TrimPrefix(msg, "WARN:")))
	case strings.HasPrefix(msg, "ERROR:"):
		slog.Error(strings.TrimSpace(strings.TrimPrefix(msg, "ERROR:")))
	default:
		slog.Info(msg)
	}
}

// logBanner emits a visual section divider. Phase is captured as a
// structured attribute so a Kusto query can filter on it directly
// (e.g. `customDimensions.phase contains "STEP 6"`).
func logBanner(s string) {
	slog.Info(strings.Repeat("=", 60), "phase", s)
	slog.Info(">>> "+s, "phase", s)
	slog.Info(strings.Repeat("=", 60), "phase", s)
}

func logBuffer(prefix, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	for _, line := range strings.Split(value, "\n") {
		if strings.TrimSpace(line) != "" {
			logf("%s: %s", prefix, line)
		}
	}
}

func cloneStringPtrMapWithoutPrefix(in map[string]*string, prefix string) map[string]*string {
	out := map[string]*string{}
	for k, v := range in {
		if strings.HasPrefix(k, prefix) {
			continue
		}
		if v == nil {
			out[k] = nil
			continue
		}
		out[k] = ptr(*v)
	}
	return out
}

func ptr[T any](v T) *T { return &v }

func strDeref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func ptrValue[T any](p *T) string {
	if p == nil {
		return ""
	}
	return fmt.Sprint(*p)
}
