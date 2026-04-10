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
	NewTransaction(pk string) DBTransaction

	// UntypedCRUD provides access documents in the subscription
	UntypedCRUD(parentResourceID azcorearm.ResourceID) (UntypedResourceCRUD, error)

	// HCPClusters retrieves a CRUD interface for managing HCPCluster resources and their nested resources.
	HCPClusters(subscriptionID, resourceGroupName string) HCPClusterCRUD

	// Operations retrieves a CRUD interface for managing operations.  Remember that operations are not directly accessible
	// to end users via ARM.  They must also survive the thing they are deleting, so they live under a subscription directly.
	Operations(subscriptionID string) OperationCRUD

	Subscriptions() SubscriptionCRUD

	// BillingDocs retrieves a CRUD interface for managing billing documents within a subscription.
	BillingDocs(subscriptionID string) BillingDocCRUD

	ServiceProviderClusters(subscriptionID, resourceGroupName, clusterName string) ServiceProviderClusterCRUD

	// GlobalListers returns interfaces for listing all resources of a particular
	// type across all partitions, intended for feeding SharedInformers.
	GlobalListers() GlobalListers

	ServiceProviderNodePools(subscriptionID, resourceGroupName, clusterName, nodePoolName string) ServiceProviderNodePoolCRUD

	ManagementClusterContents(subscriptionID, resourceGroupName, clusterName string) ManagementClusterContentCRUD

	NodePoolManagementClusterContents(subscriptionID, resourceGroupName, clusterName, nodePoolName string) ManagementClusterContentCRUD
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
		return nil, utils.TrackError(err)
	}

	billing, err := database.NewContainer(billingContainer)
	if err != nil {
		return nil, utils.TrackError(err)
	}

	locks, err := database.NewContainer(locksContainer)
	if err != nil {
		return nil, utils.TrackError(err)
	}

	lockClient, err := NewLockClient(ctx, locks)
	if err != nil {
		return nil, utils.TrackError(err)
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

func (d *cosmosDBClient) NewTransaction(pk string) DBTransaction {
	return newCosmosDBTransaction(pk, d.resources)
}

func (d *cosmosDBClient) HCPClusters(subscriptionID, resourceGroupName string) HCPClusterCRUD {
	return NewHCPClusterCRUD(d.resources, subscriptionID, resourceGroupName)
}

func (d *cosmosDBClient) Operations(subscriptionID string) OperationCRUD {
	return NewOperationCRUD(d.resources, subscriptionID)
}

func (d *cosmosDBClient) Subscriptions() SubscriptionCRUD {
	return NewCosmosResourceCRUD[arm.Subscription, GenericDocument[arm.Subscription]](
		d.resources, nil, azcorearm.SubscriptionResourceType)
}

func (d *cosmosDBClient) BillingDocs(subscriptionID string) BillingDocCRUD {
	return NewBillingDocCRUD(d.billing, subscriptionID)
}

func (d *cosmosDBClient) ServiceProviderClusters(subscriptionID, resourceGroupName, clusterName string) ServiceProviderClusterCRUD {
	clusterResourceID := NewClusterResourceID(subscriptionID, resourceGroupName, clusterName)
	return NewCosmosResourceCRUD[api.ServiceProviderCluster, GenericDocument[api.ServiceProviderCluster]](
		d.resources, clusterResourceID, api.ServiceProviderClusterResourceType)
}

func (d *cosmosDBClient) ServiceProviderNodePools(subscriptionID, resourceGroupName, clusterName, nodePoolName string) ServiceProviderNodePoolCRUD {
	nodePoolResourceID := NewNodePoolResourceID(subscriptionID, resourceGroupName, clusterName, nodePoolName)
	return NewCosmosResourceCRUD[api.ServiceProviderNodePool, GenericDocument[api.ServiceProviderNodePool]](
		d.resources, nodePoolResourceID, api.ServiceProviderNodePoolResourceType)
}

func (d *cosmosDBClient) ManagementClusterContents(subscriptionID, resourceGroupName, clusterName string) ManagementClusterContentCRUD {
	clusterResourceID := NewClusterResourceID(subscriptionID, resourceGroupName, clusterName)
	return NewCosmosResourceCRUD[api.ManagementClusterContent, GenericDocument[api.ManagementClusterContent]](
		d.resources, clusterResourceID, api.ManagementClusterContentResourceType)
}

func (d *cosmosDBClient) NodePoolManagementClusterContents(subscriptionID, resourceGroupName, clusterName, nodePoolName string) ManagementClusterContentCRUD {
	nodePoolResourceID := NewNodePoolResourceID(subscriptionID, resourceGroupName, clusterName, nodePoolName)
	return NewCosmosResourceCRUD[api.ManagementClusterContent, GenericDocument[api.ManagementClusterContent]](
		d.resources, nodePoolResourceID, api.NodePoolManagementClusterContentResourceType)
}

func (d *cosmosDBClient) UntypedCRUD(parentResourceID azcorearm.ResourceID) (UntypedResourceCRUD, error) {
	return NewUntypedCRUD(d.resources, parentResourceID), nil
}

func (d *cosmosDBClient) GlobalListers() GlobalListers {
	return NewCosmosGlobalListers(d.resources, d.billing)
}

// NewCosmosDatabaseClient instantiates a generic Cosmos database client.
func NewCosmosDatabaseClient(url string, dbName string, clientOptions azcore.ClientOptions) (*azcosmos.DatabaseClient, error) {
	credential, err := azidentity.NewDefaultAzureCredential(
		&azidentity.DefaultAzureCredentialOptions{
			ClientOptions: clientOptions,
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
