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

package hcp

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"

	authenticationv1 "k8s.io/api/authentication/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	client "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/common"
	cluster "github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/hcp"
	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/utils"
)

// HCPOptions represents shared configuration options for all hcp commands.
type HCPOptions struct {
	KubeconfigPath string // Path to kubeconfig file for Kubernetes access
}

// DefaultHCPOptions returns a new HCPOptions struct initialized with sensible defaults
// for the kubeconfig path.
func DefaultHCPOptions() *HCPOptions {
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	kubeconfig := loadingRules.GetDefaultFilename()

	return &HCPOptions{
		KubeconfigPath: kubeconfig,
	}
}

// BindHCPOptions configures cobra command flags for base options shared across all hcp commands.
func BindHCPOptions(opts *HCPOptions, cmd *cobra.Command) error {
	kubeconfigFlag := "kubeconfig"
	cmd.Flags().StringVar(&opts.KubeconfigPath, kubeconfigFlag, opts.KubeconfigPath, "path to the kubeconfig file")

	if err := cmd.MarkFlagFilename(kubeconfigFlag); err != nil {
		return fmt.Errorf("failed to mark flag %q as a file: %w", kubeconfigFlag, err)
	}
	return nil
}

// Validate performs validation of base options shared across all hcp commands.
func (o *HCPOptions) Validate() error {
	if _, err := os.Stat(o.KubeconfigPath); err != nil {
		return fmt.Errorf("kubeconfig not found at %s: %w", o.KubeconfigPath, err)
	}

	return nil
}

// ListHCPOptions represents options specific to listing HCP clusters
type ListHCPOptions struct {
	HCPOptions *HCPOptions
	Output     string
}

// ValidatedListHCPOptions represents validated configuration for HCP list operations
type ValidatedListHCPOptions struct {
	CtrlClient   client.Client
	ClientSet    kubernetes.Interface
	RestConfig   *rest.Config
	OutputFormat common.OutputFormat
}

// DefaultListHCPOptions returns a new ListHCPOptions with default values
func DefaultListHCPOptions() *ListHCPOptions {
	return &ListHCPOptions{
		HCPOptions: DefaultHCPOptions(),
		Output:     "table",
	}
}

// BindListHCPOptions binds command-line flags for HCP list operations
func BindListHCPOptions(opts *ListHCPOptions, cmd *cobra.Command) error {
	// Bind base options (kubeconfig access)
	if err := BindHCPOptions(opts.HCPOptions, cmd); err != nil {
		return err
	}

	// Add output format flag
	cmd.Flags().StringVarP(&opts.Output, "output", "o", opts.Output, "Output format: table, yaml, json")

	return nil
}

// Validate performs validation on the HCP list options
func (o *ListHCPOptions) Validate(ctx context.Context) (*ValidatedListHCPOptions, error) {
	// Validate output format
	outputFormat, err := common.ValidateOutputFormat(o.Output)
	if err != nil {
		return nil, fmt.Errorf("invalid output format '%s': %w", o.Output, err)
	}

	// Load kubeconfig
	config, err := clientcmd.BuildConfigFromFlags("", o.HCPOptions.KubeconfigPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load kubeconfig: %w", err)
	}

	// Create Kubernetes clients
	clientSet, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create Kubernetes client: %w", err)
	}

	// Create controller-runtime client with global scheme
	ctrlClient, err := client.New(config, client.Options{Scheme: common.Scheme()})
	if err != nil {
		return nil, fmt.Errorf("failed to create controller client: %w", err)
	}

	return &ValidatedListHCPOptions{
		CtrlClient:   ctrlClient,
		ClientSet:    clientSet,
		RestConfig:   config,
		OutputFormat: outputFormat,
	}, nil
}

// RawBreakglassHCPOptions represents the initial, unvalidated configuration for HCP breakglass operations.
type RawBreakglassHCPOptions struct {
	HCPOptions        *HCPOptions
	ClusterIdentifier string        // Target HCP cluster identifier (cluster ID or Azure resource ID)
	OutputPath        string        // Path to write generated kubeconfig
	SessionTimeout    time.Duration // Certificate/session timeout duration
	NoPortForward     bool          // Disable port forwarding after kubeconfig generation
	NoShell           bool          // Disable shell mode (shell is default when port-forwarding is enabled)
	Privileged        bool          // Use privileged access role (aro-sre-cluster-admin instead of aro-sre)
	ExecCommand       string        // Command to execute directly instead of spawning interactive shell
}

