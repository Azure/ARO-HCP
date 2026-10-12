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
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr/funcr"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	azfake "github.com/Azure/azure-sdk-for-go/sdk/azcore/fake"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"

	hcpsdk20240610preview "github.com/Azure/ARO-HCP/test/sdk/v20240610preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
)

func publishedTestManifest(t *testing.T) SharedClusterManifest {
	t.Helper()
	manifest := SharedClusterManifest{
		Version: 1, RunID: "run", OwnerPID: os.Getpid(), Ready: true,
		ExpiresAt: time.Now().Add(time.Hour).UTC(), SubscriptionID: "subscription",
		CustomerResourceGroup: "customer", ManagedResourceGroup: "managed", ClusterName: "cluster", Location: "eastus",
		ClusterResourceID: "/subscriptions/subscription/resourceGroups/customer/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster",
		Environment:       os.Getenv("AROHCP_ENV"), FrontendAddress: os.Getenv("FRONTEND_ADDRESS"), ResourceManagerEndpoint: os.Getenv("RESOURCE_MANAGER_ENDPOINT"),
	}
	t.Setenv(SharedFixtureRunIDEnv, manifest.RunID)
	t.Setenv(SharedFixtureOwnerPIDEnv, strconv.Itoa(manifest.OwnerPID))
	t.Setenv(SharedFixtureManifestEnv, filepath.Join(t.TempDir(), "manifest.json"))
	if err := WriteSharedClusterManifest(os.Getenv(SharedFixtureManifestEnv), manifest); err != nil {
		t.Fatal(err)
	}
	return manifest
}

func TestSharedClusterManifestValidation(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		mutate func(*SharedClusterManifest)
	}{
		{name: "valid"},
		{name: "wrong version", mutate: func(manifest *SharedClusterManifest) { manifest.Version++ }},
		{name: "wrong run", mutate: func(manifest *SharedClusterManifest) { manifest.RunID = "another" }},
		{name: "wrong owner", mutate: func(manifest *SharedClusterManifest) { manifest.OwnerPID = -1 }},
		{name: "unready", mutate: func(manifest *SharedClusterManifest) { manifest.Ready = false }},
		{name: "expired", mutate: func(manifest *SharedClusterManifest) { manifest.ExpiresAt = time.Now().Add(-time.Hour) }},
		{name: "missing RG", mutate: func(manifest *SharedClusterManifest) { manifest.CustomerResourceGroup = "" }},
		{name: "wrong identity", mutate: func(manifest *SharedClusterManifest) { manifest.ClusterResourceID += "wrong" }},
		{name: "wrong environment", mutate: func(manifest *SharedClusterManifest) { manifest.Environment += "wrong" }},
		{name: "wrong endpoint", mutate: func(manifest *SharedClusterManifest) { manifest.FrontendAddress += "wrong" }},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			manifest := publishedTestManifest(t)
			if testCase.mutate != nil {
				testCase.mutate(&manifest)
			}
			if err := WriteSharedClusterManifest(os.Getenv(SharedFixtureManifestEnv), manifest); err != nil {
				t.Fatal(err)
			}
			actual, err := ReadSharedClusterManifest()
			if testCase.mutate == nil {
				if err != nil || actual != manifest {
					t.Fatalf("round trip: manifest=%+v, error=%v", actual, err)
				}
			} else if err == nil {
				t.Fatal("invalid manifest accepted")
			}
			info, err := os.Stat(os.Getenv(SharedFixtureManifestEnv))
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode().Perm() != 0600 {
				t.Fatalf("manifest permissions: %v", info.Mode())
			}
			files, err := os.ReadDir(filepath.Dir(os.Getenv(SharedFixtureManifestEnv)))
			if err != nil || len(files) != 1 {
				t.Fatalf("temporary files left behind: %v, %v", files, err)
			}
		})
	}
}

