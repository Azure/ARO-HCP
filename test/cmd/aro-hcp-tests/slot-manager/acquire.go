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

package slotmanager

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/acquire"
	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/assets"
	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/catalog"
	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/lease"
	"github.com/Azure/ARO-HCP/test/pkg/leaseenv"
	testutilframework "github.com/Azure/ARO-HCP/test/util/framework"
)

const (
	DefaultLeaseWaitInterval = 1 * time.Minute
	DefaultMaxWaitForLease   = 30 * time.Minute
	DefaultAdmissionTimeout  = 10 * time.Minute
)

func DefaultAcquireOptions() *RawAcquireOptions {
	allowedSubscriptions, allowedLocations, selectedLocation := defaultAcquireSelectors()
	admissionTimeout := strings.TrimSpace(os.Getenv("ARO_HCP_ADMISSION_TIMEOUT"))
	if admissionTimeout == "" {
		admissionTimeout = DefaultAdmissionTimeout.String()
	}
	return &RawAcquireOptions{
		ClusterProfileDir:      strings.TrimSpace(os.Getenv("CLUSTER_PROFILE_DIR")),
		ClusterProfileDirs:     splitSelectorValues(os.Getenv("CLUSTER_PROFILE_DIRS")),
		Environment:            strings.TrimSpace(os.Getenv("ARO_HCP_SLOT_ENV")),
		DeployEnv:              strings.TrimSpace(os.Getenv(leaseenv.RuntimeDeployEnvironment)),
		AllowedSubscriptions:   allowedSubscriptions,
		AllowedLocations:       allowedLocations,
		SelectedLocation:       selectedLocation,
		LocationWeights:        strings.TrimSpace(os.Getenv("LOCATION_WEIGHTS")),
		BuildID:                strings.TrimSpace(os.Getenv("BUILD_ID")),
		SharedDir:              strings.TrimSpace(os.Getenv("SHARED_DIR")),
		DisabledAssetAdmission: splitSelectorValues(os.Getenv("ARO_HCP_DISABLE_ASSET_ADMISSION")),
		AdmissionTimeout:       admissionTimeout,
		AssetAdmissionOptions:  splitSelectorValues(os.Getenv("ARO_HCP_ASSET_ADMISSION_OPTIONS")),

		LeaseProxyServerURL: strings.TrimSpace(os.Getenv("LEASE_PROXY_SERVER_URL")),
		LeaseProxyTimeout:   lease.DefaultLeaseProxyTimeout,
		MaxWaitForLease:     DefaultMaxWaitForLease,
		LeaseWaitInterval:   DefaultLeaseWaitInterval,
		// AzureGlobalLoggerLogPath has a default of ${ARTIFACT_DIR}/azure-global-logger.log if ARTIFACT_DIR is non empty. Otherwise
		// it is azure-global-logger.log, relative to the current working directory.
		AzureGlobalLoggerLogPath: filepath.Join(os.Getenv("ARTIFACT_DIR"), "azure-global-logger.log"),
	}
}

// defaultSetupAzureGlobalLogger is the real implementation behind
// RawAcquireOptions.SetupAzureGlobalLogger: it creates path (and its parent
// directory or directories if they don't exist) and points the Azure Go SDK's global logger
// at it via testutilframework.SetupAzureGlobalLoggerWithLogrLogger. The returned io.Closer
// is the open log file. Callers must close it once logging is done.
func defaultSetupAzureGlobalLogger(path string) (io.Closer, error) {
	pathDir := filepath.Dir(path)
	if err := os.MkdirAll(pathDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create directory %q for azure global logger log file %q: %w", pathDir, path, err)
	}

	azureGlobalLoggerLogFile, err := os.Create(path)
	if err != nil {
		return nil, fmt.Errorf("failed to create azure global logger log file: %w", err)
	}

	logger := logr.FromSlogHandler(slog.NewJSONHandler(azureGlobalLoggerLogFile, &slog.HandlerOptions{}))
	testutilframework.SetupAzureGlobalLoggerWithLogrLogger(logger)

	return azureGlobalLoggerLogFile, nil
}

func defaultAcquireSelectors() ([]string, []string, string) {
	allowedSubscriptions := splitSelectorValues(os.Getenv("ALLOWED_SUBSCRIPTIONS"))
	selectedLocation := strings.TrimSpace(os.Getenv("MULTISTAGE_PARAM_OVERRIDE_LOCATION"))
	if selectedLocation != "" {
		return allowedSubscriptions, nil, selectedLocation
	}

	return allowedSubscriptions, splitSelectorValues(os.Getenv("ALLOWED_LOCATIONS")), ""
}

