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

package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"

	"k8s.io/klog/v2"

	"github.com/Azure/ARO-HCP/internal/signal"
	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/internal/version"
	"github.com/Azure/ARO-HCP/kube-applier/pkg/app"
)

// KubeApplierRootCmdFlags collects the user-facing flags for the kube-applier
// binary. Required values must be supplied as flags; the binary does not read
// from environment variables so a misconfigured pod fails fast instead of
// silently picking up a stale operator-shell value.
type KubeApplierRootCmdFlags struct {
	Kubeconfig                 string
	KubeNamespace              string
	ManagementCluster          string
	AzureCosmosDBName          string
	AzureCosmosDBURL           string
	MetricsServerListenAddress string
	HealthzServerListenAddress string
	LeaderElectionID           string
	LogVerbosity               int
	ExitOnPanic                bool
}

func (f *KubeApplierRootCmdFlags) AddFlags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&f.Kubeconfig, "kubeconfig", f.Kubeconfig,
		"Absolute path to the kubeconfig file. Empty selects the in-cluster config.")
	cmd.Flags().StringVar(&f.KubeNamespace, "namespace", f.KubeNamespace,
		"Kubernetes namespace that hosts the leader-election lease.")
	cmd.Flags().StringVar(&f.ManagementCluster, "management-cluster", f.ManagementCluster,
		"Name of the management cluster this pod runs in. This is the Cosmos partition key.")
	cmd.Flags().StringVar(&f.AzureCosmosDBName, "cosmos-name", f.AzureCosmosDBName, "Cosmos database name.")
	cmd.Flags().StringVar(&f.AzureCosmosDBURL, "cosmos-url", f.AzureCosmosDBURL, "Cosmos database URL.")
	cmd.Flags().StringVar(&f.MetricsServerListenAddress, "metrics-listen-address", f.MetricsServerListenAddress,
		"Address on which to expose Prometheus metrics.")
	cmd.Flags().StringVar(&f.HealthzServerListenAddress, "healthz-listen-address", f.HealthzServerListenAddress,
		"Address on which to expose the /healthz endpoint.")
	cmd.Flags().StringVar(&f.LeaderElectionID, "leader-election-id", f.LeaderElectionID,
		"Lease name used for leader election within --namespace.")
	cmd.Flags().IntVar(&f.LogVerbosity, "log-verbosity", f.LogVerbosity,
		"Log verbosity. 0 is INFO; higher values are more verbose.")
	cmd.Flags().BoolVar(&f.ExitOnPanic, "exit-on-panic", f.ExitOnPanic,
		"If set, the process exits on any goroutine panic via apimachinery's HandleCrash.")

	for _, name := range []string{"namespace", "management-cluster", "cosmos-name", "cosmos-url"} {
		if err := cmd.MarkFlagRequired(name); err != nil {
			// MarkFlagRequired only fails if the flag does not exist on the command,
			// which is a programming error in this very function.
			panic(fmt.Errorf("MarkFlagRequired(%q): %w", name, err))
		}
	}
}

func (f *KubeApplierRootCmdFlags) validate() error {
	// MarkFlagRequired catches missing flags; these checks reject the
	// pathological "--flag=" empty-string forms that cobra still accepts.
	if len(f.ManagementCluster) == 0 {
		return utils.TrackError(fmt.Errorf("--management-cluster must not be empty"))
	}
	if len(f.AzureCosmosDBName) == 0 {
		return utils.TrackError(fmt.Errorf("--cosmos-name must not be empty"))
	}
	if len(f.AzureCosmosDBURL) == 0 {
		return utils.TrackError(fmt.Errorf("--cosmos-url must not be empty"))
	}
	if len(f.KubeNamespace) == 0 {
		return utils.TrackError(fmt.Errorf("--namespace must not be empty"))
	}
	if len(f.LeaderElectionID) == 0 {
		return utils.TrackError(fmt.Errorf("--leader-election-id must not be empty"))
	}
	if f.LogVerbosity < 0 {
		return utils.TrackError(fmt.Errorf("--log-verbosity must be >= 0"))
	}
	return nil
}

