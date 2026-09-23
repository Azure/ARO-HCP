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
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/slots"
	"github.com/Azure/ARO-HCP/test/util/framework"
)

func TestCompareIdentityPoolInventory(t *testing.T) {
	t.Parallel()

	pools := []identityPool{{
		SubscriptionName:        "dev-sub",
		IdentityContainerPrefix: "aro-hcp-msi-container-dev",
		Slots: []slots.ExpandedSlot{{
			IdentityContainerPrefix: "aro-hcp-msi-container-dev-00",
			IdentityContainerCount:  2,
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

	result := compareIdentityPoolInventory(pools, actual)
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

func TestValidateOptionsRun(t *testing.T) {
	t.Parallel()

	pool := identityPool{
		SubscriptionName:        "dev-sub",
		SubscriptionID:          "sub-id",
		IdentityContainerPrefix: "aro-hcp-msi-container-dev",
		Slots: []slots.ExpandedSlot{{
			IdentityContainerPrefix: "aro-hcp-msi-container-dev-00",
			IdentityContainerCount:  1,
		}},
	}
	resourceGroup := pool.Slots[0].IdentityContainerNames()[0]

	t.Run("valid", func(t *testing.T) {
		t.Parallel()

		var output bytes.Buffer
		opts := &ValidateOptions{completedValidateOptions: &completedValidateOptions{
			IdentityPools: []identityPool{pool},
			LoadInventory: func(context.Context, string) (subscriptionInventory, error) {
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
			},
			Out: &output,
		}}

		if err := opts.Run(context.Background()); err != nil {
			t.Fatalf("expected validation to succeed: %v", err)
		}
		if !strings.Contains(output.String(), "result: valid") {
			t.Fatalf("expected valid result output, got %q", output.String())
		}
	})

	t.Run("drift", func(t *testing.T) {
		t.Parallel()

		var output bytes.Buffer
		opts := &ValidateOptions{completedValidateOptions: &completedValidateOptions{
			IdentityPools: []identityPool{pool},
			LoadInventory: func(context.Context, string) (subscriptionInventory, error) {
				return subscriptionInventory{}, nil
			},
			Out: &output,
		}}

		err := opts.Run(context.Background())
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
}
