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
//   DRY_RUN                     "true" to print intended actions but make no writes
//   SKIP_GUARDS                 "true" to bypass the cluster-state / non-target-pool
//                               safety guards (operator override). Has no effect when
//                               the confirmed target list is empty: the run still
//                               exits no-op so that Step 2 (maybeAbortLRO) and Step 8
//                               (reconcileTagPut) never execute without a pool to
//                               recreate.
//   LOG_VERBOSITY               integer logr verbosity for slog handler (default 0)
//
// Detection
// ---------
// Names below are the check labels used in log lines and reason
// strings throughout this binary. Checks run in the order listed
// (cluster state -> non-target pools -> ARM prechecks).
//
// Two ARM-only prechecks ([accel-net] and [empty ipconfig], below)
// decide which pools to recreate on direct ARM evidence. They give us
// a-priori knowledge of which pools WILL fail to scale up before scaling
// is attempted, so detection no longer queries the reactive NRP-KVS
// activity-log write storm (which only appears once AKS reconciles a
// wedged pool and merely confirms what the ipConfigurations array already
// shows). A pool flagged by either precheck is recreated directly.
//
//   [cluster state]      cluster provisioningState is recoverable:
//                        Succeeded, Canceled, Failed (settled) OR
//                        Updating, Upgrading (mid-LRO — the NRP-KVS
//                        wedge signature itself; step 2 decides
//                        whether to abort the LRO). Creating and
//                        Deleting are rejected; unknown states are
//                        rejected conservatively.
//   [non-target pools]   every pool not selected by NODEPOOL_TAG has
//                        count > 0 (cluster-wide safety guard).
//   [accel-net]          any selected Swift pool whose backing VMSS came
//                        up with accelerated-networking DISABLED — its
//                        delegated Swift NICs can never attach, so the
//                        pool is broken regardless of provisioning state.
//                        Direct ARM evidence; flags the pool for
//                        recreation. Fails open: skipped when there are
//                        no Swift pools or the VMSS list errors.
//   [empty ipconfig]     any selected pool whose backing VMSS has a
//                        realized instance NIC with an empty
//                        ipConfigurations array — the ARM-visible
//                        signature of the NRP null-pointer defect (the
//                        delegated Swift NIC was created but never
//                        populated, so kubelet never registers). This
//                        is authoritative direct evidence and flags the
//                        pool for recreation. Fails open: pools with no
//                        backing VMSS, no realized NICs yet, or whose
//                        NIC list errors are skipped, never flagged.
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
	armcompute "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v6"
	armcs "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v6"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/monitor/armmonitor"
	armnetwork "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v6"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"

	mcclient "github.com/Azure/ARO-HCP/sessiongate/pkg/mc"
)

const (
	lroAbortAgeMin  = 30
	lroLookupWindow = "14d"
	tempReadyTOMin  = 8
	poolReadyTOMin  = 15
	pollIntervalSec = 30

	// vmssReadyTOMin caps how long we wait for AKS to materialize the
	// backing VMSS of a freshly-created (0-node) agent pool before we can
	// inspect/patch its accelerated-networking setting (AROSLSRE-1172).
	vmssReadyTOMin      = 5
	vmssPollIntervalSec = 15

	// aksManagedPoolNameTag is the tag AKS stamps on every node-pool VMSS
	// carrying the owning agent-pool name. It is the authoritative way to
	// map an agent pool to its backing VMSS (the VMSS name itself is
	// aks-<poolName>-<hash>-vmss but the tag is exact).
	aksManagedPoolNameTag = "aks-managed-poolName"

	// swiftNodepoolTag is the tag the AKS provisioning bicep stamps on
	// Swift V2 multi-tenancy node pools (enableSwiftV2Nodepools=true). A
	// pool carrying this tag with value "true" hosts pods on delegated
	// Swift NICs, which cannot attach unless the backing VMSS has
	// accelerated-networking enabled. It is therefore both the marker that
	// a pool REQUIRES accelerated-networking (AROSLSRE-1172) and the signal
	// used to detect Swift pools whose VMSS came up with it disabled.
	// Source: dev-infrastructure/modules/aks-cluster-base.bicep (swiftNodepoolTags).
	swiftNodepoolTag      = "aks-nic-enable-multi-tenancy"
	swiftNodepoolTagValue = "true"

	// overallTimeoutMin caps the whole binary via the single
	// context.WithTimeout in run(); every Azure LRO poller, drain, and
	// pre/post-flight dump inherits that ctx, so nothing outlives it. The EV2
	// ShellExtension host honors the `timeout` field set on the corresponding
	// Shell step in mgmt-pipeline.yaml, which must stay strictly above this
	// value so the binary reaches its own deadline first and returns a logged
	// timeout error (unwinding any in-flight pollers) instead of being
	// force-killed by the host mid-operation.
	overallTimeoutMin = 120

	// nrpKVSErrorCode / vmssWriteOperation identify the NRP-KVS VMSS-write
	// failures surfaced in the post-flight activity-log dump (dumpPostflight,
	// observability only). They no longer gate detection — empty-ipconfig and
	// accelerated-networking prechecks decide which pools to recreate.
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
)

const nodePoolRoleLabel = "aro-hcp.azure.com/role"

