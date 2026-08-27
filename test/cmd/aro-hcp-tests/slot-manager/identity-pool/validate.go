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
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/msi/armmsi"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"

	"github.com/Azure/ARO-HCP/test/util/framework"
)

type RawValidateOptions struct {
	Environment   string
	SlotCatalog   string
	Subscriptions []string
	Out           io.Writer
}

type validatedValidateOptions struct {
	*RawValidateOptions
}

type ValidatedValidateOptions struct {
	*validatedValidateOptions
}

type subscriptionInventory struct {
	ResourceGroups []string
	Identities     map[string][]string
}

type inventoryLoaderFunc func(ctx context.Context, subscriptionID string) (subscriptionInventory, error)

type completedValidateOptions struct {
	IdentityPools []identityPool
	LoadInventory inventoryLoaderFunc
	Out           io.Writer
}

type ValidateOptions struct {
	*completedValidateOptions
}

type identityReference struct {
	ResourceGroup string
	Name          string
}

type validationResult struct {
	SubscriptionName         string
	ExpectedResourceGroups   int
	ActualResourceGroups     int
	ExpectedIdentities       int
	ActualIdentities         int
	MissingResourceGroups    []string
	UnexpectedResourceGroups []string
	MissingIdentities        []identityReference
	UnexpectedIdentities     []identityReference
}

func DefaultValidateOptions() *RawValidateOptions {
	return &RawValidateOptions{}
}

func BindValidateOptions(opts *RawValidateOptions, cmd *cobra.Command) error {
	cmd.Flags().StringVar(&opts.Environment, "environment", opts.Environment, "Environment short name. One of: int, stg, dev, prod")
	cmd.Flags().StringVar(&opts.SlotCatalog, "slot-catalog", opts.SlotCatalog, "Path to the canonical E2E slot catalog")
	cmd.Flags().StringSliceVar(&opts.Subscriptions, "subscription", opts.Subscriptions, "Limit validation to the named subscription(s). When set, unmanaged pools matching the filter are included.")
	if err := cmd.MarkFlagRequired("environment"); err != nil {
		return fmt.Errorf("failed to mark flag %q as required: %w", "environment", err)
	}
	return nil
}

func (o *RawValidateOptions) Validate() (*ValidatedValidateOptions, error) {
	if o.Environment == "" {
		return nil, fmt.Errorf("--environment must not be empty")
	}
	if o.Out == nil {
		o.Out = io.Discard
	}

	return &ValidatedValidateOptions{
		validatedValidateOptions: &validatedValidateOptions{
			RawValidateOptions: o,
		},
	}, nil
}

func (o *ValidatedValidateOptions) Complete(ctx context.Context) (*ValidateOptions, error) {
	tc := framework.NewTestContext()
	cred, err := tc.AzureCredential()
	if err != nil {
		return nil, fmt.Errorf("failed getting Azure credential: %w", err)
	}

	subscriptionClientFactory, err := tc.GetARMSubscriptionsClientFactory()
	if err != nil {
		return nil, fmt.Errorf("failed getting ARM subscriptions client factory: %w", err)
	}
	subscriptionClient := subscriptionClientFactory.NewClient()

	pools, err := loadIdentityPools(ctx, o.SlotCatalog, o.Environment, o.Subscriptions, func(ctx context.Context, name string) (string, error) {
		return framework.GetSubscriptionID(ctx, subscriptionClient, name)
	})
	if err != nil {
		return nil, fmt.Errorf("failed loading identity pools from slot catalog: %w", err)
	}
	if len(pools) == 0 {
		return nil, fmt.Errorf("no identity pools matched environment %q and the requested subscription filter", o.Environment)
	}

	return &ValidateOptions{
		completedValidateOptions: &completedValidateOptions{
			IdentityPools: pools,
			LoadInventory: func(ctx context.Context, subscriptionID string) (subscriptionInventory, error) {
				return loadSubscriptionInventory(ctx, subscriptionID, cred)
			},
			Out: o.Out,
		},
	}, nil
}

func (o *ValidateOptions) Run(ctx context.Context) error {
	poolsBySubscription := map[string][]identityPool{}
	subscriptionOrder := make([]string, 0)
	for _, pool := range o.IdentityPools {
		if _, found := poolsBySubscription[pool.SubscriptionID]; !found {
			subscriptionOrder = append(subscriptionOrder, pool.SubscriptionID)
		}
		poolsBySubscription[pool.SubscriptionID] = append(poolsBySubscription[pool.SubscriptionID], pool)
	}

	failedSubscriptions := 0
	for _, subscriptionID := range subscriptionOrder {
		pools := poolsBySubscription[subscriptionID]
		inventory, err := o.LoadInventory(ctx, subscriptionID)
		if err != nil {
			return fmt.Errorf("failed loading Azure identity-pool inventory for subscription %q: %w", pools[0].SubscriptionName, err)
		}

		result := compareIdentityPoolInventory(pools, inventory)
		writeValidationResult(o.Out, result)
		if !result.valid() {
			failedSubscriptions++
		}
	}

	if failedSubscriptions > 0 {
		return fmt.Errorf("identity pool validation failed for %d subscription(s)", failedSubscriptions)
	}
	return nil
}

