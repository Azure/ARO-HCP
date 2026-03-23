package dns

import (
	"context"
	"fmt"
	"strings"

	"github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/pkg/engine/steps/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
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
