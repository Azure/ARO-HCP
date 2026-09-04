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

package root

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"

	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"

	resourcegroupworkflow "github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/cmd/workflow/resourcegroup"
	sharedworkflow "github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/cmd/workflow/shared"
	cleanuprunner "github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/pkg/engine/runner"
	"github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/pkg/policy"
)

// WorkflowMode selects which cleanup workflow implementation to run.
type WorkflowMode string

const (
	// WorkflowRGOrdered runs ordered per-resource-group cleanup.
	WorkflowRGOrdered WorkflowMode = "rg-ordered"
	// WorkflowSharedLeftovers runs cleanup for shared leftovers.
	WorkflowSharedLeftovers WorkflowMode = "shared-leftovers"
)

// DefaultOptions returns the default CLI options.
func DefaultOptions() *RawOptions {
	return &RawOptions{
		Workflow:    string(WorkflowRGOrdered),
		Wait:        true,
		Parallelism: cleanuprunner.DefaultParallelism,
	}
}

// BindOptions binds CLI flags into RawOptions.
func BindOptions(opts *RawOptions, cmd *cobra.Command) error {
	cmd.Flags().StringVar(&opts.SubscriptionID, "subscription-id", opts.SubscriptionID, "Subscription ID to clean.")
	cmd.Flags().StringVar(&opts.PolicyFile, "policy", opts.PolicyFile, "Path to sweeper policy file (required for rg-ordered workflow).")

	cmd.Flags().StringVar(&opts.Workflow, "workflow", opts.Workflow, "Workflow to run: rg-ordered|shared-leftovers.")
	cmd.Flags().BoolVar(&opts.DryRun, "dry-run", opts.DryRun, fmt.Sprintf("Preview only; discover and report what would be deleted (default: %t).", opts.DryRun))
	cmd.Flags().BoolVar(&opts.Wait, "wait", opts.Wait, "Wait for long-running deletions to complete.")
	cmd.Flags().IntVar(&opts.Parallelism, "parallelism", opts.Parallelism, "Maximum parallel deletions per step.")

	cmd.Flags().StringSliceVar(&opts.ResourceGroups, "resource-group", opts.ResourceGroups, "Explicit resource group target (repeatable). Optional; when omitted, rg-ordered discovery rules drive candidate selection.")

	return nil
}

// RawOptions contains CLI flags before validation and normalization.
type RawOptions struct {
	SubscriptionID string
	PolicyFile     string

	Workflow    string
	DryRun      bool
	Wait        bool
	Parallelism int

	ResourceGroups []string
}

type validatedOptions struct {
	*RawOptions

	workflow WorkflowMode
	policy   *policy.Policy
}

// ValidatedOptions wraps options that passed validation.
type ValidatedOptions struct {
	*validatedOptions
}

type completedOptions struct {
	AzureCredential azcore.TokenCredential
	// GraphCredential is used exclusively for Microsoft Graph directory reads
	// by the shared-leftovers workflow. It defaults to AzureCredential unless a
	// dedicated Graph identity is supplied via the GRAPH_AZURE_* environment
	// variables.
	GraphCredential azcore.TokenCredential
	// DirectoryWriteCredential backs the aged-deleted-directory-object purge
	// step's Graph client. Unlike GraphCredential, it is opt-in: it is only
	// set when a dedicated DIRECTORY_WRITE_AZURE_* identity is configured, and
	// never falls back to AzureCredential or GraphCredential, since this
	// credential needs materially higher (directory-write) privilege.
	DirectoryWriteCredential azcore.TokenCredential
	Policy                   *policy.Policy

	Workflow WorkflowMode

	SubscriptionID string
	PolicyFile     string
	ReferenceTime  time.Time

	DryRun      bool
	Wait        bool
	Parallelism int

	ResourceGroups sets.Set[string]
}

// Options contains completed runtime options after validation and completion.
type Options struct {
	*completedOptions
}

