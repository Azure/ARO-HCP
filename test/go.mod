module github.com/Azure/ARO-HCP/test

go 1.24.0

toolchain go1.24.1

require (
	github.com/Azure/ARO-HCP/internal v0.0.0-00010101000000-000000000000
	github.com/onsi/ginkgo/v2 v2.22.2
	github.com/onsi/gomega v1.36.2
	github.com/sirupsen/logrus v1.9.3
)

require (
	github.com/AzureAD/microsoft-authentication-library-for-go v1.4.2 // indirect
	github.com/golang-jwt/jwt/v5 v5.2.2 // indirect
	github.com/google/pprof v0.0.0-20241210010833-40e02aabc2ad // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/kylelemons/godebug v1.1.0 // indirect
	github.com/pkg/browser v0.0.0-20240102092130-5ac0b6a4141c // indirect
	golang.org/x/crypto v0.38.0 // indirect
	golang.org/x/tools v0.31.0 // indirect
)

require (
	github.com/Azure/azure-sdk-for-go/sdk/azcore v1.18.0
	github.com/Azure/azure-sdk-for-go/sdk/internal v1.11.1 // indirect
)

require (
	github.com/Azure/azure-sdk-for-go/sdk/azidentity v1.9.0
	github.com/go-logr/logr v1.4.2 // indirect
	github.com/go-task/slim-sprig/v3 v3.0.0 // indirect
	github.com/google/go-cmp v0.7.0 // indirect
	golang.org/x/net v0.40.0 // indirect
	golang.org/x/sys v0.33.0 // indirect
	golang.org/x/text v0.25.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace github.com/Azure/ARO-HCP/internal => ../internal