func splitSelectorValues(raw string) []string {
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == '\n'
	})
	return normalizeValues(parts)
}

// normalizeValues trims whitespace, drops empty entries, and de-duplicates
// while preserving first-seen order.
func normalizeValues(parts []string) []string {
	values := make([]string, 0, len(parts))
	seen := map[string]struct{}{}
	for _, part := range parts {
		value := strings.TrimSpace(part)
		if value == "" {
			continue
		}
		if _, found := seen[value]; found {
			continue
		}
		seen[value] = struct{}{}
		values = append(values, value)
	}
	return values
}

func BindAcquireOptions(opts *RawAcquireOptions, cmd *cobra.Command) error {
	cmd.Flags().StringVar(&opts.ClusterProfileDir, "cluster-profile-dir", opts.ClusterProfileDir, "Path to CLUSTER_PROFILE_DIR")
	cmd.Flags().StringSliceVar(&opts.ClusterProfileDirs, "cluster-profile-dirs", opts.ClusterProfileDirs, "Optional list of cluster profile dirs to resolve the leased subscription's owning tenant/credentials across. Falls back to --cluster-profile-dir when unset.")
	cmd.Flags().StringVar(&opts.Environment, "environment", opts.Environment, "Logical slot environment (dev, dev-xs, int, stg, prod).")
	cmd.Flags().StringVar(&opts.DeployEnv, "deploy-env", opts.DeployEnv, "Optional deploy environment compatibility selector/assertion (ci00, ci01, ci02, int, stg, prod).")
	cmd.Flags().StringSliceVar(&opts.AllowedSubscriptions, "allowed-subscriptions", opts.AllowedSubscriptions, "Optional catalog subscriptions.e2e values allowed for candidate pool selection.")
	cmd.Flags().StringSliceVar(&opts.AllowedLocations, "allowed-locations", opts.AllowedLocations, "Optional Azure regions allowed for fixed-mode candidate pool selection.")
	cmd.Flags().StringVar(&opts.LocationWeights, "location-weights", opts.LocationWeights, "Weighted-mode location weights as comma-separated location=weight entries.")
	cmd.Flags().StringVar(&opts.BuildID, "build-id", opts.BuildID, "Stable per-run key used for deterministic weighted location selection.")
	cmd.Flags().StringVar(&opts.SharedDir, "shared-dir", opts.SharedDir, "Path to SHARED_DIR")
	cmd.Flags().StringVar(&opts.CatalogPath, "slot-catalog", opts.CatalogPath, "Path to the canonical E2E slot catalog")
	cmd.Flags().StringSliceVar(&opts.DisabledAssetAdmission, "disable-asset-admission", opts.DisabledAssetAdmission, "Emergency opt-out of admission for named asset kinds (repeatable or comma-separated). All other assets remain admitted. Defaults from ARO_HCP_DISABLE_ASSET_ADMISSION.")
	cmd.Flags().StringVar(&opts.AdmissionTimeout, "admission-timeout", opts.AdmissionTimeout, "Maximum duration for the entire asset admission phase (e.g. 20m). Defaults from ARO_HCP_ADMISSION_TIMEOUT, or 10m when unset.")
	assetAdmissionOptionUsage := "Per-asset admission option as <kind>.<key>=<value> (repeatable or comma-separated). Defaults from ARO_HCP_ASSET_ADMISSION_OPTIONS."
	if opts.AssetRegistry != nil {
		if registryUsage := opts.AssetRegistry.OptionsUsage(); registryUsage != "" {
			assetAdmissionOptionUsage += "\nAvailable options:\n" + registryUsage
		}
	}
	cmd.Flags().StringSliceVar(&opts.AssetAdmissionOptions, "asset-admission-option", opts.AssetAdmissionOptions, assetAdmissionOptionUsage)
	cmd.Flags().StringVar(&opts.LeaseProxyServerURL, "lease-proxy-server-url", opts.LeaseProxyServerURL, "Lease proxy server URL")
	cmd.Flags().DurationVar(&opts.LeaseProxyTimeout, "lease-proxy-timeout", opts.LeaseProxyTimeout, "Maximum time to spend probing a single candidate pool, including retryable proxy/network retries.")
	cmd.Flags().DurationVar(&opts.MaxWaitForLease, "max-wait-for-lease", opts.MaxWaitForLease, "Maximum total time to keep retrying after full candidate-pool passes yield no immediate lease. Zero waits forever.")
	cmd.Flags().DurationVar(&opts.LeaseWaitInterval, "lease-wait-interval", opts.LeaseWaitInterval, "Wait between retries after a full candidate-pool pass yields no immediate lease.")
	cmd.Flags().StringVar(&opts.AzureGlobalLoggerLogPath, "azure-global-logger-log-path", opts.AzureGlobalLoggerLogPath, "Path to the file where the Azure global logger writes logs to. Defaults to ${ARTIFACT_DIR}/azure-global-logger.log if ARTIFACT_DIR is non empty. Otherwise it defaults to azure-global-logger.log, relative to the current working directory. It cannot be empty.")
	return nil
}

