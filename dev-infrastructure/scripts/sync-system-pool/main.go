// Copyright 2026 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
// sync-system-pool: keeps the management-cluster AKS `system` node pool in
// sync with the desired config.yaml / bicep-template spec.
//
// Background
// ----------
// The `system` AgentPool's SKU/size/disk/zone shape cannot be changed via
// an ARM PUT once the pool exists: AKS rejects vmSize/osDiskSizeGB/zone
// changes on an existing pool. The only way to apply such a change is to
// delete and recreate the pool. This binary automates that swap so a
// config.yaml change to mgmt.aks.systemAgentPool.* rolls out without manual
// intervention, while remaining a no-op on every run where the deployed
// pool already matches the desired spec (the common case).
//
// Inputs (env vars, set by mgmt-pipeline.yaml's sync-system-pool step)
// ----------------------------------------------------------------------
//
//	CLUSTER_NAME        AKS cluster name (e.g. int-uksouth-mgmt-1)
//	RESOURCE_GROUP      Resource group containing the AKS cluster
//	SUBSCRIPTION_ID     Azure subscription ID containing the AKS cluster
//	VNET_NAME           Name of the VNet hosting the node/pod subnets
//	SYSTEM_POOL_NAME    mgmt.aks.systemAgentPool.name
//	SYSTEM_VM_SIZE      mgmt.aks.systemAgentPool.vmSize
//	SYSTEM_MIN_COUNT    mgmt.aks.systemAgentPool.minCount
//	SYSTEM_MAX_COUNT    mgmt.aks.systemAgentPool.maxCount
//	SYSTEM_OS_DISK_GB   mgmt.aks.systemAgentPool.osDiskSizeGB
//	SYSTEM_ZONES        mgmt.aks.systemAgentPool.zones (CSV, may be empty)
//	SYSTEM_ZONE_MODE    mgmt.aks.systemAgentPool.zoneRedundantMode
//	DRY_RUN             "true" to log the diff/plan but make no writes
//
// Flow
// ----
//  1. Get the managed cluster. If it does not exist (greenfield rollout),
//     exit 0 no-op: the cluster ARM step will create system pool with the
//     right spec from scratch.
//  2. Build the desired system-pool spec from the resolved config (with
//     the same defaults/derivations aks-cluster-base.bicep applies) and
//     diff it against the live system pool. If they already match, exit 0
//     no-op — this is the expected outcome on almost every run.
//  3. Otherwise, create a throwaway single-node pool with the NEW desired
//     spec (keeping the existing `system` pool's name reserved), and
//     verify ARM actually deployed that exact spec (only the name/scaling
//     fields are expected to differ). Any other discrepancy aborts here,
//     leaving the temp pool in place for a human to inspect — we must not
//     delete the working `system` pool until we've proven the new config
//     is deployable.
//  4. Cordon+drain and delete the original `system` pool, then recreate it
//     with the desired (validated) spec under its original name, so the
//     subsequent cluster ARM PUT is a no-op.
//  5. Cordon+drain and delete the temporary pool.
//
// Any failure from step 3 onward leaves the cluster in a state that needs
// human review (the whole point of gating on validated config is to make
// that the ONLY time this script fails); a failure before that point
// (steps 1-2) is always safe to retry from scratch, since nothing has been
// mutated yet.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"k8s.io/client-go/kubernetes"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	armcs "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v6"

	mcclient "github.com/Azure/ARO-HCP/sessiongate/pkg/mc"
)

const (
	systmpReadyTOMin  = 10
	systemReadyTOMin  = 10
	drainTimeoutMin   = 10
	overallTimeoutMin = 90
)

func main() {
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
	slog.SetDefault(slog.New(handler).With("component", "sync-system-pool"))

	if err := run(); err != nil {
		slog.Error("run failed", "error", err.Error())
		os.Exit(1)
	}
}

func run() error {
	ctx, cancel := context.WithTimeout(context.Background(), overallTimeoutMin*time.Minute)
	defer cancel()

	cfg, err := loadConfig(os.Getenv)
	if err != nil {
		return err
	}
	logBanner("STARTUP")
	cfg.logEnv()

	c, err := newAzureClients(cfg)
	if err != nil {
		return fmt.Errorf("init azure clients: %w", err)
	}

	return runWith(ctx, cfg, c)
}

