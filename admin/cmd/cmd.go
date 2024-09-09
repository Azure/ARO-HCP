package cmd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"

	"github.com/Azure/ARO-HCP/admin/pkg/admin"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/ocm"
	sdk "github.com/openshift-online/ocm-sdk-go"
	"github.com/spf13/cobra"
)

type AdminOpts struct {
	clustersServiceURL            string
	clusterServiceProvisionShard  string
	clusterServiceNoopProvision   bool
	clusterServiceNoopDeprovision bool
	insecure                      bool

	location string
	port     int
}

func NewRootCmd() *cobra.Command {
	opts := &AdminOpts{}
	rootCmd := &cobra.Command{
		Use:     "aro-hcp-admin",
		Version: version(),
		Args:    cobra.NoArgs,
		Short:   "Serve the ARO HCP Admin",
		Long: `Serve the ARO HCP Admin

	This command runs the ARO HCP Admin. 

	# Run ARO HCP Admin locally to connect to a local Clusters Service at http://localhost:8000
	./aro-hcp-admin --location ${LOCATION} --clusters-service-url "http://localhost:8000"
`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return opts.Run()
		},
	}

	rootCmd.Flags().StringVar(&opts.location, "location", os.Getenv("LOCATION"), "Azure location")
	rootCmd.Flags().IntVar(&opts.port, "port", 8443, "port to listen on")

	rootCmd.Flags().StringVar(&opts.clustersServiceURL, "clusters-service-url", "https://api.openshift.com", "URL of the OCM API gateway.")
	rootCmd.Flags().BoolVar(&opts.insecure, "insecure", false, "Skip validating TLS for clusters-service.")
	rootCmd.Flags().StringVar(&opts.clusterServiceProvisionShard, "cluster-service-provision-shard", "", "Manually specify provision shard for all requests to cluster service")
	rootCmd.Flags().BoolVar(&opts.clusterServiceNoopProvision, "cluster-service-noop-provision", false, "Skip cluster service provisioning steps for development purposes")
	rootCmd.Flags().BoolVar(&opts.clusterServiceNoopDeprovision, "cluster-service-noop-deprovision", false, "Skip cluster service deprovisioning steps for development purposes")

	return rootCmd
}

func (opts *AdminOpts) Run() error {
	logger := DefaultLogger()
	logger.Info(fmt.Sprintf("%s (%s) started", admin.ProgramName, version()))

	listener, err := net.Listen("tcp4", fmt.Sprintf(":%d", opts.port))
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

	csCfg := ocm.ClusterServiceConfig{
		Conn:                       conn,
		ProvisionerNoOpProvision:   opts.clusterServiceNoopDeprovision,
		ProvisionerNoOpDeprovision: opts.clusterServiceNoopDeprovision,
	}

	if opts.clusterServiceProvisionShard != "" {
		csCfg.ProvisionShardID = api.Ptr(opts.clusterServiceProvisionShard)
	}

	if len(opts.location) == 0 {
		return errors.New("location is required")
	}
	logger.Info(fmt.Sprintf("Application running in %s", opts.location))

	adm := admin.NewAdmin(logger, listener, opts.location)

	stop := make(chan struct{})
	signalChannel := make(chan os.Signal, 1)
	signal.Notify(signalChannel, syscall.SIGINT, syscall.SIGTERM)
	go adm.Run(context.Background(), stop)

	sig := <-signalChannel
	logger.Info(fmt.Sprintf("caught %s signal", sig))
	close(stop)

	adm.Join()
	logger.Info(fmt.Sprintf("%s (%s) stopped", admin.ProgramName, version()))

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

func DefaultLogger() *slog.Logger {
	handlerOptions := slog.HandlerOptions{}
	handler := slog.NewJSONHandler(os.Stdout, &handlerOptions)
	logger := slog.New(handler)
	return logger
}