func loadSubscriptionInventory(ctx context.Context, subscriptionID string, cred azcore.TokenCredential) (subscriptionInventory, error) {
	resourcesFactory, err := armresources.NewClientFactory(subscriptionID, cred, nil)
	if err != nil {
		return subscriptionInventory{}, fmt.Errorf("failed creating resources client factory: %w", err)
	}

	resourceGroups := make([]string, 0)
	resourceGroupsPager := resourcesFactory.NewResourceGroupsClient().NewListPager(nil)
	for resourceGroupsPager.More() {
		page, err := resourceGroupsPager.NextPage(ctx)
		if err != nil {
			return subscriptionInventory{}, fmt.Errorf("failed listing resource groups: %w", err)
		}
		for _, resourceGroup := range page.Value {
			if resourceGroup.Name == nil {
				return subscriptionInventory{}, fmt.Errorf("resource group list returned an entry without a name")
			}
			resourceGroups = append(resourceGroups, *resourceGroup.Name)
		}
	}

	msiFactory, err := armmsi.NewClientFactory(subscriptionID, cred, nil)
	if err != nil {
		return subscriptionInventory{}, fmt.Errorf("failed creating managed identity client factory: %w", err)
	}

	identities := map[string][]string{}
	identitiesPager := msiFactory.NewUserAssignedIdentitiesClient().NewListBySubscriptionPager(nil)
	for identitiesPager.More() {
		page, err := identitiesPager.NextPage(ctx)
		if err != nil {
			return subscriptionInventory{}, fmt.Errorf("failed listing user-assigned identities: %w", err)
		}
		for _, identity := range page.Value {
			if identity.ID == nil || identity.Name == nil {
				return subscriptionInventory{}, fmt.Errorf("managed identity list returned an entry without an ID or name")
			}
			resourceID, err := azcorearm.ParseResourceID(*identity.ID)
			if err != nil {
				return subscriptionInventory{}, fmt.Errorf("failed parsing managed identity resource ID %q: %w", *identity.ID, err)
			}
			identities[resourceID.ResourceGroupName] = append(identities[resourceID.ResourceGroupName], *identity.Name)
		}
	}

	return subscriptionInventory{
		ResourceGroups: resourceGroups,
		Identities:     identities,
	}, nil
}

func compareIdentityPoolInventory(pools []identityPool, actual subscriptionInventory) validationResult {
	expectedResourceGroups := map[string]string{}
	managedPrefixes := make([]string, 0)
	for _, pool := range pools {
		managedPrefixes = append(managedPrefixes, normalizeName(pool.IdentityContainerPrefix)+"-")
		for _, slot := range pool.Slots {
			for _, resourceGroup := range slot.IdentityContainerNames() {
				expectedResourceGroups[normalizeName(resourceGroup)] = resourceGroup
			}
		}
	}

	actualResourceGroups := map[string]string{}
	for _, resourceGroup := range actual.ResourceGroups {
		if hasAnyPrefix(resourceGroup, managedPrefixes) {
			actualResourceGroups[normalizeName(resourceGroup)] = resourceGroup
		}
	}

	expectedIdentityNames := framework.NewDefaultIdentities().ToSlice()
	expectedIdentities := map[identityReference]identityReference{}
	actualIdentities := map[identityReference]identityReference{}
	actualIdentitiesByResourceGroup := map[string][]string{}
	for resourceGroup, identityNames := range actual.Identities {
		normalizedResourceGroup := normalizeName(resourceGroup)
		actualIdentitiesByResourceGroup[normalizedResourceGroup] = append(actualIdentitiesByResourceGroup[normalizedResourceGroup], identityNames...)
	}
	for _, resourceGroup := range expectedResourceGroups {
		for _, identityName := range expectedIdentityNames {
			reference := identityReference{ResourceGroup: resourceGroup, Name: identityName}
			expectedIdentities[normalizeIdentityReference(reference)] = reference
		}
	}
	for normalizedResourceGroup, resourceGroup := range actualResourceGroups {
		for _, identityName := range actualIdentitiesByResourceGroup[normalizedResourceGroup] {
			reference := identityReference{ResourceGroup: resourceGroup, Name: identityName}
			actualIdentities[normalizeIdentityReference(reference)] = reference
		}
	}

	return validationResult{
		SubscriptionName:         pools[0].SubscriptionName,
		ExpectedResourceGroups:   len(expectedResourceGroups),
		ActualResourceGroups:     len(actualResourceGroups),
		ExpectedIdentities:       len(expectedIdentities),
		ActualIdentities:         len(actualIdentities),
		MissingResourceGroups:    differenceNamedStrings(expectedResourceGroups, actualResourceGroups),
		UnexpectedResourceGroups: differenceNamedStrings(actualResourceGroups, expectedResourceGroups),
		MissingIdentities:        differenceIdentitiesForExistingGroups(expectedIdentities, actualIdentities, actualResourceGroups),
		UnexpectedIdentities:     differenceIdentities(actualIdentities, expectedIdentities),
	}
}

