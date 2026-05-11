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
	"iter"
	"net/http"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	billingContainer   = "Billing"
	locksContainer     = "Locks"
	resourcesContainer = "Resources"
)

// ErrAmbiguousResult occurs when a database query intended
// to yield a single item unexpectedly yields multiple items.
var ErrAmbiguousResult = errors.New("ambiguous result")

func isResponseError(err error, statusCode int) bool {
	if err == nil {
		return false
	}
	var responseError *azcore.ResponseError
	return errors.As(err, &responseError) && responseError.StatusCode == statusCode
}

// IsNotFoundError returns true if err represents an HTTP 404 Not Found response.
func IsNotFoundError(err error) bool {
	return isResponseError(err, http.StatusNotFound)
}

// IsConflictError returns true if err represents an HTTP 409 Conflict response.
func IsConflictError(err error) bool {
	return isResponseError(err, http.StatusConflict)
}

// IsPreconditionFailedError returns true if err represents an HTTP 412 Precondition Failed response.
func IsPreconditionFailedError(err error) bool {
	return isResponseError(err, http.StatusPreconditionFailed)
}

func IsBadRequestError(err error) bool {
	return isResponseError(err, http.StatusBadRequest)
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

// DBClientListResourceDocsOptions allows for limiting the results of ResourcesDBClient.ListResourceDocs.
type DBClientListResourceDocsOptions struct {
	// ResourceType matches (case-insensitively) the Azure resource type. If unspecified,
	// ResourcesDBClient.ListResourceDocs will match resource documents for any resource type.
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

// ResourcesDBClientListActiveOperationDocsOptions allows for limiting the results of ResourcesDBClient.ListActiveOperationDocs.
type ResourcesDBClientListActiveOperationDocsOptions struct {
	// Request matches the type of asynchronous operation requested
	Request *OperationRequest
	// ExternalID matches (case-insensitively) the Azure resource ID of the cluster or node pool
	ExternalID *azcorearm.ResourceID
	// IncludeNestedResources includes nested resources under ExternalID
	IncludeNestedResources bool
}

// ResourcesDBClient provides a customized interface to the Cosmos DB containers used by the
// ARO-HCP resource provider.
type ResourcesDBClient interface {
	// NewTransaction initiates a new transactional batch for the given partition key.
	NewTransaction(pk string) DBTransaction

	// UntypedCRUD provides access documents in the subscription
	UntypedCRUD(parentResourceID azcorearm.ResourceID) (UntypedResourceCRUD, error)

	// HCPClusters retrieves a CRUD interface for managing HCPCluster resources and their nested resources.
	HCPClusters(subscriptionID, resourceGroupName string) HCPClusterCRUD

	// Operations retrieves a CRUD interface for managing operations.  Remember that operations are not directly accessible
	// to end users via ARM.  They must also survive the thing they are deleting, so they live under a subscription directly.
	Operations(subscriptionID string) OperationCRUD

	Subscriptions() SubscriptionCRUD

	ServiceProviderClusters(subscriptionID, resourceGroupName, clusterName string) ServiceProviderClusterCRUD

	// ResourcesGlobalListers returns interfaces for listing ARM resource documents across all partitions
	// (Resources container only), intended for feeding SharedInformers.
	ResourcesGlobalListers() ResourcesGlobalListers

	ServiceProviderNodePools(subscriptionID, resourceGroupName, clusterName, nodePoolName string) ServiceProviderNodePoolCRUD
}

var _ ResourcesDBClient = &resourcesCosmosDBClient{}

// resourcesCosmosDBClient defines the needed values to perform CRUD operations against Cosmos DB.
type resourcesCosmosDBClient struct {
	database  *azcosmos.DatabaseClient
	resources *azcosmos.ContainerClient
}

// NewResourcesDBClient instantiates a ResourcesDBClient from a Cosmos DatabaseClient instance
// targeting the Frontends async database (Resources container).
func NewResourcesDBClient(database *azcosmos.DatabaseClient) (ResourcesDBClient, error) {
	resources, err := database.NewContainer(resourcesContainer)
	if err != nil {
		return nil, utils.TrackError(err)
	}

	return &resourcesCosmosDBClient{
		database:  database,
		resources: resources,
	}, nil
}

func (d *resourcesCosmosDBClient) NewTransaction(pk string) DBTransaction {
	return newCosmosDBTransaction(pk, d.resources)
}

func (d *resourcesCosmosDBClient) HCPClusters(subscriptionID, resourceGroupName string) HCPClusterCRUD {
	return NewHCPClusterCRUD(d.resources, subscriptionID, resourceGroupName)
}

func (d *resourcesCosmosDBClient) Operations(subscriptionID string) OperationCRUD {
	return NewOperationCRUD(d.resources, subscriptionID)
}

func (d *resourcesCosmosDBClient) Subscriptions() SubscriptionCRUD {
	return NewCosmosResourceCRUD[arm.Subscription, GenericDocument[arm.Subscription]](
		d.resources, nil, azcorearm.SubscriptionResourceType)
}

func (d *resourcesCosmosDBClient) ServiceProviderClusters(subscriptionID, resourceGroupName, clusterName string) ServiceProviderClusterCRUD {
	clusterResourceID := NewClusterResourceID(subscriptionID, resourceGroupName, clusterName)
	return NewCosmosResourceCRUD[api.ServiceProviderCluster, GenericDocument[api.ServiceProviderCluster]](
		d.resources, clusterResourceID, api.ServiceProviderClusterResourceType)
}

func (d *resourcesCosmosDBClient) ServiceProviderNodePools(subscriptionID, resourceGroupName, clusterName, nodePoolName string) ServiceProviderNodePoolCRUD {
	nodePoolResourceID := NewNodePoolResourceID(subscriptionID, resourceGroupName, clusterName, nodePoolName)
	return NewCosmosResourceCRUD[api.ServiceProviderNodePool, GenericDocument[api.ServiceProviderNodePool]](
		d.resources, nodePoolResourceID, api.ServiceProviderNodePoolResourceType)
}

func (d *resourcesCosmosDBClient) UntypedCRUD(parentResourceID azcorearm.ResourceID) (UntypedResourceCRUD, error) {
	return NewUntypedCRUD(d.resources, parentResourceID), nil
}

func (d *resourcesCosmosDBClient) ResourcesGlobalListers() ResourcesGlobalListers {
	return NewCosmosResourcesGlobalListers(d.resources)
}

// NewCosmosDatabaseClient instantiates a generic Cosmos database client.
func NewCosmosDatabaseClient(url string, dbName string, clientOptions azcore.ClientOptions) (*azcosmos.DatabaseClient, error) {
	credential, err := azidentity.NewDefaultAzureCredential(
		&azidentity.DefaultAzureCredentialOptions{
			ClientOptions:                clientOptions,
			RequireAzureTokenCredentials: true,
		})
	if err != nil {
		return nil, utils.TrackError(err)
	}

	client, err := azcosmos.NewClient(
		url,
		credential,
		&azcosmos.ClientOptions{
			ClientOptions: clientOptions,
		})
	if err != nil {
		return nil, utils.TrackError(err)
	}

	return client.NewDatabase(dbName)
}
