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
	"regexp"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/util/sets"

	cleanuprunner "github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/pkg/engine/runner"
	"github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/pkg/policy"
	resourcegroupworkflow "github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/pkg/workflow/resourcegroup"
	sharedworkflow "github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/pkg/workflow/shared"
)

const defaultParallelism = 8

type WorkflowMode string

const (
	WorkflowRGOrdered       WorkflowMode = "rg-ordered"
	WorkflowSharedLeftovers WorkflowMode = "shared-leftovers"
)

func DefaultOptions() *RawOptions {
	return &RawOptions{
		Workflow:    string(WorkflowRGOrdered),
		Wait:        true,
		Parallelism: defaultParallelism,
	}
}

func BindOptions(opts *RawOptions, cmd *cobra.Command) error {
	cmd.Flags().StringVar(&opts.SubscriptionID, "subscription-id", opts.SubscriptionID, "Subscription ID to clean.")
	cmd.Flags().StringVar(&opts.PolicyFile, "policy", opts.PolicyFile, "Path to sweeper policy file (required for rg-ordered workflow).")

	cmd.Flags().StringVar(&opts.Workflow, "workflow", opts.Workflow, "Workflow to run: rg-ordered|shared-leftovers.")
	cmd.Flags().BoolVar(&opts.DryRun, "dry-run", opts.DryRun, fmt.Sprintf("Preview only; discover and report what would be deleted (default: %t).", opts.DryRun))
	cmd.Flags().BoolVar(&opts.Wait, "wait", opts.Wait, "Wait for long-running deletions to complete.")
	cmd.Flags().IntVar(&opts.Parallelism, "parallelism", opts.Parallelism, "Maximum parallel deletions per step.")

	cmd.Flags().BoolVar(&opts.DiscoverResourceGroups, "discover-resource-groups", opts.DiscoverResourceGroups, fmt.Sprintf("Discover candidate resource groups from policy rules (default: %t).", opts.DiscoverResourceGroups))
	cmd.Flags().StringSliceVar(&opts.ResourceGroups, "resource-group", opts.ResourceGroups, "Explicit resource group target (repeatable).")

	cmd.Flags().StringSliceVar(&opts.RequireTags, "require-tag", opts.RequireTags, "Require tag filter in k=v format (repeatable).")
	cmd.Flags().StringSliceVar(&opts.ExcludeNameRegexes, "exclude-name-regex", opts.ExcludeNameRegexes, "Exclude resources by name regex (repeatable).")
	cmd.Flags().StringSliceVar(&opts.ExcludeIDRegexes, "exclude-id-regex", opts.ExcludeIDRegexes, "Exclude resources by ID regex (repeatable).")
	cmd.Flags().BoolVar(&opts.FailOnDiscoveryError, "fail-on-discovery-error", opts.FailOnDiscoveryError, fmt.Sprintf("Fail immediately on discovery errors (default: %t).", opts.FailOnDiscoveryError))

	return nil
}

type RawOptions struct {
	SubscriptionID string
	PolicyFile     string

	Workflow    string
	DryRun      bool
	Wait        bool
	Parallelism int

	DiscoverResourceGroups bool
	ResourceGroups         []string

	RequireTags        []string
	ExcludeNameRegexes []string
	ExcludeIDRegexes   []string

	FailOnDiscoveryError bool
}

type validatedOptions struct {
	*RawOptions

	workflow WorkflowMode
	policy   *policy.Policy
}

type ValidatedOptions struct {
	*validatedOptions
}

type completedOptions struct {
	AzureCredential azcore.TokenCredential
	Policy          *policy.Policy

	Workflow WorkflowMode

	SubscriptionID string
	PolicyFile     string
	ReferenceTime  time.Time

	DryRun      bool
	Wait        bool
	Parallelism int

	DiscoverResourceGroups   bool
	FailOnDiscoveryError     bool

	ResourceGroups sets.Set[string]

	RequireTags        map[string]string
	ExcludeNameRegexes []*regexp.Regexp
	ExcludeIDRegexes   []*regexp.Regexp
}

type Options struct {
	*completedOptions
}

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

	if workflow == WorkflowRGOrdered && !hasRGOrderedSelectors(o) {
		return nil, fmt.Errorf("rg-ordered workflow requires at least one RG selector or --discover-resource-groups")
	}
	if workflow == WorkflowSharedLeftovers {
		if o.DiscoverResourceGroups || len(o.ResourceGroups) > 0 {
			return nil, fmt.Errorf("rg-ordered selectors are not allowed for shared-leftovers workflow")
		}
	}
	if workflow == WorkflowRGOrdered && o.DiscoverResourceGroups && len(pol.RGOrdered.Discovery.Rules) == 0 {
		return nil, fmt.Errorf("rg-ordered discovery requires rgOrdered.discovery.rules in policy")
	}
	return &ValidatedOptions{
		validatedOptions: &validatedOptions{
			RawOptions: o,
			workflow:   workflow,
			policy:     pol,
		},
	}, nil
}

