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

	"github.com/go-logr/logr"
	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/require"

	"github.com/Azure/ARO-Tools/tools/cmdutils"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/dns/armdns"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"

	"github.com/Azure/ARO-HCP/tooling/templatize/cmd/pipeline/run"
	"github.com/Azure/ARO-HCP/tooling/templatize/pkg/pipeline"
)

func persistAndRun(t *testing.T, e2eImpl E2E) {
	opts, err := e2eImpl.Persist()
	require.NoError(t, err)

	require.NoError(t, run.RunPipeline(logr.NewContext(t.Context(), testr.New(t)), opts))
}

func TestE2EMake(t *testing.T) {
	t.Parallel()
	if !shouldRunE2E() {
		t.Skip("Skipping end-to-end tests")
	}

	tmpDir := t.TempDir()

	e2eImpl, err := newE2E(tmpDir, "../../testdata/e2eMake.yaml")
	require.NoError(t, err)

	defaults, ok := e2eImpl.config["defaults"]
	if !ok {
		panic("defaults not set")
	}
	asMap, ok := defaults.(map[string]any)
	if !ok {
		panic(fmt.Sprintf("defaults not a map[string]any: %T", defaults))
	}
	asMap["test_env"] = "test_env"
	e2eImpl.config["defaults"] = asMap

	e2eImpl.makefile = `
test:
	echo ${TEST_ENV} > env.txt
`
	persistAndRun(t, e2eImpl)

	io, err := os.ReadFile(tmpDir + "/env.txt")
	require.NoError(t, err)
	require.Equal(t, string(io), "test_env\n")
}

func TestE2EKubernetes(t *testing.T) {
	t.Parallel()
	if !shouldRunE2E() {
		t.Skip("Skipping end-to-end tests")
	}

	tmpDir := t.TempDir()

	e2eImpl, err := newE2E(tmpDir, "../../testdata/e2eKubernetes.yaml")
	require.NoError(t, err)

	defaults, ok := e2eImpl.config["defaults"]
	if !ok {
		panic("defaults not set")
	}
	asMap, ok := defaults.(map[string]any)
	if !ok {
		panic(fmt.Sprintf("defaults not a map[string]any: %T", defaults))
	}
	asMap["rg"] = "hcp-underlay-dev-westus3-svc"
	e2eImpl.config["defaults"] = asMap

	persistAndRun(t, e2eImpl)
}

func TestE2EArmDeploy(t *testing.T) {
	t.Parallel()
	if !shouldRunE2E() {
		t.Skip("Skipping end-to-end tests")
	}

	tmpDir := t.TempDir()

	e2eImpl, err := newE2E(tmpDir, "../../testdata/e2eArmDeploy.yaml")
	require.NoError(t, err)

	cleanup := e2eImpl.UseRandomRG()
	defer func() {
		err := cleanup()
		require.NoError(t, err)
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

	persistAndRun(t, e2eImpl)

	// Todo move to e2e module, if needed more than once
	subsriptionID, err := pipeline.LookupSubscriptionID(map[string]string{})(context.Background(), "ARO Hosted Control Planes (EA Subscription 1)")
	require.NoError(t, err)

	cred, err := cmdutils.GetAzureTokenCredentials()
	require.NoError(t, err)

	zonesClient, err := armdns.NewZonesClient(subsriptionID, cred, nil)
	require.NoError(t, err)

	zoneResp, err := zonesClient.Get(context.Background(), e2eImpl.rgName, "e2etestarmdeploy.foo.bar.example.com", nil)
	require.NoError(t, err)
	require.Equal(t, *zoneResp.Name, "e2etestarmdeploy.foo.bar.example.com")
}

func TestE2EShell(t *testing.T) {
	t.Parallel()
	if !shouldRunE2E() {
		t.Skip("Skipping end-to-end tests")
	}

	tmpDir, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)

	e2eImpl, err := newE2E(tmpDir, "../../testdata/e2eShell.yaml")
	require.NoError(t, err)

	persistAndRun(t, e2eImpl)

	io, err := os.ReadFile(tmpDir + "/env.txt")
	require.NoError(t, err)
	require.Equal(t, string(io), tmpDir+"\n")
}

