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
// balancer created by HyperShift for the KAS in a private cluster. The KAS LB
// is identified by two checks, both verified against a real cluster:
//   - LB name starts with "int-" (HyperShift's naming convention for the KAS LB)
//   - frontend IP has a load balancing rule named "kube-apiserver"
//
// Using both checks together makes the lookup robust even when multiple
// internal LBs exist (e.g. a private ingress LB in a fully-private cluster).
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
			if lb.Name == nil || !strings.HasPrefix(*lb.Name, "int-") {
				continue
			}
			if lb.Properties == nil || lb.Properties.FrontendIPConfigurations == nil {
				continue
			}
			for _, fip := range lb.Properties.FrontendIPConfigurations {
				if fip.Properties == nil || fip.Properties.PrivateIPAddress == nil || fip.Properties.PublicIPAddress != nil {
					continue
				}
				for _, rule := range fip.Properties.LoadBalancingRules {
					if rule.ID != nil && strings.HasSuffix(*rule.ID, "/kube-apiserver") {
						return *fip.Properties.PrivateIPAddress, nil
					}
				}
			}
		}
	}

	return "", fmt.Errorf("no KAS internal load balancer found in managed resource group %q", managedResourceGroup)
}
