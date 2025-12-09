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

package cleanup

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"

	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/Azure/ARO-Tools/pkg/graph"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"

	"github.com/Azure/ARO-HCP/tooling/templatize/cmd/entrypoint/entrypointutils"
	"github.com/Azure/ARO-HCP/tooling/templatize/pkg/pipeline"
)

func DefaultOptions() *RawOptions {
	return &RawOptions{
		RawOptions:           entrypointutils.DefaultOptions(),
		IgnoreResourceGroups: []string{"global", "hcp-kusto-us"},
	}
}

func BindOptions(opts *RawOptions, cmd *cobra.Command) error {
	if err := entrypointutils.BindOptions(opts.RawOptions, cmd); err != nil {
		return err
	}

	cmd.Flags().StringArrayVar(&opts.IgnoreResourceGroups, "ignore", opts.IgnoreResourceGroups, "Ignore this resource group.")

	cmd.Flags().BoolVar(&opts.DryRun, "dry-run", opts.DryRun, "Print the resource groups that would be cleaned up without deleting them.")
	cmd.Flags().BoolVar(&opts.Wait, "wait", opts.Wait, "Wait for the resource groups to be fully cleaned up.")

	return nil
}

type RawOptions struct {
	*entrypointutils.RawOptions

	IgnoreResourceGroups []string

	DryRun bool
	Wait   bool
}

// validatedOptions is a private wrapper that enforces a call of Validate() before Complete() can be invoked.
type validatedOptions struct {
	*RawOptions
	*entrypointutils.ValidatedOptions
}

type ValidatedOptions struct {
	// Embed a private pointer that cannot be instantiated outside of this package.
	*validatedOptions
}

// completedOptions is a private wrapper that enforces a call of Complete() before config generation can be invoked.
type completedOptions struct {
	*entrypointutils.Options

	AzureCredential    azcore.TokenCredential
	SubscriptionLookup pipeline.SubscriptionLookup

	IgnoreResourceGroups sets.Set[string]

	DryRun bool
	Wait   bool
}

type Options struct {
	// Embed a private pointer that cannot be instantiated outside of this package.
	*completedOptions
}

func (o *RawOptions) Validate(ctx context.Context) (*ValidatedOptions, error) {
	validated, err := o.RawOptions.Validate(ctx)
	if err != nil {
		return nil, err
	}

	return &ValidatedOptions{
		validatedOptions: &validatedOptions{
			RawOptions:       o,
			ValidatedOptions: validated,
		},
	}, nil
}

func (o *ValidatedOptions) Complete(ctx context.Context) (*Options, error) {
	completed, err := o.ValidatedOptions.Complete(ctx)
	if err != nil {
		return nil, err
	}

	azCredential, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create Azure credential: %w", err)
	}

	return &Options{
		completedOptions: &completedOptions{
			Options: completed,

			AzureCredential:    azCredential,
			SubscriptionLookup: pipeline.LookupSubscriptionID(o.Subscriptions),

			IgnoreResourceGroups: sets.New[string](o.IgnoreResourceGroups...),

			DryRun: o.DryRun,
			Wait:   o.Wait,
		},
	}, nil
}

func (o *Options) CleanUpResources(ctx context.Context) error {
	logger, err := logr.FromContext(ctx)
	if err != nil {
		return err
	}

	var executionGraph *graph.Graph
	if o.Entrypoint != nil {
		executionGraph, err = graph.ForEntrypoint(o.Topo, o.Entrypoint, o.Pipelines)
	} else {
		executionGraph, err = graph.ForPipeline(o.Service, o.Pipelines[o.Service.ServiceGroup])
	}
	if err != nil {
		return fmt.Errorf("failed to generate execution graph: %w", err)
	}

	group, _ := errgroup.WithContext(ctx)
	clients := map[string]*armresources.ResourceGroupsClient{}
	for _, resourceGroup := range executionGraph.ResourceGroups {
		rgLogger := logger.WithValues("resourceGroup", resourceGroup.ResourceGroup)
		if o.IgnoreResourceGroups.Has(resourceGroup.ResourceGroup) {
			rgLogger.Info("Ignoring resource group")
			continue
		}
		if o.DryRun {
			rgLogger.Info("Would delete resource group.")
			continue
		}

		subscriptionID, err := o.SubscriptionLookup(ctx, resourceGroup.Subscription)
		if err != nil {
			return fmt.Errorf("failed to lookup subscription ID for %q: %w", resourceGroup.Subscription, err)
		}

		var rgClient *armresources.ResourceGroupsClient
		if client, exists := clients[subscriptionID]; exists {
			rgClient = client
		} else {
			resourceGroupClient, err := armresources.NewResourceGroupsClient(subscriptionID, o.AzureCredential, nil)
			if err != nil {
				return fmt.Errorf("failed to create resource group client for subscription %s: %w", subscriptionID, err)
			}
			rgClient = resourceGroupClient
			clients[subscriptionID] = rgClient
		}

		rgLogger.Info("Deleting resource group.")
		poller, err := rgClient.BeginDelete(ctx, resourceGroup.ResourceGroup, nil)
		var deleteErr *azcore.ResponseError
		alreadyGone := errors.As(err, &deleteErr) && deleteErr.StatusCode == http.StatusNotFound
		if alreadyGone {
			rgLogger.Info("Resource group already deleted.")
			continue
		}
		if err != nil {
			return fmt.Errorf("failed to delete resource group %s: %w", resourceGroup.ResourceGroup, err)
		}

		if o.Wait {
			group.Go(func() error {
				if _, err := poller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{Frequency: 10 * time.Second}); err != nil {
					return fmt.Errorf("failed to wait for resource group %s to be deleted: %w", resourceGroup.ResourceGroup, err)
				}
				rgLogger.Info("Deleted resource group.")
				return nil
			})
		}
	}
	if o.Wait {
		if err := group.Wait(); err != nil {
			return err
		}
	}

	if !o.DryRun {
		if err := os.RemoveAll(o.StepCacheDir); err != nil {
			return fmt.Errorf("failed to remove cache dir %s: %w", o.StepCacheDir, err)
		}
		logger.Info("Cleaned up step cache dir.", "dir", o.StepCacheDir)
	}
	return nil
}
