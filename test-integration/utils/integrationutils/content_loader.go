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

	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
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
	ListAllDocuments(ctx context.Context) ([]*cosmosstorageutils.TypedDocument, error)
}

func listAllContainerDocuments(containers map[string][]*cosmosstorageutils.TypedDocument) []*cosmosstorageutils.TypedDocument {
	var documents []*cosmosstorageutils.TypedDocument
	for _, container := range containers {
		documents = append(documents, container...)
	}
	return documents
}

// CosmosContentLoader implements ContentLoader and DocumentLister using a real Cosmos DB container.
type CosmosContentLoader struct {
	resourcesContainer *azcosmos.ContainerClient
	fleetContainer     *azcosmos.ContainerClient
}

// NewCosmosContentLoader creates a new CosmosContentLoader from a Cosmos container.
func NewCosmosContentLoader(resourcesContainer, fleetContainer *azcosmos.ContainerClient) *CosmosContentLoader {
	return &CosmosContentLoader{resourcesContainer: resourcesContainer, fleetContainer: fleetContainer}
}

// LoadContent loads a single JSON document into Cosmos DB.
func (c *CosmosContentLoader) LoadContent(ctx context.Context, content []byte) error {
	return LoadCosmosContent(ctx, c.resourcesContainer, c.fleetContainer, content)
}

// ListAllDocuments returns all documents in the Cosmos container.
func (c *CosmosContentLoader) ListAllDocuments(ctx context.Context) ([]*cosmosstorageutils.TypedDocument, error) {
	containers, err := c.ListDocumentsByContainer(ctx)
	if err != nil {
		return nil, err
	}
	return listAllContainerDocuments(containers), nil
}

func (c *CosmosContentLoader) ListDocumentsByContainer(ctx context.Context) (map[string][]*cosmosstorageutils.TypedDocument, error) {
	results := map[string][]*cosmosstorageutils.TypedDocument{}
	for name, container := range map[string]*azcosmos.ContainerClient{"Resources": c.resourcesContainer, "Fleet": c.fleetContainer} {
		documents, err := listCosmosContainerDocuments(ctx, container)
		if err != nil {
			return nil, fmt.Errorf("failed to list %s documents: %w", name, err)
		}
		results[name] = documents
	}
	return results, nil
}

func listCosmosContainerDocuments(ctx context.Context, container *azcosmos.ContainerClient) ([]*cosmosstorageutils.TypedDocument, error) {
	querySQL := "SELECT * FROM c"
	queryOptions := &azcosmos.QueryOptions{
		QueryParameters: []azcosmos.QueryParameter{},
	}

	queryPager := container.NewQueryItemsPager(querySQL, azcosmos.PartitionKey{}, queryOptions)

	var results []*cosmosstorageutils.TypedDocument
	for queryPager.More() {
		queryResponse, err := queryPager.NextPage(ctx)
		if err != nil {
			return nil, err
		}

		for _, item := range queryResponse.Items {
			var doc cosmosstorageutils.TypedDocument
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
