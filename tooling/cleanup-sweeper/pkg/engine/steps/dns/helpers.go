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

package dns

import (
	"context"
	"fmt"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"

	"github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/pkg/engine/steps/arm"
)

func VerifyPrivateDNSZonesDeleted(ctx context.Context, client *armresources.Client, resourceGroupName string) error {
	remainingResources, err := arm.ListByType(ctx, client, resourceGroupName, "Microsoft.Network/privateDnsZones")
	if err != nil {
		return fmt.Errorf("failed to verify private DNS zone deletion: %w", err)
	}

	if len(remainingResources) == 0 {
		return nil
	}

	names := make([]string, 0, len(remainingResources))
	for _, zone := range remainingResources {
		if zone == nil || zone.Name == nil {
			continue
		}
		names = append(names, *zone.Name)
	}
	return fmt.Errorf("%d private DNS zones remaining: %s", len(remainingResources), strings.Join(names, ", "))
}
