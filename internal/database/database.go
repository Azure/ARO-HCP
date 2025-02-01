package database

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"net/http"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/Azure/ARO-HCP/internal/api/arm"
)

const (
	billingContainer       = "Billing"
	locksContainer         = "Locks"
	operationsContainer    = "Operations"
	resourcesContainer     = "Resources"
	subscriptionsContainer = "Subscriptions"
	partitionKeysContainer = "PartitionKeys"

	// XXX The azcosmos SDK currently only supports single-partition queries,
	//     so there's no way to list all items in a container unless you know
	//     all the partition keys. The backend needs to list all items in the
	//     Operations container so to work around this limitation we keep all
	//     items in a single partition with a well-known name: "workaround".
	//
	//     Once [1] is fixed we could transition the Operations container to
	//     using subscription IDs as the partition key like other containers.
	//     The items are transient thanks to the container's default TTL, so
	//     GetOperationDoc would just need temporary fallback logic to check
	//     the "workaround" partition.
	//
	//     [1] https://github.com/Azure/azure-sdk-for-go/issues/18578
	operationsPartitionKey = "workaround"
)

var ErrNotFound = errors.New("not found")

func isResponseError(err error, statusCode int) bool {
	var responseError *azcore.ResponseError
	return errors.As(err, &responseError) && responseError.StatusCode == statusCode
}

// NewPartitionKey creates a partition key from an Azure subscription ID.
func NewPartitionKey(subscriptionID string) azcosmos.PartitionKey {
	return azcosmos.NewPartitionKeyString(strings.ToLower(subscriptionID))
}

type DBClientIteratorItem[T DocumentProperties] iter.Seq2[string, *T]

type DBClientIterator[T DocumentProperties] interface {
	Items(ctx context.Context) DBClientIteratorItem[T]
	GetContinuationToken() string
	GetError() error
}

// DBClient is a document store for frontend to perform required CRUD operations against
type DBClient interface {
	// DBConnectionTest is used to health check the database. If the database is not reachable or otherwise not ready
	// to be used, an error should be returned.
	DBConnectionTest(ctx context.Context) error

	// GetLockClient returns a LockClient, or nil if the DBClient does not support a LockClient.
	GetLockClient() *LockClient

	// GetResourceDoc retrieves a ResourceDocument from the database given its resourceID.
	// ErrNotFound is returned if an associated ResourceDocument cannot be found.
	GetResourceDoc(ctx context.Context, resourceID *azcorearm.ResourceID) (*ResourceDocument, error)
	CreateResourceDoc(ctx context.Context, doc *ResourceDocument) error
	UpdateResourceDoc(ctx context.Context, resourceID *azcorearm.ResourceID, callback func(*ResourceDocument) bool) (bool, error)
	// DeleteResourceDoc deletes a ResourceDocument from the database given the resourceID
	// of a Microsoft.RedHatOpenShift/HcpOpenShiftClusters resource or NodePools child resource.
	DeleteResourceDoc(ctx context.Context, resourceID *azcorearm.ResourceID) error
	ListResourceDocs(prefix *azcorearm.ResourceID, maxItems int32, continuationToken *string) DBClientIterator[ResourceDocument]

	GetOperationDoc(ctx context.Context, pk azcosmos.PartitionKey, operationID string) (*OperationDocument, error)
	CreateOperationDoc(ctx context.Context, doc *OperationDocument) (string, error)
	UpdateOperationDoc(ctx context.Context, pk azcosmos.PartitionKey, operationID string, callback func(*OperationDocument) bool) (bool, error)
	ListOperationDocs(subscriptionID string) DBClientIterator[OperationDocument]

	// GetSubscriptionDoc retrieves a subscription from the database given the subscriptionID.
	// ErrNotFound is returned if an associated subscription cannot be found.
	GetSubscriptionDoc(ctx context.Context, subscriptionID string) (*arm.Subscription, error)
	CreateSubscriptionDoc(ctx context.Context, subscriptionID string, subscription *arm.Subscription) error
	UpdateSubscriptionDoc(ctx context.Context, subscriptionID string, callback func(*arm.Subscription) bool) (bool, error)
	ListAllSubscriptionDocs() DBClientIterator[arm.Subscription]
}

var _ DBClient = &cosmosDBClient{}

// cosmosDBClient defines the needed values to perform CRUD operations against the async DB
type cosmosDBClient struct {
	database      *azcosmos.DatabaseClient
	resources     *azcosmos.ContainerClient
	operations    *azcosmos.ContainerClient
	subscriptions *azcosmos.ContainerClient
	partitionKeys *azcosmos.ContainerClient
	lockClient    *LockClient
}

