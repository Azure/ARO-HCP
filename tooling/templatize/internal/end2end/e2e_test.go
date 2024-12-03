package testutil

import (
	"context"
	"os"
	"testing"

	"gotest.tools/v3/assert"

	"github.com/Azure/ARO-HCP/tooling/templatize/cmd/pipeline/run"
	"github.com/Azure/ARO-HCP/tooling/templatize/pkg/config"
	"github.com/Azure/ARO-HCP/tooling/templatize/pkg/pipeline"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/dns/armdns"
)

func persistAndRun(t *testing.T, e2eImpl E2E) {
	err := e2eImpl.Persist()
	assert.NilError(t, err)

	cmd, err := run.NewCommand()
	assert.NilError(t, err)

	err = cmd.Execute()
	assert.NilError(t, err)
}

func TestE2EMake(t *testing.T) {
	if !shouldRunE2E() {
		t.Skip("Skipping end-to-end tests")
	}

	tmpDir := t.TempDir()

	e2eImpl := newE2E(tmpDir)
	e2eImpl.AddStep(pipeline.Step{
		Name:    "test",
		Action:  "Shell",
		Command: "make test",
		Env: []pipeline.EnvVar{
			{
				Name:      "TEST_ENV",
				ConfigRef: "test_env",
			},
		},
	})

	e2eImpl.SetConfig(config.Variables{"defaults": config.Variables{"test_env": "test_env"}})

	e2eImpl.makefile = `
test:
	echo ${TEST_ENV} > env.txt
`
	persistAndRun(t, &e2eImpl)

	io, err := os.ReadFile(tmpDir + "/env.txt")
	assert.NilError(t, err)
	assert.Equal(t, string(io), "test_env\n")
}

func TestE2EKubernetes(t *testing.T) {
	if !shouldRunE2E() {
		t.Skip("Skipping end-to-end tests")
	}

	tmpDir := t.TempDir()

	e2eImpl := newE2E(tmpDir)
	e2eImpl.AddStep(pipeline.Step{
		Name:    "test",
		Action:  "Shell",
		Command: "kubectl get namespaces",
	})
	e2eImpl.SetAKSName("aro-hcp-aks")

	e2eImpl.SetConfig(config.Variables{"defaults": config.Variables{"rg": "hcp-underlay-dev-svc"}})

	persistAndRun(t, &e2eImpl)
}

func TestE2EArmDeploy(t *testing.T) {
	if !shouldRunE2E() {
		t.Skip("Skipping end-to-end tests")
	}

	tmpDir := t.TempDir()

	e2eImpl := newE2E(tmpDir)
	e2eImpl.AddStep(pipeline.Step{
		Name:       "test",
		Action:     "ARM",
		Template:   "test.bicep",
		Parameters: "test.bicepparm",
	})

	cleanup := e2eImpl.UseRandomRG()
	defer func() {
		err := cleanup()
		assert.NilError(t, err)
	}()

	bicepFile := `
param zoneName string
resource symbolicname 'Microsoft.Network/dnsZones@2018-05-01' = {
  location: 'global'
  name: zoneName
}`
	paramFile := `
using 'test.bicep'
param zoneName = 'e2etestarmdeploy.foo.bar.example.com'
`
	e2eImpl.AddBicepTemplate(bicepFile, "test.bicep", paramFile, "test.bicepparm")

	persistAndRun(t, &e2eImpl)

	// Todo move to e2e module, if needed more than once
	subsriptionID, err := pipeline.LookupSubscriptionID(context.Background(), "ARO Hosted Control Planes (EA Subscription 1)")
	assert.NilError(t, err)

	cred, err := azidentity.NewDefaultAzureCredential(nil)
	assert.NilError(t, err)

	zonesClient, err := armdns.NewZonesClient(subsriptionID, cred, nil)
	assert.NilError(t, err)

	zoneResp, err := zonesClient.Get(context.Background(), e2eImpl.rgName, "e2etestarmdeploy.foo.bar.example.com", nil)
	assert.NilError(t, err)
	assert.Equal(t, *zoneResp.Name, "e2etestarmdeploy.foo.bar.example.com")
}