func (o *ValidatedOptions) Complete(_ context.Context) (*Options, error) {
	subscriptionID := strings.TrimSpace(o.SubscriptionID)
	policyFile := strings.TrimSpace(o.PolicyFile)
	referenceTime := time.Now().UTC()

	resourceGroups := setFromTrimmed(o.ResourceGroups)

	excludeNameRegexes, err := compileRegexSlice(sets.List(setFromTrimmed(o.ExcludeNameRegexes)), "--exclude-name-regex")
	if err != nil {
		return nil, err
	}
	excludeIDRegexes, err := compileRegexSlice(sets.List(setFromTrimmed(o.ExcludeIDRegexes)), "--exclude-id-regex")
	if err != nil {
		return nil, err
	}
	requireTags, err := parseRequiredTags(sets.List(setFromTrimmed(o.RequireTags)))
	if err != nil {
		return nil, err
	}

	cred, err := azidentity.NewDefaultAzureCredential(&azidentity.DefaultAzureCredentialOptions{RequireAzureTokenCredentials: true})
	if err != nil {
		return nil, fmt.Errorf("failed to create Azure credential: %w", err)
	}

	return &Options{
		completedOptions: &completedOptions{
			AzureCredential:          cred,
			Policy:                   o.policy,
			Workflow:                 o.workflow,
			SubscriptionID:           subscriptionID,
			PolicyFile:               policyFile,
			ReferenceTime:            referenceTime,
			DryRun:                   o.DryRun,
			Wait:                     o.Wait,
			Parallelism:              o.Parallelism,
			DiscoverResourceGroups:   o.DiscoverResourceGroups,
			FailOnDiscoveryError:     o.FailOnDiscoveryError,
			ResourceGroups:           resourceGroups,
			RequireTags:              requireTags,
			ExcludeNameRegexes:       excludeNameRegexes,
			ExcludeIDRegexes:         excludeIDRegexes,
		},
	}, nil
}

func (o *Options) Run(ctx context.Context) error {
	logger := cleanuprunner.LoggerFromContext(ctx).WithValues(
		"workflow", o.Workflow,
		"dryRun", o.DryRun,
		"subscriptionID", o.SubscriptionID,
		"policy", o.PolicyFile,
	)
	logger.Info("Starting cleanup-sweeper")

	switch o.Workflow {
	case WorkflowRGOrdered:
		err := resourcegroupworkflow.Run(ctx, resourcegroupworkflow.RunOptions{
			SubscriptionID:         o.SubscriptionID,
			AzureCredential:        o.AzureCredential,
			DryRun:                 o.DryRun,
			Wait:                   o.Wait,
			Parallelism:            o.Parallelism,
			DiscoverResourceGroups: o.DiscoverResourceGroups,
			ResourceGroups:         o.ResourceGroups,
			FailOnDiscoveryError:   o.FailOnDiscoveryError,
			Policy:                 o.Policy.RGOrdered,
			ReferenceTime:          o.ReferenceTime,
		})
		if err != nil {
			return err
		}
	case WorkflowSharedLeftovers:
		err := sharedworkflow.Run(ctx, sharedworkflow.RunOptions{
			SubscriptionID:  o.SubscriptionID,
			AzureCredential: o.AzureCredential,
			DryRun:          o.DryRun,
			Wait:            o.Wait,
			Parallelism:     o.Parallelism,
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

func compileRegexSlice(raw []string, flagName string) ([]*regexp.Regexp, error) {
	out := make([]*regexp.Regexp, 0, len(raw))
	for _, expr := range raw {
		re, err := regexp.Compile(expr)
		if err != nil {
			return nil, fmt.Errorf("invalid regex for %s: %q: %w", flagName, expr, err)
		}
		out = append(out, re)
	}
	return out, nil
}

func parseRequiredTags(raw []string) (map[string]string, error) {
	out := make(map[string]string, len(raw))
	for _, entry := range raw {
		parts := strings.SplitN(entry, "=", 2)
		if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" {
			return nil, fmt.Errorf("invalid --require-tag value %q, expected k=v", entry)
		}
		out[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
	}
	return out, nil
}

func hasRGOrderedSelectors(o *RawOptions) bool {
	return o.DiscoverResourceGroups ||
		len(o.ResourceGroups) > 0
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
