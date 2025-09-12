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
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/Azure/ARO-HCP/internal/api"
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
	// DBConnectionTest verifies the database is reachable. Intended for use in health checks.
	DBConnectionTest(ctx context.Context) error

	// GetLockClient returns a LockClient, or nil if the DBClient does not support a LockClient.
	GetLockClient() LockClientInterface

	// NewTransaction initiates a new transactional batch for the given partition key.
	NewTransaction(pk azcosmos.PartitionKey) DBTransaction

	BillingCRUD() BillingCRUD
	HCPClusterCRUD() HCPClusterCRUD
	OperationsCRUD() OperationsCRUD
	SubscriptionCRUD() SubscriptionCRUD
}

var _ DBClient = &cosmosDBClient{}

// cosmosDBClient defines the needed values to perform CRUD operations against Cosmos DB.
type cosmosDBClient struct {
	database   *azcosmos.DatabaseClient
	billing    *azcosmos.ContainerClient
	resources  *azcosmos.ContainerClient
	lockClient *LockClient

	billingCRUD      *billingCRUD
	hcpClusterCRUD   *hcpClusterCRUD
	operationsCRUD   *operationsCRUD
	subscriptionCRUD *subscriptionCRUD
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

		billingCRUD: newBillingCRUD(billing),
		hcpClusterCRUD: &hcpClusterCRUD{
			topLevelCosmosResourceCRUD: newCosmosResourceDocumentCRUD[HCPCluster](
				resources, api.ProviderNamespace, api.ClusterResourceType),
		},
		operationsCRUD:   newOperationsCRUD(resources),
		subscriptionCRUD: newSubscriptionCRUD(resources),
	}, nil
}

func (d *cosmosDBClient) DBConnectionTest(ctx context.Context) error {
	if _, err := d.database.Read(ctx, nil); err != nil {
		return fmt.Errorf("failed to read Cosmos database information during healthcheck: %v", err)
	}

	return nil
}

func (d *cosmosDBClient) GetLockClient() LockClientInterface {
	return d.lockClient
}

func (d *cosmosDBClient) NewTransaction(pk azcosmos.PartitionKey) DBTransaction {
	return newCosmosDBTransaction(pk, d.resources)
}

func (d *cosmosDBClient) BillingCRUD() BillingCRUD {
	return d.billingCRUD
}

func (d *cosmosDBClient) HCPClusterCRUD() HCPClusterCRUD {
	return d.hcpClusterCRUD
}

func (d *cosmosDBClient) OperationsCRUD() OperationsCRUD {
	return d.operationsCRUD
}

func (d *cosmosDBClient) SubscriptionCRUD() SubscriptionCRUD {
	return d.subscriptionCRUD
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
