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

package cmd

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v6"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/tooling/aro-hcp-exporter/pkg/metrics"
)

var (
	listenAddress  string
	metricsPath    string
	subscriptionID string
)

// NewServeCommand creates the serve command
func NewServeCommand() (*cobra.Command, error) {
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the Prometheus metrics HTTP server",
		Long: `Start the HTTP server that exposes Prometheus metrics.

The server will listen on the specified address and expose metrics at the metrics path.`,
		RunE: runServe,
	}

	cmd.Flags().StringVar(&listenAddress, "listen-address", ":8080", "Address to listen on for metrics")
	cmd.Flags().StringVar(&metricsPath, "metrics-path", "/metrics", "Path to expose metrics on")
	cmd.Flags().StringVar(&subscriptionID, "subscription-id", "", "Azure subscription ID (optional, defaults to AZURE_SUBSCRIPTION_ID env var)")

	return cmd, nil
}

func runServe(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()

	// Get subscription ID from flag or environment variable
	if subscriptionID == "" {
		subscriptionID = os.Getenv("AZURE_SUBSCRIPTION_ID")
	}

	// Create a new Prometheus registry
	registry := prometheus.NewRegistry()

	// Register the dummy metric
	dummyMetric := metrics.NewDummyMetric()
	if err := registry.Register(dummyMetric); err != nil {
		return fmt.Errorf("failed to register dummy metric: %w", err)
	}

	// If subscription ID is provided, set up Azure client and public IP collector
	if subscriptionID != "" {
		cmd.Printf("Setting up Azure client for subscription: %s\n", subscriptionID)

		// Create Azure credential
		cred, err := azidentity.NewDefaultAzureCredential(nil)
		if err != nil {
			return fmt.Errorf("failed to create Azure credential: %w", err)
		}

		// Create Azure network client factory
		clientFactory, err := armnetwork.NewClientFactory(subscriptionID, cred, nil)
		if err != nil {
			return fmt.Errorf("failed to create Azure network client factory: %w", err)
		}

		// Create public IP address client
		publicIPClient := clientFactory.NewPublicIPAddressesClient()

		// Create and register public IP collector
		publicIPCollector := metrics.NewPublicIPCollector(publicIPClient)
		if err := registry.Register(publicIPCollector); err != nil {
			return fmt.Errorf("failed to register public IP collector: %w", err)
		}

		cmd.Printf("Registered public IP collector for subscription %s\n", subscriptionID)
	} else {
		cmd.Printf("No subscription ID provided, skipping Azure public IP metrics\n")
		cmd.Printf("Set AZURE_SUBSCRIPTION_ID env var or use --subscription-id flag to enable Azure metrics\n")
	}

	// Create HTTP handler for metrics
	handler := promhttp.HandlerFor(registry, promhttp.HandlerOpts{
		EnableOpenMetrics: true,
	})

	// Create HTTP mux
	mux := http.NewServeMux()
	mux.Handle(metricsPath, handler)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "ARO-HCP Exporter\n\nMetrics available at %s\n", metricsPath)
		if subscriptionID != "" {
			fmt.Fprintf(w, "Monitoring subscription: %s\n", subscriptionID)
		} else {
			fmt.Fprintf(w, "No Azure subscription configured\n")
		}
	})

	// Create HTTP server
	server := &http.Server{
		Addr:         listenAddress,
		Handler:      mux,
		ReadTimeout:  30 * time.Second,  // Increased timeout for Azure API calls
		WriteTimeout: 30 * time.Second,  // Increased timeout for Azure API calls
	}

	// Start server in a goroutine
	errChan := make(chan error, 1)
	go func() {
		cmd.Printf("Starting server on %s\n", listenAddress)
		cmd.Printf("Metrics available at http://%s%s\n", listenAddress, metricsPath)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errChan <- fmt.Errorf("server error: %w", err)
		}
	}()

	// Wait for context cancellation or server error
	select {
	case <-ctx.Done():
		cmd.Printf("\nShutting down server...\n")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("server shutdown error: %w", err)
		}
		cmd.Printf("Server stopped\n")
		return nil
	case err := <-errChan:
		return err
	}
}