func TestE2EShell(t *testing.T) {
	if !shouldRunE2E() {
		t.Skip("Skipping end-to-end tests")
	}

	tmpDir := t.TempDir()

	e2eImpl := newE2E(tmpDir)

	e2eImpl.AddStep(pipeline.Step{
		Name:    "readInput",
		Action:  "Shell",
		Command: "/usr/bin/echo ${PWD} > env.txt",
	})

	persistAndRun(t, &e2eImpl)

	io, err := os.ReadFile(tmpDir + "/env.txt")
	assert.NilError(t, err)
	assert.Equal(t, string(io), tmpDir+"\n")
}

func TestE2EArmDeployWithOutput(t *testing.T) {
	if !shouldRunE2E() {
		t.Skip("Skipping end-to-end tests")
	}

	tmpDir := t.TempDir()

	e2eImpl := newE2E(tmpDir)
	e2eImpl.AddStep(pipeline.Step{
		Name:       "createZone",
		Action:     "ARM",
		Template:   "test.bicep",
		Parameters: "test.bicepparm",
	})

	e2eImpl.AddStep(pipeline.Step{
		Name:    "readInput",
		Action:  "Shell",
		Command: "echo ${zoneName} > env.txt",
		Inputs: []pipeline.Input{
			{
				Name:   "zoneName",
				Step:   "createZone",
				Output: "zoneName",
				Type:   "string",
			},
		},
	})

	cleanup := e2eImpl.UseRandomRG()
	defer func() {
		err := cleanup()
		assert.NilError(t, err)
	}()

	bicepFile := `
param zoneName string
output zoneName string = zoneName`
	paramFile := `
using 'test.bicep'
param zoneName = 'e2etestarmdeploy.foo.bar.example.com'
`
	e2eImpl.AddBicepTemplate(bicepFile, "test.bicep", paramFile, "test.bicepparm")
	persistAndRun(t, &e2eImpl)

	io, err := os.ReadFile(tmpDir + "/env.txt")
	assert.NilError(t, err)
	assert.Equal(t, string(io), "e2etestarmdeploy.foo.bar.example.com\n")
}

func TestE2EArmDeployWithOutputToArm(t *testing.T) {
	if !shouldRunE2E() {
		t.Skip("Skipping end-to-end tests")
	}

	tmpDir := t.TempDir()

	e2eImpl := newE2E(tmpDir)
	e2eImpl.AddStep(pipeline.Step{
		Name:       "parameterA",
		Action:     "ARM",
		Template:   "testa.bicep",
		Parameters: "testa.bicepparm",
	})

	e2eImpl.AddStep(pipeline.Step{
		Name:       "parameterB",
		Action:     "ARM",
		Template:   "testb.bicep",
		Parameters: "testb.bicepparm",
		Inputs: []pipeline.Input{
			{
				Name:   "parameterB",
				Step:   "parameterA",
				Output: "parameterA",
				Type:   "string",
			},
		},
	})

	e2eImpl.AddStep(pipeline.Step{
		Name:    "readInput",
		Action:  "Shell",
		Command: []string{"/bin/sh", "-c", "echo ${end} > env.txt"},
		Inputs: []pipeline.Input{
			{
				Name:   "end",
				Step:   "parameterB",
				Output: "parameterC",
				Type:   "string",
			},
		},
	})

	e2eImpl.AddBicepTemplate(`
param parameterA string
output parameterA string = parameterA`,
		"testa.bicep",
		`
using 'testa.bicep'
param parameterA = 'Hello Bicep'`,
		"testa.bicepparm")

	e2eImpl.AddBicepTemplate(`
param parameterB string
output parameterC string = parameterB
`,
		"testb.bicep",
		`
using 'testb.bicep'
param parameterB = '{{ .parameterB }}'
`,
		"testb.bicepparm")

	cleanup := e2eImpl.UseRandomRG()
	defer func() {
		err := cleanup()
		assert.NilError(t, err)
	}()
	persistAndRun(t, &e2eImpl)

	io, err := os.ReadFile(tmpDir + "/env.txt")
	assert.NilError(t, err)
	assert.Equal(t, string(io), "Hello Bicep\n")
}
