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
	"runtime/debug"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"

	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/uuid"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"

	"github.com/Azure/ARO-HCP/internal/version"
	certificatesworkflow "github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/cmd/workflow/certificates"
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
	// WorkflowCertificates runs cleanup of certificates in the Key Vault named by
	// the certificate policy.
	WorkflowCertificates WorkflowMode = "certificates"
)

// defaultCertificatesTimeout bounds a certificates run when --timeout is unset;
// the other workflows have no overall timeout by default.
const defaultCertificatesTimeout = 30 * time.Minute

// DefaultOptions returns the default CLI options.
func DefaultOptions() *RawOptions {
	return &RawOptions{
		Workflow:     string(WorkflowRGOrdered),
		Wait:         true,
		Parallelism:  cleanuprunner.DefaultParallelism,
		DeleteActive: true,
		PurgeDeleted: true,
		MaxDeletions: 1000,
		MaxPurges:    1000,
	}
}

// BindOptions binds CLI flags into RawOptions.
func BindOptions(opts *RawOptions, cmd *cobra.Command) error {
	opts.flagChanged = cmd.Flags().Changed

	cmd.Flags().StringVar(&opts.SubscriptionID, "subscription-id", opts.SubscriptionID, "Subscription ID to clean (rg-ordered and shared-leftovers workflows).")
	cmd.Flags().StringVar(&opts.PolicyFile, "policy", opts.PolicyFile, "Path to sweeper policy file (required for the rg-ordered and certificates workflows; each has its own policy document).")

	cmd.Flags().StringVar(&opts.Workflow, "workflow", opts.Workflow, "Workflow to run: rg-ordered|shared-leftovers|certificates.")
	cmd.Flags().BoolVar(&opts.DryRun, "dry-run", opts.DryRun, fmt.Sprintf("Preview only; discover and report what would be deleted (default: %t). Must be set explicitly for the certificates workflow.", opts.DryRun))
	cmd.Flags().BoolVar(&opts.Wait, "wait", opts.Wait, "Wait for long-running deletions to complete (rg-ordered and shared-leftovers workflows).")
	cmd.Flags().IntVar(&opts.Parallelism, "parallelism", opts.Parallelism, "Maximum parallel deletions per step.")
	cmd.Flags().DurationVar(&opts.Timeout, "timeout", opts.Timeout, fmt.Sprintf("Overall timeout, including discovery and all Azure requests (default: none; %s for the certificates workflow).", defaultCertificatesTimeout))

	cmd.Flags().StringSliceVar(&opts.ResourceGroups, "resource-group", opts.ResourceGroups, "Explicit resource group target (repeatable). Optional; when omitted, rg-ordered discovery rules drive candidate selection.")

	cmd.Flags().BoolVar(&opts.DeleteActive, "delete-active", opts.DeleteActive, "Certificates workflow: discover and soft-delete old, unowned active certificates.")
	cmd.Flags().BoolVar(&opts.PurgeDeleted, "purge-deleted", opts.PurgeDeleted, "Certificates workflow: purge each deleted certificate immediately and discover and purge eligible previously deleted certificates (tombstones).")
	cmd.Flags().IntVar(&opts.MaxDeletions, "max-deletions", opts.MaxDeletions, "Certificates workflow: maximum active certificates selected for deletion per run; inventory continues until this budget is filled or exhausted (must be positive; also caps dry-run selections).")
	cmd.Flags().IntVar(&opts.MaxPurges, "max-purges", opts.MaxPurges, "Certificates workflow: maximum previously deleted certificates (tombstones) purged per run; the immediate purge of each deleted active certificate is bounded by --max-deletions instead (must be positive; also caps dry-run selections).")

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
	Timeout     time.Duration

	ResourceGroups []string

	// Certificates workflow settings.
	DeleteActive bool
	PurgeDeleted bool
	MaxDeletions int
	MaxPurges    int

	// flagChanged reports whether a flag was set explicitly on the command line.
	// It is nil for options not bound to a command, where nothing counts as set.
	flagChanged func(name string) bool
}

func (o *RawOptions) changed(name string) bool {
	return o.flagChanged != nil && o.flagChanged(name)
}

type validatedOptions struct {
	*RawOptions

	workflow          WorkflowMode
	policy            *policy.Policy
	certificatePolicy *policy.CertificatePolicy
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
	GraphCredential   azcore.TokenCredential
	Policy            *policy.Policy
	CertificatePolicy *policy.CertificatePolicy

	Workflow WorkflowMode

	SubscriptionID string
	PolicyFile     string
	ReferenceTime  time.Time

	DryRun      bool
	Wait        bool
	Parallelism int
	Timeout     time.Duration

	ResourceGroups sets.Set[string]

	DeleteActive bool
	PurgeDeleted bool
	MaxDeletions int
	MaxPurges    int
}

// Options contains completed runtime options after validation and completion.
type Options struct {
	*completedOptions
}

