// Copyright 2026 Microsoft Corporation
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package cost

import (
	"context"
	"strings"
	"sync"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
)

// SubscriptionCredential is a credential that may hold a different identity
// per subscription. Collection uses the subscription's identity for its
// billing reports, and the default identity for everything else.
type SubscriptionCredential interface {
	azcore.TokenCredential
	ForSubscription(subscriptionID string) azcore.TokenCredential
}

// CLICredential uses the Azure CLI's default account, and for each
// subscription the CLI account that has access to it. Several service
// principals logged into one CLI profile (az login --service-principal, once
// per identity) can therefore cover subscriptions in different tenants.
type CLICredential struct {
	defaultCredential azcore.TokenCredential
	mu                sync.Mutex
	bySubscription    map[string]azcore.TokenCredential
}

func NewCLICredential() (*CLICredential, error) {
	credential, err := azidentity.NewAzureCLICredential(nil)
	if err != nil {
		return nil, err
	}
	return &CLICredential{defaultCredential: credential, bySubscription: map[string]azcore.TokenCredential{}}, nil
}

func (c *CLICredential) GetToken(ctx context.Context, options policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return c.defaultCredential.GetToken(ctx, options)
}

func (c *CLICredential) ForSubscription(subscriptionID string) azcore.TokenCredential {
	subscriptionID = strings.ToLower(subscriptionID)
	c.mu.Lock()
	defer c.mu.Unlock()
	if credential, ok := c.bySubscription[subscriptionID]; ok {
		return credential
	}
	credential, err := azidentity.NewAzureCLICredential(&azidentity.AzureCLICredentialOptions{Subscription: subscriptionID})
	if err != nil {
		// Only an invalid subscription ID fails here; callers pass UUIDs.
		return c.defaultCredential
	}
	c.bySubscription[subscriptionID] = credential
	return credential
}

func credentialFor(credential azcore.TokenCredential, subscriptionID string) azcore.TokenCredential {
	if s, ok := credential.(SubscriptionCredential); ok {
		return s.ForSubscription(subscriptionID)
	}
	return credential
}
