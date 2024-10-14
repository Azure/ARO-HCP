package database

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"net/http"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/Azure/ARO-HCP/internal/api/arm"
)

const (
	billingContainer       = "Billing"
	locksContainer         = "Locks"
	operationsContainer    = "Operations"
	resourcesContainer     = "Resources"
	subscriptionsContainer = "Subscriptions"

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

type DBClientIterator interface {
	Items(ctx context.Context) iter.Seq[[]byte]
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
	GetResourceDoc(ctx context.Context, resourceID *arm.ResourceID) (*ResourceDocument, error)
	CreateResourceDoc(ctx context.Context, doc *ResourceDocument) error
	UpdateResourceDoc(ctx context.Context, resourceID *arm.ResourceID, callback func(*ResourceDocument) bool) (bool, error)
	// DeleteResourceDoc deletes a ResourceDocument from the database given the resourceID
	// of a Microsoft.RedHatOpenShift/HcpOpenShiftClusters resource or NodePools child resource.
	DeleteResourceDoc(ctx context.Context, resourceID *arm.ResourceID) error
	ListResourceDocs(ctx context.Context, prefix *arm.ResourceID, resourceType *azcorearm.ResourceType, pageSizeHint int32, continuationToken *string) ([]*ResourceDocument, *string, error)

	GetOperationDoc(ctx context.Context, operationID string) (*OperationDocument, error)
	CreateOperationDoc(ctx context.Context, doc *OperationDocument) error
	UpdateOperationDoc(ctx context.Context, operationID string, callback func(*OperationDocument) bool) (bool, error)
	DeleteOperationDoc(ctx context.Context, operationID string) error
	ListAllOperationDocs(ctx context.Context) DBClientIterator

	// GetSubscriptionDoc retrieves a SubscriptionDocument from the database given the subscriptionID.
	// ErrNotFound is returned if an associated SubscriptionDocument cannot be found.
	GetSubscriptionDoc(ctx context.Context, subscriptionID string) (*SubscriptionDocument, error)
	CreateSubscriptionDoc(ctx context.Context, doc *SubscriptionDocument) error
	UpdateSubscriptionDoc(ctx context.Context, subscriptionID string, callback func(*SubscriptionDocument) bool) (bool, error)
}

var _ DBClient = &CosmosDBClient{}

// CosmosDBClient defines the needed values to perform CRUD operations against the async DB
type CosmosDBClient struct {
	database      *azcosmos.DatabaseClient
	resources     *azcosmos.ContainerClient
	operations    *azcosmos.ContainerClient
	subscriptions *azcosmos.ContainerClient
	lockClient    *LockClient
}

// NewCosmosDBClient instantiates a Cosmos DatabaseClient targeting Frontends async DB
func NewCosmosDBClient(ctx context.Context, database *azcosmos.DatabaseClient) (DBClient, error) {
	// NewContainer only fails if the container ID argument is
	// empty, so we can safely disregard the error return value.
	resources, _ := database.NewContainer(resourcesContainer)
	operations, _ := database.NewContainer(operationsContainer)
	subscriptions, _ := database.NewContainer(subscriptionsContainer)
	locks, _ := database.NewContainer(locksContainer)

	lockClient, err := NewLockClient(ctx, locks)
	if err != nil {
		return nil, err
	}

	return &CosmosDBClient{
		database:      database,
		resources:     resources,
		operations:    operations,
		subscriptions: subscriptions,
		lockClient:    lockClient,
	}, nil
}

// DBConnectionTest checks the async database is accessible on startup
func (d *CosmosDBClient) DBConnectionTest(ctx context.Context) error {
	if _, err := d.database.Read(ctx, nil); err != nil {
		return fmt.Errorf("failed to read Cosmos database information during healthcheck: %v", err)
	}

	return nil
}

func (d *CosmosDBClient) GetLockClient() *LockClient {
	return d.lockClient
}

// GetResourceDoc retrieves a resource document from the "resources" DB using resource ID
func (d *CosmosDBClient) GetResourceDoc(ctx context.Context, resourceID *arm.ResourceID) (*ResourceDocument, error) {
	// Make sure partition key is lowercase.
	pk := azcosmos.NewPartitionKeyString(strings.ToLower(resourceID.SubscriptionID))

	query := "SELECT * FROM c WHERE STRINGEQUALS(c.key, @key, true)"
	opt := azcosmos.QueryOptions{
		PageSizeHint:    1,
		QueryParameters: []azcosmos.QueryParameter{{Name: "@key", Value: resourceID.String()}},
	}

	queryPager := d.resources.NewQueryItemsPager(query, pk, &opt)

	var doc *ResourceDocument
	for queryPager.More() {
		queryResponse, err := queryPager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to advance page while querying Resources container for '%s': %w", resourceID, err)
		}

		for _, item := range queryResponse.Items {
			err = json.Unmarshal(item, &doc)
			if err != nil {
				return nil, fmt.Errorf("failed to unmarshal Resources container item for '%s': %w", resourceID, err)
			}
		}
	}
	if doc != nil {
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
		doc.Key = resourceID
		return doc, nil
	}
	return nil, fmt.Errorf("failed to read Resources container item for '%s': %w", resourceID, ErrNotFound)
}

