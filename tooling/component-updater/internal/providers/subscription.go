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

package providers

import (
	"context"
	"fmt"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armsubscriptions"
)

// ResolveSubscriptionID returns the sole Enabled subscription visible to the
// credential. Callers that already know the discovery subscription should pass
// it explicitly instead. Zero or multiple Enabled subscriptions fail closed so
// CI does not need a hard-coded ID when the SP is scoped to one sub, and local
// multi-sub tokens are forced to choose.
func ResolveSubscriptionID(ctx context.Context, credential azcore.TokenCredential) (string, error) {
	client, err := armsubscriptions.NewClient(credential, azureDiscoveryOptions())
	if err != nil {
		return "", fmt.Errorf("creating subscription list client: %w", err)
	}

	var enabled []string
	pager := client.NewListPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return "", fmt.Errorf("listing subscriptions for the credential: %w", err)
		}
		for _, sub := range page.Value {
			if sub == nil || sub.SubscriptionID == nil || sub.State == nil {
				continue
			}
			if *sub.State != armsubscriptions.SubscriptionStateEnabled {
				continue
			}
			id := strings.TrimSpace(*sub.SubscriptionID)
			if id == "" {
				continue
			}
			enabled = append(enabled, id)
		}
	}
	return pickSoleEnabledSubscription(enabled)
}

func pickSoleEnabledSubscription(enabled []string) (string, error) {
	switch len(enabled) {
	case 0:
		return "", fmt.Errorf("credential has no Enabled Azure subscriptions; set --subscription-id or AZURE_SUBSCRIPTION_ID")
	case 1:
		return enabled[0], nil
	default:
		return "", fmt.Errorf("credential can access %d Enabled Azure subscriptions; set --subscription-id or AZURE_SUBSCRIPTION_ID to choose one", len(enabled))
	}
}