func TestE2EArmDeployWithOutput(t *testing.T) {
	t.Parallel()
	if !shouldRunE2E() {
		t.Skip("Skipping end-to-end tests")
	}

	tmpDir := t.TempDir()

	e2eImpl, err := newE2E(tmpDir, "../../testdata/e2eArmDeployWithOutput.yaml")
	require.NoError(t, err)

	cleanup := e2eImpl.UseRandomRG()
	defer func() {
		err := cleanup()
		require.NoError(t, err)
	}()

	bicepFile := `
param zoneName string
output zoneName string = zoneName`
	paramFile := `
using 'test.bicep'
param zoneName = 'e2etestarmdeploy.foo.bar.example.com'
`
	e2eImpl.AddBicepTemplate(bicepFile, "test.bicep", paramFile, "test.bicepparm")

	persistAndRun(t, e2eImpl)

	io, err := os.ReadFile(tmpDir + "/env.txt")
	require.NoError(t, err)
	require.Equal(t, string(io), "e2etestarmdeploy.foo.bar.example.com\n")
}

func TestE2EArmDeployWithStaticVariable(t *testing.T) {
	t.Parallel()
	if !shouldRunE2E() {
		t.Skip("Skipping end-to-end tests")
	}

	tmpDir := t.TempDir()

	e2eImpl, err := newE2E(tmpDir, "../../testdata/e2eArmDeployWithStaticVariable.yaml")
	require.NoError(t, err)

	cleanup := e2eImpl.UseRandomRG()
	defer func() {
		err := cleanup()
		require.NoError(t, err)
	}()

	bicepFile := `
param zoneName string
output zoneName string = zoneName`
	paramFile := `
using 'test.bicep'
param zoneName = '__zoneName__'
`
	e2eImpl.AddBicepTemplate(bicepFile, "test.bicep", paramFile, "test.bicepparm")

	persistAndRun(t, e2eImpl)

	io, err := os.ReadFile(tmpDir + "/env.txt")
	require.NoError(t, err)
	require.Equal(t, string(io), "e2etestarmdeploy.foo.bar.example.com\n")
}

func TestE2EArmDeployWithOutputToArm(t *testing.T) {
	t.Parallel()
	if !shouldRunE2E() {
		t.Skip("Skipping end-to-end tests")
	}

	tmpDir := t.TempDir()

	e2eImpl, err := newE2E(tmpDir, "../../testdata/e2eArmDeployWithOutputToArm.yaml")
	require.NoError(t, err)

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
		require.NoError(t, err)
	}()

	persistAndRun(t, e2eImpl)

	io, err := os.ReadFile(tmpDir + "/env.txt")
	require.NoError(t, err)
	require.Equal(t, string(io), "Hello Bicep\n")
}

func TestE2EArmDeployWithOutputRGOverlap(t *testing.T) {
	t.Parallel()
	if !shouldRunE2E() {
		t.Skip("Skipping end-to-end tests")
	}

	tmpDir := t.TempDir()

	e2eImpl, err := newE2E(tmpDir, "../../testdata/e2eArmDeployWithOutputRGOverlap.yaml")
	require.NoError(t, err)

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
		require.NoError(t, err)
	}()
	persistAndRun(t, e2eImpl)

	io, err := os.ReadFile(tmpDir + "/env.txt")
	require.NoError(t, err)
	require.Equal(t, string(io), "Hello Bicep\n")
}

func TestE2EArmDeploySubscriptionScope(t *testing.T) {
	t.Parallel()
	if !shouldRunE2E() {
		t.Skip("Skipping end-to-end tests")
	}

	tmpDir := t.TempDir()

	e2eImpl, err := newE2E(tmpDir, "../../testdata/e2eArmDeploySubscriptionScope.yaml")
	require.NoError(t, err)

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

	persistAndRun(t, e2eImpl)

	subsriptionID, err := pipeline.LookupSubscriptionID(map[string]string{})(context.Background(), "ARO Hosted Control Planes (EA Subscription 1)")
	require.NoError(t, err)

	cred, err := cmdutils.GetAzureTokenCredentials()
	require.NoError(t, err)

	rgClient, err := armresources.NewResourceGroupsClient(subsriptionID, cred, nil)
	require.NoError(t, err)

	_, err = rgClient.BeginDelete(context.Background(), rgName, nil)
	require.NoError(t, err)
}

