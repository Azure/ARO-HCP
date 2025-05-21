// Copyright 2025 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package testutil

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/Azure/ARO-Tools/pkg/config"
	"github.com/Azure/ARO-Tools/pkg/types"

	"github.com/stretchr/testify/assert"

	"github.com/Azure/ARO-HCP/tooling/templatize/cmd/pipeline/run"
	"github.com/Azure/ARO-HCP/tooling/templatize/pkg/azauth"
	"github.com/Azure/ARO-HCP/tooling/templatize/pkg/pipeline"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/dns/armdns"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
)

func persistAndRun(t *testing.T, e2eImpl E2E) {
	err := e2eImpl.Persist()
	assert.NoError(t, err)

	cmd, err := run.NewCommand()
	assert.NoError(t, err)

	err = cmd.Execute()
	assert.NoError(t, err)
}

func TestE2EMake(t *testing.T) {
	if !shouldRunE2E() {
		t.Skip("Skipping end-to-end tests")
	}

	tmpDir := t.TempDir()

	e2eImpl := newE2E(tmpDir)
	e2eImpl.AddStep(
		types.NewShellStep("test", "make test").WithVariables(types.Variable{
			Name:      "TEST_ENV",
			ConfigRef: "test_env",
		}),
		0,
	)

	e2eImpl.SetConfig(config.Configuration{"defaults": config.Configuration{"test_env": "test_env"}})

	e2eImpl.makefile = `
test:
	echo ${TEST_ENV} > env.txt
`
	persistAndRun(t, &e2eImpl)

	io, err := os.ReadFile(tmpDir + "/env.txt")
	assert.NoError(t, err)
	assert.Equal(t, string(io), "test_env\n")
}

func TestE2EKubernetes(t *testing.T) {
	if !shouldRunE2E() {
		t.Skip("Skipping end-to-end tests")
	}

	tmpDir := t.TempDir()

	e2eImpl := newE2E(tmpDir)
	e2eImpl.AddStep(types.NewShellStep("test", "kubectl get namespaces").WithAKSCluster("dev-svc"), 0)

	e2eImpl.SetConfig(config.Configuration{"defaults": config.Configuration{"rg": "hcp-underlay-dev-svc"}})

	persistAndRun(t, &e2eImpl)
}

