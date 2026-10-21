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
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/slots"
	"github.com/Azure/ARO-HCP/test/util/framework"
)

func TestCompareIdentityPoolInventoryProvisionedReserve(t *testing.T) {
	t.Parallel()
	pool := slots.Pool{
		Name: "int-westus3", SlotCount: 1, ResourceType: "int-slot",
		IdentityContainerProvisioningCount: 100,
		SlotAssets: slots.SlotAssets{E2EIdentities: &slots.E2EIdentitiesAsset{
			ResourceGroupPrefix: "aro-hcp-msi-container-int", ResourceGroupCount: 70,
		}},
	}
	expanded := slots.ExpandSlotsForPool("int", pool)
	groups := expanded[0].ProvisionedIdentityContainerNames()
	names := framework.NewDefaultIdentities().ToSlice()
	if len(names) != 13 {
		t.Fatalf("expected 13 managed identities per group, got %d", len(names))
	}
	actual := subscriptionInventory{ResourceGroups: groups, Identities: map[string][]string{}}
	for _, group := range groups {
		actual.Identities[group] = names
	}
	pools := []identityPool{{
		SubscriptionName: "int-sub", IdentityContainerPrefix: "aro-hcp-msi-container-int", Slots: expanded,
	}}
	result, err := compareIdentityPoolInventory(pools, actual)
	if err != nil {
		t.Fatal(err)
	}
	if !result.valid() || result.ExpectedResourceGroups != 100 || result.ActualResourceGroups != 100 ||
		result.ExpectedIdentities != 1300 || result.ActualIdentities != 1300 {
		t.Fatalf("physical reserve incorrectly classified as drift: %+v", result)
	}
	actual.ResourceGroups = groups[:70]
	result, err = compareIdentityPoolInventory(pools, actual)
	if err != nil {
		t.Fatal(err)
	}
	if result.valid() || len(result.MissingResourceGroups) != 30 {
		t.Fatalf("missing physical reserve must be drift: %+v", result)
	}
	if len(expanded[0].IdentityContainerNames()) != 70 {
		t.Fatal("inventory validation changed runtime lease budget")
	}
}

func TestCompareIdentityPoolInventory(t *testing.T) {
	t.Parallel()

	pools := []identityPool{{
		SubscriptionName:        "dev-sub",
		IdentityContainerPrefix: "aro-hcp-msi-container-dev",
		Slots: []slots.ExpandedSlot{{
			IdentityContainerPrefix: "aro-hcp-msi-container-dev-00",
			IdentityContainerCount:  2,
			Assets: slots.ResolvedAssets{E2EIdentities: &slots.ResolvedE2EIdentitiesAsset{
				ResourceGroups: []string{"aro-hcp-msi-container-dev-00-00", "aro-hcp-msi-container-dev-00-01"},
			}},
		}},
	}}

	identityNames := framework.NewDefaultIdentities().ToSlice()
	actualIdentities := append([]string{}, identityNames[:len(identityNames)-1]...)
	actualIdentities = append(actualIdentities, "unexpected")
	actual := subscriptionInventory{
		ResourceGroups: []string{
			"aro-hcp-msi-container-dev-00-00",
			"aro-hcp-msi-container-dev-01-00",
			"unrelated",
		},
		Identities: map[string][]string{
			"aro-hcp-msi-container-dev-00-00": actualIdentities,
			"aro-hcp-msi-container-dev-01-00": {
				framework.ClusterApiAzureMiName,
			},
		},
	}

	result, err := compareIdentityPoolInventory(pools, actual)
	if err != nil {
		t.Fatal(err)
	}
	if result.valid() {
		t.Fatal("expected drift to be detected")
	}
	if result.ExpectedResourceGroups != 2 || result.ActualResourceGroups != 2 {
		t.Fatalf("unexpected resource group counts: %+v", result)
	}
	if result.ExpectedIdentities != len(identityNames)*2 || result.ActualIdentities != len(actualIdentities)+1 {
		t.Fatalf("unexpected identity counts: %+v", result)
	}
	if len(result.MissingResourceGroups) != 1 || result.MissingResourceGroups[0] != "aro-hcp-msi-container-dev-00-01" {
		t.Fatalf("unexpected missing resource groups: %v", result.MissingResourceGroups)
	}
	if len(result.UnexpectedResourceGroups) != 1 || result.UnexpectedResourceGroups[0] != "aro-hcp-msi-container-dev-01-00" {
		t.Fatalf("unexpected resource groups: %v", result.UnexpectedResourceGroups)
	}
	if len(result.MissingIdentities) != 1 || result.MissingIdentities[0].Name != framework.ServiceManagedIdentityName {
		t.Fatalf("unexpected missing identities: %v", result.MissingIdentities)
	}
	if len(result.UnexpectedIdentities) != 2 ||
		result.UnexpectedIdentities[0] != (identityReference{
			ResourceGroup: "aro-hcp-msi-container-dev-00-00",
			Name:          "unexpected",
		}) ||
		result.UnexpectedIdentities[1] != (identityReference{
			ResourceGroup: "aro-hcp-msi-container-dev-01-00",
			Name:          framework.ClusterApiAzureMiName,
		}) {
		t.Fatalf("unexpected identities: %v", result.UnexpectedIdentities)
	}
}