// NewDBClient instantiates a DBClient from a Cosmos DatabaseClient instance
// targeting the Frontends async database.
func NewDBClient(ctx context.Context, database *azcosmos.DatabaseClient) (DBClient, error) {
	// NewContainer only fails if the container ID argument is
	// empty, so we can safely disregard the error return value.
	resources, _ := database.NewContainer(resourcesContainer)
	operations, _ := database.NewContainer(operationsContainer)
	subscriptions, _ := database.NewContainer(subscriptionsContainer)
	partitionKeys, _ := database.NewContainer(partitionKeysContainer)
	locks, _ := database.NewContainer(locksContainer)

	lockClient, err := NewLockClient(ctx, locks)
	if err != nil {
		return nil, err
	}

	return &cosmosDBClient{
		database:      database,
		resources:     resources,
		operations:    operations,
		subscriptions: subscriptions,
		partitionKeys: partitionKeys,
		lockClient:    lockClient,
	}, nil
}

// DBConnectionTest checks the async database is accessible on startup
func (d *cosmosDBClient) DBConnectionTest(ctx context.Context) error {
	if _, err := d.database.Read(ctx, nil); err != nil {
		return fmt.Errorf("failed to read Cosmos database information during healthcheck: %v", err)
	}

	return nil
}

func (d *cosmosDBClient) GetLockClient() *LockClient {
	return d.lockClient
}

func (d *cosmosDBClient) getResourceDoc(ctx context.Context, resourceID *azcorearm.ResourceID) (*typedDocument, *ResourceDocument, error) {
	pk := NewPartitionKey(resourceID.SubscriptionID)

	const query = "SELECT * FROM c WHERE STRINGEQUALS(c.resourceType, @resourceType, true) AND STRINGEQUALS(c.properties.resourceId, @resourceId, true)"
	opt := azcosmos.QueryOptions{
		PageSizeHint: 1,
		QueryParameters: []azcosmos.QueryParameter{
			{
				Name:  "@resourceType",
				Value: resourceID.ResourceType.String(),
			},
			{
				Name:  "@resourceId",
				Value: resourceID.String(),
			},
		},
	}

	queryPager := d.resources.NewQueryItemsPager(query, pk, &opt)

	for queryPager.More() {
		queryResponse, err := queryPager.NextPage(ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to advance page while querying Resources container for '%s': %w", resourceID, err)
		}

		for _, item := range queryResponse.Items {
			typedDoc, innerDoc, err := typedDocumentUnmarshal[ResourceDocument](item)
			if err != nil {
				return nil, nil, fmt.Errorf("failed to unmarshal Resources container item for '%s': %w", resourceID, err)
			}

			return typedDoc, innerDoc, nil
		}
	}

	return nil, nil, fmt.Errorf("failed to read Resources container item for '%s': %w", resourceID, ErrNotFound)
}

// GetResourceDoc retrieves a resource document from the "resources" DB using resource ID
func (d *cosmosDBClient) GetResourceDoc(ctx context.Context, resourceID *azcorearm.ResourceID) (*ResourceDocument, error) {
	_, innerDoc, err := d.getResourceDoc(ctx, resourceID)
	if err != nil {
		return nil, err
	}

	// Replace the key field from Cosmos with the given resourceID,
	// which typically comes from the URL. This helps preserve the
	// casing of the resource group and resource name from the URL
	// to meet RPC requirements:
	//
	// Put Resource | Arguments
	//
	// The resource group names and resource names should be matched
	// case insensitively. ... Additionally, the Resource Provier must
	// preserve the casing provided by the user. The service must return
	// the most recently specified casing to the client and must not
	// normalize or return a toupper or tolower form of the resource
	// group or resource name. The resource group name and resource
	// name must come from the URL and not the request body.
	innerDoc.ResourceID = resourceID

	return innerDoc, nil
}

// CreateResourceDoc creates a resource document in the "resources" DB during resource creation
func (d *cosmosDBClient) CreateResourceDoc(ctx context.Context, doc *ResourceDocument) error {
	typedDoc := newTypedDocument(doc.ResourceID.SubscriptionID, doc.ResourceID.ResourceType)

	data, err := typedDocumentMarshal(typedDoc, doc)
	if err != nil {
		return fmt.Errorf("failed to marshal Resources container item for '%s': %w", doc.ResourceID, err)
	}

	_, err = d.resources.CreateItem(ctx, typedDoc.getPartitionKey(), data, nil)
	if err != nil {
		return fmt.Errorf("failed to create Resources container item for '%s': %w", doc.ResourceID, err)
	}

	return nil
}

