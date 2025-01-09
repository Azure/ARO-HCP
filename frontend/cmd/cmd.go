package cmd

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
	sdk "github.com/openshift-online/ocm-sdk-go"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/frontend/pkg/config"
	"github.com/Azure/ARO-HCP/frontend/pkg/frontend"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
)

type FrontendOpts struct {
	clustersServiceURL            string
	clusterServiceProvisionShard  string
	clusterServiceNoopProvision   bool
	clusterServiceNoopDeprovision bool
	insecure                      bool

	location    string
	metricsPort int
	port        int

	cosmosName string
	cosmosURL  string
}

func NewRootCmd() *cobra.Command {
	opts := &FrontendOpts{}
	rootCmd := &cobra.Command{
		Use:     "aro-hcp-frontend",
		Version: version(),
		Args:    cobra.NoArgs,
		Short:   "Serve the ARO HCP Frontend",
		Long: `Serve the ARO HCP Frontend

	This command runs the ARO HCP Frontend. It communicates with Clusters Service and a CosmosDB

	# Run ARO HCP Frontend locally to connect to a local Clusters Service at http://localhost:8000
	./aro-hcp-frontend --cosmos-name ${DB_NAME} --cosmos-url ${DB_URL} --location ${LOCATION} \
		--clusters-service-url "http://localhost:8000"
`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return opts.Run()
		},
	}

	rootCmd.Flags().StringVar(&opts.cosmosName, "cosmos-name", os.Getenv("DB_NAME"), "Cosmos database name")
	rootCmd.Flags().StringVar(&opts.cosmosURL, "cosmos-url", os.Getenv("DB_URL"), "Cosmos database url")
	rootCmd.Flags().StringVar(&opts.location, "location", os.Getenv("LOCATION"), "Azure location")
	rootCmd.Flags().IntVar(&opts.port, "port", 8443, "port to listen on")
	rootCmd.Flags().IntVar(&opts.metricsPort, "metrics-port", 8081, "port to serve metrics on")

	rootCmd.Flags().StringVar(&opts.clustersServiceURL, "clusters-service-url", "https://api.openshift.com", "URL of the OCM API gateway.")
	rootCmd.Flags().BoolVar(&opts.insecure, "insecure", false, "Skip validating TLS for clusters-service.")
	rootCmd.Flags().StringVar(&opts.clusterServiceProvisionShard, "cluster-service-provision-shard", "", "Manually specify provision shard for all requests to cluster service")
	rootCmd.Flags().BoolVar(&opts.clusterServiceNoopProvision, "cluster-service-noop-provision", false, "Skip cluster service provisioning steps for development purposes")
	rootCmd.Flags().BoolVar(&opts.clusterServiceNoopDeprovision, "cluster-service-noop-deprovision", false, "Skip cluster service deprovisioning steps for development purposes")

	rootCmd.MarkFlagsRequiredTogether("cosmos-name", "cosmos-url")

	return rootCmd
}

func (opts *FrontendOpts) Run() error {
	logger := config.DefaultLogger()
	logger.Info(fmt.Sprintf("%s (%s) started", frontend.ProgramName, version()))

	// Init prometheus emitter
	prometheusEmitter := frontend.NewPrometheusEmitter(prometheus.DefaultRegisterer)

	// Configure database configuration and client

	azcoreClientOptions := azcore.ClientOptions{
		// FIXME Cloud should be determined by other means.
		Cloud: cloud.AzurePublic,
	}

	credential, err := azidentity.NewDefaultAzureCredential(
		&azidentity.DefaultAzureCredentialOptions{
			ClientOptions: azcoreClientOptions,
		})
	if err != nil {
		return err
	}

	cosmosClient, err := azcosmos.NewClient(opts.cosmosURL, credential,
		&azcosmos.ClientOptions{
			ClientOptions: azcoreClientOptions,
		})
	if err != nil {
		return err
	}

	cosmosDatabaseClient, err := cosmosClient.NewDatabase(opts.cosmosName)
	if err != nil {
		return err
	}

	dbClient, err := database.NewCosmosDBClient(context.Background(), cosmosDatabaseClient)
	if err != nil {
		return fmt.Errorf("creating the database client failed: %v", err)
	}

	listener, err := net.Listen("tcp4", fmt.Sprintf(":%d", opts.port))
	if err != nil {
		return err
	}

	metricsListener, err := net.Listen("tcp4", fmt.Sprintf(":%d", opts.metricsPort))
	if err != nil {
		return err
	}

	// Initialize Clusters Service Client
	conn, err := sdk.NewUnauthenticatedConnectionBuilder().
		URL(opts.clustersServiceURL).
		Insecure(opts.insecure).
		Build()
	if err != nil {
		return err
	}

	csClient := ocm.ClusterServiceClient{
		Conn:                       conn,
		ProvisionerNoOpProvision:   opts.clusterServiceNoopDeprovision,
		ProvisionerNoOpDeprovision: opts.clusterServiceNoopDeprovision,
	}

	if opts.clusterServiceProvisionShard != "" {
		csClient.ProvisionShardID = api.Ptr(opts.clusterServiceProvisionShard)
	}

	if len(opts.location) == 0 {
		return errors.New("location is required")
	}
	logger.Info(fmt.Sprintf("Application running in %s", opts.location))

	f := frontend.NewFrontend(logger, listener, metricsListener, prometheusEmitter, dbClient, opts.location, &csClient)

	stop := make(chan struct{})
	signalChannel := make(chan os.Signal, 1)
	signal.Notify(signalChannel, syscall.SIGINT, syscall.SIGTERM)
	go f.Run(context.Background(), stop)

	sig := <-signalChannel
	logger.Info(fmt.Sprintf("caught %s signal", sig))
	close(stop)

	f.Join()
	logger.Info(fmt.Sprintf("%s (%s) stopped", frontend.ProgramName, version()))

	return nil
}

func version() string {
	version := "unknown"
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" {
				version = setting.Value
				break
			}
		}
	}

	return version
}
