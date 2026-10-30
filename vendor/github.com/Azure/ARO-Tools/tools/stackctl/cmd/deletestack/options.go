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

package deletestack

import (
	"context"
	"fmt"
	"regexp"
	"time"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"

	"github.com/Azure/ARO-Tools/tools/cmdutils"
	"github.com/Azure/ARO-Tools/tools/stackctl/pkg/stack"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armdeploymentstacks"
)

// stackNamePattern matches the names Azure accepts for deployment stacks.
var stackNamePattern = regexp.MustCompile(`^[-\w._()]{1,90}$`)

// RawOptions holds the unvalidated command-line input.
type RawOptions struct {
	CloudOptions   *cmdutils.RawOptions
	SubscriptionID string
	ResourceGroup  string
	Name           string
	// ActionOnUnmanage is "detach" or "delete" and has no default.
	ActionOnUnmanage string
	Timeout          time.Duration
}

type validatedOptions struct {
	*RawOptions
	cloud  *cmdutils.ValidatedOptions
	action stack.ActionOnUnmanage
}

// ValidatedOptions holds input that passed validation.
type ValidatedOptions struct {
	*validatedOptions
}

// CompletedOptions holds everything needed to run.
type CompletedOptions struct {
	*validatedOptions
	client *armdeploymentstacks.Client
}

// DefaultOptions returns options with default values.
func DefaultOptions() *RawOptions {
	return &RawOptions{
		CloudOptions: cmdutils.DefaultOptions(),
		Timeout:      30 * time.Minute,
	}
}

// BindOptions binds command-line flags to the options.
func BindOptions(opts *RawOptions, cmd *cobra.Command) error {
	if err := cmdutils.BindOptions(opts.CloudOptions, cmd); err != nil {
		return err
	}
	flags := cmd.Flags()
	flags.StringVar(&opts.SubscriptionID, "subscription", opts.SubscriptionID, "ID of the subscription that holds the resource group")
	flags.StringVar(&opts.ResourceGroup, "resource-group", opts.ResourceGroup, "Resource group that holds the deployment stack")
	flags.StringVar(&opts.Name, "name", opts.Name, "Name of the deployment stack to delete, exactly as Azure shows it")
	flags.StringVar(&opts.ActionOnUnmanage, "action-on-unmanage", opts.ActionOnUnmanage, `What happens to the resources the stack manages: "detach" keeps them, "delete" deletes them`)
	flags.DurationVar(&opts.Timeout, "timeout", opts.Timeout, "Timeout for the whole operation")
	for _, flag := range []string{"cloud", "subscription", "resource-group", "name", "action-on-unmanage"} {
		if err := cmd.MarkFlagRequired(flag); err != nil {
			return err
		}
	}
	return nil
}

// Validate checks the raw options.
func (o *RawOptions) Validate() (*ValidatedOptions, error) {
	cloud, err := o.CloudOptions.Validate()
	if err != nil {
		return nil, err
	}
	for _, item := range []struct {
		flag  string
		value string
	}{
		{flag: "subscription", value: o.SubscriptionID},
		{flag: "resource-group", value: o.ResourceGroup},
		{flag: "name", value: o.Name},
		{flag: "action-on-unmanage", value: o.ActionOnUnmanage},
	} {
		if item.value == "" {
			return nil, fmt.Errorf("--%s is required", item.flag)
		}
	}
	if !stackNamePattern.MatchString(o.Name) {
		return nil, fmt.Errorf("invalid --name %q: must be 1-90 characters of letters, digits, '-', '_', '.', '(' or ')'", o.Name)
	}
	action, err := stack.ParseActionOnUnmanage(o.ActionOnUnmanage)
	if err != nil {
		return nil, fmt.Errorf("invalid --action-on-unmanage: %w", err)
	}
	if o.Timeout <= 0 {
		return nil, fmt.Errorf("--timeout must be positive, got %s", o.Timeout)
	}
	return &ValidatedOptions{validatedOptions: &validatedOptions{RawOptions: o, cloud: cloud, action: action}}, nil
}

// Complete creates the Azure client.
func (o *ValidatedOptions) Complete() (*CompletedOptions, error) {
	cloud, err := o.cloud.Complete()
	if err != nil {
		return nil, err
	}
	cred, err := cmdutils.GetAzureTokenCredentialsForCloud(cloud.Configuration)
	if err != nil {
		return nil, fmt.Errorf("failed to obtain Azure credentials: %w", err)
	}
	client, err := armdeploymentstacks.NewClient(o.SubscriptionID, cred, &arm.ClientOptions{
		ClientOptions: azcore.ClientOptions{Cloud: cloud.Configuration},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create deployment stacks client: %w", err)
	}
	return &CompletedOptions{validatedOptions: o.validatedOptions, client: client}, nil
}

// Run validates, completes and executes the delete.
func (o *RawOptions) Run(ctx context.Context) error {
	validated, err := o.Validate()
	if err != nil {
		return err
	}
	completed, err := validated.Complete()
	if err != nil {
		return err
	}
	return completed.Run(ctx)
}

// Run deletes the deployment stack.
func (o *CompletedOptions) Run(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, o.Timeout)
	defer cancel()
	result, err := stack.DeleteAtResourceGroup(ctx, o.client, o.ResourceGroup, o.Name, o.action)
	if err != nil {
		return err
	}
	logr.FromContextOrDiscard(ctx).Info("Finished.", "result", result)
	return nil
}