func TestE2EArmDeploy(t *testing.T) {
	if !shouldRunE2E() {
		t.Skip("Skipping end-to-end tests")
	}

	tmpDir := t.TempDir()

	e2eImpl := newE2E(tmpDir)
	e2eImpl.AddStep(types.NewARMStep("test", "test.bicep", "test.bicepparm", "ResourceGroup"), 0)
	cleanup := e2eImpl.UseRandomRG()
	defer func() {
		err := cleanup()
		assert.NoError(t, err)
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
	assert.NoError(t, err)

	cred, err := azauth.GetAzureTokenCredentials()
	assert.NoError(t, err)

	zonesClient, err := armdns.NewZonesClient(subsriptionID, cred, nil)
	assert.NoError(t, err)

	zoneResp, err := zonesClient.Get(context.Background(), e2eImpl.rgName, "e2etestarmdeploy.foo.bar.example.com", nil)
	assert.NoError(t, err)
	assert.Equal(t, *zoneResp.Name, "e2etestarmdeploy.foo.bar.example.com")
}

func TestE2EShell(t *testing.T) {
	if !shouldRunE2E() {
		t.Skip("Skipping end-to-end tests")
	}

	tmpDir, err := filepath.EvalSymlinks(t.TempDir())
	assert.NoError(t, err)

	e2eImpl := newE2E(tmpDir)

	e2eImpl.AddStep(
		types.NewShellStep("readInput", "/bin/echo ${PWD} > env.txt"),
		0,
	)

	persistAndRun(t, &e2eImpl)

	io, err := os.ReadFile(tmpDir + "/env.txt")
	assert.NoError(t, err)
	assert.Equal(t, string(io), tmpDir+"\n")
}

func TestE2EArmDeployWithOutput(t *testing.T) {
	if !shouldRunE2E() {
		t.Skip("Skipping end-to-end tests")
	}

	tmpDir := t.TempDir()

	e2eImpl := newE2E(tmpDir)

	e2eImpl.AddStep(types.NewARMStep("createZone", "test.bicep", "test.bicepparm", "ResourceGroup"), 0)

	e2eImpl.AddStep(types.NewShellStep(
		"readInput", "echo ${zoneName} > env.txt",
	).WithVariables(
		types.Variable{
			Name: "zoneName",
			Input: &types.Input{
				Name: "zoneName",
				Step: "createZone",
			},
		},
	), 0)

	cleanup := e2eImpl.UseRandomRG()
	defer func() {
		err := cleanup()
		assert.NoError(t, err)
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
	assert.NoError(t, err)
	assert.Equal(t, string(io), "e2etestarmdeploy.foo.bar.example.com\n")
}

func TestE2EArmDeployWithStaticVariable(t *testing.T) {
	if !shouldRunE2E() {
		t.Skip("Skipping end-to-end tests")
	}

	tmpDir := t.TempDir()

	e2eImpl := newE2E(tmpDir)

	e2eImpl.AddStep(types.NewARMStep(
		"createZone", "test.bicep", "test.bicepparm", "ResourceGroup",
	).WithVariables(
		types.Variable{
			Name:  "zoneName",
			Value: "e2etestarmdeploy.foo.bar.example.com",
		},
	), 0)

	e2eImpl.AddStep(types.NewShellStep(
		"readInput", "echo ${zoneName} > env.txt",
	).WithVariables(
		types.Variable{
			Name: "zoneName",
			Input: &types.Input{
				Name: "zoneName",
				Step: "createZone",
			},
		},
	), 0)

	cleanup := e2eImpl.UseRandomRG()
	defer func() {
		err := cleanup()
		assert.NoError(t, err)
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
	assert.NoError(t, err)
	assert.Equal(t, string(io), "e2etestarmdeploy.foo.bar.example.com\n")
}

func TestE2EArmDeployWithOutputToArm(t *testing.T) {
	if !shouldRunE2E() {
		t.Skip("Skipping end-to-end tests")
	}

	tmpDir := t.TempDir()

	e2eImpl := newE2E(tmpDir)
	e2eImpl.AddStep(types.NewARMStep("stepA", "testa.bicep", "testa.bicepparm", "ResourceGroup"), 0)
	e2eImpl.AddStep(types.NewARMStep("stepB", "testb.bicep", "testb.bicepparm", "ResourceGroup").WithVariables(types.Variable{
		Name: "parameterB",
		Input: &types.Input{
			Name: "parameterA",
			Step: "stepA",
		},
	}), 0)

	e2eImpl.AddStep(types.NewShellStep(
		"readInput", "echo ${end} > env.txt",
	).WithVariables(
		types.Variable{
			Name: "end",
			Input: &types.Input{
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
		assert.NoError(t, err)
	}()
	persistAndRun(t, &e2eImpl)

	io, err := os.ReadFile(tmpDir + "/env.txt")
	assert.NoError(t, err)
	assert.Equal(t, string(io), "Hello Bicep\n")
}

func TestE2EArmDeployWithOutputRGOverlap(t *testing.T) {
	if !shouldRunE2E() {
		t.Skip("Skipping end-to-end tests")
	}

	tmpDir := t.TempDir()

	e2eImpl := newE2E(tmpDir)
	e2eImpl.AddStep(types.NewARMStep("parameterA", "testa.bicep", "testa.bicepparm", "ResourceGroup"), 0)

	e2eImpl.AddResourceGroup()

	e2eImpl.AddStep(types.NewShellStep("readInput", "echo ${end} > env.txt").WithVariables(
		types.Variable{
			Name: "end",
			Input: &types.Input{
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
		assert.NoError(t, err)
	}()
	persistAndRun(t, &e2eImpl)

	io, err := os.ReadFile(tmpDir + "/env.txt")
	assert.NoError(t, err)
	assert.Equal(t, string(io), "Hello Bicep\n")
}

func TestE2EArmDeploySubscriptionScope(t *testing.T) {
	if !shouldRunE2E() {
		t.Skip("Skipping end-to-end tests")
	}

	tmpDir := t.TempDir()

	e2eImpl := newE2E(tmpDir)
	e2eImpl.AddStep(types.NewARMStep("parameterA", "testa.bicep", "testa.bicepparm", "Subscription"), 0)
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
	assert.NoError(t, err)

	cred, err := azauth.GetAzureTokenCredentials()
	assert.NoError(t, err)

	rgClient, err := armresources.NewResourceGroupsClient(subsriptionID, cred, nil)
	assert.NoError(t, err)

	_, err = rgClient.BeginDelete(context.Background(), rgName, nil)
	assert.NoError(t, err)
}

func TestE2EDryRun(t *testing.T) {
	if !shouldRunE2E() {
		t.Skip("Skipping end-to-end tests")
	}

	tmpDir := t.TempDir()

	e2eImpl := newE2E(tmpDir)

	e2eImpl.AddStep(types.NewARMStep("output", "test.bicep", "test.bicepparm", "ResourceGroup"), 0)

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
	assert.NoError(t, err)

	cred, err := azauth.GetAzureTokenCredentials()
	assert.NoError(t, err)

	zonesClient, err := armdns.NewZonesClient(subsriptionID, cred, nil)
	assert.NoError(t, err)

	_, err = zonesClient.Get(context.Background(), e2eImpl.rgName, "e2etestarmdeploy.foo.bar.example.com", nil)
	assert.ErrorContains(t, err, "RESPONSE 404: 404 Not Found")
}

func TestE2EOutputOnly(t *testing.T) {
	if !shouldRunE2E() {
		t.Skip("Skipping end-to-end tests")
	}

	tmpDir := t.TempDir()

	e2eImpl := newE2E(tmpDir)
	e2eImpl.AddStep(types.NewARMStep("parameterA", "testa.bicep", "testa.bicepparm", "ResourceGroup").WithOutputOnly(), 0)

	e2eImpl.AddStep(types.NewShellStep(
		"readInput", "echo ${end} > env.txt",
	).WithVariables(
		types.Variable{
			Name: "end",
			Input: &types.Input{
				Name: "parameterA",
				Step: "parameterA",
			},
		},
	).WithDryRun(types.DryRun{
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
	assert.NoError(t, err)
	assert.Equal(t, string(io), "Hello Bicep\n")

}
