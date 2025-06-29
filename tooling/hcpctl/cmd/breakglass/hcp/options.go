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
	"time"

	"math/rand"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"
	authenticationv1 "k8s.io/api/authentication/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/cmd/breakglass/base"
	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/breakglass"
	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/cluster"
)

// ListHCPOptions represents options specific to listing HCP clusters
type ListHCPOptions struct {
	BaseOptions *base.RawBaseOptions // Only need kubeconfig access for listing
}

// ValidatedListHCPOptions represents validated configuration for HCP list operations
type ValidatedListHCPOptions struct {
	DynamicClient dynamic.Interface
	ClientSet     kubernetes.Interface
	RestConfig    *rest.Config
	Logger        logr.Logger
}

// DefaultListHCPOptions returns a new ListHCPOptions with default values
func DefaultListHCPOptions() *ListHCPOptions {
	return &ListHCPOptions{
		BaseOptions: base.DefaultBaseOptions(),
	}
}

// BindListHCPOptions binds command-line flags for HCP list operations
func BindListHCPOptions(opts *ListHCPOptions, cmd *cobra.Command) error {
	// Only bind base options (kubeconfig access)
	return base.BindBaseOptions(opts.BaseOptions, cmd)
}

// Validate performs validation on the HCP list options
func (o *ListHCPOptions) Validate(ctx context.Context) (*ValidatedListHCPOptions, error) {
	// Load kubeconfig
	config, err := clientcmd.BuildConfigFromFlags("", o.BaseOptions.KubeconfigPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load kubeconfig: %w", err)
	}

	// Create Kubernetes clients
	clientSet, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create Kubernetes client: %w", err)
	}

	dynamicClient, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create dynamic client: %w", err)
	}

	// Create logger
	logger := logr.Discard()

	return &ValidatedListHCPOptions{
		DynamicClient: dynamicClient,
		ClientSet:     clientSet,
		RestConfig:    config,
		Logger:        logger,
	}, nil
}

// RawHCPOptions represents the initial, unvalidated configuration for HCP breakglass operations.
//
// This struct contains all user-provided input before any validation or client creation.
// It follows the composition pattern by embedding base options and adding HCP-specific settings.
// Fields may be empty or invalid at this stage and must be validated before use.
type RawHCPOptions struct {
	BaseOptions       *base.RawBaseOptions // Embedded base options (kubeconfig)
	ClusterIdentifier string               // Target HCP cluster identifier (cluster ID or Azure resource ID)
	OutputPath        string               // Path to write generated kubeconfig
	SessionTimeout    time.Duration        // Certificate/session timeout duration
	NoPortForward     bool                 // Disable port forwarding after kubeconfig generation
	NoShell           bool                 // Disable shell mode (shell is default when port-forwarding is enabled)
}

// DefaultHCPOptions returns a new RawHCPOptions struct initialized with sensible defaults.
//
// This function creates base options with defaults and initializes HCP-specific options
// with reasonable values. It should be called before parsing command-line flags.
func DefaultHCPOptions() *RawHCPOptions {
	return &RawHCPOptions{
		BaseOptions:    base.DefaultBaseOptions(),
		SessionTimeout: 24 * time.Hour,
		NoPortForward:  false,
		NoShell:        false,
	}
}

// BindHCPOptions configures cobra command flags for HCP-specific options.
//
// This function first delegates to BindBaseOptions to set up shared flags,
// then adds HCP-specific flags for cluster targeting, output configuration,
// and behavioral control.
//
// Returns an error if flag binding fails, though this is rare in practice.
func BindHCPOptions(opts *RawHCPOptions, cmd *cobra.Command) error {
	// Bind base options first
	if err := base.BindBaseOptions(opts.BaseOptions, cmd); err != nil {
		return fmt.Errorf("failed to bind base options: %w", err)
	}

	// Add HCP-specific flags
	cmd.Flags().StringVarP(&opts.OutputPath, "output", "o", opts.OutputPath, "path to write the generated kubeconfig (default: sre-breakglass-<cluster-id>-<user>.kubeconfig)")
	cmd.Flags().DurationVar(&opts.SessionTimeout, "session-timeout", opts.SessionTimeout, "certificate/session expiration time")
	cmd.Flags().BoolVar(&opts.NoPortForward, "no-port-forward", opts.NoPortForward, "do not start port forwarding after generating kubeconfig")
	cmd.Flags().BoolVar(&opts.NoShell, "no-shell", opts.NoShell, "do not spawn a shell; instead wait for Ctrl+C to terminate (default: spawn shell with KUBECONFIG set)")

	return nil
}

// Validate performs comprehensive validation of all HCP-specific input parameters.
//
// This method first validates base options, then checks HCP-specific settings:
//   - Cluster ID format and constraints
//   - Username format if provided
//   - Output path is set or generates a default
//
// The validation enforces the three-stage options pattern by returning
// ValidatedHCPOptions that can only be created through this method.
//
// Returns ValidatedHCPOptions on success, or an error describing the first
// validation failure encountered.
func (o *RawHCPOptions) Validate(ctx context.Context) (*ValidatedHCPOptions, error) {
	logger := logr.FromContextOrDiscard(ctx)

	// Validate base options first
	if err := base.ValidateBaseOptions(o.BaseOptions); err != nil {
		return nil, err
	}

	// Parse and validate cluster identifier (ID or resource ID) - now mandatory
	if o.ClusterIdentifier == "" {
		return nil, fmt.Errorf("cluster identifier is required")
	}

	parsedIdentifier, err := cluster.ParseClusterIdentifier(o.ClusterIdentifier)
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

	// Get current user from kubernetes context (equivalent to kubectl auth whoami)
	user, err := getCurrentUser(ctx, o.BaseOptions.KubeconfigPath)
	if err != nil {
		return nil, fmt.Errorf("failed to get current user: %w", err)
	}
	if user == "" {
		return nil, fmt.Errorf("unable to determine current user from kubernetes context")
	}
	logger.V(1).Info("auto-detected username from kubernetes context", "user", user)

	// Set default output path if not specified
	if o.OutputPath == "" {
		o.OutputPath = generateBreakglassFilename()
	}

	return &ValidatedHCPOptions{
		validatedHCPOptions: &validatedHCPOptions{
			RawHCPOptions:     o,
			User:              user,
			ClusterIdentifier: parsedIdentifier,
		},
	}, nil
}