// Validate validates and normalizes raw CLI options.
func (o *RawOptions) Validate(_ context.Context) (*ValidatedOptions, error) {
	workflow, err := parseWorkflowMode(o.Workflow)
	if err != nil {
		return nil, err
	}
	if o.Timeout < 0 {
		return nil, fmt.Errorf("--timeout must not be negative, got %s", o.Timeout)
	}

	if workflow != WorkflowCertificates {
		if o.SubscriptionID == "" {
			return nil, fmt.Errorf("--subscription-id is required")
		}
		for _, name := range []string{"delete-active", "purge-deleted", "max-deletions", "max-purges"} {
			if o.changed(name) {
				return nil, fmt.Errorf("--%s is only valid with --workflow=%s", name, WorkflowCertificates)
			}
		}
	}
	if o.Parallelism < 1 {
		return nil, fmt.Errorf("--parallelism must be >= 1, got %d", o.Parallelism)
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

	var certificatePolicy *policy.CertificatePolicy
	if workflow == WorkflowCertificates {
		certificatePolicy, err = o.validateCertificates()
		if err != nil {
			return nil, err
		}
	}

	return &ValidatedOptions{
		validatedOptions: &validatedOptions{
			RawOptions:        o,
			workflow:          workflow,
			policy:            pol,
			certificatePolicy: certificatePolicy,
		},
	}, nil
}

// validateCertificates checks the certificates workflow options and loads its
// policy. The scope (vault and owner subscriptions) comes from the policy, so
// flags that select a subscription or resource group are rejected, and the
// destructive-by-default root --dry-run must be chosen explicitly.
func (o *RawOptions) validateCertificates() (*policy.CertificatePolicy, error) {
	if o.SubscriptionID != "" || o.changed("subscription-id") {
		return nil, fmt.Errorf("--subscription-id is not valid with --workflow=%s; its scope comes from --policy", WorkflowCertificates)
	}
	if len(o.ResourceGroups) > 0 || o.changed("resource-group") {
		return nil, fmt.Errorf("--resource-group is not valid with --workflow=%s; its scope comes from --policy", WorkflowCertificates)
	}
	if o.changed("wait") {
		return nil, fmt.Errorf("--wait is not valid with --workflow=%s", WorkflowCertificates)
	}
	if o.flagChanged != nil && !o.changed("dry-run") {
		return nil, fmt.Errorf("--dry-run must be set explicitly with --workflow=%s (--dry-run=false applies deletions)", WorkflowCertificates)
	}
	if err := (certificatesworkflow.RunOptions{
		DryRun:       o.DryRun,
		DeleteActive: o.DeleteActive,
		PurgeDeleted: o.PurgeDeleted,
		MaxDeletions: o.MaxDeletions,
		MaxPurges:    o.MaxPurges,
		Parallelism:  o.Parallelism,
	}).Validate(); err != nil {
		return nil, err
	}
	if o.PolicyFile == "" {
		return nil, fmt.Errorf("--policy is required for %s workflow", WorkflowCertificates)
	}
	return policy.LoadCertificates(o.PolicyFile)
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
	if o.workflow == WorkflowSharedLeftovers {
		graphCred, err = newGraphCredential(cred)
		if err != nil {
			return nil, err
		}
	}

	timeout := o.Timeout
	if o.workflow == WorkflowCertificates && timeout == 0 {
		timeout = defaultCertificatesTimeout
	}

	return &Options{
		completedOptions: &completedOptions{
			AzureCredential:   cred,
			GraphCredential:   graphCred,
			Policy:            o.policy,
			CertificatePolicy: o.certificatePolicy,
			Workflow:          o.workflow,
			SubscriptionID:    subscriptionID,
			PolicyFile:        policyFile,
			ReferenceTime:     referenceTime,
			DryRun:            o.DryRun,
			Wait:              o.Wait,
			Parallelism:       o.Parallelism,
			Timeout:           timeout,
			ResourceGroups:    resourceGroups,
			DeleteActive:      o.DeleteActive,
			PurgeDeleted:      o.PurgeDeleted,
			MaxDeletions:      o.MaxDeletions,
			MaxPurges:         o.MaxPurges,
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
		"runID", string(uuid.NewUUID()),
		"commitSHA", version.CommitSHA,
		"prowJob", os.Getenv("JOB_NAME"),
		"prowBuildID", os.Getenv("BUILD_ID"),
		"prowJobID", os.Getenv("PROW_JOB_ID"),
		"workflow", o.Workflow,
		"dryRun", o.DryRun,
		"subscriptionID", o.SubscriptionID,
		"policy", o.PolicyFile,
	)
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			switch setting.Key {
			case "vcs.revision", "vcs.modified":
				logger = logger.WithValues(setting.Key, setting.Value)
			}
		}
	}
	ctx = logr.NewContext(ctx, logger)
	if o.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, o.Timeout)
		defer cancel()
	}
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
			SubscriptionID:  o.SubscriptionID,
			AzureCredential: o.AzureCredential,
			GraphCredential: o.GraphCredential,
			DryRun:          o.DryRun,
			Wait:            o.Wait,
			Parallelism:     o.Parallelism,
		})
		if err != nil {
			return err
		}
	case WorkflowCertificates:
		err := certificatesworkflow.Run(ctx, certificatesworkflow.RunOptions{
			AzureCredential: o.AzureCredential,
			DryRun:          o.DryRun,
			DeleteActive:    o.DeleteActive,
			PurgeDeleted:    o.PurgeDeleted,
			MaxDeletions:    o.MaxDeletions,
			MaxPurges:       o.MaxPurges,
			Parallelism:     o.Parallelism,
			Policy:          o.CertificatePolicy,
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

// Returns the credential used for Microsoft Graph directory
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

// Parses a supported cleanup workflow.
func parseWorkflowMode(raw string) (WorkflowMode, error) {
	switch WorkflowMode(raw) {
	case WorkflowRGOrdered:
		return WorkflowRGOrdered, nil
	case WorkflowSharedLeftovers:
		return WorkflowSharedLeftovers, nil
	case WorkflowCertificates:
		return WorkflowCertificates, nil
	default:
		return "", fmt.Errorf("--workflow must be one of: %s, %s, %s", WorkflowRGOrdered, WorkflowSharedLeftovers, WorkflowCertificates)
	}
}

// Collects nonempty, trimmed selectors.
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
