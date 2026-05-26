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
//   NRP_FAIL_THRESHOLD        Failed-event count threshold (default 10)
//   NRP_FAIL_WINDOW_MIN       Activity-log lookback window in min (default 15)
//   DRY_RUN                   "true" to print intended actions but make no writes
//
// Detection guards (ALL must pass; otherwise exit 0 no-op)
// --------------------------------------------------------
//   1. `system` pool has fewer Ready k8s nodes than its minCount.
//   2. >= NRP_FAIL_THRESHOLD Failed VMSS-write events on an
//      `aks-system-*` VMSS in the last NRP_FAIL_WINDOW_MIN.
//   3. Cluster provisioningState is recoverable: Succeeded, Canceled,
//      Failed (settled) OR Updating, Upgrading (mid-LRO — the NRP-KVS
//      wedge signature itself; step 2 decides whether to abort).
//      Creating and Deleting are rejected; unknown states are
//      rejected conservatively.
//   4. Every non-system pool has count > 0.
//   5. `system` pool's own provisioningState is NOT Succeeded —
//      positive confirmation that this specific pool is wedged.
//      Accepts Failed, Canceled, Updating, Upgrading: an NRP-KVS wedge
//      typically leaves the pool in Updating while its parent cluster
//      LRO retries forever (AROSLSRE-880), or in Failed/Canceled once
//      that LRO finally times out or is aborted by an operator.
//      Rejects Succeeded (no wedge) and Creating/Deleting/unknown
//      (don't act on transitional / unrecognized states).
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
//   4. Cordon + drain existing system nodes (kubectl drain).
//   5. Delete the broken `system` pool.
//   6. Re-create `system` via SDK CreateOrUpdate with the sanitized
//      AgentPool struct from the snapshot.
//   7. Cordon + drain + delete `systmp`.
//   8. No-op reconcile via tag update (kicks cluster back to Succeeded).

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	armcs "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v6"
)

