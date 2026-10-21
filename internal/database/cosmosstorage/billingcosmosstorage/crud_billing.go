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

package billingcosmosstorage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
)

// BillingDocCRUD provides a CRUD interface for managing billing documents
// within a subscription partition.
type BillingDocCRUD interface {
	// Create a new billing document
	Create(ctx context.Context, doc *BillingDocument) error

	// GetByID retrieves a billing document by its ID
	GetByID(ctx context.Context, billingDocID string) (*BillingDocument, error)

	// List returns an iterator for all billing documents in the given subscription partition.
	// This includes both active and deleted billing documents.
	List(ctx context.Context) (cosmosstorageutils.DBClientIterator[BillingDocument], error)

	// ListActive lists all active billing documents (without deletion time) for the subscription
	ListActive(ctx context.Context) ([]*BillingDocument, error)

	// ListActiveForCluster lists active billing documents for a specific cluster
	ListActiveForCluster(ctx context.Context, resourceID *azcorearm.ResourceID) ([]*BillingDocument, error)

	// PatchByID patches a billing document by its ID
	PatchByID(ctx context.Context, billingDocID string, ops BillingDocumentPatchOperations) error

	// PatchByClusterID patches all active billing documents for a cluster
	PatchByClusterID(ctx context.Context, resourceID *azcorearm.ResourceID, ops BillingDocumentPatchOperations) error
}

type billingDocCRUD struct {
	containerClient *azcosmos.ContainerClient
	subscriptionID  string
}

// NewBillingDocCRUD creates a new BillingDocCRUD instance for a subscription
func NewBillingDocCRUD(containerClient *azcosmos.ContainerClient, subscriptionID string) BillingDocCRUD {
	return &billingDocCRUD{
		containerClient: containerClient,
		subscriptionID:  subscriptionID,
	}
}

var _ BillingDocCRUD = &billingDocCRUD{}

func (b *billingDocCRUD) List(ctx context.Context) (cosmosstorageutils.DBClientIterator[BillingDocument], error) {
	pk := cosmosstorageutils.NewPartitionKey(b.subscriptionID)
	query := "SELECT * FROM c"
	pager := b.containerClient.NewQueryItemsPager(query, pk, nil)
	return newQueryBillingIterator(pager), nil
}

func (b *billingDocCRUD) Create(ctx context.Context, doc *BillingDocument) error {
	if doc.ResourceID == nil {
		return errors.New("BillingDocument is missing a ResourceID")
	}

	if doc.ID == "" {
		return errors.New("BillingDocument is missing an ID")
	}

	if doc.CreationTime.IsZero() {
		return errors.New("BillingDocument is missing a CreationTime")
	}

	pk := cosmosstorageutils.NewPartitionKey(b.subscriptionID)
	marshalled, err := json.Marshal(doc)
	if err != nil {
		return fmt.Errorf("failed to marshal BillingDocument: %w", err)
	}

	_, err = b.containerClient.CreateItem(ctx, pk, marshalled, nil)
	if err != nil {
		return fmt.Errorf("failed to create billing document: %w", err)
	}

	return nil
}

func (b *billingDocCRUD) GetByID(ctx context.Context, billingDocID string) (*BillingDocument, error) {
	pk := cosmosstorageutils.NewPartitionKey(b.subscriptionID)

	response, err := b.containerClient.ReadItem(ctx, pk, billingDocID, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to read billing document: %w", err)
	}

	var doc BillingDocument
	err = json.Unmarshal(response.Value, &doc)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal billing document: %w", err)
	}

	return &doc, nil
}

func (b *billingDocCRUD) ListActive(ctx context.Context) ([]*BillingDocument, error) {
	pk := cosmosstorageutils.NewPartitionKey(b.subscriptionID)

	query := "SELECT * FROM c WHERE NOT IS_DEFINED(c.deletionTime)"
	opt := azcosmos.QueryOptions{}

	queryPager := b.containerClient.NewQueryItemsPager(query, pk, &opt)

	var billingDocs []*BillingDocument
	for queryPager.More() {
		queryResponse, err := queryPager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to advance page while querying Billing container for subscription '%s': %w", b.subscriptionID, err)
		}

		for _, item := range queryResponse.Items {
			var doc BillingDocument
			err = json.Unmarshal(item, &doc)
			if err != nil {
				return nil, fmt.Errorf("failed to unmarshal Billing container item for subscription '%s': %w", b.subscriptionID, err)
			}
			billingDocs = append(billingDocs, &doc)
		}
	}

	return billingDocs, nil
}

