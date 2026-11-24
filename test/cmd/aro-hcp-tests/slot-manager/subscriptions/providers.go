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

// Package subscriptions holds the subscription-level preparation that E2E
// subscriptions need before slot assets can be provisioned: resource provider
// registration and the subscription-scoped RBAC deployments. It is deliberately
// independent of the slot catalog. Every function takes explicit subscription IDs
// and plain parameters, so the same code can serve the externally owned
// subscription command and, later, the dev-ci privileged rollout.
package subscriptions

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
)

const (
	providerRegisteredState = "Registered"
	providerPollFrequency   = 10 * time.Second
	providerPollDuration    = 10 * time.Minute
)

// RegisterProviders registers and waits for each provider, reporting all failures together.
func RegisterProviders(ctx context.Context, credential azcore.TokenCredential, options *azcorearm.ClientOptions, subscriptionIDs, namespaces []string) error {
	if len(namespaces) == 0 {
		return errors.New("no resource provider namespaces to register")
	}
	var failures []error
	for _, subscriptionID := range subscriptionIDs {
		client, err := armresources.NewProvidersClient(subscriptionID, credential, options)
		if err != nil {
			failures = append(failures, fmt.Errorf("subscription %s: %w", subscriptionID, err))
			continue
		}
		for _, namespace := range namespaces {
			if err := registerProvider(ctx, client, namespace); err != nil {
				failures = append(failures, fmt.Errorf("subscription %s: register %s: %w", subscriptionID, namespace, err))
			}
		}
	}
	if err := errors.Join(failures...); err != nil {
		return fmt.Errorf("provider registration failed: %w", err)
	}
	return nil
}

func registerProvider(ctx context.Context, client *armresources.ProvidersClient, namespace string) error {
	current, err := client.Get(ctx, namespace, nil)
	if err != nil {
		return err
	}
	if current.RegistrationState != nil && *current.RegistrationState == providerRegisteredState {
		return nil
	}
	registered, err := client.Register(ctx, namespace, nil)
	if err != nil {
		return err
	}
	if registered.RegistrationState != nil && *registered.RegistrationState == providerRegisteredState {
		return nil
	}
	return wait.PollUntilContextTimeout(ctx, providerPollFrequency, providerPollDuration, false, func(ctx context.Context) (bool, error) {
		current, err := client.Get(ctx, namespace, nil)
		return current.RegistrationState != nil && *current.RegistrationState == providerRegisteredState, err
	})
}

// ParseProviderNamespaces splits a comma-separated provider list, such as
// ci.e2eSubscriptionProviders in the DEV CI configuration.
func ParseProviderNamespaces(list string) []string {
	var namespaces []string
	for _, namespace := range strings.Split(list, ",") {
		if namespace = strings.TrimSpace(namespace); namespace != "" && !slices.Contains(namespaces, namespace) {
			namespaces = append(namespaces, namespace)
		}
	}
	return namespaces
}
