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

// acr-replication manages a single regional replica of an Azure Container
// Registry: it creates the replica if missing, deletes and recreates it if it
// is stuck in a Failed state, and reconciles its regional data-endpoint to the
// desired state on drift.
//
// It replaces the manage-acr-replication.sh shell script invoked per-region by
// the ocp-acr-replication and svc-acr-replication steps in region-pipeline.yaml.
// That script grew conditional branching and drift comparison with no
// automated tests; bugs there directly affect prod ACR data-path routing (root
// cause of incident AROSLSRE-1592). Reimplementing it in Go with unit test
// coverage lets that logic evolve safely.
//
// Inputs (environment variables, set by region-pipeline.yaml):
//
//	SUBSCRIPTION_ID           - subscription holding the ACR
//	RESOURCE_GROUP            - resource group holding the ACR
//	ACR_NAME                  - name of the Azure Container Registry
//	REPLICATION_REGION        - Azure region to check/create a replica in;
//	                            the replica is named after the region
//	ENDPOINT_DISABLED_REGIONS - optional space-separated list of regions whose
//	                            regional data endpoint must be kept disabled
//	                            (e.g. a co-located canary replica). Defaults to
//	                            none, i.e. the endpoint is enabled everywhere.
//	LOG_VERBOSITY             - optional slog verbosity (default 0)
//	DRY_RUN                   - if set to any non-empty value, mutating calls
//	                            (create/delete/update) are logged instead of
//	                            executed
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerregistry/armcontainerregistry"
)

func main() {
	verbosity := 0
	if v := os.Getenv("LOG_VERBOSITY"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			verbosity = n
		}
	}
	handler := slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.Level(verbosity * -4),
	})
	slog.SetDefault(slog.New(handler).With("component", "acr-replication"))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	if err := run(ctx); err != nil {
		slog.Error("run failed", "error", err.Error())
		os.Exit(1)
	}
}

// config holds all inputs sourced from environment variables.
type config struct {
	subscriptionID  string
	resourceGroup   string
	acrName         string
	region          string
	disabledRegions map[string]bool
	dryRun          bool
}

// parseEnvConfig builds a config from environment variables only. It does not
// call any external tools or APIs, which makes it safe to unit-test.
func parseEnvConfig(env func(string) string) (*config, error) {
	c := &config{
		subscriptionID: env("SUBSCRIPTION_ID"),
		resourceGroup:  env("RESOURCE_GROUP"),
		acrName:        env("ACR_NAME"),
		region:         env("REPLICATION_REGION"),
	}

	missing := []string{}
	for k, v := range map[string]string{
		"SUBSCRIPTION_ID":    c.subscriptionID,
		"RESOURCE_GROUP":     c.resourceGroup,
		"ACR_NAME":           c.acrName,
		"REPLICATION_REGION": c.region,
	} {
		if v == "" {
			missing = append(missing, k)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("missing required environment variables: %s", strings.Join(missing, ", "))
	}

	c.disabledRegions = map[string]bool{}
	for _, r := range strings.Fields(env("ENDPOINT_DISABLED_REGIONS")) {
		c.disabledRegions[r] = true
	}

	// Mirrors the replaced shell script: any non-empty DRY_RUN enables
	// dry-run mode, in which mutating calls (create/delete/update) are
	// logged instead of executed. This keeps `make region.what-if`
	// non-mutating.
	c.dryRun = env("DRY_RUN") != ""
	return c, nil
}

// desiredEndpointEnabled reports whether the replica's regional data endpoint
// should be enabled for the configured replication region.
func (c *config) desiredEndpointEnabled() bool {
	return !c.disabledRegions[c.region]
}

func run(ctx context.Context) error {
	cfg, err := parseEnvConfig(os.Getenv)
	if err != nil {
		return err
	}

	// DefaultAzureCredential resolves to the rollout managed identity in EV2
	// and to the operator's `az login` locally; it never prompts interactively.
	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return fmt.Errorf("azidentity: %w", err)
	}

	registriesClient, err := armcontainerregistry.NewRegistriesClient(cfg.subscriptionID, cred, nil)
	if err != nil {
		return fmt.Errorf("new registries client: %w", err)
	}
	replicationsClient, err := armcontainerregistry.NewReplicationsClient(cfg.subscriptionID, cred, nil)
	if err != nil {
		return fmt.Errorf("new replications client: %w", err)
	}

	slog.Info("managing ACR replication", "acr", cfg.acrName, "region", cfg.region)

	registry, err := registriesClient.Get(ctx, cfg.resourceGroup, cfg.acrName, nil)
	if err != nil {
		return fmt.Errorf("get registry %q: %w", cfg.acrName, err)
	}
	if registry.Location == nil {
		return fmt.Errorf("registry %q has no location", cfg.acrName)
	}
	homeRegion := *registry.Location
	slog.Info("resolved registry home region", "acr", cfg.acrName, "homeRegion", homeRegion)

	if strings.EqualFold(cfg.region, homeRegion) {
		slog.Info("registry is homed in the target region; replication is only needed for different regions",
			"acr", cfg.acrName, "region", cfg.region)
		return nil
	}

	desiredEnabled := cfg.desiredEndpointEnabled()
	slog.Info("desired regional endpoint state", "region", cfg.region, "enabled", desiredEnabled)

	return reconcile(ctx, replicationsClient, cfg, desiredEnabled)
}

