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

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/openshift-eng/openshift-tests-extension/pkg/cmd"
	e "github.com/openshift-eng/openshift-tests-extension/pkg/extension"
	et "github.com/openshift-eng/openshift-tests-extension/pkg/extension/extensiontests"
	"github.com/openshift-eng/openshift-tests-extension/pkg/util/sets"

	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
)

type fakeFixtureResources struct {
	provisions        int
	cleanups          int
	manifest          framework.SharedClusterManifest
	provisionErr      error
	cleanupErr        error
	cleanupContextErr error
}

func (resources *fakeFixtureResources) Provision(context.Context) (framework.SharedClusterManifest, error) {
	resources.provisions++
	return resources.manifest, resources.provisionErr
}

func (resources *fakeFixtureResources) Cleanup(ctx context.Context) error {
	resources.cleanups++
	resources.cleanupContextErr = ctx.Err()
	return resources.cleanupErr
}

func fixtureTestManifest(runID string) framework.SharedClusterManifest {
	return framework.SharedClusterManifest{
		Version: 1, RunID: runID, OwnerPID: os.Getpid(), Ready: true,
		ExpiresAt: time.Now().Add(time.Hour).UTC(), SubscriptionID: "subscription",
		CustomerResourceGroup: "customer", ManagedResourceGroup: "managed",
		ClusterName: "cluster", Location: "eastus",
		ClusterResourceID: "/subscriptions/subscription/resourceGroups/customer/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster",
		Environment:       os.Getenv("AROHCP_ENV"), FrontendAddress: os.Getenv("FRONTEND_ADDRESS"),
		ResourceManagerEndpoint: os.Getenv("RESOURCE_MANAGER_ENDPOINT"),
	}
}

func fixtureTestCLI(specs et.ExtensionTestSpecs, factory fixtureResourceFactory) *cobra.Command {
	extension := e.NewExtension("test", "test", "fixture")
	extension.AddSpecs(specs)
	extension.AddSuite(e.Suite{Name: "borrowers", Qualifiers: []string{fmt.Sprintf("%q in labels", labels.RequireSharedReadOnlyCluster[0])}})
	extension.AddSuite(e.Suite{Name: "unrelated", Qualifiers: []string{"name == 'ordinary'"}})
	registry := e.NewRegistry()
	registry.Register(extension)
	runner := newSharedFixtureRunner(specs, factory)
	root := &cobra.Command{SilenceErrors: true, SilenceUsage: true}
	for _, command := range cmd.DefaultExtensionCommands(registry) {
		if command.Name() == "run-suite" || command.Name() == "run-test" {
			runner.wrapRun(command)
		}
		root.AddCommand(command)
	}
	return root
}

func fixtureTestSpec(name string, borrower bool, run func(context.Context) *et.ExtensionTestResult) *et.ExtensionTestSpec {
	specLabels := sets.New[string]()
	if borrower {
		specLabels.Insert(labels.RequireSharedReadOnlyCluster[0])
	}
	return &et.ExtensionTestSpec{Name: name, Labels: specLabels, Run: run, RunParallel: run}
}

func TestSharedFixtureCommandContext(t *testing.T) {
	for _, key := range []string{framework.SharedFixtureRunIDEnv, framework.SharedFixtureOwnerPIDEnv, framework.SharedFixtureManifestEnv} {
		t.Setenv(key, "")
	}
	parent := t.Context()
	runner := &sharedFixtureRunner{}
	var executionContext context.Context
	command := &cobra.Command{RunE: func(command *cobra.Command, _ []string) error {
		executionContext = command.Context()
		if executionContext != runner.active.ctx {
			t.Fatal("wrapped command must receive the fixture's signal-aware context")
		}
		if executionContext == parent || executionContext.Done() == nil {
			t.Fatal("wrapped command must receive a derived cancellable context")
		}
		if err := executionContext.Err(); err != nil {
			t.Fatalf("command context canceled before execution: %v", err)
		}
		return nil
	}}
	command.SetContext(parent)
	runner.wrapRun(command)
	if err := command.RunE(command, nil); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(executionContext.Err(), context.Canceled) {
		t.Fatalf("signal context must be released after execution, got %v", executionContext.Err())
	}
	if err := parent.Err(); err != nil {
		t.Fatalf("parent context must remain uncanceled: %v", err)
	}
}