// DefaultBreakglassHCPOptions returns a new RawBreakglassHCPOptions struct initialized with sensible defaults.
func DefaultBreakglassHCPOptions() *RawBreakglassHCPOptions {
	return &RawBreakglassHCPOptions{
		HCPOptions:     DefaultHCPOptions(),
		SessionTimeout: 24 * time.Hour,
		NoPortForward:  false,
		NoShell:        false,
		Privileged:     false,
	}
}

// BindBreakglassHCPOptions configures cobra command flags for HCP-specific options.
func BindBreakglassHCPOptions(opts *RawBreakglassHCPOptions, cmd *cobra.Command) error {
	// Bind base options first
	if err := BindHCPOptions(opts.HCPOptions, cmd); err != nil {
		return fmt.Errorf("failed to bind base options: %w", err)
	}

	// Add HCP-specific flags
	cmd.Flags().StringVarP(&opts.OutputPath, "output", "o", opts.OutputPath, "path to write the generated kubeconfig")
	cmd.Flags().DurationVar(&opts.SessionTimeout, "session-timeout", opts.SessionTimeout, "certificate/session expiration time")
	cmd.Flags().BoolVar(&opts.NoPortForward, "no-port-forward", opts.NoPortForward, "do not start port forwarding after generating kubeconfig")
	cmd.Flags().BoolVar(&opts.NoShell, "no-shell", opts.NoShell, "do not spawn a shell; instead wait for Ctrl+C to terminate (default: spawn shell with KUBECONFIG set)")
	cmd.Flags().BoolVar(&opts.Privileged, "privileged", opts.Privileged, "use privileged access role (aro-sre-cluster-admin instead of aro-sre)")
	cmd.Flags().StringVar(&opts.ExecCommand, "exec", opts.ExecCommand, "execute command directly instead of spawning interactive shell")

	return nil
}

// Validate performs comprehensive validation of all HCP-specific input parameters.
func (o *RawBreakglassHCPOptions) Validate(ctx context.Context) (*ValidatedBreakglassHCPOptions, error) {
	logger := logr.FromContextOrDiscard(ctx)

	// Validate base options first
	if err := o.HCPOptions.Validate(); err != nil {
		return nil, err
	}

	// Parse and validate cluster identifier (ID or resource ID) - now mandatory
	if o.ClusterIdentifier == "" {
		return nil, fmt.Errorf("cluster identifier is required")
	}

	hcpIdentifier, err := common.ParseHCPIdentifier(o.ClusterIdentifier)
	if err != nil {
		return nil, err
	}

	// Validate SessionTimeout
	if o.SessionTimeout < 1*time.Minute {
		return nil, fmt.Errorf("session timeout must be at least 1 minute")
	}

	if o.SessionTimeout > 30*24*time.Hour {
		return nil, fmt.Errorf("session timeout cannot exceed 30 days")
	}

	// Validate exec command options
	if o.ExecCommand != "" && o.NoPortForward {
		return nil, fmt.Errorf("--exec requires port forwarding, cannot be used with --no-port-forward")
	}

	// Get current user from kubernetes context (equivalent to kubectl auth whoami)
	user, err := getCurrentUser(ctx, o.HCPOptions.KubeconfigPath)
	if err != nil {
		return nil, fmt.Errorf("failed to get current user: %w", err)
	}
	if user == "" {
		return nil, fmt.Errorf("unable to determine current user from kubernetes context")
	}
	logger.V(1).Info("auto-detected username from kubernetes context", "user", user)

	// Set default output path if not specified
	if o.OutputPath == "" {
		o.OutputPath, err = utils.GetTempFilename("hcp-kubeconfig-*.yaml")
		if err != nil {
			return nil, fmt.Errorf("failed to get temporary filename for the kubeconfig: %w", err)
		}
	}

	return &ValidatedBreakglassHCPOptions{
		validatedBreakglassHCPOptions: &validatedBreakglassHCPOptions{
			RawBreakglassHCPOptions: o,
			User:                    user,
			ClusterIdentifier:       hcpIdentifier,
		},
	}, nil
}

// validatedBreakglassHCPOptions is a private struct that enforces the options validation pattern.
type validatedBreakglassHCPOptions struct {
	*RawBreakglassHCPOptions
	User              string                // Auto-detected username from Kubernetes context
	ClusterIdentifier *common.HCPIdentifier // Parsed cluster identifier (ID or resource ID)
}

// ValidatedBreakglassHCPOptions represents HCP configuration that has passed validation but not yet
// initialized clients or discovered cluster information.
type ValidatedBreakglassHCPOptions struct {
	// Embed a private pointer that cannot be instantiated outside of this package
	*validatedBreakglassHCPOptions
}

