// Copyright 2025 Microsoft Corporation
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

package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/go-logr/logr"
	ocmsdk "github.com/openshift-online/ocm-sdk-go"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
	"k8s.io/klog/v2"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"

	"github.com/Azure/ARO-HCP/internal/database"
)

const (
	leaderElectionLockName      = "backend-leader"
	leaderElectionLeaseDuration = 15 * time.Second
	leaderElectionRenewDeadline = 10 * time.Second
	leaderElectionRetryPeriod   = 2 * time.Second
)

var (
	argKubeconfig           string
	argNamespace            string
	argLocation             string
	argCosmosName           string
	argCosmosURL            string
	argClustersServiceURL   string
	argInsecure             bool
	argMetricsListenAddress string
	argPortListenAddress    string

	processName = filepath.Base(os.Args[0])

	rootCmd = &cobra.Command{
		Use:   processName,
		Args:  cobra.NoArgs,
		Short: "ARO HCP Backend",
		Long: fmt.Sprintf(`ARO HCP Backend

	The command runs the ARO HCP Backend. It executes background processing that
	communicates with Clusters Service and CosmosDB.

	# Run ARO HCP Backend locally to connect to a local Clusters Service at http://localhost:8000
	%s --cosmos-name ${DB_NAME} --cosmos-url ${DB_URL} --location ${LOCATION} \
		--clusters-service-url "http://localhost:8000"
`, processName),
		Version:       "unknown", // overridden by build info below
		RunE:          Run,
		SilenceErrors: true, // errors are printed after Execute
	}
)

func init() {
	rootCmd.SetErrPrefix(rootCmd.Short + " error:")

	rootCmd.Flags().StringVar(&argKubeconfig, "kubeconfig", "", "Absolute path to the kubeconfig file")
	rootCmd.Flags().StringVar(&argNamespace, "namespace", os.Getenv("NAMESPACE"), "Kubernetes namespace")
	rootCmd.Flags().StringVar(&argLocation, "location", os.Getenv("LOCATION"), "Azure location")
	rootCmd.Flags().StringVar(&argCosmosName, "cosmos-name", os.Getenv("DB_NAME"), "Cosmos database name")
	rootCmd.Flags().StringVar(&argCosmosURL, "cosmos-url", os.Getenv("DB_URL"), "Cosmos database URL")
	rootCmd.Flags().StringVar(&argClustersServiceURL, "clusters-service-url", "https://api.openshift.com", "URL of the OCM API gateway")
	rootCmd.Flags().BoolVar(&argInsecure, "insecure", false, "Skip validating TLS for clusters-service")
	rootCmd.Flags().StringVar(&argMetricsListenAddress, "metrics-listen-address", ":8081", "Address on which to expose metrics")
	rootCmd.Flags().StringVar(&argPortListenAddress, "healthz-listen-address", ":8083", "Address on which Healthz endpoint will be supported")

	rootCmd.MarkFlagsRequiredTogether("cosmos-name", "cosmos-url")

	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" {
				rootCmd.Version = setting.Value
				break
			}
		}
	}
}

func newKubeconfig(kubeconfig string) (*rest.Config, error) {
	loader := clientcmd.NewDefaultClientConfigLoadingRules()
	if kubeconfig != "" {
		loader.ExplicitPath = kubeconfig
	}
	return clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loader, nil).ClientConfig()
}