func TestSharedFixtureCommands(t *testing.T) {
	for _, testCase := range []struct {
		name          string
		args          []string
		wantProvision int
		wantError     string
		provisionErr  error
		cleanupErr    error
		subprocess    bool
	}{
		{name: "list", args: []string{"list", "tests"}},
		{name: "unrelated suite", args: []string{"run-suite", "unrelated"}},
		{name: "unrelated test", args: []string{"run-test", "ordinary"}},
		{name: "multiple borrowers", args: []string{"run-suite", "borrowers"}, wantProvision: 1},
		{name: "multiple subprocess borrowers", args: []string{"run-suite", "borrowers"}, wantProvision: 1, subprocess: true},
		{name: "standalone borrower", args: []string{"run-test", "borrower-one"}, wantProvision: 1},
		{name: "partial setup cleanup", args: []string{"run-suite", "borrowers"}, wantProvision: 1, provisionErr: errors.New("partial setup"), wantError: "partial setup"},
		{name: "cleanup failure", args: []string{"run-test", "borrower-one"}, wantProvision: 1, cleanupErr: errors.New("cleanup failed"), wantError: "cleanup failed"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			for _, key := range []string{framework.SharedFixtureRunIDEnv, framework.SharedFixtureOwnerPIDEnv, framework.SharedFixtureManifestEnv} {
				t.Setenv(key, "")
			}
			t.Setenv("ARTIFACT_DIR", t.TempDir())
			resources := &fakeFixtureResources{provisionErr: testCase.provisionErr, cleanupErr: testCase.cleanupErr}
			var manifestPath string
			factoryCalls := 0
			factory := func(runID, path string) (fixtureResources, error) {
				factoryCalls++
				manifestPath = path
				resources.manifest = fixtureTestManifest(runID)
				return resources, nil
			}
			borrow := func(ctx context.Context) *et.ExtensionTestResult {
				if testCase.subprocess {
					executable, err := os.Executable()
					if err != nil {
						return &et.ExtensionTestResult{Result: et.ResultFailed, Error: err.Error()}
					}
					worker := exec.CommandContext(ctx, executable, "-test.run=^TestSharedFixtureWorkerSubprocess$")
					worker.Env = append(os.Environ(), "SHARED_FIXTURE_TEST_WORKER=true")
					if output, err := worker.CombinedOutput(); err != nil {
						return &et.ExtensionTestResult{Result: et.ResultFailed, Error: fmt.Sprintf("worker: %v: %s", err, output)}
					}
				}
				manifest, err := framework.ReadSharedClusterManifest()
				if err != nil || manifest != resources.manifest {
					return &et.ExtensionTestResult{Result: et.ResultFailed, Error: fmt.Sprintf("wrong manifest: %v", err)}
				}
				return &et.ExtensionTestResult{Result: et.ResultPassed}
			}
			specs := et.ExtensionTestSpecs{
				fixtureTestSpec("borrower-one", true, borrow),
				fixtureTestSpec("borrower-two", true, borrow),
				fixtureTestSpec("ordinary", false, func(context.Context) *et.ExtensionTestResult { return &et.ExtensionTestResult{Result: et.ResultPassed} }),
			}
			root := fixtureTestCLI(specs, factory)
			root.SetArgs(testCase.args)
			err := root.Execute()
			if testCase.wantError == "" && err != nil || testCase.wantError != "" && (err == nil || !strings.Contains(err.Error(), testCase.wantError)) {
				t.Fatalf("Execute error = %v; want %q", err, testCase.wantError)
			}
			if factoryCalls != testCase.wantProvision || resources.provisions != testCase.wantProvision || resources.cleanups != testCase.wantProvision {
				t.Fatalf("factory/provision/cleanup = %d/%d/%d; want %d each", factoryCalls, resources.provisions, resources.cleanups, testCase.wantProvision)
			}
			if resources.cleanupContextErr != nil {
				t.Fatalf("cleanup context already canceled: %v", resources.cleanupContextErr)
			}
			if testCase.wantProvision != 0 {
				t.Setenv(framework.SharedFixtureRunIDEnv, resources.manifest.RunID)
				t.Setenv(framework.SharedFixtureOwnerPIDEnv, strconv.Itoa(os.Getpid()))
				t.Setenv(framework.SharedFixtureManifestEnv, manifestPath)
				if _, err := framework.ReadSharedClusterManifest(); err == nil {
					t.Fatal("teardown must invalidate the published manifest")
				}
			}
		})
	}
}

func TestSharedFixtureWorkerSubprocess(t *testing.T) {
	if os.Getenv("SHARED_FIXTURE_TEST_WORKER") == "true" {
		root := fixtureTestCLI(et.ExtensionTestSpecs{fixtureTestSpec("borrower", true, func(context.Context) *et.ExtensionTestResult {
			_, err := framework.ReadSharedClusterManifest()
			if err != nil {
				return &et.ExtensionTestResult{Result: et.ResultFailed, Error: err.Error()}
			}
			return &et.ExtensionTestResult{Result: et.ResultPassed}
		})}, func(string, string) (fixtureResources, error) {
			return nil, errors.New("WORKER_ATTEMPTED_PROVISIONING")
		})
		root.SetArgs([]string{"run-test", "borrower"})
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
		return
	}
	for _, testCase := range []struct {
		name                                           string
		missingFile, missingPath, staleRun, staleOwner bool
	}{
		{name: "valid"}, {name: "missing file", missingFile: true}, {name: "missing path", missingPath: true},
		{name: "stale run", staleRun: true}, {name: "stale owner", staleOwner: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			manifest := fixtureTestManifest("worker-test")
			path := filepath.Join(t.TempDir(), "manifest.json")
			if !testCase.missingFile {
				if err := framework.WriteSharedClusterManifest(path, manifest); err != nil {
					t.Fatal(err)
				}
			}
			if testCase.missingPath {
				path = ""
			}
			runID := manifest.RunID
			if testCase.staleRun {
				runID = "different-run"
			}
			ownerPID := os.Getpid()
			if testCase.staleOwner {
				ownerPID = -1
			}
			t.Setenv(framework.SharedFixtureRunIDEnv, runID)
			t.Setenv(framework.SharedFixtureOwnerPIDEnv, strconv.Itoa(ownerPID))
			t.Setenv(framework.SharedFixtureManifestEnv, path)
			t.Setenv("SHARED_FIXTURE_TEST_WORKER", "true")
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			command := exec.CommandContext(t.Context(), executable, "-test.run=^TestSharedFixtureWorkerSubprocess$")
			output, err := command.CombinedOutput()
			if strings.Contains(string(output), "WORKER_ATTEMPTED_PROVISIONING") {
				t.Fatalf("worker provisioned: %s", output)
			}
			if (err == nil) != (testCase.name == "valid") {
				t.Fatalf("unexpected worker result %v: %s", err, output)
			}
		})
	}
}
