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

package integrationutils

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/Azure/ARO-HCP/internal/api"
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
		strings.EqualFold(contentMap["resourceType"].(string), api.ClusterControllerResourceType.String()),
		strings.EqualFold(contentMap["resourceType"].(string), api.NodePoolControllerResourceType.String()),
		strings.EqualFold(contentMap["resourceType"].(string), api.ExternalAuthControllerResourceType.String()):
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
