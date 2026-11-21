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
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/go-logr/logr"

	"github.com/Azure/ARO-Tools/tools/cmdutils"
	"github.com/Azure/ARO-Tools/tools/registration"
)

const (
	providerPollFrequency = 10 * time.Second
	providerPollDuration  = 10 * time.Minute
)

// RegisterProviders makes every namespace Registered in every subscription, using
// the same implementation as the pipelines' ProviderFeatureRegistration step.
// It is idempotent. A failure on one subscription does not stop the others; all
// failures are returned together. Unlike the rest of this package, registration
// builds its own credential from AZURE_TOKEN_CREDENTIALS.
func RegisterProviders(ctx context.Context, subscriptionIDs, namespaces []string) error {
	config, err := registrationConfig(namespaces)
	if err != nil {
		return err
	}
	// registration.Register requires a logger in the context.
	ctx = logr.NewContext(ctx, logr.FromContextOrDiscard(ctx))
	var failures []error
	for _, subscriptionID := range subscriptionIDs {
		options := registration.RawOptions{
			// Cloud "dev" is the public cloud; this package only serves DEV subscriptions.
			RawOptions:     &cmdutils.RawOptions{Cloud: string(cmdutils.RolloutCloudDev)},
			ConfigJSON:     config,
			SubscriptionID: subscriptionID,
			PollFrequency:  providerPollFrequency,
			PollDuration:   providerPollDuration,
		}
		validated, err := options.Validate()
		if err == nil {
			var completed *registration.Options
			if completed, err = validated.Complete(); err == nil {
				err = completed.Register(ctx)
			}
		}
		if err != nil {
			failures = append(failures, fmt.Errorf("subscription %s: %w", subscriptionID, err))
		}
	}
	if err := errors.Join(failures...); err != nil {
		return fmt.Errorf("provider registration failed: %w", err)
	}
	return nil
}

// registrationConfig renders the registration tool's configuration: every
// provider is awaited, so a successful run leaves them all Registered.
func registrationConfig(namespaces []string) (string, error) {
	if len(namespaces) == 0 {
		return "", errors.New("no resource provider namespaces to register")
	}
	config := registration.Configuration{}
	for _, namespace := range namespaces {
		config[namespace] = registration.Provider{Poll: true}
	}
	encoded, err := json.Marshal(config)
	if err != nil {
		return "", fmt.Errorf("encode registration configuration: %w", err)
	}
	return string(encoded), nil
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