func TestReadOnlyClusterClient(t *testing.T) {
	manifest := publishedTestManifest(t)
	calls := 0
	client, err := hcpsdk20240610preview.NewHcpOpenShiftClustersClient(manifest.SubscriptionID, &azfake.TokenCredential{}, &azcorearm.ClientOptions{
		ClientOptions: azcore.ClientOptions{Retry: policy.RetryOptions{MaxRetries: -1}, Transport: &fakeTransport{do: func(request *http.Request) (*http.Response, error) {
			calls++
			if request.Method != http.MethodGet || !strings.HasPrefix(request.URL.Path, "/subscriptions/subscription/resourceGroups/customer/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters") {
				t.Fatalf("unexpected request: %s %s", request.Method, request.URL.Path)
			}
			if strings.HasSuffix(request.URL.Path, "/cluster") {
				return jobIDResponse(`{"name":"cluster"}`)
			}
			return jobIDResponse(`{"value":[{"name":"cluster"}]}`)
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	readOnly := &ReadOnlyClusterClient{manifest: manifest, client: client}
	if _, err := readOnly.Get20240610(t.Context(), manifest.ClusterName); err != nil {
		t.Fatal(err)
	}
	clusters, err := readOnly.List20240610(t.Context())
	if err != nil || len(clusters) != 1 {
		t.Fatalf("LIST: %v, %v", clusters, err)
	}
	manifest.Ready = false
	if err := WriteSharedClusterManifest(os.Getenv(SharedFixtureManifestEnv), manifest); err != nil {
		t.Fatal(err)
	}
	if _, err := readOnly.Get20240610(t.Context(), manifest.ClusterName); err == nil {
		t.Fatal("GET allowed after teardown")
	}
	if _, err := readOnly.List20240610(t.Context()); err == nil {
		t.Fatal("LIST allowed after teardown")
	}
	if calls != 2 {
		t.Fatalf("unexpected number of HTTP calls: %d", calls)
	}
}

func TestSharedFixtureRunTagOnInitialCreate(t *testing.T) {
	client := jobIDTestClient(t, func(request *http.Request) (*http.Response, error) {
		var group armresources.ResourceGroup
		if err := json.NewDecoder(request.Body).Decode(&group); err != nil {
			t.Fatal(err)
		}
		if request.Method != http.MethodPut || group.Tags[SharedFixtureRunIDTag] == nil || *group.Tags[SharedFixtureRunIDTag] != "run" {
			t.Fatalf("initial RG creation must carry run ownership: %+v", group.Tags)
		}
		return jobIDResponse(`{"name":"group","location":"eastus"}`)
	})
	if _, err := CreateResourceGroup(t.Context(), client, "group", "eastus", time.Hour, time.Minute, map[string]*string{SharedFixtureRunIDTag: to.Ptr("run")}); err != nil {
		t.Fatal(err)
	}
}

func TestRunResourceContextRefusesForeignCleanup(t *testing.T) {
	calls := 0
	factory, err := armresources.NewClientFactory("subscription", &azfake.TokenCredential{}, &azcorearm.ClientOptions{
		ClientOptions: azcore.ClientOptions{Transport: &fakeTransport{do: func(request *http.Request) (*http.Response, error) {
			calls++
			if request.Method != http.MethodGet {
				t.Fatalf("attempted mutation of foreign RG: %s", request.Method)
			}
			return jobIDResponse(`{"name":"customer","tags":{"runID.aro-hcp-ci.redhat.com":"another-run"}}`)
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	run := &RunResourceContext{resources: &perItOrDescribeTestContext{perBinaryInvocationTestContext: &perBinaryInvocationTestContext{}, armResourcesClientFactory: factory}, creationAttempted: true, manifest: SharedClusterManifest{RunID: "run", CustomerResourceGroup: "customer"}}
	if err := run.Cleanup(t.Context()); err == nil || !strings.Contains(err.Error(), "refusing fixture cleanup") {
		t.Fatalf("cleanup error = %v", err)
	}
	if calls != 1 {
		t.Fatalf("cleanup calls = %d", calls)
	}
}

func TestRunResourceContextSkipCleanup(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		value    string
		wantSkip bool
	}{
		{name: "empty"},
		{name: "false", value: "false"},
		{name: "true", value: "true", wantSkip: true},
		{name: "one", value: "1", wantSkip: true},
		{name: "uppercase", value: "TRUE", wantSkip: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Setenv("ARO_E2E_SKIP_CLEANUP", testCase.value)
			invocation, err := newInvocationContext()
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			factory, err := armresources.NewClientFactory("subscription", &azfake.TokenCredential{}, &azcorearm.ClientOptions{
				ClientOptions: azcore.ClientOptions{Transport: &fakeTransport{do: func(request *http.Request) (*http.Response, error) {
					calls++
					if request.Method != http.MethodGet {
						t.Fatalf("unexpected cleanup mutation: %s", request.Method)
					}
					return jobIDResponse(`{"name":"customer","tags":{"runID.aro-hcp-ci.redhat.com":"another-run"}}`)
				}}},
			})
			if err != nil {
				t.Fatal(err)
			}
			var observations []string
			run := &RunResourceContext{
				resources:         &perItOrDescribeTestContext{perBinaryInvocationTestContext: invocation, armResourcesClientFactory: factory},
				creationAttempted: true,
				manifest:          SharedClusterManifest{RunID: "run", CustomerResourceGroup: "customer", ManagedResourceGroup: "managed"},
				logger:            funcr.New(func(_, message string) { observations = append(observations, message) }, funcr.Options{}),
			}
			err = run.Cleanup(t.Context())
			if testCase.wantSkip {
				if err != nil || calls != 0 {
					t.Fatalf("skip cleanup must return without Azure calls: error=%v, calls=%d", err, calls)
				}
				logged := strings.Join(observations, "\n")
				for _, expected := range []string{"retained for debugging", "ARO_E2E_SKIP_CLEANUP", "customer", "managed"} {
					if !strings.Contains(logged, expected) {
						t.Errorf("retention log missing %q: %s", expected, logged)
					}
				}
			} else {
				if calls != 1 || err == nil || !strings.Contains(err.Error(), "refusing fixture cleanup") {
					t.Fatalf("normal cleanup must reach the ownership check: error=%v, calls=%d", err, calls)
				}
				if len(observations) != 0 {
					t.Fatalf("normal cleanup unexpectedly logged retention: %v", observations)
				}
			}
		})
	}
}