func TestCompareIdentityPoolInventoryMergesCaseVariantResourceGroups(t *testing.T) {
	t.Parallel()
	pool := identityPool{
		SubscriptionName:        "dev-sub",
		IdentityContainerPrefix: "aro-hcp-msi-container-dev",
		Slots: []slots.ExpandedSlot{{
			IdentityContainerPrefix: "aro-hcp-msi-container-dev-00",
			IdentityContainerCount:  1,
			Assets: slots.ResolvedAssets{E2EIdentities: &slots.ResolvedE2EIdentitiesAsset{
				ResourceGroups: []string{"aro-hcp-msi-container-dev-00-00"},
			}},
		}},
	}
	resourceGroup := pool.Slots[0].IdentityContainerNames()[0]
	identityNames := framework.NewDefaultIdentities().ToSlice()
	half := len(identityNames) / 2
	actual := subscriptionInventory{
		ResourceGroups: []string{resourceGroup},
		Identities: map[string][]string{
			resourceGroup:                  identityNames[:half],
			strings.ToUpper(resourceGroup): identityNames[half:],
		},
	}

	result, err := compareIdentityPoolInventory([]identityPool{pool}, actual)
	if err != nil {
		t.Fatal(err)
	}
	if !result.valid() || result.ActualIdentities != len(identityNames) {
		t.Fatalf("case-variant inventory entries were not merged: %+v", result)
	}
}

func TestValidatePools(t *testing.T) {
	t.Parallel()

	pool := identityPool{
		SubscriptionName:        "dev-sub",
		SubscriptionID:          "sub-id",
		IdentityContainerPrefix: "aro-hcp-msi-container-dev",
		Slots: []slots.ExpandedSlot{{
			IdentityContainerPrefix: "aro-hcp-msi-container-dev-00",
			IdentityContainerCount:  1,
			Assets: slots.ResolvedAssets{E2EIdentities: &slots.ResolvedE2EIdentitiesAsset{
				ResourceGroups: []string{"aro-hcp-msi-container-dev-00-00"},
			}},
		}},
	}
	resourceGroup := pool.Slots[0].IdentityContainerNames()[0]

	t.Run("valid", func(t *testing.T) {
		t.Parallel()

		var output bytes.Buffer
		if err := validatePools(context.Background(), []identityPool{pool}, func(context.Context, string) (subscriptionInventory, error) {
			identityNames := framework.NewDefaultIdentities().ToSlice()
			for i := range identityNames {
				identityNames[i] = strings.ToUpper(identityNames[i])
			}
			return subscriptionInventory{
				ResourceGroups: []string{strings.ToUpper(resourceGroup)},
				Identities: map[string][]string{
					strings.ToUpper(resourceGroup): identityNames,
				},
			}, nil
		}, &output); err != nil {
			t.Fatalf("expected validation to succeed: %v", err)
		}
		if !strings.Contains(output.String(), "result: valid") {
			t.Fatalf("expected valid result output, got %q", output.String())
		}
	})

	t.Run("drift", func(t *testing.T) {
		t.Parallel()

		var output bytes.Buffer
		err := validatePools(context.Background(), []identityPool{pool}, func(context.Context, string) (subscriptionInventory, error) {
			return subscriptionInventory{}, nil
		}, &output)
		if err == nil {
			t.Fatal("expected validation to fail")
		}
		if !strings.Contains(err.Error(), "identity pool validation failed") {
			t.Fatalf("unexpected validation error: %v", err)
		}
		if !strings.Contains(output.String(), resourceGroup) {
			t.Fatalf("expected missing resource group in output, got %q", output.String())
		}
	})
	t.Run("duplicate identity names with drift", func(t *testing.T) {
		t.Parallel()

		identityNames := framework.NewDefaultIdentities().ToSlice()
		duplicateNames := append([]string{}, identityNames[:len(identityNames)-1]...)
		duplicateNames = append(duplicateNames, identityNames[0])
		err := validatePools(context.Background(), []identityPool{pool}, func(context.Context, string) (subscriptionInventory, error) {
			return subscriptionInventory{
				ResourceGroups: []string{resourceGroup},
				Identities:     map[string][]string{resourceGroup: duplicateNames},
			}, nil
		}, &bytes.Buffer{})
		if err == nil || !strings.Contains(err.Error(), "duplicate identity") {
			t.Fatalf("expected duplicate identity error to fail validation, got %v", err)
		}
	})
}
