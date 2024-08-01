package database

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
)

const (
	clustersContainer   = "Clusters"
	nodePoolsContainer  = "NodePools"
	subsContainer       = "Subscriptions"
	billingContainer    = "Billing"
	operationsContainer = "Operations"
)

var ErrNotFound = errors.New("DocumentNotFound")

// DBClient is a document store for frontend to perform required CRUD operations against
type DBClient interface {
	// DBConnectionTest is used to health check the database. If the database is not reachable or otherwise not ready
	// to be used, an error should be returned.
	DBConnectionTest(ctx context.Context) error

	// GetClusterDoc retrieves an HCPOpenShiftClusterDocument from the database given its resourceID.
	// ErrNotFound is returned if an associated HCPOpenShiftClusterDocument cannot be found.
	GetClusterDoc(ctx context.Context, resourceID *azcorearm.ResourceID) (*HCPOpenShiftClusterDocument, error)
	SetClusterDoc(ctx context.Context, doc *HCPOpenShiftClusterDocument) error
	// DeleteClusterDoc deletes an HCPOpenShiftClusterDocument from the database given the resourceID
	// of a Microsoft.RedHatOpenshift/HcpOpenShiftClusters resource.
	DeleteClusterDoc(ctx context.Context, resourceID *azcorearm.ResourceID) error

	GetNodePoolDoc(ctx context.Context, resourceID *azcorearm.ResourceID) (*NodePoolDocument, error)
	SetNodePoolDoc(ctx context.Context, doc *NodePoolDocument) error
	DeleteNodePoolDoc(ctx context.Context, resourceID *azcorearm.ResourceID) error

	GetOperationDoc(ctx context.Context, operationID string) (*OperationDocument, error)
	SetOperationDoc(ctx context.Context, doc *OperationDocument) error
	DeleteOperationDoc(ctx context.Context, operationID string) error

	// GetSubscriptionDoc retrieves a SubscriptionDocument from the database given the subscriptionID.
	// ErrNotFound is returned if an associated SubscriptionDocument cannot be found.
	GetSubscriptionDoc(ctx context.Context, subscriptionID string) (*SubscriptionDocument, error)
	SetSubscriptionDoc(ctx context.Context, doc *SubscriptionDocument) error
}

var _ DBClient = &CosmosDBClient{}

// CosmosDBClient defines the needed values to perform CRUD operations against the async DB
type CosmosDBClient struct {
	client *azcosmos.DatabaseClient
	config *CosmosDBConfig
}

// CosmosDBConfig stores database and client configuration data
type CosmosDBConfig struct {
	DBName        string
	DBUrl         string
	ClientOptions *azidentity.DefaultAzureCredentialOptions
}

// NewCosmosDBConfig configures database configuration values for access
func NewCosmosDBConfig(dbName, dbURL string) *CosmosDBConfig {
	opt := &azidentity.DefaultAzureCredentialOptions{}
	c := &CosmosDBConfig{
		DBName:        dbName,
		DBUrl:         dbURL,
		ClientOptions: opt,
	}
	return c
}

// NewCosmosDBClient instantiates a Cosmos DatabaseClient targeting Frontends async DB
func NewCosmosDBClient(config *CosmosDBConfig) (DBClient, error) {
	cred, err := azidentity.NewDefaultAzureCredential(config.ClientOptions)
	if err != nil {
		return nil, err
	}

	d := &CosmosDBClient{
		config: config,
	}

	client, err := azcosmos.NewClient(d.config.DBUrl, cred, nil)
	if err != nil {
		return nil, err
	}

	// This only fails if the database ID is empty.
	d.client, err = client.NewDatabase(config.DBName)
	if err != nil {
		return nil, err
	}

	return d, nil
}

// DBConnectionTest checks the async database is accessible on startup
func (d *CosmosDBClient) DBConnectionTest(ctx context.Context) error {
	if _, err := d.client.Read(ctx, nil); err != nil {
		return fmt.Errorf("failed to read Cosmos database information during healthcheck: %v", err)
	}

	return nil
}

