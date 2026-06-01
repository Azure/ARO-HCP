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
// recreate-system-pool: detection-gated self-healing for the NRP KVS
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
//   CLUSTER_NAME              AKS cluster name (e.g. int-uksouth-mgmt-1)
//   RESOURCE_GROUP            Resource group containing the AKS cluster
//   SUBSCRIPTION_ID           Azure subscription ID containing the AKS cluster
//   NRP_FAIL_THRESHOLD        Failed-event count threshold (default 10)
//   NRP_FAIL_WINDOW_MIN       Activity-log lookback window in min (default 15)
//   DRY_RUN                   "true" to print intended actions but make no writes
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
// Action (only when all guards pass)
// ----------------------------------
//   1. Snapshot the system pool ARM JSON (raw).
//   2. Abort cluster LRO if one is active and older than 30 min. The
//      AROSLSRE-880 / NRP-KVS incident at INT (2026-05-16..18) left the
//      cluster stuck in Updating for days because the parent upgrade
//      LRO retried forever; aborting frees the cluster to accept fresh
//      PUTs. Aborts move the cluster from Updating to Canceled. If the
//      latest LRO is younger than 30 min, we no-op exit 0 instead of
//      racing a potentially-healthy in-progress operation.
//   3. Add throwaway `systmp` System pool (same vmSize + taint + label).
//   4. Cordon + drain existing system nodes (client-go drain helper).
//   5. Delete the broken `system` pool.
//   6. Re-create `system` via SDK CreateOrUpdate with the sanitized
//      AgentPool struct from the snapshot.
//   7. Cordon + drain + delete `systmp`.
//   8. No-op reconcile via tag update (kicks cluster back to Succeeded).

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	systemPoolName    = "system"
	systmpPoolName    = "systmp"
	defaultThreshold  = 5
	defaultWindowMin  = 15
	lroAbortAgeMin    = 30
	lroLookupWindow   = "14d"
	systmpReadyTOMin  = 10
	systemReadyTOMin  = 10
	pollIntervalSec   = 30
	overallTimeoutMin = 60

	// The NRP-KVS storm check requires this error code so other failure
	// modes (quota / capacity / policy / etc) cannot trip the threshold.
	nrpKVSErrorCode    = "NetworkingInternalOperationError"
	vmssWriteOperation = "Microsoft.Compute/virtualMachineScaleSets/write"

	// systemVMSSPrefix is the activity-log filter for VMSS-write events
	// scoped to the system pool's VMSS. AKS names node-pool VMSS using
	// the convention "aks-<poolName>-<random>-vmss"; this constant is
	// derived from systemPoolName so the two stay in sync if the system
	// pool is ever renamed. Stable across all AKS API versions we run
	// today (2024-10-01, 2025-07-02-preview).
	systemVMSSPrefix = "aks-" + systemPoolName + "-"

	activityLogAuthRetryTimeoutMin = 5
	activityLogAuthRetryInitialSec = 10
	activityLogAuthRetryMaxSec     = 60

	// triggerEvidence forces an AKS RP reconcile of the system pool when
	// the cluster-state / non-system-pools / system-wedge checks PASS
	// but the NRP-KVS-storm check has no recent NRP-KVS events in the
	// configured lookback window. The trigger gives the wedge a chance
	// to produce fresh evidence (or to prove the wedge is not NRP-KVS).
	// Times are short relative to the AKS RP retry cadence (~3 min) so
	// threshold-many retries can accumulate during the wait window.
	triggerEvidenceTimeoutMin      = 30 // wait at most this long for evidence
	triggerEvidencePollIntervalSec = 60 // re-query activity log every poll
	triggerEvidenceWindowMin       = 60 // activity-log lookback for the wait loop

	// abortTriggerTimeoutMin caps the wait for cleanup of the LRO that
	// forcedEvidencePath itself triggered. The abort runs with a fresh
	// context derived from context.Background so it executes even when
	// the parent run context has already expired (overall script timeout
	// or pollForNRPEvidence consuming the full triggerEvidenceTimeoutMin
	// budget). Without this, a cancelled parent context would silently
	// skip the abort and leave the AKS RP retrying the wedged write.
	abortTriggerTimeoutMin = 5
)

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
	slog.SetDefault(slog.New(handler).With("component", "recreate-system-pool"))

	if err := run(); err != nil {
		slog.Error("run failed", "error", err.Error())
		os.Exit(1)
	}
}