type nodePoolTarget struct {
	name           string
	vmssPrefix     string
	accelNetBroken bool
	emptyIPConfig  bool
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

// poolIsSwift reports whether an agent pool is a Swift V2 multi-tenancy pool,
// identified by the swiftNodepoolTag=true tag the provisioning bicep stamps on
// Swift pools. Swift pods require accelerated-networking on the backing VMSS,
// so this is the signal that a pool MUST have accelerated-networking enabled.
func poolIsSwift(p *armcs.AgentPool) bool {
	if p == nil || p.Properties == nil {
		return false
	}
	return strings.EqualFold(tagValue(p.Properties.Tags, swiftNodepoolTag), swiftNodepoolTagValue)
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
	adoptLeftoverTempPools(ctx context.Context) error
	adoptLeftoverTempPool(ctx context.Context, target nodePoolTarget) error
	snapshotPool(ctx context.Context, poolName string) (*armcs.AgentPool, error)
	maybeAbortLRO(ctx context.Context) (bool, error)
	addTempPool(ctx context.Context, target nodePoolTarget, live *armcs.AgentPool) error
	drainPool(ctx context.Context, pool string, timeout time.Duration) error
	deletePool(ctx context.Context, pool string) error
	recreatePool(ctx context.Context, poolName string, live *armcs.AgentPool) error
	reconcileTagPut(ctx context.Context) error
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

	if !cfg.dryRun {
		logBanner("LEFTOVER TEMP POOL ADOPTION")
		if err := orch.adoptLeftoverTempPools(ctx); err != nil {
			return fmt.Errorf("adopt leftover temp pools: %w", err)
		}
	}

	logBanner("DETECTION GUARDS")
	if cfg.skipGuards {
		logf("SKIP_GUARDS=true — bypassing detection guards")
	}
	targets, reason, err := orch.detect(ctx)
	if err != nil {
		return fmt.Errorf("detection: %w", err)
	}

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
		logf("  pool=%s role=%s accelNetBroken=%t emptyIPConfig=%t", target.name, cfg.nodePoolTag, target.accelNetBroken, target.emptyIPConfig)
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
	// The post-LRO detect re-issues the ARM prechecks (accelerated-networking
	// disabled, empty NIC ipConfigurations) from scratch. These are direct,
	// authoritative signals — unlike the old NRP-KVS storm probe, they do not
	// depend on having just provoked a reconcile, so the re-detect reliably
	// re-reports any pool that is still broken. We therefore trust its result
	// outright with no pre-LRO carry-forward.
	targets, reason, err = orch.detect(ctx)
	if err != nil {
		return fmt.Errorf("post-LRO detection: %w", err)
	}
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
	clusterName    string
	resourceGroup  string
	subscriptionID string
	nodeRG         string
	cpVersion      string
	nodePoolTag    string
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
		nodePoolTag:    env("NODEPOOL_TAG"),
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
	vmss         *armcompute.VirtualMachineScaleSetsClient
	nics         *armnetwork.InterfacesClient
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
	computeFactory, err := armcompute.NewClientFactory(cfg.subscriptionID, cred, nil)
	if err != nil {
		return nil, fmt.Errorf("arm compute factory: %w", err)
	}
	networkFactory, err := armnetwork.NewClientFactory(cfg.subscriptionID, cred, nil)
	if err != nil {
		return nil, fmt.Errorf("arm network factory: %w", err)
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
		vmss:         computeFactory.NewVirtualMachineScaleSetsClient(),
		nics:         networkFactory.NewInterfacesClient(),
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
// a wedge-compatible state. It is used by evalNonTargetPoolsHealthy to
// decide whether a non-target pool looks healthy enough to proceed.
//
// Accepts:
//   - Failed   — RP gave up retrying the VMSS write chain.
//   - Canceled — operator already aborted the parent LRO.
//   - Updating / Upgrading — the cluster LRO is still retrying the
//     pool update forever (AROSLSRE-880 / INT
//     2026-05-16..18 signature).
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

// guardDecision resolves a single detection guard's outcome against the
// SKIP_GUARDS operator override. It returns proceed=true when the run may
// continue past the guard, plus a human-readable log line describing the
// outcome:
//   - guard passed              -> proceed, "<name> PASS"
//   - guard failed, no override -> halt, the guard's reason (returned to the
//     caller so the run exits no-op with that reason)
//   - guard failed, skipGuards  -> proceed, "SKIP_GUARDS=true — overriding
//     <name> failure: <reason>" (operator override; failure logged, not enforced)
//
// This is what makes SKIP_GUARDS actually bypass the cluster-state and
// non-target-pool safety guards in detect(), matching its documented behavior.
func guardDecision(name string, pass bool, reason string, skipGuards bool) (proceed bool, logMsg string) {
	if pass {
		return true, name + " PASS"
	}
	if skipGuards {
		return true, fmt.Sprintf("SKIP_GUARDS=true — overriding %s failure: %s", name, reason)
	}
	return false, reason
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
	csPass, csReason := evalClusterState(cs)
	proceed, msg := guardDecision("cluster state", csPass, csReason, c.cfg.skipGuards)
	if !proceed {
		return nil, msg, nil
	}
	logf("%s", msg)

	pager := c.pools.NewListPager(c.cfg.resourceGroup, c.cfg.clusterName, nil)
	var allPools []*armcs.AgentPool
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, "", fmt.Errorf("list pools: %w", err)
		}
		allPools = append(allPools, page.Value...)
	}

	ntPass, ntReason := evalNonTargetPoolsHealthy(allPools)
	proceed, msg = guardDecision("cluster safety", ntPass, ntReason, c.cfg.skipGuards)
	if !proceed {
		return nil, msg, nil
	}
	logf("%s", msg)

	// Classify pools by role match without touching the Activity Log. The two
	// ARM-only prechecks below give us a priori knowledge of which pools are
	// broken (their NICs will never attach), so we no longer query the
	// activity-log NRP-KVS write storm — that signal is reactive (it only
	// appears after AKS reconciles a wedged pool) and merely confirms what the
	// ipConfigurations array already tells us before any scale-up is attempted.
	var selected []nodePoolTarget
	var swiftSelected []nodePoolTarget
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
		t := nodePoolTarget{name: name, vmssPrefix: vmssPrefix}
		selected = append(selected, t)
		if poolIsSwift(p) {
			swiftSelected = append(swiftSelected, t)
		}
	}
	if len(selected) == 0 {
		return nil, fmt.Sprintf("no node pools found with %s=%q", nodePoolRoleLabel, c.cfg.nodePoolTag), nil
	}

	// Accelerated-networking precheck (AROSLSRE-1172): a Swift pool whose
	// backing VMSS came up with accelerated-networking DISABLED is broken
	// regardless of provisioning state — its Swift NICs can never attach — so
	// it is flagged for recreation directly. This is at most one cheap VMSS
	// List (skipped entirely when there are no Swift pools) and fails open.
	anBroken, anErr := c.detectAccelNetBrokenPools(ctx, swiftSelected)
	if anErr != nil {
		logf("WARN: accelerated-networking precheck failed: %v (continuing with empty-ipconfig detection only)", anErr)
		anBroken = nil
	}

	// Empty-ipConfiguration precheck (Steve Kuznetsov, MSFT): a pool whose
	// backing VMSS has realized instance NICs with an empty ipConfigurations
	// array is broken by the NRP null-pointer defect (ICM 798003653) — the
	// node never attaches its Swift NIC and kubelet never registers. This is
	// the a-priori predictor: it tells us which pools WILL fail to scale up
	// before scaling is attempted, so we short-circuit the old
	// detect-storm-then-recreate flow. The activity-log write storm only
	// surfaces once AKS reconciles the pool, so gating recreation on it lets
	// broken pools linger and forces a slow one-by-one recovery across
	// rollouts; detecting the empty ipConfigurations directly recreates every
	// affected pool in a single pass. This runs over ALL selected role-matched
	// pools (so it covers the system pool too, not just Swift-tagged ones), is
	// ARM-only, and fails open so it never regresses existing behavior.
	emptyIPCfgBroken, eicErr := c.detectEmptyIPConfigPools(ctx, selected)
	if eicErr != nil {
		logf("WARN: empty-ipconfig precheck failed: %v (continuing with accelerated-networking detection only)", eicErr)
		emptyIPCfgBroken = nil
	}

	if len(anBroken) == 0 && len(emptyIPCfgBroken) == 0 {
		return nil, fmt.Sprintf("no selected node pools with %s=%q have Swift accelerated-networking disabled or empty NIC ipConfigurations", nodePoolRoleLabel, c.cfg.nodePoolTag), nil
	}

	// Both prechecks yield directly confirmed targets. Merge them, deduping a
	// pool flagged by both so it carries both markers for observability.
	var targets []nodePoolTarget
	idxByName := map[string]int{}
	for _, ab := range anBroken {
		logf("pool %s: confirmed broken by accelerated-networking precheck (Swift pool with AN disabled)", ab.name)
		idxByName[ab.name] = len(targets)
		targets = append(targets, ab)
	}
	for _, eb := range emptyIPCfgBroken {
		logf("pool %s: confirmed broken by empty-ipconfig precheck (backing VMSS has NIC(s) with empty ipConfigurations)", eb.name)
		if i, ok := idxByName[eb.name]; ok {
			targets[i].emptyIPConfig = true
			continue
		}
		idxByName[eb.name] = len(targets)
		targets = append(targets, eb)
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

// targetFromLeftoverTempPool validates a temp pool discovered during the
// pre-detection adoption scan and returns the source pool target that owns it.
// Non-temp pools and temp pools for other NODEPOOL_TAG values are ignored.
// Malformed temp pools for this run fail closed so the operator can inspect
// them before normal detection exits no-op and hides the leftover.
func targetFromLeftoverTempPool(p *armcs.AgentPool, nodePoolTag string) (*nodePoolTarget, bool, error) {
	if p == nil || p.Name == nil || p.Properties == nil {
		return nil, false, nil
	}
	if !isTempPool(p) {
		return nil, false, nil
	}
	if role := poolRoleTag(p); role != nodePoolTag {
		logf("adopt pre-scan: ignoring temp pool %q with %s=%q while this run targets %q", *p.Name, nodePoolRoleLabel, role, nodePoolTag)
		return nil, false, nil
	}

	srcID := tempPoolSourceID(p)
	if srcID == "" {
		return nil, true, fmt.Errorf("leftover temp pool %q has no %q tag", *p.Name, tempPoolSourceTag)
	}
	srcName, err := poolNameFromARMID(srcID)
	if err != nil {
		return nil, true, fmt.Errorf("leftover temp pool %q has malformed %q tag %q: %w", *p.Name, tempPoolSourceTag, srcID, err)
	}
	expectedName := tempPoolName(srcName)
	if *p.Name != expectedName {
		return nil, true, fmt.Errorf("leftover temp pool %q source tag points to %q, whose deterministic temp name is %q", *p.Name, srcName, expectedName)
	}

	return &nodePoolTarget{name: srcName, vmssPrefix: poolVMSSPrefix(srcName)}, true, nil
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

// adoptLeftoverTempPools scans all agent pools before detection can exit
// no-op. This makes the Succeeded-source and missing-source adoption paths
// reachable even when no source pool is currently wedge-compatible.
func (c *clients) adoptLeftoverTempPools(ctx context.Context) error {
	pager := c.pools.NewListPager(c.cfg.resourceGroup, c.cfg.clusterName, nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return fmt.Errorf("adopt pre-scan: list pools: %w", err)
		}
		for _, p := range page.Value {
			target, ok, err := targetFromLeftoverTempPool(p, c.cfg.nodePoolTag)
			if err != nil {
				return fmt.Errorf("adopt pre-scan: %w", err)
			}
			if !ok {
				continue
			}
			logf("adopt pre-scan: found leftover temp pool %q for source %q", strDeref(p.Name), target.name)
			if err := c.adoptLeftoverTempPool(ctx, *target); err != nil {
				return err
			}
		}
	}
	return nil
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
// pool). The temporary pool uses the source pool's current Count so it
// can absorb the source pool's capacity during drain/delete. If Count is
// unavailable, it falls back to MinCount and then to one node so remediation
// can still proceed. The temporary pool tags itself with
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
	cnt, _ := tempPoolDesiredCount(body.Properties)
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
	_, countSource := tempPoolDesiredCount(live.Properties)
	wantReady := int(*body.Properties.Count)
	if countSource == "MinCount" {
		logf("source pool %s has no positive Count; using MinCount=%d for temp pool size", target.name, wantReady)
	}
	if countSource == "default" {
		logf("WARN: source pool %s has no positive Count or MinCount; using minimal temp pool size count=1", target.name)
	}
	logf("creating temp pool %s for %s (vmSize=%s, mode=%v, count=%d, countSource=%s, k8s=%s, inherited taints)",
		tmpName, target.name, strDeref(live.Properties.VMSize), ptrValue(live.Properties.Mode), wantReady, countSource, c.cfg.cpVersion)
	// AROSLSRE-1172: AKS can bring the temp pool's VMSS up with
	// accelerated-networking (Swift) DISABLED in some regions, which makes
	// the new nodes unable to host Swift pods drained off the broken source
	// pool. Create the pool empty, verify/correct accelerated-networking on
	// the backing VMSS against the still-present live source pool, and only
	// then scale up so every node boots with the correct NIC config.
	return c.createPoolZeroThenScale(ctx, tmpName, target.name, body, wantReady, tempPoolReadyTimeout(wantReady))
}

func tempPoolDesiredCount(props *armcs.ManagedClusterAgentPoolProfileProperties) (int32, string) {
	if props != nil {
		if props.Count != nil && *props.Count > 0 {
			return *props.Count, "Count"
		}
		if props.MinCount != nil && *props.MinCount > 0 {
			return *props.MinCount, "MinCount"
		}
	}
	return 1, "default"
}

func tempPoolReadyTimeout(wantReady int) time.Duration {
	if wantReady > 1 {
		return poolReadyTOMin * time.Minute
	}
	return tempReadyTOMin * time.Minute
}

// ---------------------------------------------------------------------------
// accelerated-networking (Swift) guard for newly-created pools — AROSLSRE-1172
//
// AKS does not expose accelerated-networking on the agent-pool API; the AKS RP
// sets it on the backing VMSS NIC config at create time. In some regions the
// RP has been observed to bring a pool's VMSS up with accelerated-networking
// DISABLED even when the source pool has it enabled, which breaks Swift pod
// networking and makes the repair fail (pods can't be drained onto the temp
// pool). To guard against this, every pool this binary creates (the temp pool
// AND the recreated source pool) is first created with zero nodes so AKS
// materializes the VMSS empty; we then verify — and if necessary patch —
// accelerated-networking on the empty VMSS before scaling up, so every node that
// boots inherits the corrected NIC config with no reimage. The check fails OPEN
// (logs a WARN and proceeds) when the VMSS can't be located or its value is
// indeterminate; it fails CLOSED (refuses to scale up) only when a known-wrong,
// required value cannot be corrected, rather than create Swift-broken nodes.
// ---------------------------------------------------------------------------

// accelNetDecision is the outcome of comparing a pool's VMSS
// accelerated-networking value against its reference pool.
type accelNetDecision struct {
	patch  bool   // whether the target VMSS must be patched
	want   bool   // the accelerated-networking value to enforce
	reason string // human-readable explanation for the logs
}

// decideAccelNetworking is a pure function (unit-testable) that decides whether
// the target pool's VMSS accelerated-networking value must be corrected.
//
// When swiftRequired is true the pool is a Swift V2 multi-tenancy pool, which
// cannot attach its delegated NIC without accelerated-networking. In that case
// the value is DEMANDED to be true regardless of the reference pool: we patch
// whenever the target is not already known-enabled. This fails CLOSED for Swift
// pools — even if the reference pool itself came up Swift-broken (e.g. a
// region-wide RP regression) we still enforce true rather than mirror the bad
// value.
//
// When swiftRequired is false it mirrors the reference pool and fails OPEN: when
// either side's value is unknown it returns no-patch, so behavior is never
// regressed on clusters where we cannot read the VMSS.
func decideAccelNetworking(swiftRequired, refAN, refFound, tgtAN, tgtFound bool) accelNetDecision {
	if swiftRequired {
		if tgtFound && tgtAN {
			return accelNetDecision{patch: false, want: true, reason: "swift pool requires accelerated-networking and target already has it enabled"}
		}
		return accelNetDecision{patch: true, want: true, reason: fmt.Sprintf("swift pool requires accelerated-networking; target=%t(found=%t); will enforce true", tgtAN, tgtFound)}
	}
	switch {
	case !refFound:
		return accelNetDecision{patch: false, reason: "reference accelerated-networking unknown; proceeding without check"}
	case !tgtFound:
		return accelNetDecision{patch: false, reason: "target accelerated-networking unknown; proceeding without check"}
	case refAN == tgtAN:
		return accelNetDecision{patch: false, want: refAN, reason: fmt.Sprintf("accelerated-networking already matches reference (%t)", refAN)}
	default:
		return accelNetDecision{patch: true, want: refAN, reason: fmt.Sprintf("accelerated-networking mismatch: reference=%t target=%t; will patch target to %t", refAN, tgtAN, refAN)}
	}
}

// vmssAcceleratedNetworking reports the accelerated-networking value of a VMSS,
// derived from its NIC configurations. found is false when the VMSS carries no
// NIC config with an explicit value. enabled is the logical AND across all NIC
// configs that carry an explicit value: a Swift pod can be scheduled onto any
// node NIC, so a single NIC with accelerated-networking disabled makes the pool
// Swift-broken. enabled is therefore true only when EVERY explicit NIC has it
// enabled.
func vmssAcceleratedNetworking(vmss *armcompute.VirtualMachineScaleSet) (enabled bool, found bool) {
	if vmss == nil || vmss.Properties == nil || vmss.Properties.VirtualMachineProfile == nil ||
		vmss.Properties.VirtualMachineProfile.NetworkProfile == nil {
		return false, false
	}
	enabled = true
	for _, nic := range vmss.Properties.VirtualMachineProfile.NetworkProfile.NetworkInterfaceConfigurations {
		if nic == nil || nic.Properties == nil || nic.Properties.EnableAcceleratedNetworking == nil {
			continue
		}
		found = true
		if !*nic.Properties.EnableAcceleratedNetworking {
			enabled = false
		}
	}
	if !found {
		return false, false
	}
	return enabled, true
}

// tagValue returns the value of an Azure resource tag, or "" if absent/nil.
func tagValue(tags map[string]*string, key string) string {
	if tags == nil {
		return ""
	}
	if v, ok := tags[key]; ok {
		return strDeref(v)
	}
	return ""
}

// matchPoolVMSS picks the backing VMSS of poolName from a pre-listed slice by
// the authoritative aks-managed-poolName tag that AKS stamps on every VMSS it
// manages (including freshly created 0-node pools). Returns ("", nil) when no
// VMSS carries the tag — we deliberately do NOT guess by name convention, since
// a wrong VMSS here would patch or recreate the wrong pool.
func matchPoolVMSS(all []*armcompute.VirtualMachineScaleSet, poolName string) (string, *armcompute.VirtualMachineScaleSet) {
	for _, v := range all {
		if v == nil || v.Name == nil {
			continue
		}
		if tagValue(v.Tags, aksManagedPoolNameTag) == poolName {
			return *v.Name, v
		}
	}
	return "", nil
}

// listNodeRGVMSS lists every VMSS in the node resource group once.
func (c *clients) listNodeRGVMSS(ctx context.Context) ([]*armcompute.VirtualMachineScaleSet, error) {
	if c.vmss == nil {
		return nil, errors.New("vmss client not initialized")
	}
	pager := c.vmss.NewListPager(c.cfg.nodeRG, nil)
	var all []*armcompute.VirtualMachineScaleSet
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("list VMSS in %s: %w", c.cfg.nodeRG, err)
		}
		all = append(all, page.Value...)
	}
	return all, nil
}