func (r validationResult) valid() bool {
	return len(r.MissingResourceGroups) == 0 &&
		len(r.UnexpectedResourceGroups) == 0 &&
		len(r.MissingIdentities) == 0 &&
		len(r.UnexpectedIdentities) == 0
}

func writeValidationResult(out io.Writer, result validationResult) {
	fmt.Fprintf(out, "subscription %q:\n", result.SubscriptionName)
	fmt.Fprintf(
		out,
		"  resource groups: expected=%d actual=%d missing=%d unexpected=%d\n",
		result.ExpectedResourceGroups,
		result.ActualResourceGroups,
		len(result.MissingResourceGroups),
		len(result.UnexpectedResourceGroups),
	)
	fmt.Fprintf(
		out,
		"  managed identities: expected=%d actual=%d missing_in_existing_groups=%d unexpected=%d\n",
		result.ExpectedIdentities,
		result.ActualIdentities,
		len(result.MissingIdentities),
		len(result.UnexpectedIdentities),
	)
	writeStringList(out, "missing resource groups", result.MissingResourceGroups)
	writeStringList(out, "unexpected resource groups", result.UnexpectedResourceGroups)
	writeIdentityList(out, "missing identities in existing resource groups", result.MissingIdentities)
	writeIdentityList(out, "unexpected identities", result.UnexpectedIdentities)
	if result.valid() {
		fmt.Fprintln(out, "  result: valid")
	} else {
		fmt.Fprintln(out, "  result: drift detected")
	}
}

func writeStringList(out io.Writer, title string, values []string) {
	if len(values) == 0 {
		return
	}
	fmt.Fprintf(out, "  %s:\n", title)
	for _, value := range values {
		fmt.Fprintf(out, "    - %s\n", value)
	}
}

func writeIdentityList(out io.Writer, title string, values []identityReference) {
	if len(values) == 0 {
		return
	}
	fmt.Fprintf(out, "  %s:\n", title)
	for _, value := range values {
		fmt.Fprintf(out, "    - %s/%s\n", value.ResourceGroup, value.Name)
	}
}

func hasAnyPrefix(value string, prefixes []string) bool {
	value = normalizeName(value)
	for _, prefix := range prefixes {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}

func normalizeName(value string) string {
	return strings.ToLower(value)
}

func normalizeIdentityReference(value identityReference) identityReference {
	return identityReference{
		ResourceGroup: normalizeName(value.ResourceGroup),
		Name:          normalizeName(value.Name),
	}
}

func differenceNamedStrings(left, right map[string]string) []string {
	result := make([]string, 0)
	for normalized, value := range left {
		if _, found := right[normalized]; !found {
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return result
}

func differenceIdentities(left, right map[identityReference]identityReference) []identityReference {
	result := make([]identityReference, 0)
	for normalized, value := range left {
		if _, found := right[normalized]; !found {
			result = append(result, value)
		}
	}
	sortIdentityReferences(result)
	return result
}

func differenceIdentitiesForExistingGroups(left, right map[identityReference]identityReference, existingGroups map[string]string) []identityReference {
	result := make([]identityReference, 0)
	for normalized, value := range left {
		if _, found := existingGroups[normalized.ResourceGroup]; !found {
			continue
		}
		if _, found := right[normalized]; !found {
			result = append(result, value)
		}
	}
	sortIdentityReferences(result)
	return result
}

func sortIdentityReferences(values []identityReference) {
	sort.Slice(values, func(i, j int) bool {
		if values[i].ResourceGroup == values[j].ResourceGroup {
			return values[i].Name < values[j].Name
		}
		return values[i].ResourceGroup < values[j].ResourceGroup
	})
}
