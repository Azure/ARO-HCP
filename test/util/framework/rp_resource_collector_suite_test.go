// Copyright 2026 Microsoft Corporation
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

package framework

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/fake"

	"github.com/openshift-eng/openshift-tests-extension/pkg/cmd"
	"github.com/openshift-eng/openshift-tests-extension/pkg/extension"
	"github.com/openshift-eng/openshift-tests-extension/pkg/extension/extensiontests"
	extginkgo "github.com/openshift-eng/openshift-tests-extension/pkg/ginkgo"
)

func TestMain(tests *testing.M) {
	if os.Getenv("RP_COLLECTOR_SUITE_HELPER") != "1" {
		os.Exit(tests.Run())
	}
	invocation := invocationContext()
	invocation.azureCredentials = &fake.TokenCredential{}
	invocation.subscriptionID = rpCollectionSubscription
	for _, name := range []string{"first collector fixture", "second collector fixture"} {
		ginkgo.It(name, func(ctx context.Context) {
			NewTestContext()
		})
	}
	specs, err := extginkgo.BuildExtensionTestSpecsFromOpenShiftGinkgoSuite()
	if err != nil {
		panic(err)
	}
	ext := extension.NewExtension("aro-hcp", "payload", "cuj-e2e-tests")
	ext.AddSpecs(specs)
	ext.AddSuite(extension.Suite{Name: "rp-collector-regression", Qualifiers: []string{"true"}})
	registry := extension.NewRegistry()
	registry.Register(ext)
	root := &cobra.Command{}
	root.AddCommand(cmd.DefaultExtensionCommands(registry)...)
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}

func TestRPCollectorRunSuiteRegistration(test *testing.T) {
	for _, scenario := range []struct {
		name, artifactDir, reason string
	}{
		{name: "without artifacts", reason: "ARTIFACT_DIR unset"},
		{name: "without owned groups", artifactDir: test.TempDir(), reason: "no resource groups owned by this test"},
	} {
		test.Run(scenario.name, func(test *testing.T) {
			test.Setenv("RP_COLLECTOR_SUITE_HELPER", "1")
			test.Setenv("ARTIFACT_DIR", scenario.artifactDir)
			test.Setenv("SHARED_DIR", "")
			test.Setenv("ARO_E2E_SKIP_CLEANUP", "true")
			test.Setenv(UsePooledIdentitiesEnvvar, "false")
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			command := exec.CommandContext(ctx, os.Args[0], "run-suite", "rp-collector-regression", "--output=json", "--max-concurrency=2")
			command.Dir = test.TempDir()
			output, err := command.Output()
			require.NoError(test, err, "run-suite output: %s", output)
			var results []extensiontests.ExtensionTestResult
			require.NoError(test, json.Unmarshal(output, &results), "run-suite output: %s", output)
			require.Len(test, results, 2)
			for _, result := range results {
				require.Equal(test, extensiontests.ResultPassed, result.Result, "%+v", result)
				message := "Skipping RP resource collection: " + scenario.reason
				require.Equal(test, 1, strings.Count(result.Output, message), "%s", result.Output)
				require.Less(test, strings.Index(result.Output, message), strings.Index(result.Output, "skipping resource cleanup"), "%s", result.Output)
				test.Logf("run-suite worker %q: %s before resource cleanup", result.Name, message)
			}
		})
	}
}