// findPoolVMSS locates the backing VMSS of an agent pool in the node resource
// group by the authoritative aks-managed-poolName tag. It scans each pager page
// and returns as soon as the tagged VMSS is found, so the common single-pool
// lookup (and waitForPoolVMSS's repeated polling) does not drain and allocate
// the full RG listing on every call.
func (c *clients) findPoolVMSS(ctx context.Context, poolName string) (string, *armcompute.VirtualMachineScaleSet, error) {
	if c.vmss == nil {
		return "", nil, errors.New("vmss client not initialized")
	}
	pager := c.vmss.NewListPager(c.cfg.nodeRG, nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return "", nil, fmt.Errorf("list VMSS in %s: %w", c.cfg.nodeRG, err)
		}
		if name, vmss := matchPoolVMSS(page.Value, poolName); vmss != nil {
			return name, vmss, nil
		}
	}
	return "", nil, fmt.Errorf("no VMSS found for pool %s in %s (tag %s=%s)", poolName, c.cfg.nodeRG, aksManagedPoolNameTag, poolName)
}

// detectAccelNetBrokenPools inspects the live backing VMSS of each Swift pool
// and returns those whose accelerated-networking is explicitly DISABLED. A
// Swift V2 pool in that state cannot attach its delegated NIC, so its nodes are
// broken and the pool must be recreated (AROSLSRE-1172) — independent of the
// NRP-KVS storm signal. It fails OPEN: pools whose VMSS is missing or whose
// accelerated-networking value is indeterminate are skipped, never flagged.
func (c *clients) detectAccelNetBrokenPools(ctx context.Context, swiftPools []nodePoolTarget) ([]nodePoolTarget, error) {
	if len(swiftPools) == 0 {
		return nil, nil
	}
	all, err := c.listNodeRGVMSS(ctx)
	if err != nil {
		return nil, err
	}
	var broken []nodePoolTarget
	for _, sp := range swiftPools {
		_, vmss := matchPoolVMSS(all, sp.name)
		if vmss == nil {
			logf("accel-net precheck: swift pool %s has no backing VMSS yet; skipping (fail-open)", sp.name)
			continue
		}
		an, found := vmssAcceleratedNetworking(vmss)
		if !found {
			logf("accel-net precheck: swift pool %s accelerated-networking indeterminate; skipping (fail-open)", sp.name)
			continue
		}
		if !an {
			logf("accel-net precheck: swift pool %s VMSS has accelerated-networking DISABLED — flagging broken (Swift NICs cannot attach; pool must be recreated)", sp.name)
			t := sp
			t.accelNetBroken = true
			broken = append(broken, t)
		}
	}
	return broken, nil
}