func TestE2EDryRun(t *testing.T) {
	t.Parallel()
	if !shouldRunE2E() {
		t.Skip("Skipping end-to-end tests")
	}

	tmpDir := t.TempDir()

	e2eImpl, err := newE2E(tmpDir, "../../testdata/e2eDryRun.yaml")
	require.NoError(t, err)

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

	persistAndRun(t, e2eImpl)

	subsriptionID, err := pipeline.LookupSubscriptionID(map[string]string{})(context.Background(), "ARO Hosted Control Planes (EA Subscription 1)")
	require.NoError(t, err)

	cred, err := cmdutils.GetAzureTokenCredentials()
	require.NoError(t, err)

	zonesClient, err := armdns.NewZonesClient(subsriptionID, cred, nil)
	require.NoError(t, err)

	_, err = zonesClient.Get(context.Background(), e2eImpl.rgName, "e2etestarmdeploy.foo.bar.example.com", nil)
	require.ErrorContains(t, err, "RESPONSE 404: 404 Not Found")
}

func TestE2EOutputOnly(t *testing.T) {
	t.Parallel()
	if !shouldRunE2E() {
		t.Skip("Skipping end-to-end tests")
	}

	tmpDir := t.TempDir()

	e2eImpl, err := newE2E(tmpDir, "../../testdata/e2eOutputOnly.yaml")
	require.NoError(t, err)

	e2eImpl.AddBicepTemplate(`
param parameterA string
output parameterA string = parameterA`,
		"testa.bicep",
		`
using 'testa.bicep'
param parameterA = 'Hello Bicep'`,
		"testa.bicepparm")

	e2eImpl.EnableDryRun()

	persistAndRun(t, e2eImpl)

	io, err := os.ReadFile(tmpDir + "/env.txt")
	require.NoError(t, err)
	require.Equal(t, string(io), "Hello Bicep\n")
}

func TestE2EOutputOnlyFailsIfContainsResources(t *testing.T) {
	t.Parallel()
	if !shouldRunE2E() {
		t.Skip("Skipping end-to-end tests")
	}

	tmpDir := t.TempDir()

	e2eImpl, err := newE2E(tmpDir, "../../testdata/e2eOutputOnly.yaml")
	require.NoError(t, err)

	bicepFile := `
param parameterA string
resource symbolicname 'Microsoft.Network/dnsZones@2018-05-01' = {
  location: 'global'
  name: parameterA
}
output parameterA string = parameterA`
	paramFile := `
using 'testa.bicep'
param parameterA = 'e2etestarmdeploy.foo.bar.example.com'
`
	e2eImpl.AddBicepTemplate(bicepFile, "testa.bicep", paramFile, "testa.bicepparm")

	e2eImpl.EnableDryRun()

	opts, err := e2eImpl.Persist()
	require.NoError(t, err)

	require.Error(t, run.RunPipeline(logr.NewContext(t.Context(), testr.New(t)), opts))
}

func TestE2EOutputOnlyWithModules(t *testing.T) {
	t.Parallel()
	if !shouldRunE2E() {
		t.Skip("Skipping end-to-end tests")
	}

	tmpDir := t.TempDir()

	e2eImpl, err := newE2E(tmpDir, "../../testdata/e2eOutputOnly.yaml")
	require.NoError(t, err)

	e2eImpl.AddBicepTemplate(`
param parameterA string
module parameterModule './module.bicep' = {
  name: 'parameterA-module'
  params: {
    parameterA: parameterA
  }
}
output parameterA string = parameterModule.outputs.parameterA`,
		"testa.bicep",
		`
using 'testa.bicep'
param parameterA = 'Hello Bicep'`,
		"testa.bicepparm")

	e2eImpl.AddBicepTemplate(`
param parameterA string
output parameterA string = parameterA`,
		"module.bicep",
		"",
		"")

	e2eImpl.EnableDryRun()

	persistAndRun(t, e2eImpl)

	io, err := os.ReadFile(tmpDir + "/env.txt")
	require.NoError(t, err)
	require.Equal(t, string(io), "Hello Bicep\n")
}
