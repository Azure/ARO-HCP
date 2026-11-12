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

	"k8s.io/apimachinery/pkg/util/uuid"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"

	"github.com/Azure/ARO-HCP/internal/version"
	"github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/cmd/workflow"
	certificatesworkflow "github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/cmd/workflow/certificates"
	resourcegroupworkflow "github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/cmd/workflow/resourcegroup"
	sharedworkflow "github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/cmd/workflow/shared"
	cleanuprunner "github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/pkg/engine/runner"
)

// newRegistry builds the available workflows. Each command invocation needs its
// own registry because workflows hold the state their options are bound to.
func newRegistry() (*workflow.Registry, error) {
	return workflow.NewRegistry(
		resourcegroupworkflow.New(),
		sharedworkflow.New(),
		certificatesworkflow.New(),
	)
}

// DefaultOptions returns the default CLI options.
func DefaultOptions() *RawOptions {
	return &RawOptions{
		Workflow:    resourcegroupworkflow.Name,
		Wait:        true,
		Parallelism: cleanuprunner.DefaultParallelism,
	}
}

// BindOptions binds CLI flags into RawOptions. The registry supplies the list of
// workflows and the help for their --options.
func BindOptions(opts *RawOptions, cmd *cobra.Command, registry *workflow.Registry) error {
	opts.registry = registry
	opts.flagChanged = cmd.Flags().Changed

	cmd.Flags().StringVar(&opts.Workflow, "workflow", opts.Workflow, fmt.Sprintf("Workflow to run: %s.", strings.Join(registry.Names(), "|")))
	cmd.Flags().StringVar(&opts.PolicyFile, "policy", opts.PolicyFile, "Path to the policy file of the selected workflow (required by the rg-ordered and certificates workflows, each of which has its own policy document).")
	cmd.Flags().BoolVar(&opts.DryRun, "dry-run", opts.DryRun, fmt.Sprintf("Preview only; discover and report what would be deleted (default: %t).", opts.DryRun))
	cmd.Flags().IntVar(&opts.Parallelism, "parallelism", opts.Parallelism, "Maximum parallel deletions per step.")
	cmd.Flags().DurationVar(&opts.Timeout, "timeout", opts.Timeout, "Overall timeout, including discovery and all Azure requests (default: none; 30m for the certificates workflow).")
	cmd.Flags().StringArrayVar(&opts.Options, "options", opts.Options, "Workflow option as <workflow>.<key>=<value>; repeatable. Only options of the selected workflow are accepted (see below).")

	// Deprecated aliases for options that used to be flags of every workflow.
	cmd.Flags().StringVar(&opts.SubscriptionID, "subscription-id", opts.SubscriptionID, "")
	cmd.Flags().StringSliceVar(&opts.ResourceGroups, "resource-group", opts.ResourceGroups, "")
	cmd.Flags().BoolVar(&opts.Wait, "wait", opts.Wait, "")
	for name, replacement := range map[string]string{
		"subscription-id": "--options=<workflow>.subscription-id=<id>",
		"resource-group":  "--options=rg-ordered.resource-group=<name>",
		"wait":            "--options=<workflow>.wait=<bool>",
	} {
		if err := cmd.Flags().MarkDeprecated(name, "use "+replacement); err != nil {
			return fmt.Errorf("failed to deprecate --%s: %w", name, err)
		}
	}
	return nil
}

// RawOptions contains CLI flags before validation and normalization.
type RawOptions struct {
	Workflow    string
	PolicyFile  string
	DryRun      bool
	Parallelism int
	Timeout     time.Duration
	// Options are the --options entries, each <workflow>.<key>=<value>.
	Options []string

	// Deprecated aliases, applied as options of the selected workflow before the
	// explicit --options entries.
	SubscriptionID string
	ResourceGroups []string
	Wait           bool

	registry *workflow.Registry
	// flagChanged reports whether a flag was set explicitly on the command line.
	// It is nil for options not bound to a command, where nothing counts as set.
	flagChanged func(name string) bool
}