func generateBreakglassFilename() string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	r := rand.New(rand.NewSource(time.Now().UnixNano()))

	b := make([]byte, 10)
	for i := range b {
		b[i] = charset[r.Intn(len(charset))]
	}

	return fmt.Sprintf("sre-breakglass-%s.kubeconfig", string(b))
}

// validatedHCPOptions is a private struct that enforces the options validation pattern.
//
// This struct embeds RawHCPOptions and adds validated fields like the resolved user and cluster identifiers.
// By keeping this struct private, we ensure that ValidatedHCPOptions can only be
// created through the Validate() method, preventing skipping of validation.
type validatedHCPOptions struct {
	*RawHCPOptions
	User              string                    // Auto-detected username from Kubernetes context
	ClusterIdentifier *cluster.ParsedIdentifier // Parsed cluster identifier (ID or resource ID)
}

// ValidatedHCPOptions represents HCP configuration that has passed validation but not yet
// initialized clients or discovered cluster information.
//
// This is the second stage in the three-stage options pattern. ValidatedHCPOptions
// can only be created by calling Validate() on RawHCPOptions, ensuring all input
// has been verified. The final HCPOptions struct requires calling Complete() to
// initialize Kubernetes clients and discover the cluster namespace.
type ValidatedHCPOptions struct {
	// Embed a private pointer that cannot be instantiated outside of this package
	*validatedHCPOptions
}

// Complete performs client initialization and cluster discovery to create fully usable HCPOptions.
//
// This method:
//   - Loads and validates the kubeconfig file
//   - Creates Kubernetes and dynamic clients
//   - Discovers the cluster namespace and name using the provided cluster ID
//   - Validates that exactly one HostedCluster exists with the specified ID
//
// The resulting HCPOptions struct contains everything needed to execute the HCP breakglass
// workflow, including authenticated clients and the discovered namespace.
//
// Returns fully initialized HCPOptions on success, or an error if client creation
// or cluster discovery fails.
func (o *ValidatedHCPOptions) Complete(ctx context.Context) (*HCPOptions, error) {
	logger := logr.FromContextOrDiscard(ctx)

	// Load kubeconfig
	config, err := clientcmd.BuildConfigFromFlags("", o.BaseOptions.KubeconfigPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load kubeconfig: %w", err)
	}

	// Create kubernetes client
	kubeClient, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	// Create dynamic client for HostedCluster resources
	dynamicClient, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create dynamic client: %w", err)
	}

	var namespace, clusterName string

	// Create cluster discovery client
	clusterDiscovery := cluster.NewDiscovery(dynamicClient)

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
	namespace = clusterInfo.Namespace
	clusterName = clusterInfo.Name

	// Initialize and validate configuration
	cfg := breakglass.DefaultConfig()
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("configuration validation failed: %w", err)
	}

	logger.V(1).Info("completed HCP options",
		"clusterID", clusterID,
		"namespace", namespace,
		"clusterName", clusterName,
		"user", o.User,
	)

	return &HCPOptions{
		completedHCPOptions: &completedHCPOptions{
			validatedHCPOptions: o.validatedHCPOptions,
			KubeClient:          kubeClient,
			DynamicClient:       dynamicClient,
			RestConfig:          config,
			ClusterID:           clusterID,
			Namespace:           namespace,
			ClusterName:         clusterName,
			Config:              cfg,
		},
	}, nil
}

// completedHCPOptions is a private struct containing fully initialized clients and configuration.
//
// This struct represents the final stage of the options pattern, containing:
//   - All validated configuration from previous stages
//   - Initialized Kubernetes and dynamic clients
//   - REST configuration for client creation
//   - Discovered cluster namespace
//   - Validated breakglass configuration
//
// By keeping this private, we ensure HCPOptions can only be created through Complete().
type completedHCPOptions struct {
	*validatedHCPOptions
	KubeClient    kubernetes.Interface
	DynamicClient dynamic.Interface
	RestConfig    *rest.Config
	ClusterID     string // Always resolved cluster ID (for use in execution)
	Namespace     string
	ClusterName   string
	Config        *breakglass.Config
}

// HCPOptions represents the final, fully validated and initialized configuration for HCP breakglass operations.
//
// This struct is the culmination of the three-stage options pattern and contains everything
// needed to execute the HCP breakglass workflow:
//   - Validated input parameters (both base and HCP-specific)
//   - Authenticated Kubernetes clients
//   - Discovered cluster namespace
//   - Ready-to-use REST configuration
//
// HCPOptions can only be created by calling Complete() on ValidatedHCPOptions, ensuring
// all validation and initialization has been performed.
type HCPOptions struct {
	// Embed a private pointer that cannot be instantiated outside of this package
	*completedHCPOptions
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