func (b *billingDocCRUD) ListActiveForCluster(ctx context.Context, resourceID *azcorearm.ResourceID) ([]*BillingDocument, error) {
	pk := cosmosstorageutils.NewPartitionKey(b.subscriptionID)

	query := "SELECT * FROM c WHERE STRINGEQUALS(c.resourceId, @resourceId, true) AND NOT IS_DEFINED(c.deletionTime)"
	queryParams := []azcosmos.QueryParameter{
		{
			Name:  "@resourceId",
			Value: resourceID.String(),
		},
	}

	opt := azcosmos.QueryOptions{
		QueryParameters: queryParams,
	}

	queryPager := b.containerClient.NewQueryItemsPager(query, pk, &opt)

	var billingDocs []*BillingDocument
	for queryPager.More() {
		queryResponse, err := queryPager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to advance page while querying Billing container for '%s': %w", resourceID, err)
		}

		for _, item := range queryResponse.Items {
			var doc BillingDocument
			err = json.Unmarshal(item, &doc)
			if err != nil {
				return nil, fmt.Errorf("failed to unmarshal Billing container item for '%s': %w", resourceID, err)
			}
			billingDocs = append(billingDocs, &doc)
		}
	}

	return billingDocs, nil
}

func (b *billingDocCRUD) PatchByID(ctx context.Context, billingDocID string, ops BillingDocumentPatchOperations) error {
	pk := cosmosstorageutils.NewPartitionKey(b.subscriptionID)

	_, err := b.containerClient.PatchItem(ctx, pk, billingDocID, ops.PatchOperations, nil)
	if err != nil {
		return fmt.Errorf("failed to patch billing document: %w", err)
	}

	return nil
}

func (b *billingDocCRUD) PatchByClusterID(ctx context.Context, resourceID *azcorearm.ResourceID, ops BillingDocumentPatchOperations) error {
	pk := cosmosstorageutils.NewPartitionKey(b.subscriptionID)

	// Resource ID alone does not uniquely identify a billing document, but
	// resource ID AND the absence of a deletion timestamp should be unique.
	// However, we patch all active billing documents for the cluster in case there are duplicates.
	const query = "SELECT c.id FROM c WHERE STRINGEQUALS(c.resourceId, @resourceId, true) AND NOT IS_DEFINED(c.deletionTime)"
	opt := azcosmos.QueryOptions{
		QueryParameters: []azcosmos.QueryParameter{
			{
				Name:  "@resourceId",
				Value: resourceID.String(),
			},
		},
	}

	queryPager := b.containerClient.NewQueryItemsPager(query, pk, &opt)

	var billingIDs []string
	for queryPager.More() {
		queryResponse, err := queryPager.NextPage(ctx)
		if err != nil {
			return fmt.Errorf("failed to advance page while querying Billing container for '%s': %w", resourceID, err)
		}

		for _, item := range queryResponse.Items {
			var result map[string]string

			err = json.Unmarshal(item, &result)
			if err != nil {
				return fmt.Errorf("failed to unmarshal Billing container item for '%s': %w", resourceID, err)
			}

			if id, ok := result["id"]; ok {
				billingIDs = append(billingIDs, id)
			}
		}
	}

	if len(billingIDs) == 0 {
		return cosmosstorageutils.NewNotFoundError()
	}

	// Patch all active billing documents for this cluster
	for _, billingID := range billingIDs {
		err := b.PatchByID(ctx, billingID, ops)
		if err != nil {
			return fmt.Errorf("failed to patch billing document '%s': %w", billingID, err)
		}
	}

	return nil
}
