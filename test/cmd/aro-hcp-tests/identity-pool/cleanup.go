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

package identitypool

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/msi/armmsi"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armsubscriptions"
	"github.com/go-logr/logr"
	"github.com/spf13/cobra"
)

const identityPoolResourceGroupTagFilter = "tagName eq 'purpose' and tagValue eq 'aro-hcp-e2e-msi-pool'"

var issuerJobIDRegex = regexp.MustCompile(`j\d{3}`)
var subscriptionIDRegex = regexp.MustCompile(`^[0-9a-fA-F-]{36}$`)

func DefaultCleanupOptions() *RawCleanupOptions {
	return &RawCleanupOptions{}
}

func BindCleanupOptions(opts *RawCleanupOptions, cmd *cobra.Command) {
	cmd.Flags().StringVar(&opts.Environment, "environment", opts.Environment, "Identity pool environment (dev|int|stg|prod)")
	cmd.Flags().StringVarP(&opts.Subscription, "subscription", "s", opts.Subscription, "Subscription name or ID for the identity pool (overrides subscription env var resolution)")
	cmd.Flags().BoolVar(&opts.DryRun, "dry-run", opts.DryRun, "Compute deletions but do not delete; print what would be deleted")
	cmd.Flags().StringVarP(&opts.InfraSubscription, "infra-subscription", "i", opts.InfraSubscription, "Subscription name or ID where prow underlay RGs live (used to auto-discover active job IDs)")
}

type RawCleanupOptions struct {
	Environment       string
	Subscription      string
	InfraSubscription string
	DryRun            bool
}

type validatedCleanupOptions struct {
	*RawCleanupOptions
}

type ValidatedCleanupOptions struct {
	*validatedCleanupOptions
}

type completedCleanupOptions struct {
	IdentityPool    identityPool
	SubscriptionID  string
	AzureCredential azcore.TokenCredential
	DryRun          bool
	KeepJobIDs      map[string]struct{}
}

type CleanupOptions struct {
	*completedCleanupOptions
}

func (o *RawCleanupOptions) Validate() (*ValidatedCleanupOptions, error) {
	switch o.Environment {
	case "dev", "int", "stg", "prod":
	default:
		return nil, fmt.Errorf("invalid environment %q: must be 'dev', 'int', 'stg', or 'prod'", o.Environment)
	}

	if strings.TrimSpace(o.InfraSubscription) == "" {
		return nil, fmt.Errorf("--infra-subscription is required")
	}

	return &ValidatedCleanupOptions{
		validatedCleanupOptions: &validatedCleanupOptions{
			RawCleanupOptions: o,
		},
	}, nil
}

func (o *ValidatedCleanupOptions) Complete(ctx context.Context) (*CleanupOptions, error) {
	pool := identityPoolMapping[o.Environment]

	tc := framework.NewTestContext()
	cred, err := tc.AzureCredential()
	if err != nil {
		return nil, fmt.Errorf("failed getting Azure credential: %w", err)
	}

	var subscriptionID string
	if strings.TrimSpace(o.Subscription) != "" {
		// IMPORTANT: when the user provides -s/--subscription, bypass the framework's
		// env-driven subscription resolution entirely.
		overridden, err := resolveSubscriptionID(ctx, cred, o.Subscription)
		if err != nil {
			return nil, fmt.Errorf("failed resolving subscription override %q: %w", o.Subscription, err)
		}
		subscriptionID = overridden
	} else {
		resolved, err := tc.SubscriptionID(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed getting subscription ID: %w", err)
		}
		subscriptionID = resolved
	}
	if err := validateSubscriptionIDHash(pool, subscriptionID); err != nil {
		return nil, fmt.Errorf("failed validating subscription ID hash: %w", err)
	}

	infraSubscriptionID, err := resolveSubscriptionID(ctx, cred, o.InfraSubscription)
	if err != nil {
		return nil, fmt.Errorf("failed resolving infra subscription %q: %w", o.InfraSubscription, err)
	}

	return &CleanupOptions{
		completedCleanupOptions: &completedCleanupOptions{
			IdentityPool:    pool,
			SubscriptionID:  subscriptionID,
			AzureCredential: cred,
			DryRun:          o.DryRun,
			KeepJobIDs:      activeJobIDsFromInfraSubscription(ctx, infraSubscriptionID, cred, logr.FromContextOrDiscard(ctx)),
		},
	}, nil
}

type ficDeletion struct {
	ResourceGroup string
	IdentityName  string
	Credential    string
	Issuer        string
	JobID         string
}

