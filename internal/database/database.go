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
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"net/http"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
)

const (
	billingContainer   = "Billing"
	locksContainer     = "Locks"
	resourcesContainer = "Resources"

	operationTimeToLive = 604800 // 7 days
)

// ErrAmbiguousResult occurs when a database query intended
// to yield a single item unexpectedly yields multiple items.
var ErrAmbiguousResult = errors.New("ambiguous result")

func IsResponseError(err error, statusCode int) bool {
	if err == nil {
		return false
	}
	var responseError *azcore.ResponseError
	return errors.As(err, &responseError) && responseError.StatusCode == statusCode
}

// NewPartitionKey creates a partition key from an Azure subscription ID.
func NewPartitionKey(subscriptionID string) azcosmos.PartitionKey {
	return azcosmos.NewPartitionKeyString(strings.ToLower(subscriptionID))
}

type DBClientIteratorItem[T any] iter.Seq2[string, *T]

type DBClientIterator[T any] interface {
	Items(ctx context.Context) DBClientIteratorItem[T]
	GetContinuationToken() string
	GetError() error
}

// DBClientListResourceDocsOptions allows for limiting the results of DBClient.ListResourceDocs.
type DBClientListResourceDocsOptions struct {
	// ResourceType matches (case-insensitively) the Azure resource type. If unspecified,
	// DBClient.ListResourceDocs will match resource documents for any resource type.
	ResourceType *azcorearm.ResourceType

	// PageSizeHint can limit the number of items returned at once. A negative value will cause
	// the returned iterator to yield all matching documents (same as leaving the option nil).
	// A positive value will cause the returned iterator to include a continuation token if
	// additional items are available.
	PageSizeHint *int32

	// ContinuationToken can be supplied when limiting the number of items returned at once
	// through PageSizeHint.
	ContinuationToken *string
}

// DBClientListActiveOperationDocsOptions allows for limiting the results of DBClient.ListActiveOperationDocs.
type DBClientListActiveOperationDocsOptions struct {
	// Request matches the type of asynchronous operation requested
	Request *OperationRequest
	// ExternalID matches (case-insensitively) the Azure resource ID of the cluster or node pool
	ExternalID *azcorearm.ResourceID
	// IncludeNestedResources includes nested resources under ExternalID
	IncludeNestedResources bool
}

