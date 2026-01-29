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

	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/Azure/ARO-HCP/internal/database"
)

// ContentLoader is an interface for loading test content into a database.
// This abstraction allows tests to run against either a real Cosmos DB
// or a mock database implementation.
type ContentLoader interface {
	// LoadContent loads a single JSON document into the database.
	LoadContent(ctx context.Context, content []byte) error
}

// DocumentLister is an interface for listing all documents from a database.
// This is used by the cosmosCompare step to verify database contents.
type DocumentLister interface {
	// ListAllDocuments returns all documents in the database.
	ListAllDocuments(ctx context.Context) ([]*database.TypedDocument, error)
}

// CosmosContentLoader implements ContentLoader and DocumentLister using a real Cosmos DB container.
type CosmosContentLoader struct {
	container *azcosmos.ContainerClient
}

// NewCosmosContentLoader creates a new CosmosContentLoader from a Cosmos container.
func NewCosmosContentLoader(container *azcosmos.ContainerClient) *CosmosContentLoader {
	return &CosmosContentLoader{container: container}
}

// LoadContent loads a single JSON document into Cosmos DB.
func (c *CosmosContentLoader) LoadContent(ctx context.Context, content []byte) error {
	return LoadCosmosContent(ctx, c.container, content)
}

// ListAllDocuments returns all documents in the Cosmos container.
func (c *CosmosContentLoader) ListAllDocuments(ctx context.Context) ([]*database.TypedDocument, error) {
	querySQL := "SELECT * FROM c"
	queryOptions := &azcosmos.QueryOptions{
		QueryParameters: []azcosmos.QueryParameter{},
	}

	queryPager := c.container.NewQueryItemsPager(querySQL, azcosmos.PartitionKey{}, queryOptions)

	var results []*database.TypedDocument
	for queryPager.More() {
		queryResponse, err := queryPager.NextPage(ctx)
		if err != nil {
			return nil, err
		}

		for _, item := range queryResponse.Items {
			var doc database.TypedDocument
			if err := json.Unmarshal(item, &doc); err != nil {
				return nil, err
			}
			results = append(results, &doc)
		}
	}
	return results, nil
}

var _ ContentLoader = &CosmosContentLoader{}
var _ DocumentLister = &CosmosContentLoader{}
