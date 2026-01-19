package cmd

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

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"

	"k8s.io/klog/v2"

	"github.com/Azure/ARO-HCP/backend/pkg/app"
	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/internal/version"
)

type BackendRootCmdFlags struct {
	Kubeconfig                                      string
	K8sNamespace                                    string
	AzureLocation                                   string
	AzureCosmosDBName                               string
	AzureCosmosDBURL                                string
	ClustersServiceURL                              string
	ClustersServiceTLSInsecure                      bool
	MetricsServerListenAddress                      string
	HealthzServerListenAddress                      string
	AzureRuntimeConfigPath                          string
	AzureFirstPartyApplicationCertificateBundlePath string
	AzureFirstPartyApplicationClientID              string
}

func (f *BackendRootCmdFlags) AddFlags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&f.Kubeconfig, "kubeconfig", f.Kubeconfig, "Absolute path to the kubeconfig file")
	cmd.Flags().StringVar(&f.K8sNamespace, "namespace", f.K8sNamespace, "Kubernetes namespace")
	cmd.Flags().StringVar(&f.AzureLocation, "location", f.AzureLocation, "Azure location")
	cmd.Flags().StringVar(&f.AzureCosmosDBName, "cosmos-name", f.AzureCosmosDBName, "Cosmos database name")
	cmd.Flags().StringVar(&f.AzureCosmosDBURL, "cosmos-url", f.AzureCosmosDBURL, "Cosmos database URL")
	cmd.Flags().StringVar(&f.ClustersServiceURL, "clusters-service-url", f.ClustersServiceURL, "URL of the OCM API gateway")
	cmd.Flags().BoolVar(&f.ClustersServiceTLSInsecure, "insecure", f.ClustersServiceTLSInsecure, "Skip validating TLS for clusters-service")
	cmd.Flags().StringVar(&f.MetricsServerListenAddress, "metrics-listen-address", f.MetricsServerListenAddress, "Address on which to expose metrics")
	cmd.Flags().StringVar(&f.HealthzServerListenAddress, "healthz-listen-address", f.HealthzServerListenAddress, "Address on which Healthz endpoint will be supported")
	cmd.Flags().StringVar(
		&f.AzureRuntimeConfigPath, "azure-runtime-config-path", f.AzureRuntimeConfigPath,
		"Path to a file containing the Azure runtime configuration in JSON or YAML format following the schema defined "+
			"in backend/api/azure/v1/AzureRuntimeConfig",
	)
	cmd.Flags().StringVar(
		&f.AzureFirstPartyApplicationCertificateBundlePath,
		"azure-first-party-application-certificate-bundle-path", f.AzureFirstPartyApplicationCertificateBundlePath,
		"Path to a file containing an X.509 Certificate based client certificate, consisting of a private key and "+
			"certificate chain, in a PEM or PKCS#12 format for authenticating clients with a first party application identity",
	)
	cmd.Flags().StringVar(
		&f.AzureFirstPartyApplicationClientID,
		"azure-first-party-application-client-id",
		f.AzureFirstPartyApplicationClientID,
		"The client id of the first party application identity",
	)
}

func (f *BackendRootCmdFlags) Validate(cmd *cobra.Command) error {
	if len(f.AzureLocation) == 0 {
		return fmt.Errorf("location is required")
	}

	// TODO unsure where to put this as it doesn't return an error.
	// It also references flag names.
	cmd.MarkFlagsRequiredTogether("cosmos-name", "cosmos-url")

	return nil
}

func (f *BackendRootCmdFlags) ToBackendOptions(appShort string, appVersion string) *app.BackendOptions {
	return &app.BackendOptions{
		AppShortDescriptionName:    appShort,
		AppVersion:                 appVersion,
		Kubeconfig:                 f.Kubeconfig,
		K8sNamespace:               f.K8sNamespace,
		AzureLocation:              f.AzureLocation,
		CosmosDBName:               f.AzureCosmosDBName,
		CosmosDBURL:                f.AzureCosmosDBURL,
		ClustersServiceURL:         f.ClustersServiceURL,
		ClustersServiceTLSInsecure: f.ClustersServiceTLSInsecure,
	}
}

func NewBackendRootCmdFlags() *BackendRootCmdFlags {
	flags := &BackendRootCmdFlags{
		Kubeconfig:                 "",
		K8sNamespace:               os.Getenv("NAMESPACE"),
		AzureLocation:              os.Getenv("LOCATION"),
		AzureCosmosDBName:          os.Getenv("DB_NAME"),
		AzureCosmosDBURL:           os.Getenv("DB_URL"),
		ClustersServiceURL:         "https://api.openshift.com",
		ClustersServiceTLSInsecure: false,
		MetricsServerListenAddress: ":8081",
		HealthzServerListenAddress: ":8083",
		AzureRuntimeConfigPath:     "",
		AzureFirstPartyApplicationCertificateBundlePath: "",
		AzureFirstPartyApplicationClientID:              "",
	}

	return flags
}

func NewCmdRoot() *cobra.Command {
	processName := filepath.Base(os.Args[0])

	flags := NewBackendRootCmdFlags()

	cmd := &cobra.Command{
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
		RunE: func(cmd *cobra.Command, args []string) error {
			return RunRootCmd(cmd, flags)
		},
		SilenceErrors: true, // errors are printed after Execute
	}

	cmd.SetErrPrefix(cmd.Short + " error:")

	cmd.Version = version.CommitSHA

	flags.AddFlags(cmd)

	return cmd
}

func RunRootCmd(cmd *cobra.Command, flags *BackendRootCmdFlags) error {
	ctx := context.Background()
	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		AddSource: true,
	})
	logger := slog.New(handler)
	klog.SetLogger(logr.FromSlogHandler(handler))
	ctx = utils.ContextWithLogger(ctx, logger)

	err := flags.Validate(cmd)
	if err != nil {
		return fmt.Errorf("flags validation failed: %w", err)
	}

	backendOptions := flags.ToBackendOptions(cmd.Short, cmd.Version)
	backend := app.NewBackend(backendOptions)

	err = backend.Run(ctx)
	if err != nil {
		return fmt.Errorf("failed to run backend: %w", err)
	}

	return nil
}