// DBClient provides a customized interface to the Cosmos DB containers used by the
// ARO-HCP resource provider.
type DBClient interface {
	// GetLockClient returns a LockClient, or nil if the DBClient does not support a LockClient.
	GetLockClient() LockClientInterface

	// NewTransaction initiates a new transactional batch for the given partition key.
	NewTransaction(pk azcosmos.PartitionKey) DBTransaction

	// CreateBillingDoc creates a new document in the "Billing" container.
	CreateBillingDoc(ctx context.Context, doc *BillingDocument) error

	// PatchBillingDoc patches a document in the "Billing" container by applying a sequence
	// of patch operations. The patch operations may include a precondition which, if not
	// satisfied, will cause the function to return an azcore.ResponseError with a StatusCode
	// of http.StatusPreconditionFailed.
	PatchBillingDoc(ctx context.Context, resourceID *azcorearm.ResourceID, ops BillingDocumentPatchOperations) error

	// GetHCPClusterCRUD retrieves a CRUD interface for managing HCPCluster resources and their nested resources.
	HCPClusters(subscriptionID, resourceGroupName string) HCPClusterCRUD

	// GetResourceDoc queries the "Resources" container for a cluster or node pool document with a
	// matching resourceID.
	GetResourceDoc(ctx context.Context, resourceID *azcorearm.ResourceID) (string, *ResourceDocument, error)

	// PatchResourceDoc patches a cluster or node pool document in the "Resources" container by
	// applying a sequence of patch operations. The patch operations may include a precondition
	// which, if not satisfied, will cause the function to return an azcore.ResponseError with
	// a StatusCode of http.StatusPreconditionFailed. If successful, PatchResourceDoc returns
	// the updated document.
	PatchResourceDoc(ctx context.Context, resourceID *azcorearm.ResourceID, ops ResourceDocumentPatchOperations) (*ResourceDocument, error)

	// DeleteResourceDoc deletes a cluster or node pool document in the "Resources" container.
	// If no matching document is found, DeleteResourceDoc returns nil as though it had succeeded.
	DeleteResourceDoc(ctx context.Context, resourceID *azcorearm.ResourceID) error

	// ListResourceDocs returns an iterator that searches for cluster or node pool documents in
	// the "Resources" container that match the given resource ID prefix. The prefix must include
	// a subscription ID so the correct partition key can be inferred. The options argument can
	// further limit the search to documents that match the provided values.
	//
	// Note that ListResourceDocs does not perform the search, but merely prepares an iterator to
	// do so. Hence the lack of a Context argument. The search is performed by calling Items() on
	// the iterator in a ranged for loop.
	ListResourceDocs(prefix *azcorearm.ResourceID, options *DBClientListResourceDocsOptions) DBClientIterator[ResourceDocument]

	// GetOperationDoc retrieves an asynchronous operation document from the "Resources" container.
	GetOperationDoc(ctx context.Context, pk azcosmos.PartitionKey, operationID string) (*OperationDocument, error)

	// CreateResourceDoc creates a new asynchronous operation document in the "Resources" container.
	CreateOperationDoc(ctx context.Context, doc *OperationDocument) (string, error)

	// PatchOperationDoc patches an asynchronous operation document in the "Resources" container
	// by applying a sequence of patch operations. The patch operations may include a precondition
	// which, if not satisfied, will cause the function to return an azcore.ResponseError with a
	// StatusCode of http.StatusPreconditionFailed. If successful, PatchOperationDoc returns the
	// updated document.
	PatchOperationDoc(ctx context.Context, pk azcosmos.PartitionKey, operationID string, ops OperationDocumentPatchOperations) (*OperationDocument, error)

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
	billing    *azcosmos.ContainerClient
	resources  *azcosmos.ContainerClient
	lockClient *LockClient
}

// NewDBClient instantiates a DBClient from a Cosmos DatabaseClient instance
// targeting the Frontends async database.
func NewDBClient(ctx context.Context, database *azcosmos.DatabaseClient) (DBClient, error) {
	resources, err := database.NewContainer(resourcesContainer)
	if err != nil {
		return nil, err
	}

	billing, err := database.NewContainer(billingContainer)
	if err != nil {
		return nil, err
	}

	locks, err := database.NewContainer(locksContainer)
	if err != nil {
		return nil, err
	}

	lockClient, err := NewLockClient(ctx, locks)
	if err != nil {
		return nil, err
	}

	return &cosmosDBClient{
		database:   database,
		billing:    billing,
		resources:  resources,
		lockClient: lockClient,
	}, nil
}

func (d *cosmosDBClient) GetLockClient() LockClientInterface {
	return d.lockClient
}

func (d *cosmosDBClient) NewTransaction(pk azcosmos.PartitionKey) DBTransaction {
	return newCosmosDBTransaction(pk, d.resources)
}