func (o *RawOptions) changed(name string) bool {
	return o.flagChanged != nil && o.flagChanged(name)
}

// legacyEntries translates the deprecated flags into options of the selected
// workflow. Blank resource groups are ignored, as they always were.
func (o *RawOptions) legacyEntries() []string {
	var entries []string
	if o.SubscriptionID != "" {
		entries = append(entries, fmt.Sprintf("%s.subscription-id=%s", o.Workflow, o.SubscriptionID))
	}
	for _, resourceGroup := range o.ResourceGroups {
		if trimmed := strings.TrimSpace(resourceGroup); trimmed != "" {
			entries = append(entries, fmt.Sprintf("%s.resource-group=%s", o.Workflow, trimmed))
		}
	}
	if o.changed("wait") {
		entries = append(entries, fmt.Sprintf("%s.wait=%t", o.Workflow, o.Wait))
	}
	return entries
}

type validatedOptions struct {
	workflow workflow.Workflow
	common   workflow.Common
}

// ValidatedOptions wraps options that passed validation.
type ValidatedOptions struct {
	*validatedOptions
}

type completedOptions struct {
	AzureCredential azcore.TokenCredential

	Workflow workflow.Workflow
	Common   workflow.Common
}

// Options contains completed runtime options after validation and completion.
type Options struct {
	*completedOptions
}

// Validate validates the common options, applies the selected workflow's
// options and lets the workflow validate them.
func (o *RawOptions) Validate(_ context.Context) (*ValidatedOptions, error) {
	registry := o.registry
	if registry == nil {
		var err error
		if registry, err = newRegistry(); err != nil {
			return nil, err
		}
	}
	selected, err := registry.Get(o.Workflow)
	if err != nil {
		return nil, fmt.Errorf("--workflow: %w", err)
	}
	if o.Parallelism < 1 {
		return nil, fmt.Errorf("--parallelism must be >= 1, got %d", o.Parallelism)
	}
	if o.Timeout < 0 {
		return nil, fmt.Errorf("--timeout must not be negative, got %s", o.Timeout)
	}

	entries := append(o.legacyEntries(), o.Options...)
	if err := registry.ApplyOptions(o.Workflow, entries); err != nil {
		return nil, fmt.Errorf("--options: %w", err)
	}

	common := workflow.Common{
		DryRun:      o.DryRun,
		Parallelism: o.Parallelism,
		Timeout:     o.Timeout,
		PolicyFile:  strings.TrimSpace(o.PolicyFile),
	}
	if err := selected.Validate(common); err != nil {
		return nil, err
	}
	return &ValidatedOptions{validatedOptions: &validatedOptions{workflow: selected, common: common}}, nil
}

// Complete resolves validated options into runtime dependencies.
func (o *ValidatedOptions) Complete(_ context.Context) (*Options, error) {
	cred, err := azidentity.NewDefaultAzureCredential(&azidentity.DefaultAzureCredentialOptions{RequireAzureTokenCredentials: true})
	if err != nil {
		return nil, fmt.Errorf("failed to create Azure credential: %w", err)
	}
	return &Options{
		completedOptions: &completedOptions{
			AzureCredential: cred,
			Workflow:        o.workflow,
			Common:          o.common,
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
		"workflow", o.Workflow.Name(),
		"dryRun", o.Common.DryRun,
		"policy", o.Common.PolicyFile,
	).WithValues(o.Workflow.LogValues()...)
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			switch setting.Key {
			case "vcs.revision", "vcs.modified":
				logger = logger.WithValues(setting.Key, setting.Value)
			}
		}
	}
	ctx = logr.NewContext(ctx, logger)
	if o.Common.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, o.Common.Timeout)
		defer cancel()
	}
	logger.Info("Starting cleanup-sweeper")

	if err := o.Workflow.Run(ctx, o.Common, o.AzureCredential); err != nil {
		return err
	}

	logger.Info("Completed cleanup-sweeper")
	return nil
}