// GetClusterDoc retrieves a cluster document from async DB using resource ID
func (d *CosmosDBClient) GetClusterDoc(ctx context.Context, resourceID *azcorearm.ResourceID) (*HCPOpenShiftClusterDocument, error) {
	// Make sure lookup keys are lowercase.
	key := strings.ToLower(resourceID.String())
	pk := azcosmos.NewPartitionKeyString(strings.ToLower(resourceID.SubscriptionID))

	container, err := d.client.NewContainer(clustersContainer)
	if err != nil {
		return nil, err
	}

	query := "SELECT * FROM c WHERE c.key = @key"
	opt := azcosmos.QueryOptions{
		PageSizeHint:    1,
		QueryParameters: []azcosmos.QueryParameter{{Name: "@key", Value: key}},
	}

	queryPager := container.NewQueryItemsPager(query, pk, &opt)

	var doc *HCPOpenShiftClusterDocument
	for queryPager.More() {
		queryResponse, err := queryPager.NextPage(ctx)
		if err != nil {
			return nil, err
		}

		for _, item := range queryResponse.Items {
			err = json.Unmarshal(item, &doc)
			if err != nil {
				return nil, err
			}
		}
	}
	if doc != nil {
		return doc, nil
	}
	return nil, ErrNotFound
}

// SetClusterDoc creates/updates a cluster document in the async DB during cluster creation/patching
func (d *CosmosDBClient) SetClusterDoc(ctx context.Context, doc *HCPOpenShiftClusterDocument) error {
	// Make sure lookup keys are lowercase.
	doc.Key = strings.ToLower(doc.Key)
	doc.PartitionKey = strings.ToLower(doc.PartitionKey)

	data, err := json.Marshal(doc)
	if err != nil {
		return err
	}

	container, err := d.client.NewContainer(clustersContainer)
	if err != nil {
		return err
	}

	_, err = container.UpsertItem(ctx, azcosmos.NewPartitionKeyString(doc.PartitionKey), data, nil)
	if err != nil {
		return err
	}

	return nil
}

// DeleteClusterDoc removes a cluster document from the async DB using resource ID
func (d *CosmosDBClient) DeleteClusterDoc(ctx context.Context, resourceID *azcorearm.ResourceID) error {
	// Make sure lookup keys are lowercase.
	pk := azcosmos.NewPartitionKeyString(strings.ToLower(resourceID.SubscriptionID))

	doc, err := d.GetClusterDoc(ctx, resourceID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		return fmt.Errorf("while attempting to delete the cluster, failed to get cluster document: %w", err)
	}

	container, err := d.client.NewContainer(clustersContainer)
	if err != nil {
		return err
	}

	_, err = container.DeleteItem(ctx, pk, doc.ID, nil)
	if err != nil {
		return err
	}
	return nil
}

func (d *CosmosDBClient) GetNodePoolDoc(ctx context.Context, resourceID *azcorearm.ResourceID) (*NodePoolDocument, error) {
	panic("implement me")
}

func (d *CosmosDBClient) SetNodePoolDoc(ctx context.Context, doc *NodePoolDocument) error {
	panic("implement me")
}

// DeleteNodePoolDoc removes a NodePool document from the DB using resource ID
func (d *CosmosDBClient) DeleteNodePoolDoc(ctx context.Context, resourceID *azcorearm.ResourceID) error {
	panic("implement me")
}

