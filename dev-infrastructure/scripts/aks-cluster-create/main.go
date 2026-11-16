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

// aks-cluster-create creates a service or management cluster's AKS ManagedCluster
// resource and all its node pools (system, infra, worker) from static
// per-pool configuration through one incremental ARM deployment. The template
// is built in memory using fleet/pkg/compute and fleet/pkg/azure/agentpoolspec;
// no Bicep compiler is needed. ARM orchestrates resource writes and polling.
//
// Subsequent runs deploy the cluster only for configuration changes or failed/
// canceled provisioning recovery, avoiding no-op AKS cluster reconciliation.
// Configured pools are still redeployed. Unowned cluster configuration is
// preserved; Kubernetes versions and unconfigured pools are left alone.
// Interrupted runs wait for the previous deployment before observing resources
// and submitting a fresh template. Failed deployments leave the provisioning
// marker intact; only successful provisioning permits the conditional tag
// update that hands pool management over to Fleet.
//
// The identity needs resource-group deployment read/write permissions as well
// as AKS permissions. Serialize writers for each cluster: deployment resource
// writes do not carry the per-resource ETag guards used by the tag update.
//
// Management cluster inputs (environment variables):
//
//	SUBSCRIPTION_ID, RESOURCE_GROUP, CLUSTER_NAME, REGION
//	NODE_SUBNET_ID, POD_SUBNET_ID     - from the mgmt-infra bicep outputs
//	NETWORK_DATAPLANE, NETWORK_POLICY
//	OUTBOUND_IP_RESOURCE_ID           - from the mgmt-infra bicep outputs
//	MANAGED_IDENTITY_ID               - cluster user-assigned identity resource ID
//	ETCD_KMS_KEY_URI                  - versioned etcd KMS key URI (keyUriWithVersion)
//	KUBERNETES_VERSION
//	CLUSTER_TAGS     - CSV "key=value" list
//	OWNING_TEAM_TAG_VALUE - monitoring alert-routing owner; overrides owningTeam in CLUSTER_TAGS
//	METRIC_LABELS_ALLOWLIST, METRIC_ANNOTATIONS_ALLOWLIST - optional, default ""
//	LOG_VERBOSITY    - optional slog verbosity (default 0)
//
// Per-pool configuration (from config.yaml mgmt.aks.{systemAgentPool,userAgentPool,infraAgentPool}):
//
//	SYSTEM_POOL_NAME, SYSTEM_POOL_VM_SIZE, SYSTEM_POOL_OS_DISK_SIZE_GB,
//	SYSTEM_POOL_MIN_COUNT, SYSTEM_POOL_MAX_COUNT,
//	SYSTEM_POOL_ZONES (CSV)
//
//	USER_POOL_NAME, USER_POOL_VM_SIZE, USER_POOL_OS_DISK_SIZE_GB,
//	USER_POOL_MIN_COUNT, USER_POOL_MAX_COUNT, USER_POOL_COUNT,
//	USER_POOL_ZONES (CSV)
//
//	INFRA_POOL_NAME, INFRA_POOL_VM_SIZE, INFRA_POOL_OS_DISK_SIZE_GB,
//	INFRA_POOL_MIN_COUNT, INFRA_POOL_MAX_COUNT, INFRA_POOL_COUNT,
//	INFRA_POOL_ZONES (CSV)
//
// Existing pools retain their zones; new pools prefer explicit zones, then SKU discovery.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strconv"
	"time"

	"github.com/go-logr/logr"

	"github.com/Azure/ARO-HCP/internal/utils"
)

func main() {
	verbosity := 0
	if v := os.Getenv("LOG_VERBOSITY"); len(v) > 0 {
		if n, err := strconv.Atoi(v); err == nil {
			verbosity = n
		}
	}
	handler := slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
		// slog levels are spaced by 4 (Debug=-4, Info=0), so scale verbosity by 4:
		// LOG_VERBOSITY=1 enables Debug, higher values go progressively more verbose.
		Level: slog.Level(verbosity * -4),
	})
	slog.SetDefault(slog.New(handler).With("component", "aks-cluster-create"))
	logger := logr.FromSlogHandler(slog.Default().Handler())

	// handle a 30 min timeout here because the pipeline shell steps don't support it yet
	// TODO: remove this once the pipeline shell steps support timeouts
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	validated, err := parseCommand(os.Args[1:], os.Getenv, os.Stdout)
	if errors.Is(err, flag.ErrHelp) {
		return
	}
	if err != nil {
		logger.Error(err, "validation failed")
		os.Exit(1)
	}
	logger = logger.WithValues(
		"subscriptionID", validated.subscriptionID,
		"resourceGroup", validated.resourceGroup,
		"clusterName", validated.clusterName,
		"region", validated.region,
	)
	ctx = logr.NewContext(ctx, logger)

	completed, err := validated.Complete(ctx)
	if err != nil {
		logger.Error(err, "completing options failed")
		os.Exit(1)
	}

	started := time.Now()
	logger.Info("starting cluster reconciliation")
	if err := completed.run(ctx); err != nil {
		logger.Error(err, "run failed", "elapsed", time.Since(started).String())
		os.Exit(1)
	}
	logger.Info("cluster reconciliation completed", "elapsed", time.Since(started).String())
}

// parseCommand selects an independent input parser before any Azure clients exist.
func parseCommand(args []string, env func(string) string, output io.Writer) (*validatedOptions, error) {
	if len(args) == 0 {
		return nil, utils.TrackError(fmt.Errorf("a subcommand is required: mgmt-cluster or svc-cluster (see --help)"))
	}
	switch args[0] {
	case "--help", "-h":
		fmt.Fprintln(output, "Usage: aks-cluster-create <mgmt-cluster|svc-cluster> [--help]")
		return nil, flag.ErrHelp
	case "mgmt-cluster", "svc-cluster":
	default:
		return nil, utils.TrackError(fmt.Errorf("unknown subcommand %q: expected mgmt-cluster or svc-cluster", args[0]))
	}
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	flags.SetOutput(output)
	flags.Usage = func() {
		fmt.Fprintf(output, "Usage: aks-cluster-create %s [--help]\n", args[0])
	}
	if err := flags.Parse(args[1:]); err != nil {
		return nil, utils.TrackError(err)
	}
	if flags.NArg() != 0 {
		return nil, utils.TrackError(fmt.Errorf("%s does not accept positional arguments", args[0]))
	}
	if args[0] == "mgmt-cluster" {
		return newManagementRawOptionsFromEnv(env).Validate()
	}
	return newServiceRawOptionsFromEnv(env).Validate()
}