// reconcile creates, recreates, or updates the replica located in
// cfg.region so it ends up in a Succeeded state with the desired regional
// endpoint setting.
func reconcile(ctx context.Context, client *armcontainerregistry.ReplicationsClient, cfg *config, desiredEnabled bool) error {
	name, err := findReplicationNameByLocation(ctx, client, cfg)
	if err != nil {
		return err
	}
	if name == "" {
		slog.Info("no replication exists in region; creating", "region", cfg.region)
		return createReplication(ctx, client, cfg, desiredEnabled)
	}

	// Fetch authoritative state via Get on the discovered name rather than
	// trusting the List response's Properties: the replaced shell script
	// explicitly avoided reading state off the list/resource-list output
	// because of known bugs where it reports the wrong provisioning/endpoint
	// state, and instead did a separate `az resource show` on the replica ID.
	existing, err := client.Get(ctx, cfg.resourceGroup, cfg.acrName, name, nil)
	if isNotFound(err) {
		// Replica disappeared between the list and the get; treat as missing.
		slog.Info("no replication exists in region; creating", "region", cfg.region)
		return createReplication(ctx, client, cfg, desiredEnabled)
	}
	if err != nil {
		return fmt.Errorf("get replication %q: %w", name, err)
	}

	state := armcontainerregistry.ProvisioningState("")
	if existing.Properties != nil && existing.Properties.ProvisioningState != nil {
		state = *existing.Properties.ProvisioningState
	}
	currentEnabled := true
	if existing.Properties != nil && existing.Properties.RegionEndpointEnabled != nil {
		currentEnabled = *existing.Properties.RegionEndpointEnabled
	}
	slog.Info("found existing replication", "region", cfg.region, "name", name, "state", state, "endpointEnabled", currentEnabled)

	switch state {
	case armcontainerregistry.ProvisioningStateFailed:
		slog.Info("replication is in failed state; deleting and recreating", "region", cfg.region, "name", name)
		if err := deleteReplication(ctx, client, cfg, name); err != nil {
			return err
		}
		return createReplication(ctx, client, cfg, desiredEnabled)
	case armcontainerregistry.ProvisioningStateSucceeded:
		// Mirrors the replaced shell script: reconciliation is only ever
		// forced when the desired state is disabled (i.e. a canary region
		// must be forced back to disabled on drift). When the desired state
		// is enabled, an existing disabled replica is left untouched rather
		// than re-enabled, since disabling it may have been an intentional,
		// separately-managed mitigation.
		if !desiredEnabled && currentEnabled != desiredEnabled {
			return reconcileEndpoint(ctx, client, cfg, name, desiredEnabled)
		}
		slog.Info("endpoint reconciliation not requested or already satisfied; leaving existing state unchanged",
			"region", cfg.region, "name", name, "endpointEnabled", currentEnabled)
		return nil
	default:
		slog.Info("replication exists but is not ready for endpoint reconciliation; leaving it unchanged",
			"region", cfg.region, "name", name, "state", state)
		return nil
	}
}