func (o *CleanupOptions) Run(ctx context.Context) error {
	logger := logr.FromContextOrDiscard(ctx)

	logger.Info(
		"starting identity-pool cleanup",
		"subscriptionID", o.SubscriptionID,
		"resourceGroupBaseName", o.IdentityPool.ResourceGroupBaseName,
		"dryRun", o.DryRun,
		"keepJobIDs", keys(o.KeepJobIDs),
	)

	if len(o.KeepJobIDs) == 0 {
		logger.Info("no active job IDs found; cleanup will delete all FICs with issuers not matching any active job")
	}

	resourcesFactory, err := armresources.NewClientFactory(o.SubscriptionID, o.AzureCredential, nil)
	if err != nil {
		return fmt.Errorf("failed creating ARM resources client factory: %w", err)
	}
	rgsClient := resourcesFactory.NewResourceGroupsClient()

	msiFactory, err := armmsi.NewClientFactory(o.SubscriptionID, o.AzureCredential, nil)
	if err != nil {
		return fmt.Errorf("failed creating ARM MSI client factory: %w", err)
	}
	ficsClient := msiFactory.NewFederatedIdentityCredentialsClient()

	// 1) Discover pool resource groups
	rgPager := rgsClient.NewListPager(&armresources.ResourceGroupsClientListOptions{
		Filter: to.Ptr(identityPoolResourceGroupTagFilter),
	})
	rgNames := []string{}
	for rgPager.More() {
		page, err := rgPager.NextPage(ctx)
		if err != nil {
			return fmt.Errorf("failed listing resource groups: %w", err)
		}
		for _, rg := range page.Value {
			if rg == nil || rg.Name == nil || *rg.Name == "" {
				continue
			}
			// Narrow to the current environment's pool RG prefix. This avoids scanning
			// legacy pools that share the same purpose tag.
			if strings.HasPrefix(*rg.Name, o.IdentityPool.ResourceGroupBaseName+"-") {
				rgNames = append(rgNames, *rg.Name)
				logger.V(2).Info("discovered pool resource group", "resourceGroup", *rg.Name)
			}
		}
	}
	logger.Info("discovered identity-pool resource groups", "count", len(rgNames))

	// 2) Build deletion plan
	var plan []ficDeletion
	var identitiesTargeted, identitiesMissing, ficsSeen int
	var ficsMissingIssuer, ficsNoJobID, ficsKept, ficsPlanned int

	// These are the only identities that need federated credentials management in the pool.
	// Listing all identities in every RG is extremely slow and unnecessary.
	targetIdentityNames := []string{
		framework.DpDiskCsiDriverMiName,
		framework.DpFileCsiDriverMiName,
		framework.DpImageRegistryMiName,
	}

	for _, rgName := range rgNames {
		logger.V(1).Info("processing pool resource group", "resourceGroup", rgName)
		for _, identityName := range targetIdentityNames {
			identitiesTargeted++

			logger.V(1).Info("processing identity", "resourceGroup", rgName, "identity", identityName)

			ficsInIdentity := 0
			ficPager := ficsClient.NewListPager(rgName, identityName, nil)
			for ficPager.More() {
				ficPage, err := ficPager.NextPage(ctx)
				if err != nil {
					if isNotFoundParent(err) {
						identitiesMissing++
						logger.V(1).Info("identity not found in pool resource group; skipping", "resourceGroup", rgName, "identity", identityName)
						break
					}
					return fmt.Errorf("failed listing federated identity credentials for %s/%s: %w", rgName, identityName, err)
				}
				for _, fic := range ficPage.Value {
					if fic == nil || fic.Name == nil || *fic.Name == "" {
						continue
					}
					ficName := *fic.Name
					ficsSeen++
					ficsInIdentity++

					issuer, ok := ficIssuer(fic)
					if !ok {
						ficsMissingIssuer++
						logger.V(2).Info("FIC missing issuer; scheduling for deletion", "resourceGroup", rgName, "identity", identityName, "credential", ficName)
						plan = append(plan, ficDeletion{
							ResourceGroup: rgName,
							IdentityName:  identityName,
							Credential:    ficName,
							Issuer:        "",
							JobID:         "",
						})
						ficsPlanned++
						continue
					}

					jobID, hasJobID := jobIDFromIssuer(issuer)
					if hasJobID {
						logger.V(2).Info("parsed job ID from issuer", "resourceGroup", rgName, "identity", identityName, "credential", ficName, "jobID", jobID)
					} else {
						ficsNoJobID++
						logger.V(2).Info("could not parse job ID from issuer; scheduling for deletion", "resourceGroup", rgName, "identity", identityName, "credential", ficName, "issuer", issuer)
					}

					if hasJobID {
						if _, keep := o.KeepJobIDs[jobID]; keep {
							ficsKept++
							logger.V(2).Info("keeping FIC (job ID whitelisted)", "resourceGroup", rgName, "identity", identityName, "credential", ficName, "jobID", jobID)
							continue
						}
					}

					plan = append(plan, ficDeletion{
						ResourceGroup: rgName,
						IdentityName:  identityName,
						Credential:    ficName,
						Issuer:        issuer,
						JobID:         jobID,
					})
					ficsPlanned++
					logger.V(2).Info("scheduled FIC for deletion", "resourceGroup", rgName, "identity", identityName, "credential", ficName, "jobID", jobID)
				}
			}
			logger.V(1).Info("finished identity", "resourceGroup", rgName, "identity", identityName, "ficsListed", ficsInIdentity)
		}
	}

	logger.Info(
		"cleanup plan built",
		"resourceGroups", len(rgNames),
		"identitiesTargeted", identitiesTargeted,
		"identitiesMissing", identitiesMissing,
		"ficsSeen", ficsSeen,
		"ficsMissingIssuer", ficsMissingIssuer,
		"ficsNoJobID", ficsNoJobID,
		"ficsKept", ficsKept,
		"toDelete", len(plan),
	)

	// 3) Dry-run or execute
	if o.DryRun {
		for _, item := range plan {
			logger.V(1).Info(
				"dry-run delete federated identity credential",
				"resourceGroup", item.ResourceGroup,
				"identity", item.IdentityName,
				"credential", item.Credential,
				"jobID", item.JobID,
				"issuer", item.Issuer,
			)
		}
		return nil
	}

	deleted := 0
	for _, item := range plan {
		logger.Info(
			"deleting federated identity credential",
			"resourceGroup", item.ResourceGroup,
			"identity", item.IdentityName,
			"credential", item.Credential,
			"jobID", item.JobID,
			"issuer", item.Issuer,
		)
		_, err := ficsClient.Delete(ctx, item.ResourceGroup, item.IdentityName, item.Credential, nil)
		if err != nil {
			if isNotFoundParent(err) {
				logger.V(1).Info("already deleted or parent missing; skipping", "resourceGroup", item.ResourceGroup, "identity", item.IdentityName, "credential", item.Credential)
				continue
			}
			return fmt.Errorf("failed deleting federated identity credential %s/%s/%s: %w", item.ResourceGroup, item.IdentityName, item.Credential, err)
		}
		deleted++
	}

	logger.Info("cleanup completed", "deleted", deleted, "planned", len(plan))
	return nil
}

