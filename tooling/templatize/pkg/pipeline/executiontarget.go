// Copyright 2025 Microsoft Corporation
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

package pipeline

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armsubscriptions"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/Azure/ARO-Tools/tools/cmdutils"

	"github.com/Azure/ARO-HCP/tooling/templatize/pkg/aks"
)

type SubscriptionLookup func(ctx context.Context, subscriptionName string) (string, error)
type TopoDirLookup func(serviceGroup string) (string, error)

var subscriptionLookupBackoff = wait.Backoff{
	Steps:    5,
	Duration: 15 * time.Second,
	Factor:   2.0,
	Jitter:   0.1,
}

func LookupSubscriptionID(subscriptions map[string]string) SubscriptionLookup {
	return func(ctx context.Context, subscriptionName string) (string, error) {
		// First, check in the explicit registry
		if id, found := subscriptions[subscriptionName]; found {
			return id, nil
		}

		// Otherwise, do a lookup against Azure using the display name
		fmt.Fprintf(os.Stderr, "[subscription-lookup] %q not in explicit registry; querying Azure API\n", subscriptionName)
		cred, err := cmdutils.GetAzureTokenCredentials()
		if err != nil {
			return "", fmt.Errorf("failed to obtain a credential: %v", err)
		}

		client, err := armsubscriptions.NewClient(cred, nil)
		if err != nil {
			return "", fmt.Errorf("failed to create subscriptions client: %v", err)
		}

		var result string
		var lastFoundNames []string
		var lastErr error
		var attempt int

		err = wait.ExponentialBackoffWithContext(ctx, subscriptionLookupBackoff, func(ctx context.Context) (bool, error) {
			attempt++
			pager := client.NewListPager(nil)
			var foundNames []string
			for pager.More() {
				page, err := pager.NextPage(ctx)
				if err != nil {
					lastErr = err
					fmt.Fprintf(os.Stderr, "[subscription-lookup] attempt %d: error listing subscriptions: %v\n", attempt, err)
					return false, nil
				}
				for _, sub := range page.Value {
					displayName := ""
					subID := ""
					if sub.DisplayName != nil {
						displayName = *sub.DisplayName
					}
					if sub.SubscriptionID != nil {
						subID = *sub.SubscriptionID
					}
					fmt.Fprintf(os.Stderr, "[subscription-lookup] attempt %d: visible subscription: displayName=%q id=%s\n", attempt, displayName, subID)
					foundNames = append(foundNames, displayName)
					if displayName == subscriptionName {
						if subID == "" {
							lastErr = fmt.Errorf("subscription %q matched but has nil SubscriptionID", subscriptionName)
							fmt.Fprintf(os.Stderr, "[subscription-lookup] attempt %d: %v\n", attempt, lastErr)
							return false, nil
						}
						result = subID
						return true, nil
					}
				}
			}
			lastFoundNames = foundNames
			fmt.Fprintf(os.Stderr, "[subscription-lookup] attempt %d: %q not found; %d subscriptions visible: %v\n", attempt, subscriptionName, len(foundNames), foundNames)
			return false, nil
		})

		if err != nil {
			if lastErr != nil {
				return "", fmt.Errorf("subscription lookup for %q failed after %d attempts (last error: %v): %w", subscriptionName, attempt, lastErr, err)
			}
			return "", fmt.Errorf("subscription lookup for %q timed out after %d attempts; last visible: %v", subscriptionName, attempt, lastFoundNames)
		}
		if result == "" {
			return "", fmt.Errorf("subscription with name %q not found after %d attempts; %d subscriptions visible: %v", subscriptionName, attempt, len(lastFoundNames), lastFoundNames)
		}
		return result, nil
	}
}

func KubeConfig(ctx context.Context, subscriptionID, resourceGroup, aksClusterName string) (string, error) {
	if aksClusterName == "" {
		return "", nil
	}

	// Get Kubeconfig
	kubeconfigPath, err := aks.GetKubeConfig(ctx, subscriptionID, resourceGroup, aksClusterName)
	if err != nil {
		return "", fmt.Errorf("failed to get kubeconfig: %w", err)
	}

	// Make sure we have cluster admin
	err = aks.EnsureClusterAdmin(ctx, kubeconfigPath, subscriptionID, resourceGroup, aksClusterName, nil)
	if err != nil {
		return "", fmt.Errorf("failed to ensure cluster admin role: %w", err)
	}
	return kubeconfigPath, nil
}

type ExecutionTarget interface {
	GetSubscriptionID() string
	GetResourceGroup() string
	GetRegion() string
}

type executionTargetImpl struct {
	subscriptionName string
	subscriptionID   string
	resourceGroup    string
	region           string
}

func (target *executionTargetImpl) GetSubscriptionID() string {
	return target.subscriptionID
}

func (target *executionTargetImpl) GetResourceGroup() string {
	return target.resourceGroup
}

func (target *executionTargetImpl) GetRegion() string {
	return target.region
}