// countEmptyIPConfigs reports how many of the given realized network interfaces
// carry an empty ipConfigurations array, plus how many NICs gave a usable
// reading. A healthy NIC always has at least one ipConfiguration; an empty
// (but present) array is the ARM-visible signature of the NRP null-pointer
// defect, where NRP brings the (delegated) Swift NIC up but never populates its
// ipConfigurations. This mirrors Steve's triage query semantics
// (array_length(ipConfigurations) == 0), which matches only an explicit empty
// array — Kusto array_length(null) is null, not 0.
//
// Fail-open on indeterminate reads: NICs with nil Properties, OR with a nil
// (omitted/null) IPConfigurations slice, are skipped entirely — they do not
// count toward total or empty. Only a non-nil, zero-length slice is counted as
// the defect. In the Azure SDK an ARM `[]` deserializes to a non-nil empty
// slice while null/omitted deserializes to nil, so this distinction is exactly
// the array_length==0 vs null distinction.
func countEmptyIPConfigs(nics []*armnetwork.Interface) (total, empty int) {
	for _, nic := range nics {
		if nic == nil || nic.Properties == nil || nic.Properties.IPConfigurations == nil {
			continue
		}
		total++
		if len(nic.Properties.IPConfigurations) == 0 {
			empty++
		}
	}
	return total, empty
}

