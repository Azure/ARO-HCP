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

package subscriptions

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armsubscriptions"
)

// Lister abstracts the subscription listing API for testability.
type Lister interface {
	List(ctx context.Context) ([]*armsubscriptions.Subscription, error)
}

type azureLister struct {
	client *armsubscriptions.Client
}

func (l *azureLister) List(ctx context.Context) ([]*armsubscriptions.Subscription, error) {
	var subs []*armsubscriptions.Subscription
	pager := l.client.NewListPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("list subscriptions: %w", err)
		}
		subs = append(subs, page.Value...)
	}
	return subs, nil
}

func newAzureLister(cred azcore.TokenCredential) (Lister, error) {
	client, err := armsubscriptions.NewClient(cred, nil)
	if err != nil {
		return nil, fmt.Errorf("create subscriptions client: %w", err)
	}
	return &azureLister{client: client}, nil
}

// buildNameMap converts a subscription list into a display-name-to-ID map,
// returning an error if two subscriptions share the same display name.
func buildNameMap(subs []*armsubscriptions.Subscription) (map[string]string, error) {
	nameToID := make(map[string]string)
	for _, sub := range subs {
		if sub.DisplayName != nil && sub.SubscriptionID != nil {
			if existing, dup := nameToID[*sub.DisplayName]; dup {
				return nil, fmt.Errorf("ambiguous subscription name %q: matches both %s and %s",
					*sub.DisplayName, existing, *sub.SubscriptionID)
			}
			nameToID[*sub.DisplayName] = *sub.SubscriptionID
		}
	}
	return nameToID, nil
}

// resolveNames looks up each requested name in the map and returns an error
// if any name is missing.
func resolveNames(nameToID map[string]string, names []string) (map[string]string, error) {
	result := make(map[string]string, len(names))
	for _, name := range names {
		id, ok := nameToID[name]
		if !ok {
			available := make([]string, 0, len(nameToID))
			for n := range nameToID {
				available = append(available, n)
			}
			return nil, fmt.Errorf("subscription %q not found; credential can see %d subscriptions: %v",
				name, len(available), available)
		}
		result[name] = id
	}
	return result, nil
}

// List lists all subscriptions visible to the credential and returns a
// map of display name to subscription ID. Returns an error if two
// subscriptions share the same display name.
func List(ctx context.Context, cred azcore.TokenCredential) (map[string]string, error) {
	lister, err := newAzureLister(cred)
	if err != nil {
		return nil, err
	}
	subs, err := lister.List(ctx)
	if err != nil {
		return nil, err
	}
	return buildNameMap(subs)
}

// ResolveByName resolves subscription display names to subscription IDs.
// Returns a map of name to ID for each requested name. Returns an error
// if any requested name is not found among the visible subscriptions.
func ResolveByName(ctx context.Context, cred azcore.TokenCredential, names []string) (map[string]string, error) {
	nameToID, err := List(ctx, cred)
	if err != nil {
		return nil, err
	}
	return resolveNames(nameToID, names)
}
