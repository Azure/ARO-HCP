package database

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path"
	"strings"

	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
)

type SubscriptionCRUD interface {
	Create(ctx context.Context, subscriptionID string, subscription *SubscriptionWrapper) error
	Update(ctx context.Context, subscriptionID string, updateFn func(*arm.Subscription) bool) (bool, error)
	Get(ctx context.Context, subscriptionID string) (*SubscriptionWrapper, error)
	List(ctx context.Context, opts *DBClientListResourceDocsOptions) (DBClientIterator[SubscriptionWrapper], error)
	ListAllTransitiveDescendents(ctx context.Context, subscriptionID string, opts *DBClientListResourceDocsOptions) (DBClientIterator[ResourceDocument], error)
}

type subscriptionCRUD struct {
	containerClient *azcosmos.ContainerClient
}

func newSubscriptionCRUD(containerClient *azcosmos.ContainerClient) *subscriptionCRUD {
	return &subscriptionCRUD{
		containerClient: containerClient,
	}
}

var _ SubscriptionCRUD = &subscriptionCRUD{}

func (d *subscriptionCRUD) Create(ctx context.Context, subscriptionID string, subscription *SubscriptionWrapper) error {
	typedDoc := NewTypedDocument(subscriptionID, azcorearm.SubscriptionResourceType)
	typedDoc.ID = strings.ToLower(subscriptionID)
	subscription.SetTypedDocument(*typedDoc)

	data, err := typedDocumentMarshal(subscription)
	if err != nil {
		return fmt.Errorf("failed to marshal Subscriptions container item for '%s': %w", subscriptionID, err)
	}

	_, err = d.containerClient.CreateItem(ctx, typedDoc.getPartitionKey(), data, nil)
	if err != nil {
		return fmt.Errorf("failed to create Subscriptions container item for '%s': %w", subscriptionID, err)
	}

	return nil
}

func (d *subscriptionCRUD) Get(ctx context.Context, subscriptionID string) (*SubscriptionWrapper, error) {

	// Make sure lookup keys are lowercase.
	subscriptionID = strings.ToLower(subscriptionID)

	pk := NewPartitionKey(subscriptionID)

	response, err := d.containerClient.ReadItem(ctx, pk, subscriptionID, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to read Subscriptions container item for '%s': %w", subscriptionID, err)
	}

	ret, err := typedDocumentUnmarshal[SubscriptionWrapper](response.Value)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal Subscriptions container item for '%s': %w", subscriptionID, err)
	}

	// Expose the "_ts" field for metics reporting.
	ret.Properties.LastUpdated = ret.TypedDocument.CosmosTimestamp

	return ret, nil

}

func (d *subscriptionCRUD) Update(ctx context.Context, subscriptionID string, updateFn func(*arm.Subscription) bool) (bool, error) {
	var err error

	options := &azcosmos.ItemOptions{}

	for try := 0; try < 5; try++ {
		var data []byte

		armSubscription, err := d.Get(ctx, subscriptionID)
		if err != nil {
			return false, err
		}

		if !updateFn(&armSubscription.Properties) {
			return false, nil
		}

		data, err = typedDocumentMarshal(armSubscription)
		if err != nil {
			return false, fmt.Errorf("failed to marshal Subscriptions container item for '%s': %w", subscriptionID, err)
		}

		options.IfMatchEtag = &armSubscription.CosmosETag
		_, err = d.containerClient.ReplaceItem(ctx, armSubscription.getPartitionKey(), armSubscription.ID, data, options)
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

func (d *subscriptionCRUD) List(ctx context.Context, opts *DBClientListResourceDocsOptions) (DBClientIterator[SubscriptionWrapper], error) {
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
	pager := d.containerClient.NewQueryItemsPager(query, azcosmos.NewPartitionKey(), &opt)

	return newQueryItemsIterator[SubscriptionWrapper](pager), nil
}

func (d *subscriptionCRUD) ListAllTransitiveDescendents(ctx context.Context, subscriptionID string, opts *DBClientListResourceDocsOptions) (DBClientIterator[ResourceDocument], error) {
	parts := []string{
		"/subscriptions",
		subscriptionID,
	}
	prefix, err := azcorearm.ParseResourceID(path.Join(parts...))
	if err != nil {
		return nil, fmt.Errorf("failed to make ResourceID path for '%s': %w", subscriptionID, err)
	}

	return list[ResourceDocument](ctx, d.containerClient, nil, prefix, opts)
}