// findReplicationNameByLocation returns the name of the replica whose
// Location matches cfg.region, or "" if none exists. Matching by location
// (rather than assuming the replica is named after the region) mirrors the
// replaced shell script, which discovered the replica via `az resource list
// --query "[?location=='$REPLICATION_REGION']"` instead of relying on naming
// convention; only the name is taken from the list response, matching the
// original's use of the list purely for discovery.
func findReplicationNameByLocation(ctx context.Context, client *armcontainerregistry.ReplicationsClient, cfg *config) (string, error) {
	pager := client.NewListPager(cfg.resourceGroup, cfg.acrName, nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return "", fmt.Errorf("list replications: %w", err)
		}
		for _, r := range page.Value {
			if r != nil && r.Location != nil && r.Name != nil && strings.EqualFold(*r.Location, cfg.region) {
				return *r.Name, nil
			}
		}
	}
	return "", nil
}

// createReplication creates a new replica named after cfg.region with the
// requested regional endpoint state.
func createReplication(ctx context.Context, client *armcontainerregistry.ReplicationsClient, cfg *config, desiredEnabled bool) error {
	if cfg.dryRun {
		slog.Info("[DRY_RUN] would create replication", "region", cfg.region, "endpointEnabled", desiredEnabled)
		return nil
	}
	slog.Info("creating replication", "region", cfg.region, "endpointEnabled", desiredEnabled)
	poller, err := client.BeginCreate(ctx, cfg.resourceGroup, cfg.acrName, cfg.region, armcontainerregistry.Replication{
		Location: to.Ptr(cfg.region),
		Properties: &armcontainerregistry.ReplicationProperties{
			RegionEndpointEnabled: to.Ptr(desiredEnabled),
		},
	}, nil)
	if err != nil {
		return fmt.Errorf("create replication %q: %w", cfg.region, err)
	}
	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		return fmt.Errorf("create replication %q: %w", cfg.region, err)
	}
	slog.Info("successfully created replication", "region", cfg.region)
	return nil
}

// deleteReplication deletes the replica by its actual resource name.
func deleteReplication(ctx context.Context, client *armcontainerregistry.ReplicationsClient, cfg *config, name string) error {
	if cfg.dryRun {
		slog.Info("[DRY_RUN] would delete replication", "region", cfg.region, "name", name)
		return nil
	}
	poller, err := client.BeginDelete(ctx, cfg.resourceGroup, cfg.acrName, name, nil)
	if err != nil {
		return fmt.Errorf("delete replication %q: %w", name, err)
	}
	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		return fmt.Errorf("delete replication %q: %w", name, err)
	}
	slog.Info("successfully deleted replication", "region", cfg.region, "name", name)
	return nil
}

// reconcileEndpoint updates an existing replica's regional endpoint to the
// desired state.
func reconcileEndpoint(ctx context.Context, client *armcontainerregistry.ReplicationsClient, cfg *config, name string, desiredEnabled bool) error {
	if cfg.dryRun {
		slog.Info("[DRY_RUN] would reconcile replication regional endpoint", "region", cfg.region, "name", name, "desiredEnabled", desiredEnabled)
		return nil
	}
	slog.Info("reconciling replication regional endpoint", "region", cfg.region, "name", name, "desiredEnabled", desiredEnabled)
	poller, err := client.BeginUpdate(ctx, cfg.resourceGroup, cfg.acrName, name, armcontainerregistry.ReplicationUpdateParameters{
		Properties: &armcontainerregistry.ReplicationUpdateParametersProperties{
			RegionEndpointEnabled: to.Ptr(desiredEnabled),
		},
	}, nil)
	if err != nil {
		return fmt.Errorf("update replication %q: %w", name, err)
	}
	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		return fmt.Errorf("update replication %q: %w", name, err)
	}
	slog.Info("successfully reconciled replication regional endpoint", "region", cfg.region, "enabled", desiredEnabled)
	return nil
}

// isNotFound reports whether err is an ARM 404 response, i.e. the replica
// does not exist yet.
func isNotFound(err error) bool {
	var respErr *azcore.ResponseError
	return errors.As(err, &respErr) && respErr.StatusCode == 404
}