func runWith(ctx context.Context, cfg *systemPoolConfig, c *clients) error {
	logBanner("CLUSTER EXISTENCE CHECK")
	mc, exists, err := c.ensureCluster(ctx, cfg)
	if err != nil {
		return fmt.Errorf("ensure cluster: %w", err)
	}
	if !exists {
		logf("cluster %s/%s does not exist yet (greenfield rollout). Exiting no-op.", cfg.resourceGroup, cfg.clusterName)
		return nil
	}
	cpVersion := ""
	if mc.Properties != nil {
		cpVersion = strDeref(mc.Properties.CurrentKubernetesVersion)
	}
	if cpVersion == "" {
		return errors.New("cluster currentKubernetesVersion empty; refusing to act")
	}
	// location is not an env var; it drives the same zone defaulting
	// mgmt-cluster.bicep applies (see desiredAvailabilityZones), so it must
	// come from the live cluster to stay authoritative.
	cfg.location = strDeref(mc.Location)
	if cfg.location == "" {
		return errors.New("cluster location empty; refusing to act")
	}

	logBanner("DIFF CHECK")
	live, err := c.getPool(ctx, cfg, cfg.poolName)
	if err != nil {
		return fmt.Errorf("get live system pool %q: %w", cfg.poolName, err)
	}
	if live == nil {
		return fmt.Errorf("system pool %q not found although cluster exists; refusing to act without human review", cfg.poolName)
	}
	desired := desiredAgentPool(cfg, cfg.poolName, cpVersion)
	diffs := diffAgentPool(desired, live)
	if len(diffs) == 0 {
		logf("system pool %q already matches desired spec. Exiting no-op.", cfg.poolName)
		return nil
	}
	logf("system pool %q config drift detected:", cfg.poolName)
	for _, d := range diffs {
		logf("  - %s", d)
	}
	if cfg.dryRun {
		logf("DRY_RUN=true — would proceed with recreate. Exiting no-op.")
		return nil
	}

	logBanner("PREFLIGHT :: leftover temp pool check")
	if err := c.preflightNoLeftoverTemp(ctx, cfg); err != nil {
		return err
	}

	logBanner("KUBECONFIG BOOTSTRAP")
	if err := c.bootstrapKube(ctx, mc); err != nil {
		return fmt.Errorf("bootstrap kube client: %w", err)
	}

	logBanner("STEP 1 :: create temp pool with desired config")
	tempDesired := desiredTempAgentPool(cfg, cpVersion)
	if err := c.createOrUpdatePool(ctx, cfg, cfg.tempPoolName, tempDesired); err != nil {
		return fmt.Errorf("create temp pool: %w", err)
	}
	if err := c.waitForReadyNodes(ctx, cfg.tempPoolName, 1, systmpReadyTOMin*time.Minute); err != nil {
		return fmt.Errorf("temp pool did not become ready — needs human review: %w", err)
	}

	logBanner("STEP 2 :: validate temp pool deployed exactly the desired config")
	liveTemp, err := c.getPool(ctx, cfg, cfg.tempPoolName)
	if err != nil {
		return fmt.Errorf("get temp pool after create — needs human review: %w", err)
	}
	if liveTemp == nil {
		return fmt.Errorf("temp pool %q missing right after create — needs human review", cfg.tempPoolName)
	}
	tempDiffs := diffAgentPool(tempDesired, liveTemp)
	if len(tempDiffs) > 0 {
		logf("temp pool %q does not match the config it was created with:", cfg.tempPoolName)
		for _, d := range tempDiffs {
			logf("  - %s", d)
		}
		return fmt.Errorf("temp pool validation failed (%d mismatches) — leaving %q in place for human review, NOT touching %q",
			len(tempDiffs), cfg.tempPoolName, cfg.poolName)
	}
	logf("temp pool validated: deployed config matches desired spec")

	logBanner("STEP 3 :: cordon + drain + delete original system pool")
	if err := c.drainPool(ctx, cfg.poolName, drainTimeoutMin*time.Minute); err != nil {
		return fmt.Errorf("drain system pool — needs human review: %w", err)
	}
	if err := c.deletePool(ctx, cfg, cfg.poolName); err != nil {
		return fmt.Errorf("delete system pool — needs human review, temp pool %q still holds capacity: %w", cfg.tempPoolName, err)
	}

	logBanner("STEP 4 :: recreate system pool with validated desired config")
	if err := c.createOrUpdatePool(ctx, cfg, cfg.poolName, desired); err != nil {
		return fmt.Errorf("recreate system pool — needs human review, cluster has NO system pool right now: %w", err)
	}
	expected := int(cfg.minCount)
	if expected < 1 {
		expected = 1
	}
	if err := c.waitForReadyNodes(ctx, cfg.poolName, expected, systemReadyTOMin*time.Minute); err != nil {
		return fmt.Errorf("system pool did not become ready after recreate — needs human review: %w", err)
	}

	logBanner("STEP 5 :: validate recreated system pool")
	liveSystem, err := c.getPool(ctx, cfg, cfg.poolName)
	if err != nil {
		return fmt.Errorf("get system pool after recreate — needs human review: %w", err)
	}
	if liveSystem == nil {
		return fmt.Errorf("system pool %q missing right after recreate — needs human review", cfg.poolName)
	}
	finalDiffs := diffAgentPool(desired, liveSystem)
	if len(finalDiffs) > 0 {
		logf("recreated system pool does not match desired spec:")
		for _, d := range finalDiffs {
			logf("  - %s", d)
		}
		return fmt.Errorf("post-recreate validation failed (%d mismatches) — needs human review", len(finalDiffs))
	}
	logf("system pool recreated and validated against desired spec")

	logBanner("STEP 6 :: cordon + drain + delete temp pool")
	if err := c.drainPool(ctx, cfg.tempPoolName, drainTimeoutMin*time.Minute); err != nil {
		logf("WARN: drain temp pool returned: %v (continuing to delete)", err)
	}
	if err := c.deletePool(ctx, cfg, cfg.tempPoolName); err != nil {
		return fmt.Errorf("delete temp pool %q (system pool is already healthy; this is non-fatal cleanup but needs human review): %w", cfg.tempPoolName, err)
	}

	logBanner("DONE")
	return nil
}

