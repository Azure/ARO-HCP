module github.com/Azure/ARO-HCP/tooling/acrauth

go 1.25.7

require github.com/Azure/ARO-Tools/tools/acrauth v0.0.0

require (
	github.com/Azure/azure-sdk-for-go/sdk/azcore v1.23.0 // indirect
	github.com/Azure/azure-sdk-for-go/sdk/azidentity v1.14.0 // indirect
	github.com/Azure/azure-sdk-for-go/sdk/containers/azcontainerregistry v0.2.3 // indirect
	github.com/Azure/azure-sdk-for-go/sdk/internal v1.12.0 // indirect
	github.com/AzureAD/microsoft-authentication-library-for-go v1.7.2 // indirect
	github.com/golang-jwt/jwt/v5 v5.3.1 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/kylelemons/godebug v1.1.0 // indirect
	github.com/pkg/browser v0.0.0-20240102092130-5ac0b6a4141c // indirect
	github.com/spf13/cobra v1.10.2 // indirect
	github.com/spf13/pflag v1.0.10 // indirect
	golang.org/x/crypto v0.54.0 // indirect
	golang.org/x/net v0.57.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/text v0.40.0 // indirect
)

// TEMPORARY: remove before merge, replace with the published pseudo-version once
// https://github.com/Azure/ARO-Tools/pull/XXX lands.
replace github.com/Azure/ARO-Tools/tools/acrauth => ../../ARO-Tools/tools/acrauth
