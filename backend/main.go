package main

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"syscall"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
	ocmsdk "github.com/openshift-online/ocm-sdk-go"
	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/internal/database"
)

var (
	argLocation           string
	argCosmosName         string
	argCosmosURL          string
	argClustersServiceURL string
	argInsecure           bool

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

	rootCmd.Flags().StringVar(&argLocation, "location", os.Getenv("LOCATION"), "Azure location")
	rootCmd.Flags().StringVar(&argCosmosName, "cosmos-name", os.Getenv("DB_NAME"), "Cosmos database name")
	rootCmd.Flags().StringVar(&argCosmosURL, "cosmos-url", os.Getenv("DB_URL"), "Cosmos database URL")
	rootCmd.Flags().StringVar(&argClustersServiceURL, "clusters-service-url", "https://api.openshift.com", "URL of the OCM API gateway")
	rootCmd.Flags().BoolVar(&argInsecure, "insecure", false, "Skip validating TLS for clusters-service")

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

func newCosmosDBClient() (database.DBClient, error) {
	azcoreClientOptions := azcore.ClientOptions{
		// FIXME Cloud should be determined by other means.
		Cloud: cloud.AzurePublic,
	}

	credential, err := azidentity.NewDefaultAzureCredential(
		&azidentity.DefaultAzureCredentialOptions{
			ClientOptions: azcoreClientOptions,
		})
	if err != nil {
		return nil, err
	}

	client, err := azcosmos.NewClient(argCosmosURL, credential,
		&azcosmos.ClientOptions{
			ClientOptions: azcoreClientOptions,
		})
	if err != nil {
		return nil, err
	}

	databaseClient, err := client.NewDatabase(argCosmosName)
	if err != nil {
		return nil, err
	}

	return database.NewCosmosDBClient(context.Background(), databaseClient)
}

func Run(cmd *cobra.Command, args []string) error {
	handler := slog.NewJSONHandler(os.Stdout, nil)
	logger := slog.New(handler)

	// Create database client
	dbClient, err := newCosmosDBClient()
	if err != nil {
		return fmt.Errorf("Failed to create database client: %w", err)
	}

	// Create OCM connection
	ocmConnection, err := ocmsdk.NewUnauthenticatedConnectionBuilder().
		URL(argClustersServiceURL).
		Insecure(argInsecure).
		Build()
	if err != nil {
		return fmt.Errorf("Failed to create OCM connection: %w", err)
	}

	logger.Info(fmt.Sprintf("%s (%s) started", cmd.Short, cmd.Version))

	operationsScanner := NewOperationsScanner(dbClient, ocmConnection)

	stop := make(chan struct{})
	signalChannel := make(chan os.Signal, 1)
	signal.Notify(signalChannel, syscall.SIGINT, syscall.SIGTERM)

	go operationsScanner.Run(logger, stop)

	sig := <-signalChannel
	logger.Info(fmt.Sprintf("caught %s signal", sig))
	close(stop)

	operationsScanner.Join()

	logger.Info(fmt.Sprintf("%s (%s) stopped", cmd.Short, cmd.Version))

	return nil
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		rootCmd.PrintErrln(rootCmd.ErrPrefix(), err.Error())
		os.Exit(1)
	}
}