func Run(cmd *cobra.Command, args []string) error {
	handler := slog.NewJSONHandler(os.Stdout, nil)
	logger := slog.New(handler)
	klog.SetLogger(logr.FromSlogHandler(handler))

	// Use pod name as the lock identity.
	hostname, err := os.Hostname()
	if err != nil {
		return err
	}

	kubeconfig, err := newKubeconfig(argKubeconfig)
	if err != nil {
		return fmt.Errorf("failed to create Kubernetes configuration: %w", err)
	}

	leaderElectionLock, err := resourcelock.NewFromKubeconfig(
		resourcelock.LeasesResourceLock,
		argNamespace,
		leaderElectionLockName,
		resourcelock.ResourceLockConfig{
			Identity: hostname,
		},
		kubeconfig,
		leaderElectionRenewDeadline)
	if err != nil {
		return fmt.Errorf("failed to create leader election lock: %w", err)
	}

	// Create the database client.
	cosmosDatabaseClient, err := database.NewCosmosDatabaseClient(
		argCosmosURL,
		argCosmosName,
		azcore.ClientOptions{
			// FIXME Cloud should be determined by other means.
			Cloud: cloud.AzurePublic,
		},
	)
	if err != nil {
		return fmt.Errorf("failed to create the CosmosDB client: %w", err)
	}

	dbClient, err := database.NewDBClient(context.Background(), cosmosDatabaseClient)
	if err != nil {
		return fmt.Errorf("failed to create the database client: %w", err)
	}

	// Create OCM connection
	ocmConnection, err := ocmsdk.NewUnauthenticatedConnectionBuilder().
		URL(argClustersServiceURL).
		Insecure(argInsecure).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create OCM connection: %w", err)
	}

	logger.Info(fmt.Sprintf("%s (%s) started", cmd.Short, cmd.Version))

	// Create HealthzAdaptor for leader election
	electionChecker := leaderelection.NewLeaderHealthzAdaptor(time.Second * 20)

	group, ctx := errgroup.WithContext(context.Background())

	// Handle requests directly for /healthz endpoint
	if argPortListenAddress != "" {
		backendHealthGauge := promauto.With(prometheus.DefaultRegisterer).NewGauge(prometheus.GaugeOpts{Name: "backend_health", Help: "backend_health is 1 when healthy"})

		http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {

			if err := electionChecker.Check(r); err != nil {
				http.Error(w, "lease not renewed", http.StatusServiceUnavailable)
				backendHealthGauge.Set(0.0)
				return
			}
			w.WriteHeader(http.StatusOK)
			backendHealthGauge.Set(1.0)
		})

		healthzServer := &http.Server{Addr: argPortListenAddress}

		group.Go(func() error {
			logger.Info(fmt.Sprintf("Healthz server listening on %s", argPortListenAddress))
			err := healthzServer.ListenAndServe()
			if errors.Is(err, http.ErrServerClosed) {
				return nil
			}
			return err
		})
	}

	var srv *http.Server
	if argMetricsListenAddress != "" {
		http.Handle("/metrics", promhttp.InstrumentMetricHandler(
			prometheus.DefaultRegisterer,
			promhttp.HandlerFor(
				prometheus.DefaultGatherer,
				promhttp.HandlerOpts{},
			),
		))

		srv = &http.Server{Addr: argMetricsListenAddress}

		group.Go(func() error {
			logger.Info(fmt.Sprintf("metrics server listening on %s", argMetricsListenAddress))
			err := srv.ListenAndServe()
			if errors.Is(err, http.ErrServerClosed) {
				return nil
			}

			return err
		})
	}

	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		<-ctx.Done()
		logger.Info("Caught interrupt signal")
		if srv != nil {
			_ = srv.Close()
		}
	}()

	group.Go(func() error {
		var (
			startedLeading    atomic.Bool
			operationsScanner = NewOperationsScanner(dbClient, ocmConnection)
		)

		// FIXME Integrate leaderelection.HealthzAdaptor into a /healthz endpoint.
		le, err := leaderelection.NewLeaderElector(leaderelection.LeaderElectionConfig{
			Lock:          leaderElectionLock,
			LeaseDuration: leaderElectionLeaseDuration,
			RenewDeadline: leaderElectionRenewDeadline,
			RetryPeriod:   leaderElectionRetryPeriod,
			Callbacks: leaderelection.LeaderCallbacks{
				OnStartedLeading: func(ctx context.Context) {
					operationsScanner.leaderGauge.Set(1)
					startedLeading.Store(true)
					go operationsScanner.Run(ctx, logger)
				},
				OnStoppedLeading: func() {
					operationsScanner.leaderGauge.Set(0)
					if startedLeading.Load() {
						operationsScanner.Join()
					}
				},
			},
			ReleaseOnCancel: true,
			WatchDog:        electionChecker,
			Name:            leaderElectionLockName,
		})
		if err != nil {
			return err
		}

		le.Run(ctx)
		return nil
	})

	if err := group.Wait(); err != nil {
		logger.Error(err.Error())
		os.Exit(1)
	}

	logger.Info(fmt.Sprintf("%s (%s) stopped", cmd.Short, cmd.Version))

	return nil
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		rootCmd.PrintErrln(rootCmd.ErrPrefix(), err.Error())
		os.Exit(1)
	}
}