// listVMSSInstanceNICs lists every realized per-instance network interface of
// vmssName in the node resource group. This is the ARM equivalent of the
// NRP_Entities networkinterfaces view used to triage the defect.
func (c *clients) listVMSSInstanceNICs(ctx context.Context, vmssName string) ([]*armnetwork.Interface, error) {
	if c.nics == nil {
		return nil, errors.New("network interfaces client not initialized")
	}
	pager := c.nics.NewListVirtualMachineScaleSetNetworkInterfacesPager(c.cfg.nodeRG, vmssName, nil)
	var all []*armnetwork.Interface
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("list NICs for VMSS %s in %s: %w", vmssName, c.cfg.nodeRG, err)
		}
		all = append(all, page.Value...)
	}
	return all, nil
}

// detectEmptyIPConfigPools inspects the realized instance NICs of each pool's
// backing VMSS and returns those with at least one NIC whose ipConfigurations
// array is empty — the ARM-visible signature of the NRP null-pointer defect
// (ICM 798003653). Steve Kuznetsov (MSFT) established that this state is the
// authoritative "broken" signal and that every VMSS exhibiting it must be
// recreated, regardless of whether the Activity Log shows an NRP-KVS write
// storm: the write failures only surface once AKS reconciles the pool, so
// gating recreation on them lets broken pools linger across rollouts and forces
// a slow one-by-one recovery. Flagging on empty ipConfigurations lets a single
// run recreate every affected pool at once.
//
// It fails OPEN: a pool whose backing VMSS is missing, whose NIC listing
// errors, or which has no realized NICs yet (a freshly-created or
// scaling-to-zero pool) is skipped, never flagged. A pool is flagged only when
// at least one realized NIC was observed AND at least one of them has an empty
// ipConfigurations array.
func (c *clients) detectEmptyIPConfigPools(ctx context.Context, pools []nodePoolTarget) ([]nodePoolTarget, error) {
	if len(pools) == 0 {
		return nil, nil
	}
	all, err := c.listNodeRGVMSS(ctx)
	if err != nil {
		return nil, err
	}
	var broken []nodePoolTarget
	for _, p := range pools {
		vmssName, vmss := matchPoolVMSS(all, p.name)
		if vmss == nil {
			logf("empty-ipconfig precheck: pool %s has no backing VMSS yet; skipping (fail-open)", p.name)
			continue
		}
		nics, err := c.listVMSSInstanceNICs(ctx, vmssName)
		if err != nil {
			logf("WARN: empty-ipconfig precheck: listing NICs for pool %s VMSS %s failed: %v (skipping, fail-open)", p.name, vmssName, err)
			continue
		}
		total, empty := countEmptyIPConfigs(nics)
		if total == 0 {
			logf("empty-ipconfig precheck: pool %s VMSS %s has no realized instance NICs yet; skipping (fail-open)", p.name, vmssName)
			continue
		}
		if empty > 0 {
			logf("empty-ipconfig precheck: pool %s VMSS %s has %d/%d instance NIC(s) with empty ipConfigurations — flagging broken (NRP null-pointer; pool must be recreated)", p.name, vmssName, empty, total)
			t := p
			t.emptyIPConfig = true
			broken = append(broken, t)
		}
	}
	return broken, nil
}