type RawAcquireOptions struct {
	WriteState             func(string, *lease.AcquiredSlotState) error
	ClusterProfileDir      string
	ClusterProfileDirs     []string
	Environment            string
	DeployEnv              string
	AllowedSubscriptions   []string
	AllowedLocations       []string
	SelectedLocation       string
	LocationWeights        string
	BuildID                string
	SharedDir              string
	CatalogPath            string
	LeaseProxyServerURL    string
	LeaseProxyTimeout      time.Duration
	MaxWaitForLease        time.Duration
	LeaseWaitInterval      time.Duration
	Now                    func() time.Time
	AssetRegistry          *assets.Registry
	DisabledAssetAdmission []string
	AdmissionTimeout       string
	AssetAdmissionOptions  []string
	ResolveSubscriptions   func(context.Context, string, string, string, string) (catalog.ResolvedSubscriptions, error)
	// AzureGlobalLoggerLogPath is the file the Azure SDK's global logger writes
	// to. See DefaultAcquireOptions for how the CLI computes a default.
	AzureGlobalLoggerLogPath string
	// SetupAzureGlobalLogger wires Azure Go SDK's global log listener log output to the given path.
	// Introduced so tests can override it with a no-op so they never touch the filesystem or that global Azure Go SDK logger.
	// When not specified, defaultSetupAzureGlobalLogger is used as the SetupAzureGlobalLogger function.
	SetupAzureGlobalLogger func(path string) (io.Closer, error)
}

type validatedAcquireOptions struct {
	*RawAcquireOptions
	admissionTimeout time.Duration
}

type ValidatedAcquireOptions struct {
	*validatedAcquireOptions
}

type completedAcquireOptions struct {
	*acquire.Plan
	acquire.RunConfig
	// AzureGlobalLoggerCloser is the open azure-global-logger log file, produced
	// by SetupAzureGlobalLogger during Complete. Run closes it, guarding against
	// nil since it's reachable from code that doesn't call Complete.
	AzureGlobalLoggerCloser io.Closer
}

type AcquireOptions struct {
	*completedAcquireOptions
}

func newAcquireCommand(registry *assets.Registry) (*cobra.Command, error) {
	opts := DefaultAcquireOptions()
	opts.AssetRegistry = registry

	cmd := &cobra.Command{
		Use:   "acquire",
		Short: "Acquire an ARO-HCP E2E slot lease and write shared artifacts.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return Acquire(cmd.Context(), opts)
		},
	}

	if err := BindAcquireOptions(opts, cmd); err != nil {
		return nil, err
	}
	return cmd, nil
}

func Acquire(ctx context.Context, opts *RawAcquireOptions) error {
	validated, err := opts.Validate()
	if err != nil {
		return err
	}
	completed, err := validated.Complete(ctx)
	if err != nil {
		return err
	}
	return completed.Run(ctx)
}

func (o *RawAcquireOptions) Validate() (*ValidatedAcquireOptions, error) {
	switch {
	case len(o.effectiveClusterProfileDirs()) == 0:
		return nil, fmt.Errorf("--cluster-profile-dir or --cluster-profile-dirs must not be empty")
	case o.Environment == "" && o.DeployEnv == "":
		return nil, fmt.Errorf("--environment or --deploy-env must not be empty")
	case o.SharedDir == "":
		return nil, fmt.Errorf("--shared-dir must not be empty")
	case o.LeaseProxyServerURL == "":
		return nil, fmt.Errorf("--lease-proxy-server-url must not be empty")
	case o.LeaseProxyTimeout <= 0:
		return nil, fmt.Errorf("--lease-proxy-timeout must be greater than zero")
	case o.MaxWaitForLease < 0:
		return nil, fmt.Errorf("--max-wait-for-lease must not be negative")
	case o.LeaseWaitInterval <= 0:
		return nil, fmt.Errorf("--lease-wait-interval must be greater than zero")
	case o.AzureGlobalLoggerLogPath == "":
		return nil, fmt.Errorf("--azure-global-logger-log-path must not be empty")
	}

	admissionTimeout := DefaultAdmissionTimeout
	if o.AdmissionTimeout != "" {
		var err error
		admissionTimeout, err = time.ParseDuration(o.AdmissionTimeout)
		if err != nil {
			return nil, fmt.Errorf("--admission-timeout: %w", err)
		}
	}
	if admissionTimeout <= 0 {
		return nil, errors.New("--admission-timeout must be greater than zero")
	}
	return &ValidatedAcquireOptions{
		validatedAcquireOptions: &validatedAcquireOptions{RawAcquireOptions: o, admissionTimeout: admissionTimeout},
	}, nil
}