func ficIssuer(fic *armmsi.FederatedIdentityCredential) (string, bool) {
	if fic.Properties == nil || fic.Properties.Issuer == nil || strings.TrimSpace(*fic.Properties.Issuer) == "" {
		return "", false
	}
	return strings.TrimSpace(*fic.Properties.Issuer), true
}

func jobIDFromIssuer(issuer string) (string, bool) {
	m := issuerJobIDRegex.FindString(issuer)
	if m == "" {
		return "", false
	}
	return m, true
}

func resolveSubscriptionID(ctx context.Context, cred azcore.TokenCredential, subscriptionNameOrID string) (string, error) {
	subscriptionNameOrID = strings.TrimSpace(subscriptionNameOrID)
	if subscriptionIDRegex.MatchString(subscriptionNameOrID) {
		return subscriptionNameOrID, nil
	}

	clientFactory, err := armsubscriptions.NewClientFactory(cred, nil)
	if err != nil {
		return "", fmt.Errorf("failed creating subscriptions client factory: %w", err)
	}
	return framework.GetSubscriptionID(ctx, clientFactory.NewClient(), subscriptionNameOrID)
}

func activeJobIDsFromInfraSubscription(ctx context.Context, infraSubscriptionID string, cred azcore.TokenCredential, logger logr.Logger) map[string]struct{} {
	keep := map[string]struct{}{}

	clientFactory, err := armresources.NewClientFactory(infraSubscriptionID, cred, nil)
	if err != nil {
		// Keep set empty; higher-level logic will behave as "delete all".
		logger.Error(err, "failed creating resources client factory for infra subscription", "infraSubscriptionID", infraSubscriptionID)
		return keep
	}

	rgsClient := clientFactory.NewResourceGroupsClient()
	pager := rgsClient.NewListPager(nil)
	seen := 0
	matched := 0
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			logger.Error(err, "failed listing infra subscription resource groups", "infraSubscriptionID", infraSubscriptionID)
			return keep
		}
		for _, rg := range page.Value {
			if rg == nil || rg.Name == nil || *rg.Name == "" {
				continue
			}
			seen++
			name := *rg.Name
			// Active prow underlay RGs have names like: hcp-underlay-prow-usw3j384-svc
			if !strings.HasPrefix(name, "hcp-underlay-prow-") || !strings.HasSuffix(name, "-svc") {
				continue
			}
			jobID, ok := jobIDFromIssuer(name)
			if !ok {
				logger.V(2).Info("underlay RG matched pattern but had no job ID", "resourceGroup", name)
				continue
			}
			matched++
			keep[jobID] = struct{}{}
			logger.V(2).Info("discovered active job ID from underlay RG", "resourceGroup", name, "jobID", jobID)
		}
	}

	logger.Info(
		"discovered active job IDs from infra subscription",
		"infraSubscriptionID", infraSubscriptionID,
		"resourceGroupsSeen", seen,
		"underlayGroupsMatched", matched,
		"jobIDs", keys(keep),
	)

	return keep
}

func keys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func isNotFoundParent(err error) bool {
	var respErr *azcore.ResponseError
	if errors.As(err, &respErr) {
		if respErr.StatusCode == 404 {
			return true
		}
		if respErr.ErrorCode == "ParentResourceNotFound" || respErr.ErrorCode == "ResourceNotFound" {
			return true
		}
	}
	return false
}