// ---------------------------------------------------------------------------
// config loading
// ---------------------------------------------------------------------------

func loadConfig(env func(string) string) (*systemPoolConfig, error) {
	c := &systemPoolConfig{
		subscriptionID:    env("SUBSCRIPTION_ID"),
		resourceGroup:     env("RESOURCE_GROUP"),
		clusterName:       env("CLUSTER_NAME"),
		vnetName:          env("VNET_NAME"),
		poolName:          env("SYSTEM_POOL_NAME"),
		vmSize:            env("SYSTEM_VM_SIZE"),
		zoneRedundantMode: env("SYSTEM_ZONE_MODE"),
	}
	c.tempPoolName = tempPoolName(c.poolName)

	required := map[string]string{
		"SUBSCRIPTION_ID":  c.subscriptionID,
		"RESOURCE_GROUP":   c.resourceGroup,
		"CLUSTER_NAME":     c.clusterName,
		"VNET_NAME":        c.vnetName,
		"SYSTEM_POOL_NAME": c.poolName,
		"SYSTEM_VM_SIZE":   c.vmSize,
		"SYSTEM_ZONE_MODE": c.zoneRedundantMode,
	}
	for name, v := range required {
		if strings.TrimSpace(v) == "" {
			return nil, fmt.Errorf("%s is required", name)
		}
	}

	minCount, err := parseInt32(env("SYSTEM_MIN_COUNT"), "SYSTEM_MIN_COUNT")
	if err != nil {
		return nil, err
	}
	maxCount, err := parseInt32(env("SYSTEM_MAX_COUNT"), "SYSTEM_MAX_COUNT")
	if err != nil {
		return nil, err
	}
	osDiskGB, err := parseInt32(env("SYSTEM_OS_DISK_GB"), "SYSTEM_OS_DISK_GB")
	if err != nil {
		return nil, err
	}
	// Fail fast on an obviously-broken config, before anything gets
	// mutated: a bad min/max/disk value here would otherwise only surface
	// after the original system pool is already gone, with no valid config
	// to recreate it from.
	if minCount < 1 {
		return nil, fmt.Errorf("SYSTEM_MIN_COUNT must be >= 1 for a system pool, got %d", minCount)
	}
	if maxCount < minCount {
		return nil, fmt.Errorf("SYSTEM_MAX_COUNT (%d) must be >= SYSTEM_MIN_COUNT (%d)", maxCount, minCount)
	}
	if osDiskGB < 1 {
		return nil, fmt.Errorf("SYSTEM_OS_DISK_GB must be > 0, got %d", osDiskGB)
	}
	c.minCount = minCount
	c.maxCount = maxCount
	c.osDiskSizeGB = osDiskGB
	c.zones = csvToSlice(env("SYSTEM_ZONES"))

	if v := strings.ToLower(strings.TrimSpace(env("DRY_RUN"))); v == "true" || v == "1" || v == "yes" {
		c.dryRun = true
	}
	return c, nil
}

func parseInt32(v, name string) (int32, error) {
	if strings.TrimSpace(v) == "" {
		return 0, fmt.Errorf("%s is required", name)
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", name, err)
	}
	if n < math.MinInt32 || n > math.MaxInt32 {
		return 0, fmt.Errorf("%s: value %d out of range for int32", name, n)
	}
	return int32(n), nil
}

// tempPoolName derives the throwaway pool's name from the system pool
// name. AKS agent pool names are limited to 12 chars, so we can't simply
// suffix; "systmp" is used unconditionally to match the well-known
// operational name used for this kind of swap (see history of the
// now-removed recreate-system-pool tool).
func tempPoolName(_ string) string {
	return "systmp"
}

