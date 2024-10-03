module github.com/Azure/ARO-HCP/backend

go 1.23.0

require (
	github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos v1.1.0
	github.com/openshift-online/ocm-sdk-go v0.1.444
	github.com/spf13/cobra v1.8.1
)

replace github.com/Azure/ARO-HCP/internal => ../internal