// effectiveClusterProfileDirs returns the cluster profile dirs to resolve the
// leased subscription's owning credentials across. --cluster-profile-dirs
// (CLUSTER_PROFILE_DIRS) takes precedence; otherwise it falls back to the
// single --cluster-profile-dir (CLUSTER_PROFILE_DIR) for backward
// compatibility. Values are trimmed, de-duplicated, and empty entries dropped
// so callers get a clean list regardless of how they were supplied (flag or
// env, e.g. trailing commas or stray whitespace).
func (o *RawAcquireOptions) effectiveClusterProfileDirs() []string {
	if dirs := normalizeValues(o.ClusterProfileDirs); len(dirs) > 0 {
		return dirs
	}
	if dir := strings.TrimSpace(o.ClusterProfileDir); dir != "" {
		return []string{dir}
	}
	return nil
}

func (o *ValidatedAcquireOptions) Complete(_ context.Context) (*AcquireOptions, error) {
	slotCatalog, err := catalog.LoadCatalog(o.CatalogPath)
	if err != nil {
		return nil, err
	}
	plan, err := acquire.NewPlan(acquire.PlanInput{
		Catalog:              slotCatalog,
		Environment:          o.Environment,
		DeployEnv:            o.DeployEnv,
		AllowedSubscriptions: o.AllowedSubscriptions,
		AllowedLocations:     o.AllowedLocations,
		SelectedLocation:     o.SelectedLocation,
		LocationWeights:      o.LocationWeights,
		BuildID:              o.BuildID,
	})
	if err != nil {
		return nil, err
	}

	registry := o.AssetRegistry
	if registry == nil {
		registry, err = newAssetRegistry()
		if err != nil {
			return nil, err
		}
	}
	if err := registry.ValidateRequirements(plan.CandidatePools); err != nil {
		return nil, err
	}
	if err := registry.ApplyOptions(o.AssetAdmissionOptions); err != nil {
		return nil, fmt.Errorf("--asset-admission-option: %w", err)
	}
	assetInventories, err := slotCatalog.AssetInventories()
	if err != nil {
		return nil, err
	}
	disabledAssetAdmission := make([]assets.Kind, 0, len(o.DisabledAssetAdmission))
	for _, kind := range o.DisabledAssetAdmission {
		disabledAssetAdmission = append(disabledAssetAdmission, assets.Kind(strings.TrimSpace(kind)))
	}

	setupAzureGlobalLoggerFn := o.SetupAzureGlobalLogger
	if setupAzureGlobalLoggerFn == nil {
		setupAzureGlobalLoggerFn = defaultSetupAzureGlobalLogger
	}
	azureGlobalLoggerCloser, err := setupAzureGlobalLoggerFn(o.AzureGlobalLoggerLogPath)
	if err != nil {
		return nil, err
	}

	return &AcquireOptions{
		completedAcquireOptions: &completedAcquireOptions{
			Plan: plan,
			RunConfig: acquire.RunConfig{
				AssetInventories:       assetInventories,
				WriteState:             o.WriteState,
				ClusterProfileDirs:     o.effectiveClusterProfileDirs(),
				DeployEnvironment:      o.DeployEnv,
				SharedDir:              o.SharedDir,
				LeaseProxyURL:          o.LeaseProxyServerURL,
				LeaseProxyTimeout:      o.LeaseProxyTimeout,
				MaxWaitForLease:        o.MaxWaitForLease,
				LeaseWaitInterval:      o.LeaseWaitInterval,
				AssetRegistry:          registry,
				DisabledAssetAdmission: disabledAssetAdmission,
				AdmissionTimeout:       o.admissionTimeout,
				ResolveSubscriptions:   o.ResolveSubscriptions,
				Now:                    o.Now,
			},
			AzureGlobalLoggerCloser: azureGlobalLoggerCloser,
		},
	}, nil
}

func (o *AcquireOptions) Run(ctx context.Context) error {
	if o.AzureGlobalLoggerCloser != nil {
		defer o.AzureGlobalLoggerCloser.Close()
	}
	return o.Plan.Run(ctx, o.RunConfig)
}
