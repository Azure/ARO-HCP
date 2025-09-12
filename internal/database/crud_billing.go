package database

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
)

type BillingCRUD interface {
	Create(ctx context.Context, doc *BillingDocument) error
	Patch(ctx context.Context, resourceID *azcorearm.ResourceID, ops BillingDocumentPatchOperations) error
}

type billingCRUD struct {
	containerClient *azcosmos.ContainerClient
}

func newBillingCRUD(containerClient *azcosmos.ContainerClient) *billingCRUD {
	return &billingCRUD{
		containerClient: containerClient,
	}
}

var _ BillingCRUD = &billingCRUD{}

func (d *billingCRUD) getBillingID(ctx context.Context, resourceID *azcorearm.ResourceID) (string, error) {
	var billingID string

	pk := NewPartitionKey(resourceID.SubscriptionID)

	// Resource ID alone does not uniquely identify a billing document, but
	// resource ID AND the absence of a deletion timestamp should be unique.
	const query = "SELECT c.id FROM c WHERE STRINGEQUALS(c.resourceId, @resourceId, true) AND NOT IS_DEFINED(c.deletionTime)"
	opt := azcosmos.QueryOptions{
		QueryParameters: []azcosmos.QueryParameter{
			{
				Name:  "@resourceId",
				Value: resourceID.String(),
			},
		},
	}

	queryPager := d.containerClient.NewQueryItemsPager(query, pk, &opt)

	for queryPager.More() {
		queryResponse, err := queryPager.NextPage(ctx)
		if err != nil {
			return "", fmt.Errorf("failed to advance page while querying Billing container for '%s': %w", resourceID, err)
		}

		for _, item := range queryResponse.Items {
			var result map[string]string

			if billingID != "" {
				return "", ErrAmbiguousResult
			}

			err = json.Unmarshal(item, &result)
			if err != nil {
				return "", fmt.Errorf("failed to unmarshal Billing container item for '%s': %w", resourceID, err)
			}

			// Let the pager finish to ensure we get a single result.
			if id, ok := result["id"]; ok {
				billingID = id
			}
		}
	}

	if billingID == "" {
		// Fabricate a "404 Not Found" ResponseError to wrap.
		err := &azcore.ResponseError{StatusCode: http.StatusNotFound}
		return "", fmt.Errorf("failed to read Billing container item for '%s': %w", resourceID, err)
	}

	return billingID, nil
}

func (d *billingCRUD) Create(ctx context.Context, doc *BillingDocument) error {
	if doc.ResourceID == nil {
		return errors.New("BillingDocument is missing a ResourceID")
	}

	pk := NewPartitionKey(doc.ResourceID.SubscriptionID)

	data, err := json.Marshal(doc)
	if err != nil {
		return fmt.Errorf("failed to marshal Billing container item for '%s': %w", doc.ResourceID, err)
	}

	_, err = d.containerClient.CreateItem(ctx, pk, data, nil)
	if err != nil {
		return fmt.Errorf("failed to create Billing container item for '%s': %w", doc.ResourceID, err)
	}

	return nil
}

func (d *billingCRUD) Patch(ctx context.Context, resourceID *azcorearm.ResourceID, ops BillingDocumentPatchOperations) error {
	billingID, err := d.getBillingID(ctx, resourceID)
	if err != nil {
		return err
	}

	pk := NewPartitionKey(resourceID.SubscriptionID)

	_, err = d.containerClient.PatchItem(ctx, pk, billingID, ops.PatchOperations, nil)
	if err != nil {
		return fmt.Errorf("failed to patch Billing container item for '%s': %w", resourceID, err)
	}

	return nil
}
