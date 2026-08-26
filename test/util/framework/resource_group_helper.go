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

package framework

import (
	"context"
	"fmt"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v6"
)

// GetPrivateKASInternalIP finds the private IP address of the internal load
// balancer created by HyperShift for the KAS in a private cluster. The KAS
// internal LB is identified by its kube-apiserver load balancing rule —
// fully-private clusters have a second internal LB for private ingress that
// must not be confused with the KAS LB.
func GetPrivateKASInternalIP(ctx context.Context, tc interface {
	SubscriptionID(ctx context.Context) (string, error)
	AzureCredential() (azcore.TokenCredential, error)
}, managedResourceGroup string) (string, error) {
	subscriptionID, err := tc.SubscriptionID(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get subscription ID: %w", err)
	}

	azCreds, err := tc.AzureCredential()
	if err != nil {
		return "", fmt.Errorf("failed to get Azure credentials: %w", err)
	}

	lbClient, err := armnetwork.NewLoadBalancersClient(subscriptionID, azCreds, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create load balancers client: %w", err)
	}

	pager := lbClient.NewListPager(managedResourceGroup, nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return "", fmt.Errorf("failed to list load balancers in %q: %w", managedResourceGroup, err)
		}
		for _, lb := range page.Value {
			if lb.Properties == nil || lb.Properties.FrontendIPConfigurations == nil {
				continue
			}
			for _, fip := range lb.Properties.FrontendIPConfigurations {
				if fip.Properties == nil {
					continue
				}
				// Internal LBs have a private IP and no public IP.
				// Fully-private clusters have two internal LBs (KAS and ingress),
				// so also check for the kube-apiserver load balancing rule to
				// identify the KAS LB specifically.
				if fip.Properties.PrivateIPAddress != nil && fip.Properties.PublicIPAddress == nil {
					for _, rule := range fip.Properties.LoadBalancingRules {
						if rule.ID != nil && strings.HasSuffix(*rule.ID, "/kube-apiserver") {
							return *fip.Properties.PrivateIPAddress, nil
						}
					}
				}
			}
		}
	}

	return "", fmt.Errorf("no internal load balancer found in managed resource group %q", managedResourceGroup)
}
