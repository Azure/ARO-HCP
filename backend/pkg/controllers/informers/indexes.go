package informers

import (
	"fmt"
	"path"
	"strings"

	"k8s.io/client-go/tools/cache"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
)

const (
	SubscriptionIndex  = "Subscription"
	ResourceGroupIndex = "ResourceGroup"
	ParentIndex        = "Parent"
)

func SubscriptionIndexers() map[string]cache.IndexFunc {
	return map[string]cache.IndexFunc{
		SubscriptionIndex: DeletionHandlingSubscriptionIndexFunc,
	}
}

func ResourceGroupIndexers() map[string]cache.IndexFunc {
	return map[string]cache.IndexFunc{
		SubscriptionIndex:  DeletionHandlingSubscriptionIndexFunc,
		ResourceGroupIndex: DeletionHandlingResourceGroupIndexFunc,
	}
}

func ParentIndexers() map[string]cache.IndexFunc {
	return map[string]cache.IndexFunc{
		SubscriptionIndex:  DeletionHandlingSubscriptionIndexFunc,
		ResourceGroupIndex: DeletionHandlingResourceGroupIndexFunc,
		ParentIndex:        DeletionHandlingParentResourceIDKeyFunc,
	}
}

func DeletionHandlingResourceIDKeyFunc(obj interface{}) (string, error) {
	if d, ok := obj.(cache.DeletedFinalStateUnknown); ok {
		return d.Key, nil
	}
	switch t := obj.(type) {
	case api.CosmosMetadataAccessor:
		return strings.ToLower(t.GetResourceID().String()), nil
	case api.CosmosPersistable:
		return strings.ToLower(t.GetCosmosData().GetResourceID().String()), nil
	default:
		return "", fmt.Errorf("unhandled type %T", obj)
	}
}

func ResourceIDToSubscriptionIndex(resourceID *azcorearm.ResourceID) ([]string, error) {
	if resourceID == nil {
		return nil, fmt.Errorf("resourceID is nil")
	}
	return []string{strings.ToLower(resourceID.SubscriptionID)}, nil
}

func DeletionHandlingSubscriptionIndexFunc(obj interface{}) ([]string, error) {
	if d, ok := obj.(cache.DeletedFinalStateUnknown); ok {
		return []string{d.Key}, nil
	}
	switch t := obj.(type) {
	case api.CosmosMetadataAccessor:
		return ResourceIDToSubscriptionIndex(t.GetResourceID())
	case api.CosmosPersistable:
		return ResourceIDToSubscriptionIndex(t.GetCosmosData().GetResourceID())
	default:
		return nil, fmt.Errorf("unhandled type %T", obj)
	}
}

func ResourceIDToResourceGroupIndex(resourceID *azcorearm.ResourceID) ([]string, error) {
	if resourceID == nil {
		return nil, fmt.Errorf("resourceID is nil")
	}
	if len(resourceID.ResourceGroupName) == 0 {
		return nil, fmt.Errorf("resourceID.ResourceGroupName is empty")
	}
	return []string{
		strings.ToLower(path.Join(
			"/subscriptions",
			resourceID.SubscriptionID,
			"resourceGroups",
			resourceID.ResourceGroupName,
		)),
	}, nil
}

func DeletionHandlingResourceGroupIndexFunc(obj interface{}) ([]string, error) {
	if d, ok := obj.(cache.DeletedFinalStateUnknown); ok {
		return []string{d.Key}, nil
	}
	switch t := obj.(type) {
	case api.CosmosMetadataAccessor:
		return ResourceIDToResourceGroupIndex(t.GetResourceID())
	case api.CosmosPersistable:
		return ResourceIDToResourceGroupIndex(t.GetCosmosData().GetResourceID())
	default:
		return nil, fmt.Errorf("unhandled type %T", obj)
	}
}

func ResourceIDToParentResourceIDIndex(resourceID *azcorearm.ResourceID) ([]string, error) {
	if resourceID == nil {
		return nil, fmt.Errorf("resourceID is nil")
	}
	if resourceID.Parent == nil {
		return nil, fmt.Errorf("resourceID.Parent is nil")
	}
	return []string{
		strings.ToLower(resourceID.Parent.String()),
	}, nil
}

func DeletionHandlingParentResourceIDKeyFunc(obj interface{}) ([]string, error) {
	if d, ok := obj.(cache.DeletedFinalStateUnknown); ok {
		return []string{d.Key}, nil
	}
	switch t := obj.(type) {
	case api.CosmosMetadataAccessor:
		return ResourceIDToParentResourceIDIndex(t.GetResourceID())
	case api.CosmosPersistable:
		return ResourceIDToParentResourceIDIndex(t.GetCosmosData().GetResourceID())
	default:
		return nil, fmt.Errorf("unhandled type %T", obj)
	}
}