func (d *cosmosDBClient) getBillingID(ctx context.Context, resourceID *azcorearm.ResourceID) (string, error) {
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

	queryPager := d.billing.NewQueryItemsPager(query, pk, &opt)

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

func (d *cosmosDBClient) CreateBillingDoc(ctx context.Context, doc *BillingDocument) error {
	if doc.ResourceID == nil {
		return errors.New("BillingDocument is missing a ResourceID")
	}

	pk := NewPartitionKey(doc.ResourceID.SubscriptionID)

	data, err := json.Marshal(doc)
	if err != nil {
		return fmt.Errorf("failed to marshal Billing container item for '%s': %w", doc.ResourceID, err)
	}

	_, err = d.billing.CreateItem(ctx, pk, data, nil)
	if err != nil {
		return fmt.Errorf("failed to create Billing container item for '%s': %w", doc.ResourceID, err)
	}

	return nil
}

func (d *cosmosDBClient) PatchBillingDoc(ctx context.Context, resourceID *azcorearm.ResourceID, ops BillingDocumentPatchOperations) error {
	billingID, err := d.getBillingID(ctx, resourceID)
	if err != nil {
		return err
	}

	pk := NewPartitionKey(resourceID.SubscriptionID)

	_, err = d.billing.PatchItem(ctx, pk, billingID, ops.PatchOperations, nil)
	if err != nil {
		return fmt.Errorf("failed to patch Billing container item for '%s': %w", resourceID, err)
	}

	return nil
}

func (d *cosmosDBClient) getResourceDoc(ctx context.Context, resourceID *azcorearm.ResourceID) (*TypedDocument, *ResourceDocument, error) {
	var responseItem []byte

	pk := NewPartitionKey(resourceID.SubscriptionID)

	const query = "SELECT * FROM c WHERE STRINGEQUALS(c.resourceType, @resourceType, true) AND STRINGEQUALS(c.properties.resourceId, @resourceId, true)"
	opt := azcosmos.QueryOptions{
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
			// Let the pager finish to ensure we get a single result.
			if responseItem == nil {
				responseItem = item
			} else {
				return nil, nil, ErrAmbiguousResult
			}
		}
	}

	if responseItem == nil {
		// Fabricate a "404 Not Found" ResponseError to wrap.
		err := &azcore.ResponseError{StatusCode: http.StatusNotFound}
		return nil, nil, fmt.Errorf("failed to read Resources container item for '%s': %w", resourceID, err)
	}

	typedDoc, innerDoc, err := typedDocumentUnmarshal[ResourceDocument](responseItem)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to unmarshal Resources container item for '%s': %w", resourceID, err)
	}

	return typedDoc, innerDoc, nil
}

func (d *cosmosDBClient) GetResourceDoc(ctx context.Context, resourceID *azcorearm.ResourceID) (string, *ResourceDocument, error) {
	typedDoc, innerDoc, err := d.getResourceDoc(ctx, resourceID)
	if err != nil {
		return "", nil, err
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

	return typedDoc.ID, innerDoc, nil
}

func (d *cosmosDBClient) PatchResourceDoc(ctx context.Context, resourceID *azcorearm.ResourceID, ops ResourceDocumentPatchOperations) (*ResourceDocument, error) {
	typedDoc, _, err := d.getResourceDoc(ctx, resourceID)
	if err != nil {
		return nil, err
	}

	options := &azcosmos.ItemOptions{EnableContentResponseOnWrite: true}
	response, err := d.resources.PatchItem(ctx, typedDoc.getPartitionKey(), typedDoc.ID, ops.PatchOperations, options)
	if err != nil {
		return nil, fmt.Errorf("failed to patch Resources container item for '%s': %w", resourceID, err)
	}

	_, innerDoc, err := typedDocumentUnmarshal[ResourceDocument](response.Value)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal Resources container item for '%s': %w", resourceID, err)
	}

	return innerDoc, nil
}

