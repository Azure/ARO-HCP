package config

type AzureDataPlaneIdentitiesOIDCConfiguration struct {
	storageAccountBlobContainerName string
	storageAccountBlobServiceURL    string
	oidcIssuerBaseURL               string
}

func NewAzureDataPlaneIdentitiesOIDCConfiguration(containerName, serviceURL,
	issuerUrl string) AzureDataPlaneIdentitiesOIDCConfiguration {
	return AzureDataPlaneIdentitiesOIDCConfiguration{
		storageAccountBlobContainerName: containerName,
		storageAccountBlobServiceURL:    serviceURL,
		oidcIssuerBaseURL:               issuerUrl,
	}
}

func (a AzureDataPlaneIdentitiesOIDCConfiguration) ContainerName() string {
	return a.storageAccountBlobContainerName
}

func (a AzureDataPlaneIdentitiesOIDCConfiguration) ServiceURL() string {
	return a.storageAccountBlobServiceURL
}

func (a AzureDataPlaneIdentitiesOIDCConfiguration) BaseIssuerUrl() string {
	return a.oidcIssuerBaseURL
}
