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

package cosmosstorageutils

import (
	"context"
	"fmt"
	"strings"

	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
)

// GlobalLister lists all resources of a particular type across all partitions.
type GlobalLister[T any] interface {
	List(ctx context.Context, options *DBClientListResourceDocsOptions) (DBClientIterator[T], error)
}

// CosmosGlobalLister lists documents whose resourceType matches any of ResourceTypes.
// An empty PartitionKey triggers a cross-partition query; a non-empty value scopes the query
// to that single partition. The query also filters out partition-header documents that lack
// a resourceID — the kube-applier container has no such documents, so the filter is a no-op
// there, while the Resources container relies on it.
type CosmosGlobalLister[InternalAPIType, CosmosAPIType any] struct {
	ContainerClient *azcosmos.ContainerClient
	ResourceTypes   []azcorearm.ResourceType
	PartitionKey    string
}

func (l *CosmosGlobalLister[InternalAPIType, CosmosAPIType]) List(ctx context.Context, options *DBClientListResourceDocsOptions) (DBClientIterator[InternalAPIType], error) {
	var resourceTypeConditions []string
	for _, resourceType := range l.ResourceTypes {
		resourceTypeConditions = append(resourceTypeConditions, fmt.Sprintf("STRINGEQUALS(c.resourceType, %q, true)", resourceType.String()))
	}
	whereClause := strings.Join(resourceTypeConditions, " OR ")
	query := fmt.Sprintf("SELECT * FROM c WHERE LENGTH(c.resourceID) > 0 AND (NOT IS_DEFINED(c.deletionTimestamp)) AND (%s)", whereClause)

	queryOptions := azcosmos.QueryOptions{
		PageSizeHint: -1,
	}
	if options != nil {
		if options.PageSizeHint != nil {
			queryOptions.PageSizeHint = max(*options.PageSizeHint, -1)
		}
		queryOptions.ContinuationToken = options.ContinuationToken
	}

	var partitionKey azcosmos.PartitionKey
	if l.PartitionKey == "" {
		partitionKey = azcosmos.NewPartitionKey()
	} else {
		partitionKey = azcosmos.NewPartitionKeyString(l.PartitionKey)
	}
	pager := l.ContainerClient.NewQueryItemsPager(query, partitionKey, &queryOptions)

	if options != nil && ptr.Deref(options.PageSizeHint, -1) > 0 {
		return NewQueryResourcesSinglePageIterator[InternalAPIType, CosmosAPIType](pager), nil
	}
	return NewQueryResourcesIterator[InternalAPIType, CosmosAPIType](pager), nil
}