// waitForPoolVMSS polls until the backing VMSS of poolName exists or timeout
// elapses. AKS creates the VMSS shortly after the agent-pool PUT completes,
// but not always synchronously, so a short poll is needed.
func (c *clients) waitForPoolVMSS(ctx context.Context, poolName string, timeout time.Duration) (string, *armcompute.VirtualMachineScaleSet, error) {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		name, vmss, err := c.findPoolVMSS(ctx, poolName)
		if err == nil {
			return name, vmss, nil
		}
		lastErr = err
		if time.Now().After(deadline) {
			return "", nil, fmt.Errorf("timed out after %s waiting for VMSS of pool %s: %w", timeout, poolName, lastErr)
		}
		select {
		case <-ctx.Done():
			return "", nil, ctx.Err()
		case <-time.After(vmssPollIntervalSec * time.Second):
		}
	}
}

// patchVMSSAccelNetworking sets accelerated-networking to want on every NIC
// configuration of the VMSS via a scoped PATCH that carries only the network
// profile. Directly patching an AKS-managed VMSS is unsupported by AKS, but
// Microsoft explicitly asked us to do this in ICM 815156644 as the mitigation
// for the RP bringing Swift VMSS up with accelerated-networking disabled;
// callers must re-verify the value afterwards because AKS may reconcile it back.
//
// A scoped PATCH (Update) is used instead of a read-modify-write full PUT
// (CreateOrUpdate) on purpose. A full PUT re-sends the VMSS
// storageProfile.imageReference, which on AKS nodes points at a shared-image
// gallery version in a first-party (RP-owned) subscription. ARM then runs a
// linked-access check (Microsoft.Compute/galleries/images/versions/read) on
// that subscription, which the deploying identity is not authorized for, so the
// PUT fails with 403 LinkedAuthorizationFailed (observed in INT, AROSLSRE-1172).
// Sending only the network profile omits storageProfile entirely, avoiding the
// image link while preserving every NIC/IP configuration verbatim (the existing
// configs are read back and re-emitted unchanged except for the AN flag).
func (c *clients) patchVMSSAccelNetworking(ctx context.Context, vmssName string, want bool) error {
	resp, err := c.vmss.Get(ctx, c.cfg.nodeRG, vmssName, nil)
	if err != nil {
		return fmt.Errorf("get VMSS %s: %w", vmssName, err)
	}
	vmss := resp.VirtualMachineScaleSet
	if vmss.Properties == nil || vmss.Properties.VirtualMachineProfile == nil ||
		vmss.Properties.VirtualMachineProfile.NetworkProfile == nil ||
		len(vmss.Properties.VirtualMachineProfile.NetworkProfile.NetworkInterfaceConfigurations) == 0 {
		return fmt.Errorf("VMSS %s has no NIC configurations to patch", vmssName)
	}
	for _, nic := range vmss.Properties.VirtualMachineProfile.NetworkProfile.NetworkInterfaceConfigurations {
		if nic == nil {
			continue
		}
		if nic.Properties == nil {
			nic.Properties = &armcompute.VirtualMachineScaleSetNetworkConfigurationProperties{}
		}
		nic.Properties.EnableAcceleratedNetworking = ptr(want)
	}
	updNICs, err := toUpdateNetworkInterfaceConfigs(vmss.Properties.VirtualMachineProfile.NetworkProfile.NetworkInterfaceConfigurations)
	if err != nil {
		return fmt.Errorf("convert VMSS %s network config for patch: %w", vmssName, err)
	}
	update := armcompute.VirtualMachineScaleSetUpdate{
		Properties: &armcompute.VirtualMachineScaleSetUpdateProperties{
			VirtualMachineProfile: &armcompute.VirtualMachineScaleSetUpdateVMProfile{
				NetworkProfile: &armcompute.VirtualMachineScaleSetUpdateNetworkProfile{
					NetworkInterfaceConfigurations: updNICs,
				},
			},
		},
	}
	logf("patching VMSS %s accelerated-networking=%t (scoped network PATCH; storageProfile omitted to avoid linked image auth)", vmssName, want)
	poller, err := c.vmss.BeginUpdate(ctx, c.cfg.nodeRG, vmssName, update, nil)
	if err != nil {
		return fmt.Errorf("begin update VMSS %s: %w", vmssName, err)
	}
	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		return fmt.Errorf("poll update VMSS %s: %w", vmssName, err)
	}
	return nil
}