const (
	systemPoolName    = "system"
	systmpPoolName    = "systmp"
	defaultThreshold  = 10
	defaultWindowMin  = 15
	lroAbortAgeMin    = 30
	systmpReadyTOMin  = 10
	systemReadyTOMin  = 10
	pollIntervalSec   = 30
	overallTimeoutMin = 60

	// aksAADServerAppID is the well-known Azure AD application ID of
	// the AKS API server in the public cloud. The MSI fetches tokens
	// scoped to this app to authenticate against kube-apiserver, which
	// is how this binary (and child kubectl invocations) talk to the
	// cluster without depending on `kubelogin` being installed in the
	// EV2 runner image.
	aksAADServerAppID = "6dae42f8-4368-4678-94ff-3960e28e3630"
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

func run() error {
	ctx, cancel := context.WithTimeout(context.Background(), overallTimeoutMin*time.Minute)
	defer cancel()

	cfg, err := loadConfig(ctx)
	if err != nil {
		return err
	}
	logBanner("STARTUP")
	cfg.logEnv()

	clients, err := newAzureClients(cfg)
	if err != nil {
		return fmt.Errorf("init azure clients: %w", err)
	}

	logBanner("CLUSTER EXISTENCE CHECK")
	mc, exists, err := clients.ensureCluster(ctx)
	if err != nil {
		return fmt.Errorf("ensure cluster: %w", err)
	}
	if !exists {
		logf("cluster %s/%s does not exist yet (greenfield rollout). Exiting no-op.", cfg.resourceGroup, cfg.clusterName)
		return nil
	}
	logf("cluster found: nodeResourceGroup=%s currentKubernetesVersion=%s", cfg.nodeRG, cfg.cpVersion)

	logBanner("KUBECONFIG BOOTSTRAP")
	kubeconfigPath, err := clients.bootstrapKube(ctx, mc)
	if err != nil {
		return fmt.Errorf("bootstrap kube client: %w", err)
	}
	defer func() {
		if rerr := os.Remove(kubeconfigPath); rerr != nil && !os.IsNotExist(rerr) {
			logf("WARN: failed to remove kubeconfig %s: %v", kubeconfigPath, rerr)
		}
	}()
	logf("kubeconfig=%s", kubeconfigPath)

	logBanner("PRE-FLIGHT STATE")
	if err := clients.dumpPreflight(ctx); err != nil {
		logf("WARN: pre-flight dump partial: %v", err)
	}

	logBanner("DETECTION GUARDS")
	act, reason, err := clients.detect(ctx)
	if err != nil {
		return fmt.Errorf("detection: %w", err)
	}
	if !act {
		logf("guards did not fire: %s. Exiting no-op.", reason)
		return nil
	}
	logf("ALL GUARDS PASSED — proceeding with recreate")

	if cfg.dryRun {
		logf("DRY_RUN=true — guards passed; would proceed with recreate. Exiting no-op.")
		return nil
	}

	if err := clients.preflightChecks(ctx); err != nil {
		return err
	}

	logBanner("STEP 1 :: snapshot system pool")
	live, err := clients.snapshotSystem(ctx)
	if err != nil {
		return fmt.Errorf("snapshot: %w", err)
	}

	logBanner("STEP 2 :: abort long-stuck cluster LRO if any")
	proceed, err := clients.maybeAbortLRO(ctx)
	if err != nil {
		return fmt.Errorf("abort LRO: %w", err)
	}
	if !proceed {
		logf("active LRO is younger than %dm; refusing to fight an in-progress op. Exiting no-op.", lroAbortAgeMin)
		return nil
	}

	logBanner("STEP 3 :: add throwaway 'systmp' System pool")
	if err := clients.addSystmp(ctx, live); err != nil {
		return fmt.Errorf("add systmp: %w", err)
	}

	logBanner("STEP 4 :: cordon + drain existing system nodes")
	if err := clients.drainPool(ctx, systemPoolName, 10*time.Minute); err != nil {
		return fmt.Errorf("drain system: %w", err)
	}

	logBanner("STEP 5 :: delete the broken 'system' pool")
	if err := clients.deletePool(ctx, systemPoolName); err != nil {
		return fmt.Errorf("delete system: %w", err)
	}

	logBanner("STEP 6 :: re-create 'system' via SDK CreateOrUpdate")
	if err := clients.recreateSystem(ctx, live); err != nil {
		return fmt.Errorf("recreate system: %w", err)
	}

	logBanner("STEP 7 :: drain + delete throwaway 'systmp' pool")
	if err := clients.drainPool(ctx, systmpPoolName, 5*time.Minute); err != nil {
		logf("WARN: systmp drain returned: %v (continuing to delete)", err)
	}
	if err := clients.deletePool(ctx, systmpPoolName); err != nil {
		return fmt.Errorf("delete systmp: %w", err)
	}

	logBanner("STEP 8 :: no-op reconcile via tag update")
	if err := clients.reconcileTagPut(ctx); err != nil {
		return fmt.Errorf("tag reconcile: %w", err)
	}

	logBanner("DONE")
	if err := clients.dumpPostflight(ctx); err != nil {
		logf("WARN: post-flight dump partial: %v", err)
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
	threshold      int
	windowMin      int
	dryRun         bool
}

// parseEnvConfig builds a config from environment variables only. It does
// not call any external tools or APIs, which makes it safe to unit-test.
func parseEnvConfig(env func(string) string) (*config, error) {
	c := &config{
		clusterName:   env("CLUSTER_NAME"),
		resourceGroup: env("RESOURCE_GROUP"),
		threshold:     defaultThreshold,
		windowMin:     defaultWindowMin,
	}
	if c.clusterName == "" {
		return nil, errors.New("CLUSTER_NAME is required")
	}
	if c.resourceGroup == "" {
		return nil, errors.New("RESOURCE_GROUP is required")
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
	return c, nil
}

func loadConfig(ctx context.Context) (*config, error) {
	c, err := parseEnvConfig(os.Getenv)
	if err != nil {
		return nil, err
	}
	sub, err := azShellTSV(ctx, "account", "show", "--query", "id")
	if err != nil {
		return nil, fmt.Errorf("az account show: %w", err)
	}
	c.subscriptionID = sub
	return c, nil
}

func (c *config) logEnv() {
	logf("CLUSTER_NAME=%s", c.clusterName)
	logf("RESOURCE_GROUP=%s", c.resourceGroup)
	logf("SUBSCRIPTION_ID=%s", c.subscriptionID)
	logf("NRP_FAIL_THRESHOLD=%d", c.threshold)
	logf("NRP_FAIL_WINDOW_MIN=%d", c.windowMin)
	logf("DRY_RUN=%t", c.dryRun)
}

// ---------------------------------------------------------------------------
// clients
// ---------------------------------------------------------------------------

type clients struct {
	cfg     *config
	cred    azcore.TokenCredential
	pools   *armcs.AgentPoolsClient
	cluster *armcs.ManagedClustersClient
	kube    kubernetes.Interface
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
	return &clients{
		cfg:     cfg,
		cred:    cred,
		pools:   clientFactory.NewAgentPoolsClient(),
		cluster: clientFactory.NewManagedClustersClient(),
	}, nil
}

// ensureCluster does an ARM Get on the managed cluster. If the cluster
// does not exist (HTTP 404), returns (zero, false, nil) so the caller
// can no-op exit cleanly. On any other error returns (zero, false, error).
// On success populates cfg.nodeRG and cfg.cpVersion from the live cluster.
func (c *clients) ensureCluster(ctx context.Context) (armcs.ManagedCluster, bool, error) {
	resp, err := c.cluster.Get(ctx, c.cfg.resourceGroup, c.cfg.clusterName, nil)
	if err != nil {
		if isNotFoundErr(err) {
			return armcs.ManagedCluster{}, false, nil
		}
		return armcs.ManagedCluster{}, false, fmt.Errorf("cluster get: %w", err)
	}
	mc := resp.ManagedCluster
	if mc.Properties == nil {
		return mc, true, errors.New("cluster has no properties")
	}
	if mc.Properties.NodeResourceGroup != nil {
		c.cfg.nodeRG = *mc.Properties.NodeResourceGroup
	}
	if mc.Properties.CurrentKubernetesVersion != nil {
		c.cfg.cpVersion = *mc.Properties.CurrentKubernetesVersion
	}
	if c.cfg.nodeRG == "" {
		return mc, true, errors.New("nodeResourceGroup empty")
	}
	if c.cfg.cpVersion == "" {
		return mc, true, errors.New("currentKubernetesVersion empty")
	}
	return mc, true, nil
}

// bootstrapKube fetches the AKS user kubeconfig via ARM
// (ListClusterUserCredentials), extracts the API server URL and CA from
// it, and writes a new kubeconfig that authenticates with a bearer
// token from the MSI scoped to the AKS AAD server app. The resulting
// kubeconfig works for both client-go (in this binary) and child
// `kubectl` invocations without depending on `kubelogin` being
// installed. The path is exported via KUBECONFIG so kubectl picks it up.
func (c *clients) bootstrapKube(ctx context.Context, mc armcs.ManagedCluster) (string, error) {
	credsResp, err := c.cluster.ListClusterUserCredentials(ctx, c.cfg.resourceGroup, c.cfg.clusterName, nil)
	if err != nil {
		return "", fmt.Errorf("ListClusterUserCredentials: %w", err)
	}
	if len(credsResp.Kubeconfigs) == 0 || credsResp.Kubeconfigs[0] == nil || credsResp.Kubeconfigs[0].Value == nil {
		return "", errors.New("ListClusterUserCredentials returned no kubeconfig")
	}
	rawKubeconfig := credsResp.Kubeconfigs[0].Value

	server, caData, err := extractAPIServerAndCA(rawKubeconfig)
	if err != nil {
		return "", fmt.Errorf("parse kubeconfig: %w", err)
	}

	tok, err := c.cred.GetToken(ctx, policy.TokenRequestOptions{Scopes: []string{aksAADServerAppID + "/.default"}})
	if err != nil {
		return "", fmt.Errorf("MSI token for AKS scope: %w", err)
	}

	kubeconfigPath := filepath.Join(os.TempDir(), fmt.Sprintf("recreate-system-pool-%d.kubeconfig", os.Getpid()))
	cfg := kubeconfigWithBearerToken(c.cfg.clusterName, server, caData, tok.Token)
	if err := clientcmd.WriteToFile(*cfg, kubeconfigPath); err != nil {
		return "", fmt.Errorf("write kubeconfig: %w", err)
	}
	if err := os.Setenv("KUBECONFIG", kubeconfigPath); err != nil {
		return kubeconfigPath, fmt.Errorf("set KUBECONFIG: %w", err)
	}

	restCfg := &rest.Config{
		Host:            server,
		BearerToken:     tok.Token,
		TLSClientConfig: rest.TLSClientConfig{CAData: caData},
	}
	kc, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		return kubeconfigPath, fmt.Errorf("kubernetes client: %w", err)
	}
	c.kube = kc
	return kubeconfigPath, nil
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

// extractAPIServerAndCA parses an AKS-emitted kubeconfig blob and
// returns the API server URL and CA bundle for its (single) cluster
// entry. We deliberately ignore any auth info in the kubeconfig — the
// caller substitutes a bearer token from the MSI.
func extractAPIServerAndCA(raw []byte) (string, []byte, error) {
	if len(raw) == 0 {
		return "", nil, errors.New("empty kubeconfig")
	}
	apiCfg, err := clientcmd.Load(raw)
	if err != nil {
		return "", nil, err
	}
	if len(apiCfg.Clusters) == 0 {
		return "", nil, errors.New("kubeconfig has no clusters")
	}
	for _, cl := range apiCfg.Clusters {
		if cl == nil {
			continue
		}
		if cl.Server == "" {
			return "", nil, errors.New("kubeconfig cluster has empty server")
		}
		if len(cl.CertificateAuthorityData) == 0 {
			return "", nil, errors.New("kubeconfig cluster has empty CA data")
		}
		return cl.Server, cl.CertificateAuthorityData, nil
	}
	return "", nil, errors.New("kubeconfig contained only nil clusters")
}

// kubeconfigWithBearerToken builds an in-memory kubeconfig that talks
// to `server` with the given CA, authenticating via a static bearer
// token. The context name matches the cluster name for clarity.
func kubeconfigWithBearerToken(clusterName, server string, caData []byte, token string) *clientcmdapi.Config {
	cfg := clientcmdapi.NewConfig()
	cfg.Clusters[clusterName] = &clientcmdapi.Cluster{
		Server:                   server,
		CertificateAuthorityData: caData,
	}
	cfg.AuthInfos[clusterName] = &clientcmdapi.AuthInfo{
		Token: token,
	}
	cfg.Contexts[clusterName] = &clientcmdapi.Context{
		Cluster:  clusterName,
		AuthInfo: clusterName,
	}
	cfg.CurrentContext = clusterName
	return cfg
}

// ---------------------------------------------------------------------------
// pre/post-flight dumps
// ---------------------------------------------------------------------------

func (c *clients) dumpPreflight(ctx context.Context) error {
	logf("--- nodepools ---")
	if _, err := runCmd(ctx, "az", "aks", "nodepool", "list",
		"-g", c.cfg.resourceGroup, "--cluster-name", c.cfg.clusterName,
		"--query", "[].{name:name,mode:mode,state:provisioningState,count:count,min:minCount,max:maxCount,k8s:currentOrchestratorVersion,vmSize:vmSize}",
		"-o", "table"); err != nil {
		return err
	}
	logf("--- cluster ---")
	if _, err := runCmd(ctx, "az", "aks", "show",
		"-g", c.cfg.resourceGroup, "-n", c.cfg.clusterName,
		"--query", "{prov:provisioningState,power:powerState.code,cpVer:currentKubernetesVersion,target:kubernetesVersion}",
		"-o", "json"); err != nil {
		return err
	}
	logf("--- k8s nodes (all) ---")
	_, _ = runCmd(ctx, "kubectl", "get", "nodes", "-o", "wide")
	logf("--- k8s nodes (system) ---")
	_, _ = runCmd(ctx, "kubectl", "get", "nodes", "-l", "agentpool="+systemPoolName, "-o", "wide")
	return nil
}

func (c *clients) dumpPostflight(ctx context.Context) error {
	logf("--- final nodepools ---")
	_, _ = runCmd(ctx, "az", "aks", "nodepool", "list",
		"-g", c.cfg.resourceGroup, "--cluster-name", c.cfg.clusterName,
		"--query", "[].{name:name,mode:mode,state:provisioningState,count:count,min:minCount,max:maxCount,k8s:currentOrchestratorVersion}",
		"-o", "table")
	logf("--- final cluster ---")
	_, _ = runCmd(ctx, "az", "aks", "show",
		"-g", c.cfg.resourceGroup, "-n", c.cfg.clusterName,
		"--query", "{prov:provisioningState,power:powerState.code,cpVer:currentKubernetesVersion}",
		"-o", "json")
	logf("--- post-flight: residual NRP failures (informational) ---")
	out, err := azJSON(ctx, "monitor", "activity-log", "list", "-g", c.cfg.nodeRG, "--offset", "10m")
	if err == nil {
		hits := countNRPFailures(out, "")
		logf("Failed VMSS-write events on %s in last 10m: %d", c.cfg.nodeRG, hits)
		if hits > 0 {
			ids := nrpResourceIDs(out)
			for _, id := range ids {
				logf("    %s", id)
			}
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// detection
// ---------------------------------------------------------------------------

// evalGuard1 reports whether the system pool is degraded (ready < minCount).
// Returns (pass, reason). If pass is false, reason explains why.
func evalGuard1(readyCount, systemMin int32) (bool, string) {
	if systemMin <= 0 {
		return false, fmt.Sprintf("guard 1 FAIL: system minCount=%d (no degradation possible)", systemMin)
	}
	if readyCount >= systemMin {
		return false, fmt.Sprintf("guard 1 FAIL: ready (%d) >= minCount (%d)", readyCount, systemMin)
	}
	return true, ""
}

// evalGuard2 reports whether NRP failure count exceeds the threshold.
func evalGuard2(failures, threshold int) (bool, string) {
	if threshold <= 0 {
		return false, fmt.Sprintf("guard 2 FAIL: threshold=%d (invalid)", threshold)
	}
	if failures < threshold {
		return false, fmt.Sprintf("guard 2 FAIL: only %d NRP failures < %d", failures, threshold)
	}
	return true, ""
}

// evalGuard3 reports whether the cluster is in a state where we can act.
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
func evalGuard3(provisioningState string) (bool, string) {
	switch provisioningState {
	case "Succeeded", "Canceled", "Failed", "Updating", "Upgrading":
		return true, ""
	case "Creating":
		return false, "guard 3 FAIL: cluster provisioningState=\"Creating\" (cluster not fully provisioned)"
	case "Deleting":
		return false, "guard 3 FAIL: cluster provisioningState=\"Deleting\" (cluster is being torn down)"
	case "":
		return false, "guard 3 FAIL: cluster provisioningState is empty"
	}
	return false, fmt.Sprintf("guard 3 FAIL: cluster provisioningState=%q is not a recognized recoverable state", provisioningState)
}

// evalGuard4 reports whether all non-system pools have count > 0 and a
// system pool exists. Also reports the system pool's minCount and
// provisioningState back to the caller so the latter can be fed into
// evalGuard5 without a second list-pools API call.
//
// Returns (pass, systemMin, systemProvState, reason).
func evalGuard4(pools []*armcs.AgentPool) (bool, int32, string, string) {
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
			return false, 0, "", fmt.Sprintf("guard 4 FAIL: non-system pool %q has count=0", name)
		}
	}
	if !systemFound {
		return false, 0, "", "guard 4 FAIL: no system pool found"
	}
	return true, systemMin, systemProvState, ""
}

// evalGuard5 reports whether the system pool itself is in a wedge-
// compatible state. Refines guard 2 (NRP failure storm) with a positive
// signal scoped to this exact agent-pool resource.
//
// Accepts:
//   - Failed   — RP gave up retrying the VMSS write chain.
//   - Canceled — operator already aborted the parent LRO.
//   - Updating / Upgrading — the cluster LRO is still retrying the
//     pool update forever (AROSLSRE-880 / INT
//     2026-05-16..18 signature). Guard 2 confirms
//     that the retries are NRP errors and not a
//     healthy upgrade.
//
// Rejects:
//   - Succeeded — pool is healthy; no wedge.
//   - Creating  — pool not fully created yet; do not interfere.
//   - Deleting  — pool being torn down; do not interfere.
//   - empty / unknown — fail conservatively.
func evalGuard5(systemProvState string) (bool, string) {
	switch systemProvState {
	case "Failed", "Canceled", "Updating", "Upgrading":
		return true, ""
	case "Succeeded":
		return false, "guard 5 FAIL: system pool provisioningState=\"Succeeded\" (no wedge)"
	case "Creating":
		return false, "guard 5 FAIL: system pool provisioningState=\"Creating\" (not fully created)"
	case "Deleting":
		return false, "guard 5 FAIL: system pool provisioningState=\"Deleting\" (being torn down)"
	case "":
		return false, "guard 5 FAIL: system pool provisioningState is empty"
	}
	return false, fmt.Sprintf("guard 5 FAIL: system pool provisioningState=%q is not a recognized wedge-compatible state", systemProvState)
}

func (c *clients) detect(ctx context.Context) (bool, string, error) {
	// Guard 3: cluster provisioning state
	mc, err := c.cluster.Get(ctx, c.cfg.resourceGroup, c.cfg.clusterName, nil)
	if err != nil {
		return false, "", fmt.Errorf("guard 3 cluster get: %w", err)
	}
	cs := ""
	if mc.Properties != nil && mc.Properties.ProvisioningState != nil {
		cs = *mc.Properties.ProvisioningState
	}
	logf("guard 3 :: cluster provisioningState=%s (accept: Succeeded/Canceled/Failed/Updating/Upgrading; reject: Creating/Deleting/unknown)", cs)
	if pass, reason := evalGuard3(cs); !pass {
		return false, reason, nil
	}
	logf("guard 3 PASS")

	// Guard 4: non-system pools healthy, plus discover system minCount.
	pager := c.pools.NewListPager(c.cfg.resourceGroup, c.cfg.clusterName, nil)
	var allPools []*armcs.AgentPool
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return false, "", fmt.Errorf("guard 4 list pools: %w", err)
		}
		allPools = append(allPools, page.Value...)
	}
	pass, systemMin, systemProvState, reason := evalGuard4(allPools)
	if !pass {
		return false, reason, nil
	}
	logf("guard 4 PASS (system minCount=%d systemProvState=%q)", systemMin, systemProvState)

	// Guard 5: system pool itself is in Failed state.
	if pass, reason := evalGuard5(systemProvState); !pass {
		return false, reason, nil
	}
	logf("guard 5 PASS (system pool is Failed)")

	// Guard 1: ready system nodes < minCount
	nodes, err := c.kube.CoreV1().Nodes().List(ctx, metav1.ListOptions{
		LabelSelector: "agentpool=" + systemPoolName,
	})
	if err != nil {
		return false, "", fmt.Errorf("guard 1 list nodes: %w", err)
	}
	readyCount := int32(0)
	for _, n := range nodes.Items {
		if isNodeReady(&n) {
			readyCount++
		}
	}
	logf("guard 1 :: system minCount=%d ready=%d", systemMin, readyCount)
	if pass, reason := evalGuard1(readyCount, systemMin); !pass {
		return false, reason, nil
	}
	logf("guard 1 PASS")

	// Guard 2: NRP retry loop on aks-system-* VMSS
	logf("guard 2 :: checking activity log on %s for last %d min", c.cfg.nodeRG, c.cfg.windowMin)
	out, err := azJSON(ctx, "monitor", "activity-log", "list", "-g", c.cfg.nodeRG, "--offset", fmt.Sprintf("%dm", c.cfg.windowMin))
	if err != nil {
		// Reader role on node RG missing is a fail-closed scenario.
		return false, fmt.Sprintf("guard 2 FAIL: activity log query failed: %v", err), nil
	}
	hits := countNRPFailures(out, "aks-system-")
	logf("guard 2 :: NRP Failed events on aks-system-* in window: %d (threshold %d)", hits, c.cfg.threshold)
	if pass, reason := evalGuard2(hits, c.cfg.threshold); !pass {
		return false, reason, nil
	}
	logf("guard 2 PASS")

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

// sanitizeForRecreate produces a deep-copy of the snapshotted AgentPool with
// read-only fields and AKS-managed tags stripped, ready to feed back into
// CreateOrUpdate. The input is never mutated.
//
// Read-only fields stripped (RP rejects user-supplied values):
//   - top-level: id, name, type
//   - properties: provisioningState, currentOrchestratorVersion,
//     nodeImageVersion, powerState, creationData
//
// orchestratorVersion is overwritten with the live cluster control-plane
// version to guarantee we never request a version downgrade.
//
// Tags prefixed `aks-managed-` are stripped (RP rejects user PUTs that
// contain them; they will be re-added by AKS).
func sanitizeForRecreate(live *armcs.AgentPool, cpVersion string) (*armcs.AgentPool, error) {
	if live == nil {
		return nil, errors.New("sanitizeForRecreate: nil input")
	}
	// Deep-copy via JSON round-trip so we never mutate the snapshot the
	// caller still holds. Slower than reflect-based copy, but bullet-proof
	// against future SDK shape changes.
	raw, err := json.Marshal(live)
	if err != nil {
		return nil, fmt.Errorf("sanitizeForRecreate: marshal: %w", err)
	}
	var out armcs.AgentPool
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("sanitizeForRecreate: unmarshal: %w", err)
	}

	out.ID = nil
	out.Name = nil
	out.Type = nil

	if out.Properties == nil {
		return nil, errors.New("sanitizeForRecreate: nil properties after copy")
	}
	out.Properties.ProvisioningState = nil
	out.Properties.CurrentOrchestratorVersion = nil
	out.Properties.NodeImageVersion = nil
	out.Properties.PowerState = nil
	out.Properties.CreationData = nil
	// Pin to the live CP version so we never request a downgrade.
	v := cpVersion
	out.Properties.OrchestratorVersion = &v
	// Strip AKS-managed tags.
	if out.Properties.Tags != nil {
		cleaned := map[string]*string{}
		for k, v := range out.Properties.Tags {
			if !strings.HasPrefix(k, "aks-managed-") {
				cleaned[k] = v
			}
		}
		out.Properties.Tags = cleaned
	}
	return &out, nil
}

// ---------------------------------------------------------------------------
// step 2 :: maybe abort LRO
// ---------------------------------------------------------------------------

// maybeAbortLRO inspects the cluster's latest LRO and decides what to do:
//
//   - no latest LRO accessible            -> proceed (nothing to abort).
//   - latest LRO already finished         -> proceed (nothing to abort).
//   - active LRO younger than 30 min      -> NO-OP (return proceed=false,
//     no error). The caller exits 0
//     to avoid racing a potentially
//     healthy in-progress operation.
//   - active LRO >= 30 min old            -> abort via `az aks
//     operation-abort`, then proceed.
//
// The 30-min threshold is the same heuristic the on-call SREs used during
// AROSLSRE-924 / AROSLSRE-880: a healthy upgrade or scale converges
// within minutes; anything older than 30 min on the same correlation
// chain is the NRP-KVS retry storm and is safe to abort.
func (c *clients) maybeAbortLRO(ctx context.Context) (bool, error) {
	// SDK lacks a typed "show-latest cluster operation" helper; use az.
	out, err := azJSON(ctx, "aks", "operation", "show-latest", "-g", c.cfg.resourceGroup, "-n", c.cfg.clusterName)
	if err != nil {
		logf("no latest LRO accessible (continuing): %v", err)
		return true, nil
	}
	var op struct {
		Status    string `json:"status"`
		StartTime string `json:"startTime"`
		EndTime   string `json:"endTime"`
	}
	if err := json.Unmarshal(out, &op); err != nil {
		logf("could not parse latest LRO: %v (continuing)", err)
		return true, nil
	}
	logf("latest LRO: status=%s start=%s end=%s", op.Status, op.StartTime, op.EndTime)
	if op.EndTime != "" || op.Status == "" {
		logf("no active LRO")
		return true, nil
	}
	start, err := time.Parse(time.RFC3339, op.StartTime)
	if err != nil {
		logf("could not parse LRO startTime: %v (continuing)", err)
		return true, nil
	}
	age := time.Since(start)
	logf("active LRO age: %s", age.Round(time.Minute))
	if age < lroAbortAgeMin*time.Minute {
		return false, nil
	}
	logf("aborting LRO (age >= %dm)", lroAbortAgeMin)
	if _, err := runCmd(ctx, "az", "aks", "operation-abort", "-g", c.cfg.resourceGroup, "-n", c.cfg.clusterName); err != nil {
		return false, fmt.Errorf("operation-abort: %w", err)
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
// across environments). The temporary pool inherits Count=1, plus the
// CriticalAddonsOnly taint and an obviously-temporary label so it does
// not pick up arbitrary workloads.
func buildSystmpAgentPool(live *armcs.AgentPool, cpVersion string) (*armcs.AgentPool, error) {
	if live == nil || live.Properties == nil {
		return nil, errors.New("buildSystmpAgentPool: live snapshot has no properties")
	}
	if live.Properties.VMSize == nil || *live.Properties.VMSize == "" {
		return nil, errors.New("buildSystmpAgentPool: live snapshot has no VMSize")
	}
	if live.Properties.OSDiskSizeGB == nil || *live.Properties.OSDiskSizeGB <= 0 {
		return nil, errors.New("buildSystmpAgentPool: live snapshot has no OSDiskSizeGB")
	}
	if cpVersion == "" {
		return nil, errors.New("buildSystmpAgentPool: empty cpVersion")
	}
	mode := armcs.AgentPoolModeSystem
	cnt := int32(1)
	maxPods := int32(100)
	tru := true
	v := cpVersion
	diskGB := *live.Properties.OSDiskSizeGB
	body := &armcs.AgentPool{
		Properties: &armcs.ManagedClusterAgentPoolProfileProperties{
			Mode:                   &mode,
			VMSize:                 ptr(*live.Properties.VMSize),
			OrchestratorVersion:    &v,
			MaxPods:                &maxPods,
			Count:                  &cnt,
			OSDiskSizeGB:           &diskGB,
			EnableEncryptionAtHost: &tru,
			EnableFIPS:             &tru,
			NodeTaints:             []*string{ptr("CriticalAddonsOnly=true:NoSchedule")},
			NodeLabels:             map[string]*string{"aro-hcp.azure.com/role": ptr("system")},
			Tags:                   map[string]*string{"purpose": ptr("temp-system-aroslsre-924")},
		},
	}
	// Inherit OS family + disk type from live where the snapshot supplied them.
	if live.Properties.OSType != nil {
		t := *live.Properties.OSType
		body.Properties.OSType = &t
	} else {
		t := armcs.OSTypeLinux
		body.Properties.OSType = &t
	}
	if live.Properties.OSSKU != nil {
		sku := *live.Properties.OSSKU
		body.Properties.OSSKU = &sku
	}
	if live.Properties.OSDiskType != nil {
		dt := *live.Properties.OSDiskType
		body.Properties.OSDiskType = &dt
	}
	if live.Properties.VnetSubnetID != nil {
		body.Properties.VnetSubnetID = ptr(*live.Properties.VnetSubnetID)
	}
	if live.Properties.PodSubnetID != nil {
		body.Properties.PodSubnetID = ptr(*live.Properties.PodSubnetID)
	}
	return body, nil
}

func (c *clients) addSystmp(ctx context.Context, live *armcs.AgentPool) error {
	body, err := buildSystmpAgentPool(live, c.cfg.cpVersion)
	if err != nil {
		return err
	}
	logf("creating systmp (vmSize=%s, 1 node, k8s=%s, CriticalAddonsOnly tainted)", strDeref(live.Properties.VMSize), c.cfg.cpVersion)
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
// step 4/7 :: drain (shell out to kubectl)
// ---------------------------------------------------------------------------

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
	timeoutStr := fmt.Sprintf("%ds", int(timeout.Seconds()))
	for _, n := range nodes.Items {
		name := n.Name
		logf(">>> cordoning %s", name)
		if _, err := runCmd(ctx, "kubectl", "cordon", name); err != nil {
			logf("WARN: cordon %s: %v (continuing)", name, err)
		}
		logf(">>> draining %s (timeout=%s)", name, timeoutStr)
		if _, err := runCmd(ctx, "kubectl", "drain", name,
			"--ignore-daemonsets", "--delete-emptydir-data", "--timeout", timeoutStr); err != nil {
			// Don't fail the whole script on drain hiccups; delete-pool will force-evict.
			logf("WARN: drain %s returned: %v (continuing)", name, err)
		}
	}
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
	body, err := sanitizeForRecreate(live, c.cfg.cpVersion)
	if err != nil {
		return fmt.Errorf("sanitize: %w", err)
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
// step 8 :: no-op tag reconcile (shell out — no clean SDK path for tag-only PATCH)
// ---------------------------------------------------------------------------

func (c *clients) reconcileTagPut(ctx context.Context) error {
	// Use nanosecond precision so repeated invocations within the same
	// minute produce different values (forcing ARM to see a real change).
	ts := time.Now().UTC().Format(time.RFC3339Nano)
	id := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.ContainerService/managedClusters/%s",
		c.cfg.subscriptionID, c.cfg.resourceGroup, c.cfg.clusterName)
	_, err := runCmd(ctx, "az", "resource", "update", "--ids", id,
		"--latest-include-preview",
		"--set", "tags.aroslsre-924-recreate="+ts)
	return err
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func (c *clients) waitForReadyNodes(ctx context.Context, pool string, want int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		nodes, err := c.kube.CoreV1().Nodes().List(ctx, metav1.ListOptions{
			LabelSelector: "agentpool=" + pool,
		})
		if err != nil {
			return fmt.Errorf("list nodes for pool %s: %w", pool, err)
		}
		ready := 0
		for _, n := range nodes.Items {
			if isNodeReady(&n) {
				ready++
			}
		}
		logf("  pool=%s ready=%d/%d", pool, ready, want)
		if ready >= want {
			return nil
		}
		if time.Now().After(deadline) {
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

// ---------------------------------------------------------------------------
// activity-log parsing
// ---------------------------------------------------------------------------

type activityEvent struct {
	Status        struct{ Value string } `json:"status"`
	OperationName struct{ Value string } `json:"operationName"`
	ResourceID    string                 `json:"resourceId"`
	CorrelationID string                 `json:"correlationId"`
	EventTime     string                 `json:"eventTimestamp"`
}

func countNRPFailures(raw []byte, vmssPrefix string) int {
	var events []activityEvent
	if err := json.Unmarshal(raw, &events); err != nil {
		return 0
	}
	n := 0
	for _, e := range events {
		if e.Status.Value != "Failed" {
			continue
		}
		if !strings.Contains(strings.ToLower(e.OperationName.Value), "virtualmachinescalesets") {
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
	return n
}

func nrpResourceIDs(raw []byte) []string {
	var events []activityEvent
	if err := json.Unmarshal(raw, &events); err != nil {
		return nil
	}
	seen := map[string]struct{}{}
	var out []string
	for _, e := range events {
		if e.Status.Value != "Failed" {
			continue
		}
		if !strings.Contains(strings.ToLower(e.OperationName.Value), "virtualmachinescalesets") {
			continue
		}
		if _, ok := seen[e.ResourceID]; ok {
			continue
		}
		seen[e.ResourceID] = struct{}{}
		out = append(out, e.ResourceID)
	}
	return out
}

// ---------------------------------------------------------------------------
// shell helpers
// ---------------------------------------------------------------------------

func runCmd(ctx context.Context, name string, args ...string) ([]byte, error) {
	logf("$ %s %s", name, strings.Join(args, " "))
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	return nil, nil
}

func azJSON(ctx context.Context, args ...string) ([]byte, error) {
	full := append([]string{"-o", "json"}, args...)
	logf("$ az %s", strings.Join(full, " "))
	cmd := exec.CommandContext(ctx, "az", full...)
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("az %v failed: %w: %s", args, err, string(ee.Stderr))
		}
		return nil, err
	}
	return out, nil
}

func azShellTSV(ctx context.Context, args ...string) (string, error) {
	full := append([]string{"-o", "tsv"}, args...)
	out, err := exec.CommandContext(ctx, "az", full...).Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return "", fmt.Errorf("az %v failed: %w: %s", args, err, string(ee.Stderr))
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
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

func ptr[T any](v T) *T { return &v }

func strDeref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
