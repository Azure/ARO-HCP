package databasemutationhelpers

import (
	"path/filepath"
	"testing"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/database"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/equality"
)

type ResourceCRUDTestSpecializer[InternalAPIType any] interface {
	ResourceCRUDFromKey(t *testing.T, cosmosContainer *azcosmos.ContainerClient, key CosmosCRUDKey) database.ResourceCRUD[InternalAPIType]
	InstanceEquals(expected, actual *InternalAPIType) bool
	NameFromInstance(*InternalAPIType) string
	WriteCosmosID(newObj, oldObj *InternalAPIType)
}

type ControllerCRUDSpecializer struct {
}

var _ ResourceCRUDTestSpecializer[api.Controller] = &ControllerCRUDSpecializer{}

func (ControllerCRUDSpecializer) ResourceCRUDFromKey(t *testing.T, cosmosContainer *azcosmos.ContainerClient, key CosmosCRUDKey) database.ResourceCRUD[api.Controller] {
	parentResourceID, err := azcorearm.ParseResourceID(key.ParentResourceID)
	require.NoError(t, err)
	controllerResourceType, err := azcorearm.ParseResourceType(filepath.Join(parentResourceID.ResourceType.String(), api.ControllerResourceTypeName))
	require.NoError(t, err)

	return database.NewControllerCRUD(cosmosContainer, parentResourceID, controllerResourceType)
}

func (ControllerCRUDSpecializer) InstanceEquals(expected, actual *api.Controller) bool {
	// clear the fields that don't compare
	actual.CosmosUID = ""
	return equality.Semantic.DeepEqual(expected, actual)
}

func (ControllerCRUDSpecializer) NameFromInstance(obj *api.Controller) string {
	return obj.ControllerName
}

func (ControllerCRUDSpecializer) WriteCosmosID(newObj, oldObj *api.Controller) {
	newObj.CosmosUID = oldObj.CosmosUID
}

type OperationCRUDSpecializer struct {
}

var _ ResourceCRUDTestSpecializer[api.Operation] = &OperationCRUDSpecializer{}

func (OperationCRUDSpecializer) ResourceCRUDFromKey(t *testing.T, cosmosContainer *azcosmos.ContainerClient, key CosmosCRUDKey) database.ResourceCRUD[api.Operation] {
	parentResourceID, err := azcorearm.ParseResourceID(key.ParentResourceID)
	require.NoError(t, err)

	return database.NewCosmosResourceCRUD[api.Operation, database.OperationStatus](cosmosContainer, parentResourceID, api.OperationStatusResourceType)
}

func (OperationCRUDSpecializer) InstanceEquals(expected, actual *api.Operation) bool {
	return equality.Semantic.DeepEqual(expected, actual)
}

func (OperationCRUDSpecializer) NameFromInstance(obj *api.Operation) string {
	return obj.CosmosUID
}

func (OperationCRUDSpecializer) WriteCosmosID(newObj, oldObj *api.Operation) {
	newObj.CosmosUID = oldObj.CosmosUID
}