// ToKubeApplierOptions resolves flags into the wired Options that the app
// layer consumes. Each external dependency (kubeconfig, leader-election lock,
// Cosmos client) is constructed here so that Run() never sees raw flag values.
func (f *KubeApplierRootCmdFlags) ToKubeApplierOptions(ctx context.Context, cmd *cobra.Command) (*app.Options, error) {
	kubeconfig, err := app.NewKubeconfig(f.Kubeconfig)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to create Kubernetes configuration: %w", err))
	}
	hostname, err := os.Hostname()
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to get hostname: %w", err))
	}
	leaderElectionLock, err := app.NewLeaderElectionLock(hostname, kubeconfig, f.KubeNamespace, f.LeaderElectionID)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to create leader election lock: %w", err))
	}

	kubeApplierDBClient, err := app.NewKubeApplierDBClient(ctx, f.AzureCosmosDBURL, f.AzureCosmosDBName)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to create kube-applier Cosmos client: %w", err))
	}

	dyn, err := app.NewDynamicClient(kubeconfig)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to create dynamic client: %w", err))
	}

	return &app.Options{
		ManagementCluster:          f.ManagementCluster,
		LeaderElectionLock:         leaderElectionLock,
		KubeApplierDBClient:        kubeApplierDBClient,
		DynamicClient:              dyn,
		MetricsServerListenAddress: f.MetricsServerListenAddress,
		HealthzServerListenAddress: f.HealthzServerListenAddress,
		ExitOnPanic:                f.ExitOnPanic,
	}, nil
}

func NewKubeApplierRootCmdFlags() *KubeApplierRootCmdFlags {
	return &KubeApplierRootCmdFlags{
		MetricsServerListenAddress: ":8081",
		HealthzServerListenAddress: ":8083",
		LeaderElectionID:           "kube-applier",
		LogVerbosity:               0,
		ExitOnPanic:                true,
	}
}

func NewCmdRoot() *cobra.Command {
	processName := filepath.Base(os.Args[0])

	flags := NewKubeApplierRootCmdFlags()

	cmd := &cobra.Command{
		Use:   processName,
		Args:  cobra.NoArgs,
		Short: app.AppShortDescriptionName,
		Long: fmt.Sprintf(`%s

	The kube-applier reconciles ApplyDesire, DeleteDesire, and ReadDesire
	documents stored in the kube-applier Cosmos container against the
	management cluster's local kube-apiserver.

	# Run kube-applier locally pointing at a personal-dev Cosmos and the
	# in-cluster kubeconfig.
	%s --management-cluster ${MANAGEMENT_CLUSTER} \
		--cosmos-name ${DB_NAME} --cosmos-url ${DB_URL} \
		--namespace ${RP_NAMESPACE}
`, app.AppShortDescriptionName, processName),
		RunE: func(cmd *cobra.Command, args []string) error {
			err := RunRootCmd(cmd, flags)
			if err != nil {
				return utils.TrackError(fmt.Errorf("failed to run: %w", err))
			}
			return nil
		},
		SilenceErrors: true,
	}

	cmd.SetErrPrefix(cmd.Short + " error:")
	cmd.Version = version.CommitSHA
	flags.AddFlags(cmd)

	return cmd
}

func RunRootCmd(cmd *cobra.Command, flags *KubeApplierRootCmdFlags) error {
	if err := flags.validate(); err != nil {
		return utils.TrackError(fmt.Errorf("flags validation failed: %w", err))
	}

	ctx := signal.SetupSignalContext()

	handlerOptions := &slog.HandlerOptions{Level: slog.Level(flags.LogVerbosity * -1), AddSource: true}
	slogJSONHandler := slog.NewJSONHandler(os.Stdout, handlerOptions)
	logger := logr.FromSlogHandler(slogJSONHandler)
	ctx = utils.ContextWithLogger(ctx, logger)
	klog.SetLogger(logger)

	options, err := flags.ToKubeApplierOptions(ctx, cmd)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to convert flags to options: %w", err))
	}

	if err := options.Run(ctx); err != nil {
		return utils.TrackError(fmt.Errorf("failed to run kube-applier: %w", err))
	}
	return nil
}