// UpdateResourceDoc updates a resource document by first fetching the document and passing it to
// the provided callback for modifications to be applied. It then attempts to replace the existing
// document with the modified document and an "etag" precondition. Upon a precondition failure the
// function repeats for a limited number of times before giving up.
//
// The callback function should return true if modifications were applied, signaling to proceed
// with the document replacement. The boolean return value reflects this: returning true if the
// document was sucessfully replaced, or false with or without an error to indicate no change.
func (d *cosmosDBClient) UpdateResourceDoc(ctx context.Context, resourceID *azcorearm.ResourceID, callback func(*ResourceDocument) bool) (bool, error) {
	var err error

	options := &azcosmos.ItemOptions{}

	for try := 0; try < 5; try++ {
		var typedDoc *typedDocument
		var innerDoc *ResourceDocument
		var data []byte

		typedDoc, innerDoc, err = d.getResourceDoc(ctx, resourceID)
		if err != nil {
			return false, err
		}

		if !callback(innerDoc) {
			return false, nil
		}

		data, err = typedDocumentMarshal(typedDoc, innerDoc)
		if err != nil {
			return false, fmt.Errorf("failed to marshal Resources container item for '%s': %w", resourceID, err)
		}

		options.IfMatchEtag = &typedDoc.CosmosETag
		_, err = d.resources.ReplaceItem(ctx, typedDoc.getPartitionKey(), typedDoc.ID, data, options)
		if err == nil {
			return true, nil
		}

		var responseError *azcore.ResponseError
		err = fmt.Errorf("failed to replace Resources container item for '%s': %w", resourceID, err)
		if !errors.As(err, &responseError) || responseError.StatusCode != http.StatusPreconditionFailed {
			return false, err
		}
	}

	return false, err
}

// DeleteResourceDoc removes a resource document from the "resources" DB using resource ID
func (d *cosmosDBClient) DeleteResourceDoc(ctx context.Context, resourceID *azcorearm.ResourceID) error {
	typedDoc, _, err := d.getResourceDoc(ctx, resourceID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		return err
	}

	_, err = d.resources.DeleteItem(ctx, typedDoc.getPartitionKey(), typedDoc.ID, nil)
	if err != nil {
		return fmt.Errorf("failed to delete Resources container item for '%s': %w", resourceID, err)
	}
	return nil
}

// ListResourceDocs searches for resource documents that match the given resource ID prefix.
// maxItems can limit the number of items returned at once. A negative value will cause the
// returned iterator to yield all matching items. A positive value will cause the returned
// iterator to include a continuation token if additional items are available.
func (d *cosmosDBClient) ListResourceDocs(prefix *azcorearm.ResourceID, maxItems int32, continuationToken *string) DBClientIterator[ResourceDocument] {
	pk := NewPartitionKey(prefix.SubscriptionID)

	// XXX The Cosmos DB REST API gives special meaning to -1 for "x-ms-max-item-count"
	//     but it's not clear if it treats all negative values equivalently. The Go SDK
	//     passes the PageSizeHint value as provided so normalize negative values to -1
	//     to be safe.
	maxItems = max(maxItems, -1)

	const query = "SELECT * FROM c WHERE STARTSWITH(c.properties.resourceId, @prefix, true)"
	opt := azcosmos.QueryOptions{
		PageSizeHint:      maxItems,
		ContinuationToken: continuationToken,
		QueryParameters: []azcosmos.QueryParameter{
			{
				Name:  "@prefix",
				Value: prefix.String() + "/",
			},
		},
	}

	pager := d.resources.NewQueryItemsPager(query, pk, &opt)

	if maxItems > 0 {
		return newQueryItemsSinglePageIterator[ResourceDocument](pager)
	} else {
		return newQueryItemsIterator[ResourceDocument](pager)
	}
}

func (d *cosmosDBClient) getOperationDoc(ctx context.Context, pk azcosmos.PartitionKey, operationID string) (*typedDocument, *OperationDocument, error) { //nolint:staticcheck
	// Make sure lookup keys are lowercase.
	operationID = strings.ToLower(operationID)

	pk = NewPartitionKey(operationsPartitionKey) //nolint:staticcheck

	response, err := d.operations.ReadItem(ctx, pk, operationID, nil)
	if err != nil {
		if isResponseError(err, http.StatusNotFound) {
			err = ErrNotFound
		}
		return nil, nil, fmt.Errorf("failed to read Operations container item for '%s': %w", operationID, err)
	}

	typedDoc, innerDoc, err := typedDocumentUnmarshal[OperationDocument](response.Value)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to unmarshal Operations container item for '%s': %w", operationID, err)
	}

	return typedDoc, innerDoc, nil
}