// Validate validates and normalizes raw CLI options.
func (o *RawOptions) Validate(_ context.Context) (*ValidatedOptions, error) {
	if o.SubscriptionID == "" {
		return nil, fmt.Errorf("--subscription-id is required")
	}
	if o.Parallelism < 1 {
		return nil, fmt.Errorf("--parallelism must be >= 1, got %d", o.Parallelism)
	}

	workflow, err := parseWorkflowMode(o.Workflow)
	if err != nil {
		return nil, err
	}
	normalizedResourceGroups := setFromTrimmed(o.ResourceGroups)

	pol := &policy.Policy{}
	if workflow == WorkflowRGOrdered {
		if o.PolicyFile == "" {
			return nil, fmt.Errorf("--policy is required for rg-ordered workflow")
		}

		loadedPolicy, err := policy.Load(o.PolicyFile)
		if err != nil {
			return nil, err
		}
		if err := loadedPolicy.Validate(); err != nil {
			return nil, fmt.Errorf("invalid --policy content: %w", err)
		}
		pol = loadedPolicy
	}

	if workflow == WorkflowSharedLeftovers {
		if normalizedResourceGroups.Len() > 0 {
			return nil, fmt.Errorf("rg-ordered selectors are not allowed for shared-leftovers workflow")
		}
	}

	return &ValidatedOptions{
		validatedOptions: &validatedOptions{
			RawOptions: o,
			workflow:   workflow,
			policy:     pol,
		},
	}, nil
}

// Complete resolves validated options into runtime dependencies.
func (o *ValidatedOptions) Complete(_ context.Context) (*Options, error) {
	subscriptionID := strings.TrimSpace(o.SubscriptionID)
	policyFile := strings.TrimSpace(o.PolicyFile)
	referenceTime := time.Now().UTC()

	resourceGroups := setFromTrimmed(o.ResourceGroups)

	cred, err := azidentity.NewDefaultAzureCredential(&azidentity.DefaultAzureCredentialOptions{RequireAzureTokenCredentials: true})
	if err != nil {
		return nil, fmt.Errorf("failed to create Azure credential: %w", err)
	}

	// Only the shared-leftovers workflow performs Microsoft Graph directory
	// reads, so resolve the (optionally dedicated) Graph credential only then.
	// This keeps a partial GRAPH_AZURE_* configuration from failing workflows
	// that never touch Graph (for example, rg-ordered).
	var graphCred azcore.TokenCredential = cred
	var directoryWriteCred azcore.TokenCredential
	if o.workflow == WorkflowSharedLeftovers {
		graphCred, err = newGraphCredential(cred)
		if err != nil {
			return nil, err
		}
		directoryWriteCred, err = newDirectoryWriteCredential()
		if err != nil {
			return nil, err
		}
	}

	return &Options{
		completedOptions: &completedOptions{
			AzureCredential:          cred,
			GraphCredential:          graphCred,
			DirectoryWriteCredential: directoryWriteCred,
			Policy:                   o.policy,
			Workflow:                 o.workflow,
			SubscriptionID:           subscriptionID,
			PolicyFile:               policyFile,
			ReferenceTime:            referenceTime,
			DryRun:                   o.DryRun,
			Wait:                     o.Wait,
			Parallelism:              o.Parallelism,
			ResourceGroups:           resourceGroups,
		},
	}, nil
}

// Run executes the selected cleanup workflow.
func (o *Options) Run(ctx context.Context) error {
	logger, err := logr.FromContext(ctx)
	if err != nil {
		panic(err)
	}
	logger = logger.WithValues(
		"workflow", o.Workflow,
		"dryRun", o.DryRun,
		"subscriptionID", o.SubscriptionID,
		"policy", o.PolicyFile,
	)
	logger.Info("Starting cleanup-sweeper")

	switch o.Workflow {
	case WorkflowRGOrdered:
		err := resourcegroupworkflow.Run(ctx, resourcegroupworkflow.RunOptions{
			SubscriptionID:  o.SubscriptionID,
			AzureCredential: o.AzureCredential,
			DryRun:          o.DryRun,
			Wait:            o.Wait,
			Parallelism:     o.Parallelism,
			ResourceGroups:  o.ResourceGroups,
			Policy:          o.Policy.RGOrdered,
			ReferenceTime:   o.ReferenceTime,
		})
		if err != nil {
			return err
		}
	case WorkflowSharedLeftovers:
		err := sharedworkflow.Run(ctx, sharedworkflow.RunOptions{
			SubscriptionID:           o.SubscriptionID,
			AzureCredential:          o.AzureCredential,
			GraphCredential:          o.GraphCredential,
			DirectoryWriteCredential: o.DirectoryWriteCredential,
			DryRun:                   o.DryRun,
			Wait:                     o.Wait,
			Parallelism:              o.Parallelism,
		})
		if err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported workflow %q", o.Workflow)
	}

	logger.Info("Completed cleanup-sweeper")
	return nil
}

