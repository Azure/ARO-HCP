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

package e2eidentities

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"

	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/msi/armmsi"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"

	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/assets/azureutil"
	"github.com/Azure/ARO-HCP/test/util/framework"
)

type subscriptionInventory struct {
	ResourceGroups []string
	Identities     map[string][]string
}

type inventoryLoaderFunc func(ctx context.Context, subscriptionID string) (subscriptionInventory, error)

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

func validatePools(ctx context.Context, pools []identityPool, loadInventory inventoryLoaderFunc, out io.Writer) error {
	poolsBySubscription := map[string][]identityPool{}
	subscriptionOrder := make([]string, 0)
	for _, pool := range pools {
		if _, found := poolsBySubscription[pool.SubscriptionID]; !found {
			subscriptionOrder = append(subscriptionOrder, pool.SubscriptionID)
		}
		poolsBySubscription[pool.SubscriptionID] = append(poolsBySubscription[pool.SubscriptionID], pool)
	}
	failedSubscriptions := 0
	for _, subscriptionID := range subscriptionOrder {
		grouped := poolsBySubscription[subscriptionID]
		inventory, err := loadInventory(ctx, subscriptionID)
		if err != nil {
			return fmt.Errorf("failed loading Azure identity-pool inventory for subscription %q: %w", grouped[0].SubscriptionName, err)
		}
		result, err := compareIdentityPoolInventory(grouped, inventory)
		if err != nil {
			return fmt.Errorf("failed validating Azure identity-pool inventory for subscription %q: %w", grouped[0].SubscriptionName, err)
		}
		writeValidationResult(out, result)
		if result.valid() {
			continue
		}
		failedSubscriptions++
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

func compareIdentityPoolInventory(pools []identityPool, actual subscriptionInventory) (validationResult, error) {
	expectedResourceGroups := map[string]string{}
	prefixes := sets.New[string]()
	for _, pool := range pools {
		prefixes.Insert(strings.ToLower(pool.IdentityContainerPrefix) + "-")
		for _, slot := range pool.Slots {
			for _, resourceGroup := range slot.ProvisionedIdentityContainerNames() {
				expectedResourceGroups[strings.ToLower(resourceGroup)] = resourceGroup
			}
		}
	}
	actualResourceGroups := map[string]string{}
	identitiesByGroup := map[string][]string{}
	for resourceGroup, names := range actual.Identities {
		normalized := strings.ToLower(resourceGroup)
		identitiesByGroup[normalized] = append(identitiesByGroup[normalized], names...)
	}
	for _, resourceGroup := range actual.ResourceGroups {
		if hasAnyPrefix(resourceGroup, prefixes) {
			actualResourceGroups[strings.ToLower(resourceGroup)] = resourceGroup
		}
	}
	expectedIdentityNames := framework.NewDefaultIdentities().ToSlice()
	missingIdentities := make([]identityReference, 0)
	unexpectedIdentities := make([]identityReference, 0)
	actualIdentities := 0
	for normalized, resourceGroup := range expectedResourceGroups {
		actualGroup, exists := actualResourceGroups[normalized]
		if !exists {
			continue
		}
		reported, err := azureutil.UniqueReportedNames(resourceGroup, identitiesByGroup[normalized])
		if err != nil {
			return validationResult{}, err
		}
		actualIdentities += len(reported)
		missing, unexpected := azureutil.IdentityNameDiff(azureutil.NormalizedNameSet(expectedIdentityNames), reported)
		for _, name := range missing {
			missingIdentities = append(missingIdentities, identityReference{ResourceGroup: resourceGroup, Name: name})
		}
		for _, name := range unexpected {
			unexpectedIdentities = append(unexpectedIdentities, identityReference{ResourceGroup: actualGroup, Name: name})
		}
	}
	for normalized, resourceGroup := range actualResourceGroups {
		if _, expected := expectedResourceGroups[normalized]; expected {
			continue
		}
		for _, name := range identitiesByGroup[normalized] {
			unexpectedIdentities = append(unexpectedIdentities, identityReference{ResourceGroup: resourceGroup, Name: name})
			actualIdentities++
		}
	}
	sortIdentityReferences(missingIdentities)
	sortIdentityReferences(unexpectedIdentities)
	return validationResult{
		SubscriptionName:         pools[0].SubscriptionName,
		ExpectedResourceGroups:   len(expectedResourceGroups),
		ActualResourceGroups:     len(actualResourceGroups),
		ExpectedIdentities:       len(expectedResourceGroups) * len(expectedIdentityNames),
		ActualIdentities:         actualIdentities,
		MissingResourceGroups:    differenceNamedStrings(expectedResourceGroups, actualResourceGroups),
		UnexpectedResourceGroups: differenceNamedStrings(actualResourceGroups, expectedResourceGroups),
		MissingIdentities:        missingIdentities,
		UnexpectedIdentities:     unexpectedIdentities,
	}, nil
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
		return
	}
	fmt.Fprintln(out, "  result: drift detected")
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

func hasAnyPrefix(value string, prefixes sets.Set[string]) bool {
	value = strings.ToLower(value)
	for _, prefix := range sets.List(prefixes) {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}

func differenceNamedStrings(left, right map[string]string) []string {
	result := make([]string, 0)
	for normalized, value := range left {
		if _, found := right[normalized]; found {
			continue
		}
		result = append(result, value)
	}
	sort.Strings(result)
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
