package integrationutils

import (
	"encoding/json"
	"fmt"
	"strings"
	"context"

	"github.com/Azure/ARO-HCP/internal/api"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
)

func CreateInitialCosmosContent(ctx context.Context, cosmosContainer *azcosmos.ContainerClient, content []byte) error {
	contentMap := map[string]any{}
	if err := json.Unmarshal(content, &contentMap); err != nil {
		return fmt.Errorf("failed to unmarshal content: %w", err)
	}

	var err error
	switch {
	case strings.EqualFold(contentMap["resourceType"].(string), api.ClusterResourceType.String()),
		strings.EqualFold(contentMap["resourceType"].(string), api.NodePoolResourceType.String()),
		strings.EqualFold(contentMap["resourceType"].(string), api.ExternalAuthResourceType.String()),
		strings.EqualFold(contentMap["resourceType"].(string), api.ControllerResourceType.String()):
		partitionKey := azcosmos.NewPartitionKeyString(contentMap["partitionKey"].(string))
		_, err = cosmosContainer.CreateItem(ctx, partitionKey, content, nil)

	case strings.EqualFold(contentMap["resourceType"].(string), azcorearm.SubscriptionResourceType.String()):
		partitionKey := azcosmos.NewPartitionKeyString(contentMap["partitionKey"].(string))
		_, err = cosmosContainer.CreateItem(ctx, partitionKey, content, nil)

	default:
		return fmt.Errorf("unknown content type: %v", contentMap["resourceType"])
	}

	if err != nil {
		return fmt.Errorf("failed to create item: %w", err)
	}

	return nil
}