// GetOperationDoc retrieves the asynchronous operation document for the given
// operation ID from the "operations" container
func (d *cosmosDBClient) GetOperationDoc(ctx context.Context, pk azcosmos.PartitionKey, operationID string) (*OperationDocument, error) {
	_, innerDoc, err := d.getOperationDoc(ctx, pk, operationID)
	return innerDoc, err
}

// CreateOperationDoc writes an asynchronous operation document to the "operations"
// container
func (d *cosmosDBClient) CreateOperationDoc(ctx context.Context, doc *OperationDocument) (string, error) {
	typedDoc := newTypedDocument(operationsPartitionKey, OperationResourceType)

	data, err := typedDocumentMarshal(typedDoc, doc)
	if err != nil {
		return "", fmt.Errorf("failed to marshal Operations container item for '%s': %w", typedDoc.ID, err)
	}

	_, err = d.operations.CreateItem(ctx, typedDoc.getPartitionKey(), data, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create Operations container item for '%s': %w", typedDoc.ID, err)
	}

	return typedDoc.ID, nil
}

// UpdateOperationDoc updates an operation document by first fetching the document and
// passing it to the provided callback for modifications to be applied. It then attempts to
// replace the existing document with the modified document and an "etag" precondition. Upon
// a precondition failure the function repeats for a limited number of times before giving up.
//
// The callback function should return true if modifications were applied, signaling to proceed
// with the document replacement. The boolean return value reflects this: returning true if the
// document was successfully replaced, or false with or without an error to indicate no change.
func (d *cosmosDBClient) UpdateOperationDoc(ctx context.Context, pk azcosmos.PartitionKey, operationID string, callback func(*OperationDocument) bool) (bool, error) {
	var err error

	options := &azcosmos.ItemOptions{}

	for try := 0; try < 5; try++ {
		var typedDoc *typedDocument
		var innerDoc *OperationDocument
		var data []byte

		typedDoc, innerDoc, err = d.getOperationDoc(ctx, pk, operationID)
		if err != nil {
			return false, err
		}

		if !callback(innerDoc) {
			return false, nil
		}

		data, err = typedDocumentMarshal(typedDoc, innerDoc)
		if err != nil {
			return false, fmt.Errorf("failed to marshal Operations container item for '%s': %w", operationID, err)
		}

		options.IfMatchEtag = &typedDoc.CosmosETag
		_, err = d.operations.ReplaceItem(ctx, typedDoc.getPartitionKey(), typedDoc.ID, data, options)
		if err == nil {
			return true, nil
		}

		var responseError *azcore.ResponseError
		err = fmt.Errorf("failed to replace Operations container item for '%s': %w", operationID, err)
		if !errors.As(err, &responseError) || responseError.StatusCode != http.StatusPreconditionFailed {
			return false, err
		}
	}

	return false, err
}

func (d *cosmosDBClient) ListOperationDocs(subscriptionID string) DBClientIterator[OperationDocument] {
	pk := azcosmos.NewPartitionKeyString(operationsPartitionKey)

	const query = "SELECT * FROM c WHERE STRINGEQUALS(c.resourceType, @resourceType, true) AND STARTSWITH(c.externalId, @prefix, true)"
	opt := azcosmos.QueryOptions{
		QueryParameters: []azcosmos.QueryParameter{
			{
				Name:  "@resourceType",
				Value: OperationResourceType.String(),
			},
			{
				Name:  "@prefix",
				Value: "/subscriptions/" + strings.ToLower(subscriptionID),
			},
		},
	}

	pager := d.operations.NewQueryItemsPager(query, pk, &opt)

	return newQueryItemsIterator[OperationDocument](pager)
}

func (d *cosmosDBClient) getSubscriptionDoc(ctx context.Context, subscriptionID string) (*typedDocument, *arm.Subscription, error) {
	// Make sure lookup keys are lowercase.
	subscriptionID = strings.ToLower(subscriptionID)

	pk := NewPartitionKey(subscriptionID)

	response, err := d.subscriptions.ReadItem(ctx, pk, subscriptionID, nil)
	if err != nil {
		if isResponseError(err, http.StatusNotFound) {
			err = ErrNotFound
		}
		return nil, nil, fmt.Errorf("failed to read Subscriptions container item for '%s': %w", subscriptionID, err)
	}

	typedDoc, innerDoc, err := typedDocumentUnmarshal[arm.Subscription](response.Value)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to unmarshal Subscriptions container item for '%s': %w", subscriptionID, err)
	}

	// Expose the "_ts" field for metics reporting.
	innerDoc.LastUpdated = typedDoc.CosmosTimestamp

	return typedDoc, innerDoc, nil
}

