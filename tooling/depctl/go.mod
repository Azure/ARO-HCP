module github.com/Azure/ARO-HCP/tooling/depctl

go 1.26.0

require (
	github.com/Azure/ARO-HCP/tooling/github v0.0.0
	github.com/dusted-go/logging v1.3.0
	github.com/go-logr/logr v1.4.4
	github.com/google/go-github/v69 v69.2.0
	github.com/spf13/cobra v1.10.2
	golang.org/x/mod v0.41.0
	golang.org/x/sys v0.48.0
)

require (
	github.com/google/go-querystring v1.2.0 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/spf13/pflag v1.0.10 // indirect
)

replace github.com/Azure/ARO-HCP/tooling/github => ../github