// GetOperationDoc retrieves the asynchronous operation document for the given
// operation ID from the "operations" container
func (d *CosmosDBClient) GetOperationDoc(ctx context.Context, operationID string) (*OperationDocument, error) {
	// Make sure lookup keys are lowercase.
	operationID = strings.ToLower(operationID)

	container, err := d.client.NewContainer(operationsContainer)
	if err != nil {
		return nil, err
	}

	pk := azcosmos.NewPartitionKeyString(operationID)

	response, err := container.ReadItem(ctx, pk, operationID, nil)
	if err != nil {
		var responseErr *azcore.ResponseError
		errors.As(err, &responseErr)
		if responseErr.StatusCode == http.StatusNotFound {
			return nil, ErrNotFound
		}
		return nil, err
	}

	var doc *OperationDocument
	err = json.Unmarshal(response.Value, &doc)
	if err != nil {
		return nil, err
	}

	return doc, nil
}

// SetOperationDoc writes an asynchronous operation document to the "operations"
// container
func (d *CosmosDBClient) SetOperationDoc(ctx context.Context, doc *OperationDocument) error {
	// Make sure lookup keys are lowercase.
	doc.ID = strings.ToLower(doc.ID)

	container, err := d.client.NewContainer(operationsContainer)
	if err != nil {
		return err
	}

	pk := azcosmos.NewPartitionKeyString(doc.ID)

	data, err := json.Marshal(doc)
	if err != nil {
		return err
	}

	_, err = container.UpsertItem(ctx, pk, data, nil)
	if err != nil {
		return err
	}

	return nil
}

// DeleteOperationDoc deletes the asynchronous operation document for the given
// operation ID from the "operations" container
func (d *CosmosDBClient) DeleteOperationDoc(ctx context.Context, operationID string) error {
	// Make sure lookup keys are lowercase.
	operationID = strings.ToLower(operationID)

	container, err := d.client.NewContainer(operationsContainer)
	if err != nil {
		return err
	}

	pk := azcosmos.NewPartitionKeyString(operationID)

	_, err = container.DeleteItem(ctx, pk, operationID, nil)
	if err != nil {
		var responseErr *azcore.ResponseError
		errors.As(err, &responseErr)
		if responseErr.StatusCode == http.StatusNotFound {
			return ErrNotFound
		}
		return err
	}

	return nil
}

// GetSubscriptionDoc retreives a subscription document from async DB using the subscription ID
func (d *CosmosDBClient) GetSubscriptionDoc(ctx context.Context, subscriptionID string) (*SubscriptionDocument, error) {
	// Make sure lookup keys are lowercase.
	subscriptionID = strings.ToLower(subscriptionID)

	container, err := d.client.NewContainer(subsContainer)
	if err != nil {
		return nil, err
	}

	query := "SELECT * FROM c WHERE c.partitionKey = @partitionKey"
	opt := azcosmos.QueryOptions{
		PageSizeHint:    1,
		QueryParameters: []azcosmos.QueryParameter{{Name: "@partitionKey", Value: subscriptionID}},
	}

	pk := azcosmos.NewPartitionKeyString(subscriptionID)
	queryPager := container.NewQueryItemsPager(query, pk, &opt)

	var doc *SubscriptionDocument
	for queryPager.More() {
		queryResponse, err := queryPager.NextPage(ctx)
		if err != nil {
			return nil, err
		}

		for _, item := range queryResponse.Items {
			err = json.Unmarshal(item, &doc)
			if err != nil {
				return nil, err
			}
		}
	}
	if doc != nil {
		return doc, nil
	}
	return nil, ErrNotFound
}

// SetSubscriptionDoc creates/updates a subscription document in the async DB during cluster creation/patching
func (d *CosmosDBClient) SetSubscriptionDoc(ctx context.Context, doc *SubscriptionDocument) error {
	// Make sure lookup keys are lowercase.
	doc.PartitionKey = strings.ToLower(doc.PartitionKey)

	data, err := json.Marshal(doc)
	if err != nil {
		return err
	}

	container, err := d.client.NewContainer(subsContainer)
	if err != nil {
		return err
	}

	_, err = container.UpsertItem(ctx, azcosmos.NewPartitionKeyString(doc.PartitionKey), data, nil)
	if err != nil {
		return err
	}
	return nil
}