func csvToSlice(v string) []string {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil
	}
	var out []string
	for _, part := range strings.Split(v, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func (c *systemPoolConfig) logEnv() {
	logf("SUBSCRIPTION_ID=%s", c.subscriptionID)
	logf("RESOURCE_GROUP=%s", c.resourceGroup)
	logf("CLUSTER_NAME=%s", c.clusterName)
	logf("VNET_NAME=%s", c.vnetName)
	logf("SYSTEM_POOL_NAME=%s", c.poolName)
	logf("SYSTEM_VM_SIZE=%s", c.vmSize)
	logf("SYSTEM_MIN_COUNT=%d", c.minCount)
	logf("SYSTEM_MAX_COUNT=%d", c.maxCount)
	logf("SYSTEM_OS_DISK_GB=%d", c.osDiskSizeGB)
	logf("SYSTEM_ZONES=%v", c.zones)
	logf("SYSTEM_ZONE_MODE=%s", c.zoneRedundantMode)
	logf("DRY_RUN=%t", c.dryRun)
}

// ---------------------------------------------------------------------------
// azure/kube clients
// ---------------------------------------------------------------------------

type clients struct {
	cred  azcore.TokenCredential
	pools *armcs.AgentPoolsClient
	mc    *armcs.ManagedClustersClient
	kube  kubernetes.Interface
}

func newAzureClients(cfg *systemPoolConfig) (*clients, error) {
	cred, err := azidentity.NewDefaultAzureCredential(&azidentity.DefaultAzureCredentialOptions{
		RequireAzureTokenCredentials: true,
	})
	if err != nil {
		return nil, fmt.Errorf("azidentity: %w", err)
	}
	factory, err := armcs.NewClientFactory(cfg.subscriptionID, cred, nil)
	if err != nil {
		return nil, fmt.Errorf("arm containerservice factory: %w", err)
	}
	return &clients{
		cred:  cred,
		pools: factory.NewAgentPoolsClient(),
		mc:    factory.NewManagedClustersClient(),
	}, nil
}

func (c *clients) ensureCluster(ctx context.Context, cfg *systemPoolConfig) (armcs.ManagedCluster, bool, error) {
	resp, err := c.mc.Get(ctx, cfg.resourceGroup, cfg.clusterName, nil)
	if err != nil {
		if isNotFoundErr(err) {
			return armcs.ManagedCluster{}, false, nil
		}
		return armcs.ManagedCluster{}, false, fmt.Errorf("cluster get: %w", err)
	}
	return resp.ManagedCluster, true, nil
}

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

// getPool returns nil (no error) when the pool does not exist.
func (c *clients) getPool(ctx context.Context, cfg *systemPoolConfig, name string) (*armcs.AgentPool, error) {
	resp, err := c.pools.Get(ctx, cfg.resourceGroup, cfg.clusterName, name, nil)
	if err != nil {
		if isNotFoundErr(err) {
			return nil, nil
		}
		return nil, err
	}
	return &resp.AgentPool, nil
}

// preflightNoLeftoverTemp fails closed: if Get for the temp pool returns
// anything other than HTTP 404, we must not proceed. A leftover temp pool
// means a previous run aborted after step 1 and needs human cleanup before
// we can safely retry (creating a duplicate/overlapping temp pool would
// fail with a much less actionable error later).
func (c *clients) preflightNoLeftoverTemp(ctx context.Context, cfg *systemPoolConfig) error {
	pool, err := c.getPool(ctx, cfg, cfg.tempPoolName)
	if err != nil {
		return fmt.Errorf("preflight get temp pool: %w", err)
	}
	if pool != nil {
		return fmt.Errorf("leftover temp pool %q present from a previous run; needs human cleanup before retrying", cfg.tempPoolName)
	}
	return nil
}

func (c *clients) createOrUpdatePool(ctx context.Context, cfg *systemPoolConfig, name string, body *armcs.AgentPool) error {
	logf("BeginCreateOrUpdate pool %q", name)
	poller, err := c.pools.BeginCreateOrUpdate(ctx, cfg.resourceGroup, cfg.clusterName, name, *body, nil)
	if err != nil {
		return fmt.Errorf("begin create/update %s: %w", name, err)
	}
	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		return fmt.Errorf("poll create/update %s: %w", name, err)
	}
	return nil
}

func (c *clients) deletePool(ctx context.Context, cfg *systemPoolConfig, name string) error {
	logf("deleting pool %s", name)
	poller, err := c.pools.BeginDelete(ctx, cfg.resourceGroup, cfg.clusterName, name, nil)
	if err != nil {
		return fmt.Errorf("begin delete %s: %w", name, err)
	}
	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		return fmt.Errorf("poll delete %s: %w", name, err)
	}
	logf("pool %s deleted", name)
	return nil
}

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

func logf(format string, args ...any) {
	slog.Info(fmt.Sprintf(format, args...))
}

func logBanner(s string) {
	logf("=== %s ===", s)
}
