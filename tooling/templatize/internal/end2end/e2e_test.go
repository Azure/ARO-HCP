package testutil

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"gotest.tools/v3/assert"

	"github.com/Azure/ARO-Tools/pkg/config"

	"github.com/Azure/ARO-HCP/tooling/templatize/cmd/pipeline/run"
	"github.com/Azure/ARO-HCP/tooling/templatize/pkg/azauth"
	"github.com/Azure/ARO-HCP/tooling/templatize/pkg/pipeline"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/dns/armdns"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
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
	e2eImpl.AddStep(
		pipeline.NewShellStep("test", "make test").WithVariables(pipeline.Variable{
			Name:      "TEST_ENV",
			ConfigRef: "test_env",
		}),
		0,
	)

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
	e2eImpl.AddStep(pipeline.NewShellStep("test", "kubectl get namespaces"), 0)
	e2eImpl.SetAKSName("dev-svc")

	e2eImpl.SetConfig(config.Variables{"defaults": config.Variables{"rg": "hcp-underlay-dev-svc"}})

	persistAndRun(t, &e2eImpl)
}

func TestE2EArmDeploy(t *testing.T) {
	if !shouldRunE2E() {
		t.Skip("Skipping end-to-end tests")
	}

	tmpDir := t.TempDir()

	e2eImpl := newE2E(tmpDir)
	e2eImpl.AddStep(pipeline.NewARMStep("test", "test.bicep", "test.bicepparm", "ResourceGroup"), 0)
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

	cred, err := azauth.GetAzureTokenCredentials()
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

	tmpDir, err := filepath.EvalSymlinks(t.TempDir())
	assert.NilError(t, err)

	e2eImpl := newE2E(tmpDir)

	e2eImpl.AddStep(
		pipeline.NewShellStep("readInput", "/bin/echo ${PWD} > env.txt"),
		0,
	)

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

	e2eImpl.AddStep(pipeline.NewARMStep("createZone", "test.bicep", "test.bicepparm", "ResourceGroup"), 0)

	e2eImpl.AddStep(pipeline.NewShellStep(
		"readInput", "echo ${zoneName} > env.txt",
	).WithVariables(
		pipeline.Variable{
			Name: "zoneName",
			Input: &pipeline.Input{
				Name: "zoneName",
				Step: "createZone",
			},
		},
	), 0)

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

func TestE2EArmDeployWithStaticVariable(t *testing.T) {
	if !shouldRunE2E() {
		t.Skip("Skipping end-to-end tests")
	}

	tmpDir := t.TempDir()

	e2eImpl := newE2E(tmpDir)

	e2eImpl.AddStep(pipeline.NewARMStep(
		"createZone", "test.bicep", "test.bicepparm", "ResourceGroup",
	).WithVariables(
		pipeline.Variable{
			Name:  "zoneName",
			Value: "e2etestarmdeploy.foo.bar.example.com",
		},
	), 0)

	e2eImpl.AddStep(pipeline.NewShellStep(
		"readInput", "echo ${zoneName} > env.txt",
	).WithVariables(
		pipeline.Variable{
			Name: "zoneName",
			Input: &pipeline.Input{
				Name: "zoneName",
				Step: "createZone",
			},
		},
	), 0)

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
param zoneName = '__zoneName__'
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
	e2eImpl.AddStep(pipeline.NewARMStep("stepA", "testa.bicep", "testa.bicepparm", "ResourceGroup"), 0)
	e2eImpl.AddStep(pipeline.NewARMStep("stepB", "testb.bicep", "testb.bicepparm", "ResourceGroup").WithVariables(pipeline.Variable{
		Name: "parameterB",
		Input: &pipeline.Input{
			Name: "parameterA",
			Step: "stepA",
		},
	}), 0)

	e2eImpl.AddStep(pipeline.NewShellStep(
		"readInput", "echo ${end} > env.txt",
	).WithVariables(
		pipeline.Variable{
			Name: "end",
			Input: &pipeline.Input{
				Name: "parameterC",
				Step: "stepB",
			},
		},
	), 0)

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
param parameterB = '< provided at runtime >'
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

func TestE2EArmDeployWithOutputRGOverlap(t *testing.T) {
	if !shouldRunE2E() {
		t.Skip("Skipping end-to-end tests")
	}

	tmpDir := t.TempDir()

	e2eImpl := newE2E(tmpDir)
	e2eImpl.AddStep(pipeline.NewARMStep("parameterA", "testa.bicep", "testa.bicepparm", "ResourceGroup"), 0)

	e2eImpl.AddResourceGroup()

	e2eImpl.AddStep(pipeline.NewShellStep("readInput", "echo ${end} > env.txt").WithVariables(
		pipeline.Variable{
			Name: "end",
			Input: &pipeline.Input{
				Name: "parameterA",
				Step: "parameterA",
			},
		},
	), 1)

	e2eImpl.AddBicepTemplate(`
param parameterA string
output parameterA string = parameterA`,
		"testa.bicep",
		`
using 'testa.bicep'
param parameterA = 'Hello Bicep'`,
		"testa.bicepparm")

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

func TestE2EArmDeploySubscriptionScope(t *testing.T) {
	if !shouldRunE2E() {
		t.Skip("Skipping end-to-end tests")
	}

	tmpDir := t.TempDir()

	e2eImpl := newE2E(tmpDir)
	e2eImpl.AddStep(pipeline.NewARMStep("parameterA", "testa.bicep", "testa.bicepparm", "Subscription"), 0)
	rgName := GenerateRandomRGName()
	e2eImpl.AddBicepTemplate(fmt.Sprintf(`
targetScope='subscription'

resource newRG 'Microsoft.Resources/resourceGroups@2024-03-01' = {
  name: '%s'
  location: 'westus3'
}`, rgName),
		"testa.bicep",
		"using 'testa.bicep'",
		"testa.bicepparm")

	persistAndRun(t, &e2eImpl)

	subsriptionID, err := pipeline.LookupSubscriptionID(context.Background(), "ARO Hosted Control Planes (EA Subscription 1)")
	assert.NilError(t, err)

	cred, err := azauth.GetAzureTokenCredentials()
	assert.NilError(t, err)

	rgClient, err := armresources.NewResourceGroupsClient(subsriptionID, cred, nil)
	assert.NilError(t, err)

	_, err = rgClient.BeginDelete(context.Background(), rgName, nil)
	assert.NilError(t, err)
}

func TestE2EDryRun(t *testing.T) {
	if !shouldRunE2E() {
		t.Skip("Skipping end-to-end tests")
	}

	tmpDir := t.TempDir()

	e2eImpl := newE2E(tmpDir)

	e2eImpl.AddStep(pipeline.NewARMStep("output", "test.bicep", "test.bicepparm", "ResourceGroup"), 0)

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

	e2eImpl.EnableDryRun()

	persistAndRun(t, &e2eImpl)

	subsriptionID, err := pipeline.LookupSubscriptionID(context.Background(), "ARO Hosted Control Planes (EA Subscription 1)")
	assert.NilError(t, err)

	cred, err := azauth.GetAzureTokenCredentials()
	assert.NilError(t, err)

	zonesClient, err := armdns.NewZonesClient(subsriptionID, cred, nil)
	assert.NilError(t, err)

	_, err = zonesClient.Get(context.Background(), e2eImpl.rgName, "e2etestarmdeploy.foo.bar.example.com", nil)
	assert.ErrorContains(t, err, "RESPONSE 404: 404 Not Found")
}

func TestE2EOutputOnly(t *testing.T) {
	// if !shouldRunE2E() {
	// 	t.Skip("Skipping end-to-end tests")
	// }

	tmpDir := t.TempDir()

	e2eImpl := newE2E(tmpDir)
	e2eImpl.AddStep(pipeline.NewARMStep("parameterA", "testa.bicep", "testa.bicepparm", "ResourceGroup").WithOutputOnly(), 0)

	e2eImpl.AddStep(pipeline.NewShellStep(
		"readInput", "echo ${end} > env.txt",
	).WithVariables(
		pipeline.Variable{
			Name: "end",
			Input: &pipeline.Input{
				Name: "parameterA",
				Step: "parameterA",
			},
		},
	).WithDryRun(pipeline.DryRun{
		Command: "echo ${end} > env.txt"}),
		0)

	e2eImpl.AddBicepTemplate(`
param parameterA string
output parameterA string = parameterA`,
		"testa.bicep",
		`
using 'testa.bicep'
param parameterA = 'Hello Bicep'`,
		"testa.bicepparm")

	e2eImpl.EnableDryRun()

	persistAndRun(t, &e2eImpl)

	io, err := os.ReadFile(tmpDir + "/env.txt")
	assert.NilError(t, err)
	assert.Equal(t, string(io), "Hello Bicep\n")

}