// CreateResourceDoc creates a resource document in the "resources" DB during resource creation
func (d *CosmosDBClient) CreateResourceDoc(ctx context.Context, doc *ResourceDocument) error {
	// Make sure partition key is lowercase.
	doc.PartitionKey = strings.ToLower(doc.PartitionKey)

	data, err := json.Marshal(doc)
	if err != nil {
		return fmt.Errorf("failed to marshal Resources container item for '%s': %w", doc.Key, err)
	}

	_, err = d.resources.CreateItem(ctx, azcosmos.NewPartitionKeyString(doc.PartitionKey), data, nil)
	if err != nil {
		return fmt.Errorf("failed to create Resources container item for '%s': %w", doc.Key, err)
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
func (d *CosmosDBClient) UpdateResourceDoc(ctx context.Context, resourceID *arm.ResourceID, callback func(*ResourceDocument) bool) (bool, error) {
	var err error

	// Make sure partition key is lowercase.
	pk := azcosmos.NewPartitionKeyString(strings.ToLower(resourceID.SubscriptionID))

	options := &azcosmos.ItemOptions{}

	for try := 0; try < 5; try++ {
		var doc *ResourceDocument
		var data []byte

		doc, err = d.GetResourceDoc(ctx, resourceID)
		if err != nil {
			return false, err
		}

		if !callback(doc) {
			return false, nil
		}

		data, err = json.Marshal(doc)
		if err != nil {
			return false, fmt.Errorf("failed to marshal Resources container item for '%s': %w", resourceID, err)
		}

		options.IfMatchEtag = &doc.ETag
		_, err = d.resources.ReplaceItem(ctx, pk, doc.ID, data, options)
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
func (d *CosmosDBClient) DeleteResourceDoc(ctx context.Context, resourceID *arm.ResourceID) error {
	// Make sure partition key is lowercase.
	pk := azcosmos.NewPartitionKeyString(strings.ToLower(resourceID.SubscriptionID))

	doc, err := d.GetResourceDoc(ctx, resourceID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		return err
	}

	_, err = d.resources.DeleteItem(ctx, pk, doc.ID, nil)
	if err != nil {
		return fmt.Errorf("failed to delete Resources container item for '%s': %w", resourceID, err)
	}
	return nil
}

func (d *CosmosDBClient) ListResourceDocs(ctx context.Context, prefix *arm.ResourceID, resourceType *azcorearm.ResourceType, pageSizeHint int32, continuationToken *string) ([]*ResourceDocument, *string, error) {
	// Make sure partition key is lowercase.
	pk := azcosmos.NewPartitionKeyString(strings.ToLower(prefix.SubscriptionID))

	query := "SELECT * FROM c WHERE STARTSWITH(c.key, @prefix, true)"
	opt := azcosmos.QueryOptions{
		PageSizeHint:      pageSizeHint,
		ContinuationToken: continuationToken,
		QueryParameters: []azcosmos.QueryParameter{
			{
				Name:  "@prefix",
				Value: prefix.String() + "/",
			},
		},
	}

	var response azcosmos.QueryItemsResponse
	resourceDocs := make([]*ResourceDocument, 0, pageSizeHint)

	// Loop until we fill the pre-allocated resourceDocs slice,
	// or until we run out of items from the resources container.
	for opt.PageSizeHint > 0 {
		var err error

		response, err = d.resources.NewQueryItemsPager(query, pk, &opt).NextPage(ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to advance page while querying Resources container for items with a key prefix of '%s': %w", prefix, err)
		}

		for _, item := range response.Items {
			var doc ResourceDocument
			err = json.Unmarshal(item, &doc)
			if err != nil {
				return nil, nil, fmt.Errorf("failed to unmarshal item while querying Resources container for items with a key prefix of '%s': %w", prefix, err)
			}
			if resourceType == nil || strings.EqualFold(resourceType.String(), doc.Key.ResourceType.String()) {
				resourceDocs = append(resourceDocs, &doc)
			}
		}

		if response.ContinuationToken == nil {
			break
		}

		opt.PageSizeHint = int32(cap(resourceDocs) - len(resourceDocs))
		opt.ContinuationToken = response.ContinuationToken
	}

	return resourceDocs, response.ContinuationToken, nil
}

// GetOperationDoc retrieves the asynchronous operation document for the given
// operation ID from the "operations" container
func (d *CosmosDBClient) GetOperationDoc(ctx context.Context, operationID string) (*OperationDocument, error) {
	// Make sure lookup keys are lowercase.
	operationID = strings.ToLower(operationID)

	pk := azcosmos.NewPartitionKeyString(operationsPartitionKey)

	response, err := d.operations.ReadItem(ctx, pk, operationID, nil)
	if err != nil {
		if isResponseError(err, http.StatusNotFound) {
			err = ErrNotFound
		}
		return nil, fmt.Errorf("failed to read Operations container item for '%s': %w", operationID, err)
	}

	var doc *OperationDocument
	err = json.Unmarshal(response.Value, &doc)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal Operations container item for '%s': %w", operationID, err)
	}

	return doc, nil
}

// CreateOperationDoc writes an asynchronous operation document to the "operations"
// container
func (d *CosmosDBClient) CreateOperationDoc(ctx context.Context, doc *OperationDocument) error {
	pk := azcosmos.NewPartitionKeyString(operationsPartitionKey)

	data, err := json.Marshal(doc)
	if err != nil {
		return fmt.Errorf("failed to marshal Operations container item for '%s': %w", doc.ID, err)
	}

	_, err = d.operations.CreateItem(ctx, pk, data, nil)
	if err != nil {
		return fmt.Errorf("failed to create Operations container item for '%s': %w", doc.ID, err)
	}

	return nil
}

// UpdateOperationDoc updates an operation document by first fetching the document and
// passing it to the provided callback for modifications to be applied. It then attempts to
// replace the existing document with the modified document and an "etag" precondition. Upon
// a precondition failure the function repeats for a limited number of times before giving up.
//
// The callback function should return true if modifications were applied, signaling to proceed
// with the document replacement. The boolean return value reflects this: returning true if the
// document was successfully replaced, or false with or without an error to indicate no change.
func (d *CosmosDBClient) UpdateOperationDoc(ctx context.Context, operationID string, callback func(*OperationDocument) bool) (bool, error) {
	var err error

	pk := azcosmos.NewPartitionKeyString(operationsPartitionKey)

	options := &azcosmos.ItemOptions{}

	for try := 0; try < 5; try++ {
		var doc *OperationDocument
		var data []byte

		doc, err = d.GetOperationDoc(ctx, operationID)
		if err != nil {
			return false, err
		}

		if !callback(doc) {
			return false, nil
		}

		data, err = json.Marshal(doc)
		if err != nil {
			return false, fmt.Errorf("failed to marshal Operations container item for '%s': %w", operationID, err)
		}

		options.IfMatchEtag = &doc.ETag
		_, err = d.operations.ReplaceItem(ctx, pk, doc.ID, data, options)
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

// DeleteOperationDoc deletes the asynchronous operation document for the given
// operation ID from the "operations" container
func (d *CosmosDBClient) DeleteOperationDoc(ctx context.Context, operationID string) error {
	// Make sure lookup keys are lowercase.
	operationID = strings.ToLower(operationID)

	pk := azcosmos.NewPartitionKeyString(operationsPartitionKey)

	_, err := d.operations.DeleteItem(ctx, pk, operationID, nil)
	if err != nil && !isResponseError(err, http.StatusNotFound) {
		return fmt.Errorf("failed to delete Operations container item for '%s': %w", operationID, err)
	}

	return nil
}

func (d *CosmosDBClient) ListAllOperationDocs(ctx context.Context) DBClientIterator {
	pk := azcosmos.NewPartitionKeyString(operationsPartitionKey)
	return NewQueryItemsIterator(d.operations.NewQueryItemsPager("SELECT * FROM c", pk, nil))
}

// GetSubscriptionDoc retreives a subscription document from async DB using the subscription ID
func (d *CosmosDBClient) GetSubscriptionDoc(ctx context.Context, subscriptionID string) (*SubscriptionDocument, error) {
	// Make sure lookup keys are lowercase.
	subscriptionID = strings.ToLower(subscriptionID)

	pk := azcosmos.NewPartitionKeyString(subscriptionID)

	response, err := d.subscriptions.ReadItem(ctx, pk, subscriptionID, nil)
	if err != nil {
		if isResponseError(err, http.StatusNotFound) {
			err = ErrNotFound
		}
		return nil, fmt.Errorf("failed to read Subscriptions container item for '%s': %w", subscriptionID, err)
	}

	var doc *SubscriptionDocument
	err = json.Unmarshal(response.Value, &doc)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal Subscriptions container item for '%s': %w", subscriptionID, err)
	}

	return doc, nil
}

// CreateSubscriptionDoc creates/updates a subscription document in the async DB during cluster creation/patching
func (d *CosmosDBClient) CreateSubscriptionDoc(ctx context.Context, doc *SubscriptionDocument) error {
	// Make sure lookup keys are lowercase.
	doc.ID = strings.ToLower(doc.ID)

	pk := azcosmos.NewPartitionKeyString(doc.ID)

	data, err := json.Marshal(doc)
	if err != nil {
		return fmt.Errorf("failed to marshal Subscriptions container item for '%s': %w", doc.ID, err)
	}

	_, err = d.subscriptions.CreateItem(ctx, pk, data, nil)
	if err != nil {
		return fmt.Errorf("failed to create Subscriptions container item for '%s': %w", doc.ID, err)
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
func (d *CosmosDBClient) UpdateSubscriptionDoc(ctx context.Context, subscriptionID string, callback func(*SubscriptionDocument) bool) (bool, error) {
	var err error

	// Make sure partition key is lowercase.
	pk := azcosmos.NewPartitionKeyString(strings.ToLower(subscriptionID))

	options := &azcosmos.ItemOptions{}

	for try := 0; try < 5; try++ {
		var doc *SubscriptionDocument
		var data []byte

		doc, err = d.GetSubscriptionDoc(ctx, subscriptionID)
		if err != nil {
			return false, err
		}

		if !callback(doc) {
			return false, nil
		}

		data, err = json.Marshal(doc)
		if err != nil {
			return false, fmt.Errorf("failed to marshal Subscriptions container item for '%s': %w", subscriptionID, err)
		}

		options.IfMatchEtag = &doc.ETag
		_, err = d.subscriptions.ReplaceItem(ctx, pk, doc.ID, data, options)
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