// toUpdateNetworkInterfaceConfigs converts the full-model NIC configurations
// read from a VMSS into their PATCH/Update equivalents, preserving every field
// (IP configurations, subnets, load-balancer pools, ...) by round-tripping
// through the shared ARM JSON representation. The create and update Go types
// differ only in their wrapper struct; their JSON wire keys are identical, so a
// marshal/unmarshal is a faithful, field-drift-proof translation that keeps the
// existing network configuration intact and changes only what the caller set.
func toUpdateNetworkInterfaceConfigs(nics []*armcompute.VirtualMachineScaleSetNetworkConfiguration) ([]*armcompute.VirtualMachineScaleSetUpdateNetworkConfiguration, error) {
	out := make([]*armcompute.VirtualMachineScaleSetUpdateNetworkConfiguration, 0, len(nics))
	for _, nic := range nics {
		if nic == nil {
			continue
		}
		b, err := json.Marshal(nic)
		if err != nil {
			return nil, fmt.Errorf("marshal NIC config: %w", err)
		}
		var upd armcompute.VirtualMachineScaleSetUpdateNetworkConfiguration
		if err := json.Unmarshal(b, &upd); err != nil {
			return nil, fmt.Errorf("unmarshal NIC config into update form: %w", err)
		}
		out = append(out, &upd)
	}
	if len(out) == 0 {
		return nil, errors.New("no NIC configurations to convert")
	}
	return out, nil
}

// ensurePoolAccelNetworking ensures poolName's backing VMSS has the correct
// accelerated-networking value before scale-up, patching it when it does not.
// When swiftRequired is true the value is DEMANDED to be enabled (the pool is a
// Swift V2 pool whose NICs cannot attach without it); otherwise the value is
// mirrored from refPool's VMSS. It returns whether a patch was applied and the
// value that was enforced, so the caller can re-verify after scale-up.
//
// It fails OPEN (returns patched=false, no error) when the VMSS cannot be
// located or the value is indeterminate, so clusters where the VMSS is
// unreadable keep the prior behavior. It fails CLOSED (returns an error) only
// when a known-required value cannot be corrected — refusing to scale onto a
// VMSS that would create Swift-broken nodes.
func (c *clients) ensurePoolAccelNetworking(ctx context.Context, poolName, refPool string, swiftRequired bool) (patched bool, want bool, err error) {
	tgtName, tgtVMSS, err := c.waitForPoolVMSS(ctx, poolName, vmssReadyTOMin*time.Minute)
	if err != nil {
		logf("WARN: pool %s: could not locate backing VMSS to verify accelerated-networking: %v (proceeding without check)", poolName, err)
		return false, false, nil
	}
	tgtAN, tgtFound := vmssAcceleratedNetworking(tgtVMSS)

	// A Swift pool demands accelerated-networking unconditionally, so the
	// reference value is only consulted for non-Swift pools.
	var refAN, refFound bool
	if !swiftRequired {
		_, refVMSS, err := c.findPoolVMSS(ctx, refPool)
		if err != nil {
			logf("WARN: reference pool %s: could not locate backing VMSS to read desired accelerated-networking for %s: %v (proceeding without check)", refPool, poolName, err)
			return false, false, nil
		}
		refAN, refFound = vmssAcceleratedNetworking(refVMSS)
	}

	d := decideAccelNetworking(swiftRequired, refAN, refFound, tgtAN, tgtFound)
	logf("pool %s accelerated-networking check: swiftRequired=%t reference(%s)=%t(found=%t) target=%t(found=%t): %s",
		poolName, swiftRequired, refPool, refAN, refFound, tgtAN, tgtFound, d.reason)
	if !d.patch {
		return false, d.want, nil
	}
	if err := c.patchVMSSAccelNetworking(ctx, tgtName, d.want); err != nil {
		return false, false, fmt.Errorf("pool %s: patch VMSS %s accelerated-networking=%t: %w", poolName, tgtName, d.want, err)
	}
	_, confirmVMSS, err := c.findPoolVMSS(ctx, poolName)
	if err != nil {
		return false, false, fmt.Errorf("pool %s: re-read VMSS after accelerated-networking patch: %w", poolName, err)
	}
	gotAN, gotFound := vmssAcceleratedNetworking(confirmVMSS)
	if !gotFound || gotAN != d.want {
		return false, false, fmt.Errorf("pool %s: accelerated-networking still %t (want %t) after patch; refusing to scale up onto a VMSS that would create Swift-broken nodes — manual intervention required", poolName, gotAN, d.want)
	}
	logf("pool %s: accelerated-networking patched to %t and confirmed; proceeding to scale up", poolName, d.want)
	return true, d.want, nil
}