// Complete performs client initialization and cluster discovery to create fully usable BreakglassHCPOptions.
func (o *ValidatedBreakglassHCPOptions) Complete(ctx context.Context) (*BreakglassHCPOptions, error) {
	logger := logr.FromContextOrDiscard(ctx)

	// Load kubeconfig
	config, err := clientcmd.BuildConfigFromFlags("", o.HCPOptions.KubeconfigPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load kubeconfig: %w", err)
	}

	// Create kubernetes client
	kubeClient, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	// Create controller-runtime client with global scheme
	ctrlClient, err := client.New(config, client.Options{Scheme: common.Scheme()})
	if err != nil {
		return nil, fmt.Errorf("failed to create controller client: %w", err)
	}

	// Create cluster discovery client
	clusterDiscovery := cluster.NewDiscovery(ctrlClient)

	// Determine Cluster ID to use (cluster identifier is now mandatory)
	var clusterID string
	// We have a parsed identifier, use it to determine the cluster ID
	if o.ClusterIdentifier.ClusterID != "" {
		clusterID = o.ClusterIdentifier.ClusterID
	} else if o.ClusterIdentifier.ResourceID != nil {
		// If we have a resource ID, resolve it to cluster ID
		clusterInfo, err := clusterDiscovery.DiscoverClusterByResourceID(ctx, o.ClusterIdentifier.ResourceID)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve cluster name %s to cluster ID: %w", o.ClusterIdentifier.ResourceID.Name, err)
		}
		clusterID = clusterInfo.ID
	}

	// Discover the cluster info using the cluster ID
	clusterInfo, err := clusterDiscovery.DiscoverClusterByID(ctx, clusterID)
	if err != nil {
		return nil, fmt.Errorf("failed to discover cluster info: %w", err)
	}

	logger.V(1).Info("completed HCP options",
		"clusterID", clusterID,
		"namespace", clusterInfo.Namespace,
		"clusterName", clusterInfo.Name,
		"user", o.User,
	)

	return &BreakglassHCPOptions{
		completedBreakglassHCPOptions: &completedBreakglassHCPOptions{
			validatedBreakglassHCPOptions: o.validatedBreakglassHCPOptions,
			KubeClient:                    kubeClient,
			CtrlClient:                    ctrlClient,
			RestConfig:                    config,
			ClusterID:                     clusterID,
			Namespace:                     clusterInfo.Namespace,
			ClusterName:                   clusterInfo.Name,
		},
	}, nil
}

// completedBreakglassHCPOptions is a private struct containing fully initialized clients and configuration.
//
// This struct represents the final stage of the options pattern, containing:
//   - All validated configuration from previous stages
//   - Initialized Kubernetes and dynamic clients
//   - REST configuration for client creation
//   - Discovered cluster namespace
//
// By keeping this private, we ensure BreakglassHCPOptions can only be created through Complete().
type completedBreakglassHCPOptions struct {
	*validatedBreakglassHCPOptions
	KubeClient  kubernetes.Interface
	CtrlClient  client.Client
	RestConfig  *rest.Config
	ClusterID   string
	Namespace   string
	ClusterName string
}

// BreakglassHCPOptions represents the final, fully validated and initialized configuration for HCP breakglass operations.
//
// This struct is the culmination of the three-stage options pattern and contains everything
// needed to execute the HCP breakglass workflow:
//   - Validated input parameters (both base and HCP-specific)
//   - Authenticated Kubernetes clients
//   - Discovered cluster namespace
//   - Ready-to-use REST configuration
//
// BreakglassHCPOptions can only be created by calling Complete() on ValidatedBreakglassHCPOptions, ensuring
// all validation and initialization has been performed.
type BreakglassHCPOptions struct {
	// Embed a private pointer that cannot be instantiated outside of this package
	*completedBreakglassHCPOptions
}

// getCurrentUser gets the current authenticated user from the Kubernetes context
// This is equivalent to running `kubectl auth whoami`
func getCurrentUser(ctx context.Context, kubeconfigPath string) (string, error) {
	// Load kubeconfig
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	if err != nil {
		return "", fmt.Errorf("failed to load kubeconfig: %w", err)
	}

	// Create kubernetes client
	kubeClient, err := kubernetes.NewForConfig(config)
	if err != nil {
		return "", fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	// Make a SelfSubjectReview request to get the current user
	// This is the same approach used by kubectl auth whoami
	selfSubjectReview := &authenticationv1.SelfSubjectReview{}

	result, err := kubeClient.AuthenticationV1().SelfSubjectReviews().Create(ctx, selfSubjectReview, metav1.CreateOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to get current user info: %w", err)
	}

	// Extract username from the result
	if result.Status.UserInfo.Username == "" {
		return "", fmt.Errorf("unable to determine current user from kubernetes context")
	}

	return result.Status.UserInfo.Username, nil
}