type orchestrator interface {
	ensureCluster(ctx context.Context) (armcs.ManagedCluster, bool, error)
	bootstrapKube(ctx context.Context, mc armcs.ManagedCluster) error
	detect(ctx context.Context) (bool, string, error)
	dumpPreflight(ctx context.Context) error
	dumpPostflight(ctx context.Context) error
	preflightChecks(ctx context.Context) error
	snapshotSystem(ctx context.Context) (*armcs.AgentPool, error)
	maybeAbortLRO(ctx context.Context) (bool, error)
	addSystmp(ctx context.Context, live *armcs.AgentPool) error
	drainPool(ctx context.Context, pool string, timeout time.Duration) error
	deletePool(ctx context.Context, pool string) error
	recreateSystem(ctx context.Context, live *armcs.AgentPool) error
	reconcileTagPut(ctx context.Context) error
	triggerSystemReconcile(ctx context.Context, live *armcs.AgentPool) error
	pollForNRPEvidence(ctx context.Context, timeout time.Duration, pollInterval time.Duration, windowMin int, threshold int) (int, error)
	abortSystemReconcile(ctx context.Context) error
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
	act, reason, err := orch.detect(ctx)
	if err != nil {
		return fmt.Errorf("detection: %w", err)
	}
	if !act && !cfg.skipGuards && !cfg.dryRun && reasonIsNRPStormFail(reason) {
		act, reason, err = forcedEvidencePath(ctx, cfg, orch, reason)
		if err != nil {
			return err
		}
	}
	if !act && !cfg.skipGuards {
		logf("guards did not fire: %s. Exiting no-op.", reason)
		return nil
	}
	if act {
		logf("ALL GUARDS PASSED — proceeding with recreate")
	} else {
		logf("guards did not fire (%s) but SKIP_GUARDS=true — forcing recreate", reason)
	}

	if cfg.dryRun {
		logf("DRY_RUN=true — guards passed; would proceed with recreate. Exiting no-op.")
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

	if err := orch.preflightChecks(ctx); err != nil {
		return err
	}

	logBanner("STEP 1 :: snapshot system pool")
	if _, err := orch.snapshotSystem(ctx); err != nil {
		return fmt.Errorf("snapshot: %w", err)
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
	act, reason, err = orch.detect(ctx)
	if err != nil {
		return fmt.Errorf("post-LRO detection: %w", err)
	}
	if !act && !cfg.skipGuards {
		logf("guards no longer fire after LRO handling: %s. Exiting no-op.", reason)
		return nil
	}
	if act {
		logf("guards still pass after LRO handling")
	} else {
		logf("guards no longer fire (%s) but SKIP_GUARDS=true — continuing", reason)
	}
	live, err := orch.snapshotSystem(ctx)
	if err != nil {
		return fmt.Errorf("post-LRO snapshot: %w", err)
	}

	logBanner("STEP 3 :: add throwaway 'systmp' System pool")
	if err := orch.addSystmp(ctx, live); err != nil {
		return fmt.Errorf("add systmp: %w", err)
	}

	logBanner("STEP 4 :: cordon + drain existing system nodes")
	if err := orch.drainPool(ctx, systemPoolName, 10*time.Minute); err != nil {
		return fmt.Errorf("drain system: %w", err)
	}

	logBanner("STEP 5 :: delete the broken 'system' pool")
	if err := orch.deletePool(ctx, systemPoolName); err != nil {
		return fmt.Errorf("delete system: %w", err)
	}

	logBanner("STEP 6 :: re-create 'system' via SDK CreateOrUpdate")
	if err := orch.recreateSystem(ctx, live); err != nil {
		return fmt.Errorf("recreate system: %w", err)
	}

	logBanner("STEP 7 :: drain + delete throwaway 'systmp' pool")
	if err := orch.drainPool(ctx, systmpPoolName, 5*time.Minute); err != nil {
		logf("WARN: systmp drain returned: %v (continuing to delete)", err)
	}
	if err := orch.deletePool(ctx, systmpPoolName); err != nil {
		return fmt.Errorf("delete systmp: %w", err)
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

// forcedEvidencePath triggers a no-op reconcile on the live system
// pool so the AKS RP attempts a fresh VMSS write. It then polls the
// activity log for NRP-KVS Failed events and aborts the LRO it started
// once threshold-many hits are observed or the timeout elapses.
//
// Returns (act, reason, error). act=true means evidence reached the
// threshold and the caller should proceed with the recreate flow.
// act=false with no error means the trigger was inconclusive (the
// cluster is wedged for a different reason) and the caller should
// exit no-op.
func forcedEvidencePath(ctx context.Context, cfg *config, orch orchestrator, initialReason string) (bool, string, error) {
	logBanner("FORCED EVIDENCE :: triggering reconcile to verify NRP-KVS signature")
	logf("initial NRP-KVS storm check saw no evidence in last %dm: %s", cfg.windowMin, initialReason)

	live, err := orch.snapshotSystem(ctx)
	if err != nil {
		return false, initialReason, fmt.Errorf("forced evidence snapshot: %w", err)
	}

	if err := orch.triggerSystemReconcile(ctx, live); err != nil {
		logf("WARN: triggerSystemReconcile failed: %v; treating as no-op", err)
		return false, initialReason, nil
	}

	logf("triggered system pool reconcile; polling activity log every %ds for up to %dm",
		triggerEvidencePollIntervalSec, triggerEvidenceTimeoutMin)
	hits, pollErr := orch.pollForNRPEvidence(
		ctx,
		triggerEvidenceTimeoutMin*time.Minute,
		triggerEvidencePollIntervalSec*time.Second,
		triggerEvidenceWindowMin,
		cfg.threshold,
	)

	// Always best-effort abort the LRO we started so the cluster doesn't
	// keep retrying the wedged write after we're done observing. Use a
	// fresh context.Background-rooted context so the abort still runs
	// when the parent ctx is already cancelled (overall script timeout
	// or pollForNRPEvidence consuming its full budget) — the LRO we
	// triggered here is our responsibility to tear down.
	abortCtx, abortCancel := context.WithTimeout(context.Background(), abortTriggerTimeoutMin*time.Minute)
	defer abortCancel()
	if abortErr := orch.abortSystemReconcile(abortCtx); abortErr != nil {
		logf("WARN: abortSystemReconcile failed: %v", abortErr)
	}

	if pollErr != nil {
		return false, initialReason, fmt.Errorf("poll for NRP evidence: %w", pollErr)
	}
	if hits < cfg.threshold {
		reason := fmt.Sprintf("forced evidence inconclusive: only %d NRP failures < %d after %dm", hits, cfg.threshold, triggerEvidenceTimeoutMin)
		logf("%s. Exiting no-op.", reason)
		return false, reason, nil
	}
	logf("forced evidence confirmed NRP-KVS (%d hits >= threshold %d) — proceeding with recreate", hits, cfg.threshold)
	return true, "", nil
}

// ---------------------------------------------------------------------------
// config
// ---------------------------------------------------------------------------

type config struct {
	clusterName    string
	resourceGroup  string
	subscriptionID string
	nodeRG         string
	cpVersion      string
	threshold      int
	windowMin      int
	dryRun         bool
	skipGuards     bool
}

// parseEnvConfig builds a config from environment variables only. It does
// not call any external tools or APIs, which makes it safe to unit-test.
func parseEnvConfig(env func(string) string) (*config, error) {
	c := &config{
		clusterName:    env("CLUSTER_NAME"),
		resourceGroup:  env("RESOURCE_GROUP"),
		subscriptionID: env("SUBSCRIPTION_ID"),
		threshold:      defaultThreshold,
		windowMin:      defaultWindowMin,
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
	logf("NRP_FAIL_THRESHOLD=%d", c.threshold)
	logf("NRP_FAIL_WINDOW_MIN=%d", c.windowMin)
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
	logf("--- k8s nodes (system) ---")
	c.dumpKubeNodes(ctx, systemPoolName)
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
	logf("--- final k8s nodes (system) ---")
	c.dumpKubeNodes(ctx, systemPoolName)
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

// evalNonSystemPools reports whether all non-system pools have count > 0
// and a system pool exists. Also reports the system pool's minCount
// and provisioningState back to the caller so the latter can be fed
// into evalSystemWedge without a second list-pools API call.
//
// Returns (pass, systemMin, systemProvState, reason).
func evalNonSystemPools(pools []*armcs.AgentPool) (bool, int32, string, string) {
	var systemMin int32
	var systemProvState string
	systemFound := false
	for _, p := range pools {
		if p == nil || p.Name == nil || p.Properties == nil {
			continue
		}
		name := *p.Name
		if name == systemPoolName {
			systemFound = true
			if p.Properties.MinCount != nil {
				systemMin = *p.Properties.MinCount
			}
			if p.Properties.ProvisioningState != nil {
				systemProvState = *p.Properties.ProvisioningState
			}
			continue
		}
		cnt := int32(0)
		if p.Properties.Count != nil {
			cnt = *p.Properties.Count
		}
		if cnt == 0 {
			return false, 0, "", fmt.Sprintf("non-system pools FAIL: pool %q has count=0", name)
		}
	}
	if !systemFound {
		return false, 0, "", "non-system pools FAIL: no system pool found"
	}
	return true, systemMin, systemProvState, ""
}

// evalSystemWedge reports whether the system pool itself is in a
// wedge-compatible state. Refines the NRP-KVS storm check with a
// positive signal scoped to this exact agent-pool resource.
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
func evalSystemWedge(systemProvState string) (bool, string) {
	switch systemProvState {
	case "Failed", "Canceled", "Updating", "Upgrading":
		return true, ""
	case "Succeeded":
		return false, "system wedge FAIL: provisioningState=\"Succeeded\" (no wedge)"
	case "Creating":
		return false, "system wedge FAIL: provisioningState=\"Creating\" (not fully created)"
	case "Deleting":
		return false, "system wedge FAIL: provisioningState=\"Deleting\" (being torn down)"
	case "":
		return false, "system wedge FAIL: provisioningState is empty"
	}
	return false, fmt.Sprintf("system wedge FAIL: provisioningState=%q is not a recognized wedge-compatible state", systemProvState)
}

func (c *clients) detect(ctx context.Context) (bool, string, error) {
	// Cluster state check
	mc, err := c.cluster.Get(ctx, c.cfg.resourceGroup, c.cfg.clusterName, nil)
	if err != nil {
		return false, "", fmt.Errorf("cluster state get: %w", err)
	}
	cs := ""
	if mc.Properties != nil && mc.Properties.ProvisioningState != nil {
		cs = *mc.Properties.ProvisioningState
	}
	logf("cluster state :: provisioningState=%s (accept: Succeeded/Canceled/Failed/Updating/Upgrading; reject: Creating/Deleting/unknown)", cs)
	if pass, reason := evalClusterState(cs); !pass {
		return false, reason, nil
	}
	logf("cluster state PASS")

	// Non-system pools check (also discovers system minCount and state).
	pager := c.pools.NewListPager(c.cfg.resourceGroup, c.cfg.clusterName, nil)
	var allPools []*armcs.AgentPool
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return false, "", fmt.Errorf("non-system pools list: %w", err)
		}
		allPools = append(allPools, page.Value...)
	}
	pass, systemMin, systemProvState, reason := evalNonSystemPools(allPools)
	if !pass {
		return false, reason, nil
	}
	logf("non-system pools PASS (system minCount=%d systemProvState=%q)", systemMin, systemProvState)

	// System wedge check (system pool itself is in a wedge-compatible state).
	if pass, reason := evalSystemWedge(systemProvState); !pass {
		return false, reason, nil
	}
	logf("system wedge PASS (system pool provisioningState=%q)", systemProvState)

	// NRP-KVS storm check (activity log on aks-system-* VMSS)
	logf("NRP-KVS storm :: checking activity log on %s for last %d min", c.cfg.nodeRG, c.cfg.windowMin)
	out, err := c.activityLogJSON(ctx, c.cfg.nodeRG, fmt.Sprintf("%dm", c.cfg.windowMin))
	if err != nil {
		return false, "", fmt.Errorf("NRP-KVS storm activity-log query failed: %w", err)
	}
	hits, err := countNRPFailures(out, systemVMSSPrefix)
	if err != nil {
		return false, "", fmt.Errorf("NRP-KVS storm activity-log parse failed: %w", err)
	}
	logf("NRP-KVS storm :: NRP-KVS (%s) Failed events on aks-system-* in window: %d (threshold %d)",
		nrpKVSErrorCode, hits, c.cfg.threshold)
	if pass, reason := evalNRPStorm(hits, c.cfg.threshold); !pass {
		return false, reason, nil
	}
	logf("NRP-KVS storm PASS")

	return true, "", nil
}

// preflightChecks fails CLOSED: if the AKS Get for systmp returns
// anything other than HTTP 404 we must not proceed. Treating
// auth/throttling/transient errors as "pool does not exist" would let
// us create a duplicate systmp on top of an existing one, which would
// fail with a less actionable error later.
func (c *clients) preflightChecks(ctx context.Context) error {
	_, err := c.pools.Get(ctx, c.cfg.resourceGroup, c.cfg.clusterName, systmpPoolName, nil)
	switch {
	case err == nil:
		return fmt.Errorf("leftover 'systmp' pool present from previous run; clean it up then re-run")
	case isNotFoundErr(err):
		return nil
	default:
		return fmt.Errorf("preflight Get systmp: %w", err)
	}
}

// ---------------------------------------------------------------------------
// step 1 :: snapshot
// ---------------------------------------------------------------------------

func (c *clients) snapshotSystem(ctx context.Context) (*armcs.AgentPool, error) {
	resp, err := c.pools.Get(ctx, c.cfg.resourceGroup, c.cfg.clusterName, systemPoolName, nil)
	if err != nil {
		return nil, fmt.Errorf("get system pool: %w", err)
	}
	live := resp.AgentPool
	// Audit-friendly stdout dump.
	if b, err := json.MarshalIndent(live, "", "  "); err == nil {
		logf("--- live system pool (raw) ---\n%s", string(b))
	}
	if live.Properties == nil {
		return nil, errors.New("system pool has no properties")
	}
	if live.Properties.VMSize == nil || *live.Properties.VMSize == "" {
		return nil, errors.New("system pool has no VMSize; refusing to act")
	}
	if live.Properties.VnetSubnetID == nil || *live.Properties.VnetSubnetID == "" {
		return nil, errors.New("system pool has no VnetSubnetID; refusing to act")
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
// step 3 :: systmp
// ---------------------------------------------------------------------------

// buildSystmpAgentPool constructs the throwaway System pool body from a
// live system-pool snapshot. Extracted from addSystmp for unit testing.
//
// All compute-sizing fields (VMSize, OSDiskSizeGB, OSDiskType, OSType,
// OSSKU) are inherited from the live snapshot — hard-coding these is
// unsafe because management clusters across stg/prod use different VM
// SKUs and disk sizes (see config/config.yaml entries for systemAgentPool
// across environments). The temporary pool overrides Count=1 and adds
// an obviously-temporary purpose tag while otherwise relying on the
// sanitized live-pool clone for node labels, taints and VMSS tags.
func buildSystmpAgentPool(live *armcs.AgentPool, cpVersion string) (*armcs.AgentPool, error) {
	body, err := agentPoolForCreate(live, cpVersion)
	if err != nil {
		return nil, fmt.Errorf("buildSystmpAgentPool: %w", err)
	}
	if body.Properties.VMSize == nil || *body.Properties.VMSize == "" {
		return nil, errors.New("buildSystmpAgentPool: live snapshot has no VMSize")
	}
	if body.Properties.OSDiskSizeGB == nil || *body.Properties.OSDiskSizeGB <= 0 {
		return nil, errors.New("buildSystmpAgentPool: live snapshot has no OSDiskSizeGB")
	}
	if cpVersion == "" {
		return nil, errors.New("buildSystmpAgentPool: empty cpVersion")
	}
	mode := armcs.AgentPoolModeSystem
	cnt := int32(1)
	body.Properties.Mode = &mode
	body.Properties.Count = &cnt
	body.Properties.MinCount = nil
	body.Properties.MaxCount = nil
	body.Properties.EnableAutoScaling = nil
	if body.Properties.Tags == nil {
		body.Properties.Tags = map[string]*string{}
	}
	body.Properties.Tags["purpose"] = ptr("temp-system-aroslsre-924")
	return body, nil
}

func (c *clients) addSystmp(ctx context.Context, live *armcs.AgentPool) error {
	body, err := buildSystmpAgentPool(live, c.cfg.cpVersion)
	if err != nil {
		return err
	}
	logf("creating systmp (vmSize=%s, 1 node, k8s=%s, inherited taints)", strDeref(live.Properties.VMSize), c.cfg.cpVersion)
	poller, err := c.pools.BeginCreateOrUpdate(ctx, c.cfg.resourceGroup, c.cfg.clusterName, systmpPoolName, *body, nil)
	if err != nil {
		return fmt.Errorf("begin create systmp: %w", err)
	}
	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		return fmt.Errorf("poll create systmp: %w", err)
	}
	logf("systmp pool created; waiting for k8s node Ready")
	return c.waitForReadyNodes(ctx, systmpPoolName, 1, systmpReadyTOMin*time.Minute)
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

func (c *clients) recreateSystem(ctx context.Context, live *armcs.AgentPool) error {
	body, err := agentPoolForCreate(live, c.cfg.cpVersion)
	if err != nil {
		return fmt.Errorf("agent pool clone: %w", err)
	}
	if b, err := json.MarshalIndent(body, "", "  "); err == nil {
		logf("--- sanitized PUT body ---\n%s", string(b))
	}
	logf("BeginCreateOrUpdate system pool")
	poller, err := c.pools.BeginCreateOrUpdate(ctx, c.cfg.resourceGroup, c.cfg.clusterName, systemPoolName, *body, nil)
	if err != nil {
		return fmt.Errorf("begin recreate system: %w", err)
	}
	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		return fmt.Errorf("poll recreate system: %w", err)
	}
	expected := int32(1)
	if body.Properties != nil {
		if body.Properties.MinCount != nil {
			expected = *body.Properties.MinCount
		} else if body.Properties.Count != nil {
			expected = *body.Properties.Count
		}
	}
	logf("system pool ARM-Succeeded; waiting for %d Ready k8s node(s)", expected)
	return c.waitForReadyNodes(ctx, systemPoolName, int(expected), systemReadyTOMin*time.Minute)
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
				"aroslsre-924-recreate": &ts,
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

// triggerSystemReconcile starts an AKS RP reconcile of the live system
// pool by issuing a sanitized CreateOrUpdate with the snapshot spec.
// It does not wait for the LRO to finish — the caller polls the activity
// log for NRP-KVS evidence in parallel and aborts the LRO when done.
func (c *clients) triggerSystemReconcile(ctx context.Context, live *armcs.AgentPool) error {
	body, err := agentPoolForCreate(live, c.cfg.cpVersion)
	if err != nil {
		return fmt.Errorf("triggerSystemReconcile: %w", err)
	}
	logf("triggering AgentPool CreateOrUpdate on %q (no-op reconcile with live spec)", systemPoolName)
	if _, err := c.pools.BeginCreateOrUpdate(ctx, c.cfg.resourceGroup, c.cfg.clusterName, systemPoolName, *body, nil); err != nil {
		return fmt.Errorf("begin trigger system reconcile: %w", err)
	}
	return nil
}

// pollForNRPEvidence re-queries the activity log on a fixed interval
// until the NRP-KVS Failed-event count reaches threshold or the timeout
// elapses. windowMin controls how far back each poll looks (a window
// equal to the timeout makes every poll see all events since the
// trigger).
func (c *clients) pollForNRPEvidence(ctx context.Context, timeout time.Duration, pollInterval time.Duration, windowMin int, threshold int) (int, error) {
	if pollInterval <= 0 {
		pollInterval = 60 * time.Second
	}
	deadline := time.Now().Add(timeout)
	last := 0
	for {
		out, err := c.activityLogJSON(ctx, c.cfg.nodeRG, fmt.Sprintf("%dm", windowMin))
		if err != nil {
			return last, fmt.Errorf("forced-evidence activity-log query: %w", err)
		}
		hits, parseErr := countNRPFailures(out, systemVMSSPrefix)
		if parseErr != nil {
			return last, fmt.Errorf("forced-evidence activity-log parse: %w", parseErr)
		}
		last = hits
		logf("forced evidence poll: NRP-KVS hits=%d threshold=%d (window=%dm)", hits, threshold, windowMin)
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

// abortSystemReconcile aborts the latest LRO on the system pool, which
// (when the forced-evidence trigger started one) cancels the in-flight
// CreateOrUpdate. Best-effort: failures here are logged by the caller
// but not propagated.
func (c *clients) abortSystemReconcile(ctx context.Context) error {
	poller, err := c.pools.BeginAbortLatestOperation(ctx, c.cfg.resourceGroup, c.cfg.clusterName, systemPoolName, nil)
	if err != nil {
		return fmt.Errorf("begin abort system reconcile: %w", err)
	}
	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		return fmt.Errorf("poll abort system reconcile: %w", err)
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

// reasonIsNRPStormFail reports whether a detect() reason string
// indicates that the cluster-state, non-system-pools, and system-wedge
// checks all PASSed and only the NRP-KVS-storm check FAILed.
// Used to gate the forced-evidence trigger path: the cluster is in a
// wedge-compatible state, but the activity log has no recent NRP-KVS
// events in the configured lookback window. The forced-evidence path
// triggers a no-op reconcile on the live system pool to give AKS a
// chance to re-attempt the failing VMSS write before deciding to act.
func reasonIsNRPStormFail(reason string) bool {
	return strings.HasPrefix(strings.TrimSpace(reason), "NRP-KVS storm FAIL")
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