// verifyPoolAccelNetworking re-reads poolName's VMSS after scale-up and errors
// if accelerated-networking regressed away from want. AKS may reconcile an
// unsupported VMSS edit back to its own value during the scale-up PUT, so this
// post-scale gate prevents the binary from declaring success on Swift-broken
// nodes. A transient read failure is logged but not treated as fatal.
func (c *clients) verifyPoolAccelNetworking(ctx context.Context, poolName string, want bool) error {
	_, vmss, err := c.findPoolVMSS(ctx, poolName)
	if err != nil {
		logf("WARN: pool %s: could not re-read VMSS to confirm accelerated-networking after scale-up: %v", poolName, err)
		return nil
	}
	got, found := vmssAcceleratedNetworking(vmss)
	if !found {
		logf("WARN: pool %s: accelerated-networking indeterminate on VMSS after scale-up; skipping post-scale confirmation", poolName)
		return nil
	}
	if got != want {
		return fmt.Errorf("pool %s: accelerated-networking regressed to %t (want %t) after scale-up; AKS reconciled the VMSS back to the broken value — refusing to use Swift-broken nodes, manual intervention required", poolName, got, want)
	}
	logf("pool %s: accelerated-networking=%t confirmed after scale-up", poolName, want)
	return nil
}

// zeroNodePoolBody returns a deep copy of body with the node count forced to
// zero and autoscaling disabled, so AKS creates (or recreates) the backing
// VMSS with no instances. The input body is never mutated; the deep copy is
// produced via a JSON round-trip so the autoscaling/count overrides cannot
// alias the caller's final-size body.
func zeroNodePoolBody(body *armcs.AgentPool) (*armcs.AgentPool, error) {
	if body == nil || body.Properties == nil {
		return nil, errors.New("zeroNodePoolBody: nil body")
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}
	var out armcs.AgentPool
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("unmarshal: %w", err)
	}
	out.Properties.Count = ptr(int32(0))
	out.Properties.EnableAutoScaling = ptr(false)
	out.Properties.MinCount = nil
	out.Properties.MaxCount = nil
	return &out, nil
}

// putPoolWait issues an agent-pool CreateOrUpdate PUT and blocks until ARM
// reports the operation done.
func (c *clients) putPoolWait(ctx context.Context, poolName string, body *armcs.AgentPool) error {
	poller, err := c.pools.BeginCreateOrUpdate(ctx, c.cfg.resourceGroup, c.cfg.clusterName, poolName, *body, nil)
	if err != nil {
		return fmt.Errorf("begin create/update pool %s: %w", poolName, err)
	}
	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		return fmt.Errorf("poll create/update pool %s: %w", poolName, err)
	}
	return nil
}

// createPoolZeroThenScale creates (or recreates) an agent pool with the
// accelerated-networking guard described above: create empty → verify/correct
// the VMSS against refPool → scale up to wantCount → wait for Ready nodes →
// re-verify when a patch was applied. finalBody carries the pool's intended
// final spec (size, autoscaling, taints, etc); the zero-node create transiently
// disables autoscaling so the VMSS comes up empty, and the scale-up PUT
// restores finalBody verbatim.
func (c *clients) createPoolZeroThenScale(ctx context.Context, poolName, refPool string, finalBody *armcs.AgentPool, wantCount int, readyTimeout time.Duration) error {
	zeroBody, err := zeroNodePoolBody(finalBody)
	if err != nil {
		return fmt.Errorf("build zero-node body for %s: %w", poolName, err)
	}
	logf("creating pool %s at 0 nodes (autoscaling disabled) to inspect accelerated-networking before scale-up", poolName)
	if err := c.putPoolWait(ctx, poolName, zeroBody); err != nil {
		return fmt.Errorf("create pool %s at 0 nodes: %w", poolName, err)
	}

	swiftRequired := poolIsSwift(finalBody)
	patched, wantAN, err := c.ensurePoolAccelNetworking(ctx, poolName, refPool, swiftRequired)
	if err != nil {
		return err
	}

	logf("scaling pool %s up to %d node(s)", poolName, wantCount)
	if err := c.putPoolWait(ctx, poolName, finalBody); err != nil {
		return fmt.Errorf("scale pool %s to %d: %w", poolName, wantCount, err)
	}
	logf("pool %s scaled; waiting for %d Ready k8s node(s) (timeout=%s)", poolName, wantCount, readyTimeout)
	if err := c.waitForReadyNodes(ctx, poolName, wantCount, readyTimeout); err != nil {
		return err
	}
	// Re-verify after scale-up when we patched the VMSS, and also for Swift
	// pools that came up already-enabled (wantAN==true, no patch): AKS may
	// reconcile the VMSS during the scale-up PUT, so a Swift pool — where
	// accelerated-networking is mandatory — must be confirmed even when no
	// patch was applied. The wantAN guard keeps the fail-open path intact:
	// when the VMSS was unreadable, ensurePoolAccelNetworking returns
	// wantAN==false and we skip the post-scale assertion rather than demand a
	// value we never managed to read (AROSLSRE-1172).
	if patched || (swiftRequired && wantAN) {
		return c.verifyPoolAccelNetworking(ctx, poolName, wantAN)
	}
	return nil
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
	expected := int32(1)
	if body.Properties != nil {
		if body.Properties.MinCount != nil {
			expected = *body.Properties.MinCount
		} else if body.Properties.Count != nil {
			expected = *body.Properties.Count
		}
	}
	logf("recreating pool %s (target %d Ready node(s))", poolName, expected)
	// AROSLSRE-1172: apply the same accelerated-networking guard to the
	// recreated source pool. By this point the source pool's own VMSS has
	// been deleted, so the reference for the desired accelerated-networking
	// value is the temp pool's VMSS, which addTempPool already verified (and
	// corrected if needed). Create the pool empty, verify/correct the VMSS,
	// then scale to the original size so the recreated nodes also boot with
	// the correct NIC config.
	return c.createPoolZeroThenScale(ctx, poolName, tempPoolName(poolName), body, int(expected), poolReadyTOMin*time.Minute)
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