// GetSubscriptionDoc retreives a subscription document from async DB using the subscription ID
func (d *cosmosDBClient) GetSubscriptionDoc(ctx context.Context, subscriptionID string) (*arm.Subscription, error) {
	_, innerDoc, err := d.getSubscriptionDoc(ctx, subscriptionID)
	return innerDoc, err
}

// CreateSubscriptionDoc creates/updates a subscription document in the async DB during cluster creation/patching
func (d *cosmosDBClient) CreateSubscriptionDoc(ctx context.Context, subscriptionID string, subscription *arm.Subscription) error {
	typedDoc := newTypedDocument(subscriptionID, azcorearm.SubscriptionResourceType)
	typedDoc.ID = strings.ToLower(subscriptionID)

	data, err := typedDocumentMarshal(typedDoc, subscription)
	if err != nil {
		return fmt.Errorf("failed to marshal Subscriptions container item for '%s': %w", subscriptionID, err)
	}

	_, err = d.subscriptions.CreateItem(ctx, typedDoc.getPartitionKey(), data, nil)
	if err != nil {
		return fmt.Errorf("failed to create Subscriptions container item for '%s': %w", subscriptionID, err)
	}

	// Add an item to the PartitionKeys container, which serves
	// as a partition key index for the Resources container.
	err = upsertPartitionKey(ctx, d.partitionKeys, subscriptionID)
	if err != nil {
		return fmt.Errorf("failed to upsert partition keys index for '%s': %w", subscriptionID, err)
	}

	return nil
}

// UpdateSubscriptionDoc updates a subscription document by first fetching the document and
// passing it to the provided callback for modifications to be applied. It then attempts to
// replace the existing document with the modified document and an "etag" precondition. Upon
// a precondition failure the function repeats for a limited number of times before giving up.
//
// The callback function should return true if modifications were applied, signaling to proceed
// with the document replacement. The boolean return value reflects this: returning true if the
// document was successfully replaced, or false with or without an error to indicate no change.
func (d *cosmosDBClient) UpdateSubscriptionDoc(ctx context.Context, subscriptionID string, callback func(*arm.Subscription) bool) (bool, error) {
	var err error

	options := &azcosmos.ItemOptions{}

	for try := 0; try < 5; try++ {
		var typedDoc *typedDocument
		var innerDoc *arm.Subscription
		var data []byte

		typedDoc, innerDoc, err = d.getSubscriptionDoc(ctx, subscriptionID)
		if err != nil {
			return false, err
		}

		if !callback(innerDoc) {
			return false, nil
		}

		data, err = typedDocumentMarshal(typedDoc, innerDoc)
		if err != nil {
			return false, fmt.Errorf("failed to marshal Subscriptions container item for '%s': %w", subscriptionID, err)
		}

		options.IfMatchEtag = &typedDoc.CosmosETag
		_, err = d.subscriptions.ReplaceItem(ctx, typedDoc.getPartitionKey(), typedDoc.ID, data, options)
		if err == nil {
			return true, nil
		}

		var responseError *azcore.ResponseError
		err = fmt.Errorf("failed to replace Subscriptions container item for '%s': %w", subscriptionID, err)
		if !errors.As(err, &responseError) || responseError.StatusCode != http.StatusPreconditionFailed {
			return false, err
		}
	}

	return false, err
}

func (d *cosmosDBClient) ListAllSubscriptionDocs() DBClientIterator[arm.Subscription] {
	return listPartitionKeys(d.partitionKeys, d)
}

// NewCosmosDatabaseClient instantiates a generic Cosmos database client.
func NewCosmosDatabaseClient(url string, dbName string, clientOptions azcore.ClientOptions) (*azcosmos.DatabaseClient, error) {
	credential, err := azidentity.NewDefaultAzureCredential(
		&azidentity.DefaultAzureCredentialOptions{
			ClientOptions: clientOptions,
		})
	if err != nil {
		return nil, err
	}

	client, err := azcosmos.NewClient(
		url,
		credential,
		&azcosmos.ClientOptions{
			ClientOptions: clientOptions,
		})
	if err != nil {
		return nil, err
	}

	return client.NewDatabase(dbName)
}
