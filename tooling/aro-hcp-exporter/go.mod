module github.com/Azure/ARO-HCP/tooling/aro-hcp-exporter

go 1.25.7

require (
	github.com/Azure/ARO-HCP/tooling/azutils v0.0.0-00010101000000-000000000000
	github.com/Azure/ARO-HCP/tooling/hcpctl v0.0.0-20260323141821-e06bce560a90
	github.com/Azure/ARO-HCP/tooling/metricscache v0.0.0-00010101000000-000000000000
	github.com/Azure/azure-sdk-for-go/sdk/azcore v1.21.1
	github.com/Azure/azure-sdk-for-go/sdk/azidentity v1.13.1
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resourcegraph/armresourcegraph v0.9.0
	github.com/go-logr/logr v1.4.3
	github.com/go-viper/mapstructure/v2 v2.5.0
	github.com/prometheus/client_golang v1.23.2
	github.com/spf13/cobra v1.10.2
	k8s.io/apimachinery v0.35.3
)

require (
	github.com/Azure/azure-kusto-go/azkustodata v1.2.1 // indirect
	github.com/Azure/azure-sdk-for-go/sdk/internal v1.12.0 // indirect
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armsubscriptions v1.3.0 // indirect
	github.com/AzureAD/microsoft-authentication-library-for-go v1.7.0 // indirect
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/golang-jwt/jwt/v5 v5.3.1 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/klauspost/compress v1.18.6 // indirect
	github.com/kylelemons/godebug v1.1.0 // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/pkg/browser v0.0.0-20240102092130-5ac0b6a4141c // indirect
	github.com/prometheus/client_model v0.6.2 // indirect
	github.com/prometheus/common v0.67.5 // indirect
	github.com/prometheus/procfs v0.20.1 // indirect
	github.com/samber/lo v1.53.0 // indirect
	github.com/shopspring/decimal v1.4.0 // indirect
	github.com/spf13/pflag v1.0.10 // indirect
	go.yaml.in/yaml/v2 v2.4.4 // indirect
	golang.org/x/crypto v0.51.0 // indirect
	golang.org/x/net v0.55.0 // indirect
	golang.org/x/sync v0.20.0 // indirect
	golang.org/x/sys v0.45.0 // indirect
	golang.org/x/text v0.37.0 // indirect
	google.golang.org/protobuf v1.36.12-0.20260120151049-f2248ac996af // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
	k8s.io/klog/v2 v2.140.0 // indirect
)

replace (
	github.com/Azure/ARO-HCP/tooling/azutils => ../azutils
	github.com/Azure/ARO-HCP/tooling/hcpctl => ../hcpctl
	github.com/Azure/ARO-HCP/tooling/metricscache => ../metricscache
)
