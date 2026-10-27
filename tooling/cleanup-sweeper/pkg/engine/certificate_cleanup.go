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

package engine

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorepolicy "github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armsubscriptions"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azcertificates"

	"github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/pkg/engine/runner"
	certsteps "github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/pkg/engine/steps/certificates"
	"github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/pkg/policy"
)

// CertificateWorkflowOptions controls the certificate cleanup workflow.
type CertificateWorkflowOptions struct {
	DryRun       bool
	DeleteActive bool
	PurgeDeleted bool
	MaxDeletions int
	MaxPurges    int
	Parallelism  int
	// Policy must be validated. It defines the vault, the owner inventory
	// subscriptions and the discovery rules.
	Policy policy.CertificatePolicy
}

// CertificateCleanupWorkflow builds the cleanup workflow for certificates in the
// vault named by the policy. The workflow soft-deletes (and immediately
// purges) eligible active certificates first, then purges eligible deleted
// certificates, including any tombstone the first step could not purge.
// Resource-group ownership is read from the policy's owner inventory
// subscriptions, resolved by display name, whenever a rule needs an owner guard.
func CertificateCleanupWorkflow(ctx context.Context, credential azcore.TokenCredential, opts CertificateWorkflowOptions) (*runner.Engine, error) {
	if credential == nil {
		return nil, fmt.Errorf("azure credential is required")
	}
	// Do not replay a DELETE after a retry delay without rerunning the safety checks.
	client, err := azcertificates.NewClient(opts.Policy.VaultURL, credential, &azcertificates.ClientOptions{
		ClientOptions: azcore.ClientOptions{Retry: azcorepolicy.RetryOptions{MaxRetries: -1}},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create certificates client: %w", err)
	}

	var owners certsteps.OwnerInventoryFn
	if opts.Policy.Discovery.RequiresOwnerInventory() {
		subscriptionsClient, err := armsubscriptions.NewClient(credential, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create subscriptions client: %w", err)
		}
		subscriptionIDs, err := resolveSubscriptionIDs(ctx, subscriptionsClient, opts.Policy.OwnerInventory.Subscriptions)
		if err != nil {
			return nil, err
		}
		groups := make(map[string]certsteps.ResourceGroupLister, len(subscriptionIDs))
		for _, subscriptionID := range subscriptionIDs {
			groupsClient, err := armresources.NewResourceGroupsClient(subscriptionID, credential, nil)
			if err != nil {
				return nil, fmt.Errorf("failed to create resource groups client for %s: %w", subscriptionID, err)
			}
			groups[subscriptionID] = groupsClient
		}
		owners = certsteps.NewOwnerInventory(subscriptionIDs, groups, time.Now)
		logr.FromContextOrDiscard(ctx).Info(
			"Resolved certificate owner inventory subscriptions",
			"names", opts.Policy.OwnerInventory.Subscriptions,
			"subscriptionIDs", subscriptionIDs,
		)
	}
	logr.FromContextOrDiscard(ctx).Info("Certificate cleanup scope", "vault", opts.Policy.VaultURL)

	session, err := certsteps.NewSession(certsteps.Config{
		Client:       client,
		VaultURL:     opts.Policy.VaultURL,
		Policy:       opts.Policy.Discovery,
		Owners:       owners,
		DryRun:       opts.DryRun,
		DeleteActive: opts.DeleteActive,
		PurgeDeleted: opts.PurgeDeleted,
		MaxDeletions: opts.MaxDeletions,
		MaxPurges:    opts.MaxPurges,
	})
	if err != nil {
		return nil, err
	}
	return &runner.Engine{
		Steps:       session.Steps(),
		Parallelism: opts.Parallelism,
		DryRun:      opts.DryRun,
		Wait:        true,
		PostRunFn:   session.Finish,
	}, nil
}

// subscriptionLister lists the subscriptions visible to the credential.
type subscriptionLister interface {
	NewListPager(*armsubscriptions.ClientListOptions) *runtime.Pager[armsubscriptions.ClientListResponse]
}

// resolveSubscriptionIDs maps subscription display names to IDs, in order. A
// name that matches no visible subscription, or more than one, is an error:
// an owner inventory that silently skips a subscription would weaken the guard.
func resolveSubscriptionIDs(ctx context.Context, lister subscriptionLister, names []string) ([]string, error) {
	matches := make(map[string][]string, len(names))
	pager := lister.NewListPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to list subscriptions: %w", err)
		}
		for _, subscription := range page.Value {
			if subscription == nil || subscription.DisplayName == nil || subscription.SubscriptionID == nil {
				continue
			}
			matches[*subscription.DisplayName] = append(matches[*subscription.DisplayName], *subscription.SubscriptionID)
		}
	}

	ids := make([]string, 0, len(names))
	for _, name := range names {
		switch found := matches[name]; len(found) {
		case 0:
			return nil, fmt.Errorf("owner inventory subscription %q was not found among the subscriptions visible to the credential", name)
		case 1:
			ids = append(ids, found[0])
		default:
			return nil, fmt.Errorf("owner inventory subscription name %q is ambiguous: it matches %d subscriptions", name, len(found))
		}
	}
	return ids, nil
}