// newGraphCredential returns the credential used for Microsoft Graph directory
// reads. When the GRAPH_AZURE_TENANT_ID, GRAPH_AZURE_CLIENT_ID and
// GRAPH_AZURE_CLIENT_SECRET environment variables are all set, a dedicated
// client-secret credential is built for that identity (allowing directory read
// permissions to be supplied by a different service principal than the one used
// for ARM). Otherwise it falls back to the provided ARM credential.
func newGraphCredential(fallback azcore.TokenCredential) (azcore.TokenCredential, error) {
	tenantID := strings.TrimSpace(os.Getenv("GRAPH_AZURE_TENANT_ID"))
	clientID := strings.TrimSpace(os.Getenv("GRAPH_AZURE_CLIENT_ID"))
	clientSecret := strings.TrimSpace(os.Getenv("GRAPH_AZURE_CLIENT_SECRET"))

	if tenantID == "" && clientID == "" && clientSecret == "" {
		return fallback, nil
	}
	if tenantID == "" || clientID == "" || clientSecret == "" {
		return nil, fmt.Errorf("GRAPH_AZURE_TENANT_ID, GRAPH_AZURE_CLIENT_ID and GRAPH_AZURE_CLIENT_SECRET must all be set to use a dedicated Graph credential")
	}

	cred, err := azidentity.NewClientSecretCredential(tenantID, clientID, clientSecret, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create dedicated Graph credential: %w", err)
	}
	return cred, nil
}

// newDirectoryWriteCredential returns the credential used by the
// aged-deleted-directory-object purge step, which needs directory-write
// permission (Directory.ReadWrite.All / Application.ReadWrite.All). Unlike
// newGraphCredential, it never falls back to another credential: the step is
// materially more privileged (it permanently deletes directory objects), so
// it is only enabled when a dedicated identity is explicitly configured via
// the DIRECTORY_WRITE_AZURE_* environment variables. Returns a nil credential
// (and nil error) when unset, which the shared-leftovers workflow treats as
// "omit this step".
func newDirectoryWriteCredential() (azcore.TokenCredential, error) {
	tenantID := strings.TrimSpace(os.Getenv("DIRECTORY_WRITE_AZURE_TENANT_ID"))
	clientID := strings.TrimSpace(os.Getenv("DIRECTORY_WRITE_AZURE_CLIENT_ID"))
	clientSecret := strings.TrimSpace(os.Getenv("DIRECTORY_WRITE_AZURE_CLIENT_SECRET"))

	if tenantID == "" && clientID == "" && clientSecret == "" {
		return nil, nil
	}
	if tenantID == "" || clientID == "" || clientSecret == "" {
		return nil, fmt.Errorf("DIRECTORY_WRITE_AZURE_TENANT_ID, DIRECTORY_WRITE_AZURE_CLIENT_ID and DIRECTORY_WRITE_AZURE_CLIENT_SECRET must all be set to enable the aged-deleted-directory-object purge step")
	}

	cred, err := azidentity.NewClientSecretCredential(tenantID, clientID, clientSecret, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create directory-write credential: %w", err)
	}
	return cred, nil
}

func parseWorkflowMode(raw string) (WorkflowMode, error) {
	switch WorkflowMode(raw) {
	case WorkflowRGOrdered:
		return WorkflowRGOrdered, nil
	case WorkflowSharedLeftovers:
		return WorkflowSharedLeftovers, nil
	default:
		return "", fmt.Errorf("--workflow must be one of: %s, %s", WorkflowRGOrdered, WorkflowSharedLeftovers)
	}
}

func setFromTrimmed(values []string) sets.Set[string] {
	result := sets.New[string]()
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		result.Insert(trimmed)
	}
	return result
}
