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

package release

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/cli"
	"k8s.io/client-go/kubernetes"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/version"
)

func DefaultReleaseStatusOptions() *RawReleaseStatusOptions {
	return &RawReleaseStatusOptions{
		OutputFormat: "yaml",
	}
}

func BindReleaseStatusOptions(opts *RawReleaseStatusOptions, cmd *cobra.Command) error {
	cmd.Flags().StringVarP(&opts.ReleaseName, "release", "r", opts.ReleaseName, "Helm release name (optional, discovers all if not specified)")
	cmd.Flags().StringVarP(&opts.Namespace, "namespace", "n", opts.Namespace, "Kubernetes namespace (optional, searches all if not specified)")
	cmd.Flags().StringVarP(&opts.OutputFormat, "output", "o", opts.OutputFormat, "Output format (yaml, json)")
	cmd.Flags().StringVar(&opts.KubeConfig, "kubeconfig", opts.KubeConfig, "Path to kubeconfig file")
	cmd.Flags().StringVar(&opts.KubeContext, "kube-context", opts.KubeContext, "Kubernetes context to use")
	cmd.Flags().StringVar(&opts.AroHcpCommit, "aro-hcp-commit", version.GetVersionInfo().Commit, "ARO-HCP GitHub commit SHA (overrides embedded version)")
	cmd.Flags().StringVar(&opts.SdpPipelinesCommit, "sdp-pipelines-commit", opts.SdpPipelinesCommit, "SDP Pipelines commit SHA")

	return nil
}

// RawReleaseStatusOptions holds input values.
type RawReleaseStatusOptions struct {
	ReleaseName        string
	Namespace          string
	OutputFormat       string
	KubeConfig         string
	KubeContext        string
	AroHcpCommit       string
	SdpPipelinesCommit string
}

// validatedReleaseStatusOptions is a private wrapper that enforces a call of Validate() before Complete() can be invoked.
type validatedReleaseStatusOptions struct {
	*RawReleaseStatusOptions
	settings *cli.EnvSettings
}

type ValidatedReleaseStatusOptions struct {
	// Embed a private pointer that cannot be instantiated outside of this package.
	*validatedReleaseStatusOptions
}

// completedReleaseStatusOptions is a private wrapper that enforces a call of Complete() before execution can be invoked.
type completedReleaseStatusOptions struct {
	ReleaseName        string
	Namespace          string
	OutputFormat       string
	HelmClient         *action.Configuration
	KubeClient         kubernetes.Interface
	AroHcpCommit       string
	SdpPipelinesCommit string
}

type ReleaseStatusOptions struct {
	// Embed a private pointer that cannot be instantiated outside of this package.
	*completedReleaseStatusOptions
}

func (o *RawReleaseStatusOptions) Validate(ctx context.Context) (*ValidatedReleaseStatusOptions, error) {
	// Validate output format
	switch o.OutputFormat {
	case "yaml", "json":
		// Valid formats
	default:
		return nil, fmt.Errorf("unsupported output format: %s (supported: yaml, json)", o.OutputFormat)
	}

	// Create Helm settings
	settings := cli.New()

	// Set kubeconfig if provided
	if o.KubeConfig != "" {
		if _, err := os.Stat(o.KubeConfig); os.IsNotExist(err) {
			return nil, fmt.Errorf("kubeconfig file does not exist: %s", o.KubeConfig)
		}
		settings.KubeConfig = o.KubeConfig
	}

	// Set kube-context if provided
	if o.KubeContext != "" {
		settings.KubeContext = o.KubeContext
	}

	return &ValidatedReleaseStatusOptions{
		validatedReleaseStatusOptions: &validatedReleaseStatusOptions{
			RawReleaseStatusOptions: o,
			settings:                settings,
		},
	}, nil
}

func (o *ValidatedReleaseStatusOptions) Complete(ctx context.Context) (*ReleaseStatusOptions, error) {
	// Create Helm action configuration
	actionConfig := new(action.Configuration)

	// Initialize with the appropriate namespace
	// If no namespace specified, we'll use "" which means all namespaces for discovery
	namespace := o.Namespace
	if namespace == "" {
		// For discovery mode, we need to query all namespaces
		// Helm will handle this appropriately in the list action
		namespace = ""
	}

	if err := actionConfig.Init(o.settings.RESTClientGetter(), namespace, os.Getenv("HELM_DRIVER"), func(format string, v ...interface{}) {
		// Helm debug function - we can implement proper logging here if needed
	}); err != nil {
		return nil, fmt.Errorf("failed to initialize Helm action configuration: %w", err)
	}

	// Create Kubernetes client
	restConfig, err := o.settings.RESTClientGetter().ToRESTConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to get REST config: %w", err)
	}

	kubeClient, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create Kubernetes client: %w", err)
	}

	return &ReleaseStatusOptions{
		completedReleaseStatusOptions: &completedReleaseStatusOptions{
			ReleaseName:        o.ReleaseName,
			Namespace:          o.Namespace,
			OutputFormat:       o.OutputFormat,
			HelmClient:         actionConfig,
			KubeClient:         kubeClient,
			AroHcpCommit:       o.AroHcpCommit,
			SdpPipelinesCommit: o.SdpPipelinesCommit,
		},
	}, nil
}
