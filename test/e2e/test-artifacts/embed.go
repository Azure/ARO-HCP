package testartifacts

import "embed"

//go:embed generated-test-artifacts
var TestArtifactsFS embed.FS
