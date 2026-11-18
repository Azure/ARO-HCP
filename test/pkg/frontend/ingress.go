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

package frontend

import (
	"context"
	"fmt"
	"net/netip"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v6"
)

// GrantIngress replaces only the frontend ingress child rule and waits for Azure
// to complete the update. source must be a validated IPv4 address.
func GrantIngress(ctx context.Context, client *armnetwork.SecurityRulesClient, resourceGroup string, source netip.Addr) error {
	rule := armnetwork.SecurityRule{
		Properties: &armnetwork.SecurityRulePropertiesFormat{
			Priority:                 to.Ptr[int32](1000),
			Protocol:                 to.Ptr(armnetwork.SecurityRuleProtocolTCP),
			Access:                   to.Ptr(armnetwork.SecurityRuleAccessAllow),
			Direction:                to.Ptr(armnetwork.SecurityRuleDirectionInbound),
			SourceAddressPrefix:      to.Ptr(source.String()),
			SourcePortRange:          to.Ptr("*"),
			DestinationAddressPrefix: to.Ptr("*"),
			DestinationPortRange:     to.Ptr("443"),
		},
	}
	poller, err := client.BeginCreateOrUpdate(ctx, resourceGroup, "svc-cluster-node-nsg", "allow-istio-ingress", rule, nil)
	if err != nil {
		return fmt.Errorf("create frontend ingress rule: %w", err)
	}
	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		return fmt.Errorf("wait for frontend ingress rule: %w", err)
	}
	return nil
}