func (d *cosmosDBClient) DeleteResourceDoc(ctx context.Context, resourceID *azcorearm.ResourceID) error {
	typedDoc, _, err := d.getResourceDoc(ctx, resourceID)
	if err != nil {
		if IsResponseError(err, http.StatusNotFound) {
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

func (d *cosmosDBClient) ListResourceDocs(prefix *azcorearm.ResourceID, options *DBClientListResourceDocsOptions) DBClientIterator[ResourceDocument] {
	pk := NewPartitionKey(prefix.SubscriptionID)

	query := "SELECT * FROM c WHERE STARTSWITH(c.properties.resourceId, @prefix, true)"

	queryOptions := azcosmos.QueryOptions{
		PageSizeHint: -1,
		QueryParameters: []azcosmos.QueryParameter{
			{
				Name:  "@prefix",
				Value: prefix.String() + "/",
			},
		},
	}

	if options != nil {
		if options.ResourceType != nil {
			query += " AND STRINGEQUALS(c.resourceType, @resourceType, true)"
			queryParameter := azcosmos.QueryParameter{
				Name:  "@resourceType",
				Value: string(options.ResourceType.String()),
			}
			queryOptions.QueryParameters = append(queryOptions.QueryParameters, queryParameter)
		}

		// XXX The Cosmos DB REST API gives special meaning to -1 for "x-ms-max-item-count"
		//     but it's not clear if it treats all negative values equivalently. The Go SDK
		//     passes the PageSizeHint value as provided so normalize negative values to -1
		//     to be safe.
		if options.PageSizeHint != nil {
			queryOptions.PageSizeHint = max(*options.PageSizeHint, -1)
		}
		queryOptions.ContinuationToken = options.ContinuationToken
	}

	pager := d.resources.NewQueryItemsPager(query, pk, &queryOptions)

	if queryOptions.PageSizeHint > 0 {
		return newQueryItemsSinglePageIterator[ResourceDocument](pager)
	} else {
		return newQueryItemsIterator[ResourceDocument](pager)
	}
}

func (d *cosmosDBClient) getOperationDoc(ctx context.Context, pk azcosmos.PartitionKey, operationID string) (*TypedDocument, *OperationDocument, error) {
	// Make sure lookup keys are lowercase.
	operationID = strings.ToLower(operationID)

	response, err := d.resources.ReadItem(ctx, pk, operationID, nil)
	if err != nil {
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

func (d *cosmosDBClient) PatchOperationDoc(ctx context.Context, pk azcosmos.PartitionKey, operationID string, ops OperationDocumentPatchOperations) (*OperationDocument, error) {
	options := &azcosmos.ItemOptions{EnableContentResponseOnWrite: true}
	response, err := d.resources.PatchItem(ctx, pk, operationID, ops.PatchOperations, options)
	if err != nil {
		return nil, fmt.Errorf("failed to patch Operations container item for '%s': %w", operationID, err)
	}

	_, innerDoc, err := typedDocumentUnmarshal[OperationDocument](response.Value)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal Operations container item for '%s': %w", operationID, err)
	}

	return innerDoc, nil
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
			query += " AND c.properties.request = @request"
			queryParameter := azcosmos.QueryParameter{
				Name:  "@request",
				Value: string(*options.Request),
			}
			queryOptions.QueryParameters = append(queryOptions.QueryParameters, queryParameter)
		}

		if options.ExternalID != nil {
			query += " AND "
			const resourceFilter = "STRINGEQUALS(c.properties.externalId, @externalId, true)"
			if options.IncludeNestedResources {
				const nestedResourceFilter = "STARTSWITH(c.properties.externalId, CONCAT(@externalId, \"/\"), true)"
				query += fmt.Sprintf("(%s OR %s)", resourceFilter, nestedResourceFilter)
			} else {
				query += resourceFilter
			}
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

func (d *cosmosDBClient) getSubscriptionDoc(ctx context.Context, subscriptionID string) (*TypedDocument, *arm.Subscription, error) {
	// Make sure lookup keys are lowercase.
	subscriptionID = strings.ToLower(subscriptionID)

	pk := NewPartitionKey(subscriptionID)

	response, err := d.resources.ReadItem(ctx, pk, subscriptionID, nil)
	if err != nil {
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
		var typedDoc *TypedDocument
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

func (d *cosmosDBClient) HCPClusters(subscriptionID, resourceGroupName string) HCPClusterCRUD {
	return &hcpClusterCRUD{
		topLevelCosmosResourceCRUD: newTopLevelResourceCRUD[api.HCPOpenShiftCluster, HCPCluster](d.resources, api.ClusterResourceType, subscriptionID, resourceGroupName),
	}
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
