package database

import (
	"context"
	"fmt"
	"strings"

	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
)

type OperationsCRUD interface {
	Create(ctx context.Context, doc *OperationDocumentWrapper) (string, error)
	Patch(ctx context.Context, resource *OperationDocumentWrapper, opts OperationDocumentPatchOperations) (*OperationDocumentWrapper, error)
	Get(ctx context.Context, subscriptionID, operationID string) (*OperationDocumentWrapper, error)
	ListActive(ctx context.Context, subscriptionID string, opts *DBClientListActiveOperationDocsOptions) (DBClientIterator[OperationDocumentWrapper], error)
}

type operationsCRUD struct {
	containerClient *azcosmos.ContainerClient
}

func newOperationsCRUD(containerClient *azcosmos.ContainerClient) *operationsCRUD {
	return &operationsCRUD{
		containerClient: containerClient,
	}
}

var _ OperationsCRUD = &operationsCRUD{}

func (d *operationsCRUD) Create(ctx context.Context, doc *OperationDocumentWrapper) (string, error) {
	// Make sure partition key is lowercase.
	subscriptionID := strings.ToLower(doc.Properties.ExternalID.SubscriptionID)

	typedDoc := NewTypedDocument(subscriptionID, OperationResourceType)
	typedDoc.TimeToLive = operationTimeToLive
	doc.SetTypedDocument(*typedDoc)

	data, err := typedDocumentMarshal(doc)
	if err != nil {
		return "", fmt.Errorf("failed to marshal Operations container item for '%s': %w", typedDoc.ID, err)
	}

	_, err = d.containerClient.CreateItem(ctx, typedDoc.getPartitionKey(), data, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create Operations container item for '%s': %w", typedDoc.ID, err)
	}

	return typedDoc.ID, nil
}

func (d *operationsCRUD) Get(ctx context.Context, subscriptionID, operationID string) (*OperationDocumentWrapper, error) {
	// Make sure lookup keys are lowercase.
	operationID = strings.ToLower(operationID)

	response, err := d.containerClient.ReadItem(ctx, azcosmos.NewPartitionKeyString(subscriptionID), operationID, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to read Operations container item for '%s': %w", operationID, err)
	}

	ret, err := typedDocumentUnmarshal[OperationDocumentWrapper](response.Value)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal Operations container item for '%s': %w", operationID, err)
	}

	return ret, nil
}

func (d *operationsCRUD) Patch(ctx context.Context, resource *OperationDocumentWrapper, ops OperationDocumentPatchOperations) (*OperationDocumentWrapper, error) {
	return patch(ctx, d.containerClient, resource, ops.PatchOperations)
}

func (d *operationsCRUD) ListActive(ctx context.Context, subscriptionID string, opts *DBClientListActiveOperationDocsOptions) (DBClientIterator[OperationDocumentWrapper], error) {
	var queryOptions azcosmos.QueryOptions

	query := fmt.Sprintf(
		"SELECT * FROM c WHERE STRINGEQUALS(c.resourceType, %q, true) "+
			"AND NOT ARRAYCONTAINS([%q, %q, %q], c.properties.status)",
		OperationResourceType.String(),
		arm.ProvisioningStateSucceeded,
		arm.ProvisioningStateFailed,
		arm.ProvisioningStateCanceled)

	if opts != nil {
		if opts.Request != nil {
			query += " AND c.properties.request == @request"
			queryParameter := azcosmos.QueryParameter{
				Name:  "@request",
				Value: string(*opts.Request),
			}
			queryOptions.QueryParameters = append(queryOptions.QueryParameters, queryParameter)
		}

		if opts.ExternalID != nil {
			query += " AND "
			const resourceFilter = "STRINGEQUALS(c.properties.externalId, @externalId, true)"
			if opts.IncludeNestedResources {
				const nestedResourceFilter = "STARTSWITH(c.properties.externalId, CONCAT(@externalId, \"/\"), true)"
				query += fmt.Sprintf("(%s OR %s)", resourceFilter, nestedResourceFilter)
			} else {
				query += resourceFilter
			}
			queryParameter := azcosmos.QueryParameter{
				Name:  "@externalId",
				Value: opts.ExternalID.String(),
			}
			queryOptions.QueryParameters = append(queryOptions.QueryParameters, queryParameter)
		}
	}

	pager := d.resources.NewQueryItemsPager(query, azcosmos.NewPartitionKeyString(subscriptionID), &queryOptions)

	return newQueryItemsIterator[OperationDocumentWrapper](pager), nil
}
