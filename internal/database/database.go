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

package database

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
	billingContainer   = "Billing"
	locksContainer     = "Locks"
	resourcesContainer = "Resources"

	operationTimeToLive = 604800 // 7 days
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

// DBClientListActiveOperationDocsOptions allows for limiting the results of DBClient.ListActiveOperationDocs.
type DBClientListActiveOperationDocsOptions struct {
	// Request matches the type of asynchronous operation requested
	Request *OperationRequest
	// ExternalID matches (case-insensitively) the Azure resource ID of the cluster or node pool
	ExternalID *azcorearm.ResourceID
}

// DBClient provides a customized interface to the Cosmos DB containers used by the
// ARO-HCP resource provider.
type DBClient interface {
	// DBConnectionTest verifies the database is reachable. Intended for use in health checks.
	DBConnectionTest(ctx context.Context) error

	// GetLockClient returns a LockClient, or nil if the DBClient does not support a LockClient.
	GetLockClient() *LockClient

	// GetResourceDoc queries the "Resources" container for a cluster or node pool document with a
	// matching resourceID.
	GetResourceDoc(ctx context.Context, resourceID *azcorearm.ResourceID) (*ResourceDocument, error)

	// CreateResourceDoc creates a new cluster or node pool document in the "Resources" container.
	CreateResourceDoc(ctx context.Context, doc *ResourceDocument) error

	// UpdateResourceDoc updates a cluster or node pool document in the "Resources" container by
	// first fetching the document and passing it to the provided callback for modifications to be
	// applied. It then attempts to replace the existing document with the modified document and an
	// "etag" precondition. Upon a precondition failure the function repeats for a limited number of
	// times before giving up.
	//
	// The callback function should return true if modifications were applied, signaling to proceed
	// with the document replacement. The boolean return value reflects this: returning true if the
	// document was successfully replaced, or false with or without an error to indicate no change.
	UpdateResourceDoc(ctx context.Context, resourceID *azcorearm.ResourceID, callback func(*ResourceDocument) bool) (bool, error)

	// DeleteResourceDoc deletes a cluster or node pool document in the "Resources" container.
	// If no matching document is found, DeleteResourceDoc returns nil as though it had succeeded.
	DeleteResourceDoc(ctx context.Context, resourceID *azcorearm.ResourceID) error

	// ListResourceDocs returns an iterator that searches for cluster or node pool documents in
	// the "Resources" container that match the given resource ID prefix. The prefix must include
	// a subscription ID so the correct partition key can be inferred.
	//
	// Note that ListResourceDocs does not perform the search, but merely prepares an iterator to
	// do so. Hence the lack of a Context argument. The search is performed by calling Items() on
	// the iterator in a ranged for loop.
	//
	// maxItems can limit the number of items returned at once. A negative value will cause the
	// returned iterator to yield all matching documents. A positive value will cause the returned
	// iterator to include a continuation token if additional items are available. The continuation
	// token can be supplied on a subsequent call to obtain those additional items.
	ListResourceDocs(prefix *azcorearm.ResourceID, maxItems int32, continuationToken *string) DBClientIterator[ResourceDocument]

	// GetOperationDoc retrieves an asynchronous operation document from the "Resources" container.
	GetOperationDoc(ctx context.Context, pk azcosmos.PartitionKey, operationID string) (*OperationDocument, error)

	// CreateResourceDoc creates a new asynchronous operation document in the "Resources" container.
	CreateOperationDoc(ctx context.Context, doc *OperationDocument) (string, error)

	// UpdateOperationDoc updates an asynchronous operation document in the "Resources" container
	// by first fetching the document and passing it to the provided callback for modifications to
	// be applied. It then attempts to replace the existing document with the modified document and
	// an "etag" precondition. Upon a precondition failure the function repeats for a limited number
	// of times before giving up.
	//
	// The callback function should return true if modifications were applied, signaling to proceed
	// with the document replacement. The boolean return value reflects this: returning true if the
	// document was successfully replaced, or false with or without an error to indicate no change.
	UpdateOperationDoc(ctx context.Context, pk azcosmos.PartitionKey, operationID string, callback func(*OperationDocument) bool) (bool, error)

	// ListActiveOperationDocs returns an iterator that searches for asynchronous operation documents
	// with a non-terminal status in the "Resources" container under the given partition key. The
	// options argument can further limit the search to documents that match the provided values.
	//
	// Note that ListActiveOperationDocs does not perform the search, but merely prepares an iterator
	// to do so. Hence the lack of a Context argument. The search is performed by calling Items() on
	// the iterator in a ranged for loop.
	ListActiveOperationDocs(pk azcosmos.PartitionKey, options *DBClientListActiveOperationDocsOptions) DBClientIterator[OperationDocument]

	// GetSubscriptionDoc retrieves a subscription document from the "Resources" container.
	GetSubscriptionDoc(ctx context.Context, subscriptionID string) (*arm.Subscription, error)

	// CreateSubscriptionDoc creates a new subscription document in the "Resources" container.
	CreateSubscriptionDoc(ctx context.Context, subscriptionID string, subscription *arm.Subscription) error

	// UpdateSubscriptionDoc updates a subscription document in the "Resources" container by first
	// fetching the document and passing it to the provided callback for modifications to be applied.
	// It then attempts to replace the existing document with the modified document an an "etag"
	// precondition. Upon a precondition failure the function repeats for a limited number of times
	// before giving up.
	//
	// The callback function should return true if modifications were applied, signaling to proceed
	// with the document replacement. The boolean return value reflects this: returning true if the
	// document was successfully replaced, or false with or without an error to indicate no change.
	UpdateSubscriptionDoc(ctx context.Context, subscriptionID string, callback func(*arm.Subscription) bool) (bool, error)

	// ListAllSubscriptionDocs() returns an iterator that searches for all subscription documents in
	// the "Resources" container. Since the "Resources" container is partitioned by subscription ID,
	// there will only be one subscription document per logical partition. Thus, this method enables
	// iterating over all the logical partitions in the "Resources" container.
	//
	// Note that ListAllSubscriptionDocs does not perform the search, but merely prepares an iterator
	// to do so. Hence the lack of a Context argument. The search is performed by calling Items() on
	// the iterator in a ranged for loop.
	ListAllSubscriptionDocs() DBClientIterator[arm.Subscription]
}

