package config

import (
	"reflect"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
)

type AzureContainerRegistry struct {
	// Resource Id of the Azure Container Registry
	resourceID azcorearm.ResourceID
	// URL of the Azure Container Registry.
	// It should only contain the hostname, without any protocol, port or paths.
	url string
	// Scope map name for the Azure Container Registry repository
	scopeMapName string
}

func NewAzureContainerRegistry(
	resourceID azcorearm.ResourceID, url string, scopeMapName string) AzureContainerRegistry {
	return AzureContainerRegistry{
		resourceID:   resourceID,
		url:          url,
		scopeMapName: scopeMapName,
	}
}

func (acr AzureContainerRegistry) ResourceID() azcorearm.ResourceID {
	return acr.resourceID
}

func (acr AzureContainerRegistry) URL() string {
	return acr.url
}

func (acr AzureContainerRegistry) ScopeMapName() string {
	return acr.scopeMapName
}

func (acr AzureContainerRegistry) RegistryName() string {
	return acr.resourceID.Name
}

func (acr AzureContainerRegistry) ResourceGroupName() string {
	return acr.resourceID.ResourceGroupName
}

func (acr AzureContainerRegistry) SubscriptionID() string {
	return acr.resourceID.SubscriptionID
}

// Returns true if acr object is empty and false if not
func (acr AzureContainerRegistry) IsEmpty() bool {
	return reflect.DeepEqual(acr, AzureContainerRegistry{})
}
