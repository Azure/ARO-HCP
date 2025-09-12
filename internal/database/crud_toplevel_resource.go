package database

import (
	"context"
	"fmt"
	"net/http"
	"path"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
)

type TopLevelResourceCRUD[T any] interface {
	Patch(ctx context.Context, resource *T, opts ResourceDocumentPatchOperations) (*T, error)
	Get(ctx context.Context, subscriptionID, resourceGroup, resourceID string) (string, *T, error)
	ListAll(ctx context.Context, subscriptionID string, opts *DBClientListResourceDocsOptions) (DBClientIterator[T], error)
	List(ctx context.Context, subscriptionID, resourceGroup string, opts *DBClientListResourceDocsOptions) (DBClientIterator[T], error)
	ListAllTransitiveDescendents(ctx context.Context, subscriptionID, resourceGroup, resourceID string, opts *DBClientListResourceDocsOptions) (DBClientIterator[ResourceDocument], error)
	Delete(ctx context.Context, subscriptionID, resourceGroup, resourceID string) error
}

type topLevelCosmosResourceCRUD[T any] struct {
	containerClient   *azcosmos.ContainerClient
	providerNamespace string
	resourceType      *azcorearm.ResourceType
}

func newCosmosResourceDocumentCRUD[T any](resources *azcosmos.ContainerClient, providerNamespace string, resourceType *azcorearm.ResourceType) *topLevelCosmosResourceCRUD[T] {
	return &topLevelCosmosResourceCRUD[T]{
		containerClient:   resources,
		providerNamespace: providerNamespace,
		resourceType:      resourceType,
	}
}

var _ TopLevelResourceCRUD[HCPCluster] = &topLevelCosmosResourceCRUD[HCPCluster]{}

func (d *topLevelCosmosResourceCRUD[T]) makeResourceIDPath(subscriptionID, resourceGroupID, resourceID string) (*azcorearm.ResourceID, error) {
	if len(subscriptionID) == 0 {
		return nil, fmt.Errorf("subscriptionID is required")
	}

	// this is valid for top level resource in azure.
	if len(resourceGroupID) == 0 {
		parts := []string{
			"/subscriptions",
			subscriptionID,
		}
		return azcorearm.ParseResourceID(path.Join(parts...))
	}

	parts := []string{
		"/subscriptions",
		subscriptionID,
		"resourceGroups",
		resourceGroupID,
		"providers",
		d.providerNamespace,
	}

	parts = append(parts, d.resourceType.Type)

	if len(resourceID) > 0 {
		parts = append(parts, resourceID)
	}

	return azcorearm.ParseResourceID(path.Join(parts...))
}

func (d *topLevelCosmosResourceCRUD[T]) Patch(ctx context.Context, resource *T, ops ResourceDocumentPatchOperations) (*T, error) {
	return patch[T](ctx, d.containerClient, resource, ops.PatchOperations)
}

func (d *topLevelCosmosResourceCRUD[T]) Get(ctx context.Context, subscriptionID, resourceGroup, resourceID string) (string, *T, error) {
	completeResourceID, err := d.makeResourceIDPath(subscriptionID, resourceGroup, resourceID)
	if err != nil {
		return "", nil, fmt.Errorf("failed to make ResourceID path for '%s': %w", resourceID, err)
	}

	return get[T](ctx, d.containerClient, completeResourceID)
}

func (d *topLevelCosmosResourceCRUD[T]) ListAll(ctx context.Context, subscriptionID string, options *DBClientListResourceDocsOptions) (DBClientIterator[T], error) {
	prefix, err := d.makeResourceIDPath(subscriptionID, "", "")
	if err != nil {
		return nil, fmt.Errorf("failed to make ResourceID path for '%s': %w", subscriptionID, err)
	}

	return list[T](ctx, d.containerClient, d.resourceType, prefix, options)
}

func (d *topLevelCosmosResourceCRUD[T]) List(ctx context.Context, subscriptionID, resourceGroup string, options *DBClientListResourceDocsOptions) (DBClientIterator[T], error) {
	prefix, err := d.makeResourceIDPath(subscriptionID, resourceGroup, "")
	if err != nil {
		return nil, fmt.Errorf("failed to make ResourceID path for '%s': %w", resourceGroup, err)
	}

	return list[T](ctx, d.containerClient, d.resourceType, prefix, options)
}

func (d *topLevelCosmosResourceCRUD[T]) ListAllTransitiveDescendents(ctx context.Context, subscriptionID, resourceGroupID, hcpClusterID string, options *DBClientListResourceDocsOptions) (DBClientIterator[ResourceDocument], error) {
	prefix, err := d.makeResourceIDPath(subscriptionID, resourceGroupID, hcpClusterID)
	if err != nil {
		return nil, fmt.Errorf("failed to make ResourceID path for '%s': %w", subscriptionID, err)
	}

	return list[ResourceDocument](ctx, d.containerClient, nil, prefix, options)
}

func (d *topLevelCosmosResourceCRUD[T]) Delete(ctx context.Context, subscriptionID, resourceGroup, resourceID string) error {
	_, toDelete, err := d.Get(ctx, subscriptionID, resourceGroup, resourceID)
	if err != nil {
		if IsResponseError(err, http.StatusNotFound) {
			return nil
		}
		return err
	}

	castToDelete, ok := any(toDelete).(DocumentProperties)
	if !ok {
		return fmt.Errorf("type %T does not implement DocumentProperties interface", toDelete)
	}

	return deleteItem(ctx, d.containerClient, castToDelete)
}