var _ DBClient = &cosmosDBClient{}

// cosmosDBClient defines the needed values to perform CRUD operations against Cosmos DB.
type cosmosDBClient struct {
	database   *azcosmos.DatabaseClient
	resources  *azcosmos.ContainerClient
	lockClient *LockClient
}

// NewDBClient instantiates a DBClient from a Cosmos DatabaseClient instance
// targeting the Frontends async database.
func NewDBClient(ctx context.Context, database *azcosmos.DatabaseClient) (DBClient, error) {
	// NewContainer only fails if the container ID argument is
	// empty, so we can safely disregard the error return value.
	resources, _ := database.NewContainer(resourcesContainer)
	locks, _ := database.NewContainer(locksContainer)

	lockClient, err := NewLockClient(ctx, locks)
	if err != nil {
		return nil, err
	}

	return &cosmosDBClient{
		database:   database,
		resources:  resources,
		lockClient: lockClient,
	}, nil
}

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

func (d *cosmosDBClient) getOperationDoc(ctx context.Context, pk azcosmos.PartitionKey, operationID string) (*typedDocument, *OperationDocument, error) {
	// Make sure lookup keys are lowercase.
	operationID = strings.ToLower(operationID)

	response, err := d.resources.ReadItem(ctx, pk, operationID, nil)
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

func (d *cosmosDBClient) GetOperationDoc(ctx context.Context, pk azcosmos.PartitionKey, operationID string) (*OperationDocument, error) {
	_, innerDoc, err := d.getOperationDoc(ctx, pk, operationID)
	return innerDoc, err
}

func (d *cosmosDBClient) CreateOperationDoc(ctx context.Context, doc *OperationDocument) (string, error) {
	// Make sure partition key is lowercase.
	subscriptionID := strings.ToLower(doc.ExternalID.SubscriptionID)

	typedDoc := newTypedDocument(subscriptionID, OperationResourceType)
	typedDoc.TimeToLive = operationTimeToLive

	data, err := typedDocumentMarshal(typedDoc, doc)
	if err != nil {
		return "", fmt.Errorf("failed to marshal Operations container item for '%s': %w", typedDoc.ID, err)
	}

	_, err = d.resources.CreateItem(ctx, typedDoc.getPartitionKey(), data, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create Operations container item for '%s': %w", typedDoc.ID, err)
	}

	return typedDoc.ID, nil
}

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
		_, err = d.resources.ReplaceItem(ctx, pk, typedDoc.ID, data, options)
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

func (d *cosmosDBClient) ListActiveOperationDocs(pk azcosmos.PartitionKey, options *DBClientListActiveOperationDocsOptions) DBClientIterator[OperationDocument] {
	var queryOptions azcosmos.QueryOptions

	query := fmt.Sprintf(
		"SELECT * FROM c WHERE STRINGEQUALS(c.resourceType, %q, true) "+
			"AND NOT ARRAYCONTAINS([%q, %q, %q], c.properties.status)",
		OperationResourceType.String(),
		arm.ProvisioningStateSucceeded,
		arm.ProvisioningStateFailed,
		arm.ProvisioningStateCanceled)

	if options != nil {
		if options.Request != nil {
			query += " AND c.properties.request == @request"
			queryParameter := azcosmos.QueryParameter{
				Name:  "@request",
				Value: string(*options.Request),
			}
			queryOptions.QueryParameters = append(queryOptions.QueryParameters, queryParameter)
		}

		if options.ExternalID != nil {
			query += " AND STRINGEQUALS(c.properties.externalId, @externalId, true)"
			queryParameter := azcosmos.QueryParameter{
				Name:  "@externalId",
				Value: options.ExternalID.String(),
			}
			queryOptions.QueryParameters = append(queryOptions.QueryParameters, queryParameter)
		}
	}

	pager := d.resources.NewQueryItemsPager(query, pk, &queryOptions)

	return newQueryItemsIterator[OperationDocument](pager)
}

func (d *cosmosDBClient) getSubscriptionDoc(ctx context.Context, subscriptionID string) (*typedDocument, *arm.Subscription, error) {
	// Make sure lookup keys are lowercase.
	subscriptionID = strings.ToLower(subscriptionID)

	pk := NewPartitionKey(subscriptionID)

	response, err := d.resources.ReadItem(ctx, pk, subscriptionID, nil)
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

func (d *cosmosDBClient) GetSubscriptionDoc(ctx context.Context, subscriptionID string) (*arm.Subscription, error) {
	_, innerDoc, err := d.getSubscriptionDoc(ctx, subscriptionID)
	return innerDoc, err
}

func (d *cosmosDBClient) CreateSubscriptionDoc(ctx context.Context, subscriptionID string, subscription *arm.Subscription) error {
	typedDoc := newTypedDocument(subscriptionID, azcorearm.SubscriptionResourceType)
	typedDoc.ID = strings.ToLower(subscriptionID)

	data, err := typedDocumentMarshal(typedDoc, subscription)
	if err != nil {
		return fmt.Errorf("failed to marshal Subscriptions container item for '%s': %w", subscriptionID, err)
	}

	_, err = d.resources.CreateItem(ctx, typedDoc.getPartitionKey(), data, nil)
	if err != nil {
		return fmt.Errorf("failed to create Subscriptions container item for '%s': %w", subscriptionID, err)
	}

	return nil
}

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
		_, err = d.resources.ReplaceItem(ctx, typedDoc.getPartitionKey(), typedDoc.ID, data, options)
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
	const query = "SELECT * FROM c WHERE STRINGEQUALS(c.resourceType, @resourceType, true)"
	opt := azcosmos.QueryOptions{
		QueryParameters: []azcosmos.QueryParameter{
			{
				Name:  "@resourceType",
				Value: azcorearm.SubscriptionResourceType.String(),
			},
		},
	}

	// Empty partition key triggers a cross-partition query.
	pager := d.resources.NewQueryItemsPager(query, azcosmos.NewPartitionKey(), &opt)

	return newQueryItemsIterator[arm.Subscription](pager)
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
