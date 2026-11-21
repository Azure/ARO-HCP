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
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	azfake "github.com/Azure/azure-sdk-for-go/sdk/azcore/fake"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
)

func TestStageCanaryScope(t *testing.T) {
	for _, test := range []struct{ cloud, environment, suite string }{
		{"public", "stg", "stage/parallel"},
		{"public", "prod", "stage/parallel"},
		{"public", "int", "integration/parallel"},
		{"dev", "dev", "rp-api-compat-all/parallel"},
		{"public", "stg", "stage/parallel/all"},
		{"", "stg", "stage/parallel"},
	} {
		t.Run(test.cloud+"/"+test.environment+"/"+test.suite, func(t *testing.T) {
			t.Setenv("ARO_HCP_CLOUD", test.cloud)
			t.Setenv("ARO_HCP_DEPLOY_ENV", test.environment)
			t.Setenv("ARO_HCP_SUITE_NAME", test.suite)
			t.Setenv(UsePooledIdentitiesEnvvar, "true")
			want := test.cloud == "public" && test.environment == "stg" && test.suite == "stage/parallel"
			if StageCanaryEnabled() != want || pooledIdentities() == want {
				t.Fatal("canary filtering and dedicated identities must be limited to the explicit Stage suite")
			}
		})
	}
}

func TestRetainStageCanaryResourceGroup(t *testing.T) {
	for _, scenario := range []string{"success", "wrong job", "pooled identities", "another environment", "missing build ID", "missing shared dir", "unrelated group", "multiple groups", "patch failure", "marker failure"} {
		t.Run(scenario, func(t *testing.T) {
			t.Setenv("ARO_HCP_CLOUD", "public")
			t.Setenv("ARO_HCP_DEPLOY_ENV", "stg")
			t.Setenv("ARO_HCP_SUITE_NAME", "stage/parallel")
			t.Setenv("BUILD_ID", "123")
			name := StageCanaryResourceGroupPrefix + "-example"
			shared := t.TempDir()
			marker := filepath.Join(shared, "tracked-resource-group_"+name)
			if scenario == "marker failure" {
				if err := os.Mkdir(marker, 0755); err != nil {
					t.Fatal(err)
				}
				marker = filepath.Join(marker, "keep")
			}
			if err := os.WriteFile(marker, nil, 0644); err != nil {
				t.Fatal(err)
			}
			patches := 0
			factory, err := armresources.NewClientFactory(fakeSubscriptionID, &azfake.TokenCredential{}, &azcorearm.ClientOptions{
				ClientOptions: azcore.ClientOptions{Retry: policy.RetryOptions{MaxRetries: -1}, Transport: &fakeTransport{do: func(request *http.Request) (*http.Response, error) {
					if request.Method == http.MethodGet {
						job := "123"
						if scenario == "wrong job" {
							job = "other"
						}
						return jobIDResponse(`{"tags":{"jobID.aro-hcp-ci.redhat.com":"` + job + `","e2e.aro-hcp-ci.redhat.com":"true","unrelated":"preserved"}}`)
					}
					if request.Method != http.MethodPatch {
						t.Fatalf("unexpected method %s", request.Method)
					}
					patches++
					var group armresources.ResourceGroupPatchable
					if err := json.NewDecoder(request.Body).Decode(&group); err != nil {
						t.Fatal(err)
					}
					_, hasImmediateJobTag := group.Tags[ProwJobIDTag]
					if hasImmediateJobTag || ptr.Deref(group.Tags[retainedCanaryJobIDTag], "") != "123" ||
						ptr.Deref(group.Tags["e2e.aro-hcp-ci.redhat.com"], "") != "true" || ptr.Deref(group.Tags["unrelated"], "") != "preserved" {
						t.Fatal("retention must preserve provenance, expiry-cleaner eligibility and unrelated tags")
					}
					expires, err := time.Parse(time.RFC3339, ptr.Deref(group.Tags["deleteAfter.aro-hcp-ci.redhat.com"], ""))
					if err != nil || time.Until(expires) < 23*time.Hour || time.Until(expires) > 24*time.Hour {
						t.Fatal("retention must expire within 24 hours")
					}
					if scenario == "patch failure" {
						return &http.Response{StatusCode: http.StatusForbidden, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"error":{"code":"Forbidden","message":"test"}}`))}, nil
					}
					return jobIDResponse(`{}`)
				}}},
			})
			if err != nil {
				t.Fatal(err)
			}
			tc := &perItOrDescribeTestContext{
				perBinaryInvocationTestContext: &perBinaryInvocationTestContext{sharedDir: shared, pooledIdentities: scenario == "pooled identities"},
				knownResourceGroups:            []string{name},
				armResourcesClientFactory:      factory,
			}
			switch scenario {
			case "another environment":
				t.Setenv("ARO_HCP_DEPLOY_ENV", "prod")
			case "missing build ID":
				t.Setenv("BUILD_ID", " ")
			case "missing shared dir":
				tc.perBinaryInvocationTestContext.sharedDir = ""
			case "unrelated group":
				tc.knownResourceGroups = []string{"other"}
			case "multiple groups":
				tc.knownResourceGroups = append(tc.knownResourceGroups, "other")
			}
			_, err = tc.RetainStageCanaryResourceGroup(context.Background(), name)
			if scenario == "success" {
				if err != nil || !tc.retainedStageCanary || patches != 1 {
					t.Fatalf("canary handoff failed: %v", err)
				}
				if _, err := os.Stat(marker); !os.IsNotExist(err) {
					t.Fatal("retained group must be unregistered from the post-job tracked-file cleanup")
				}
			} else {
				if err == nil || tc.retainedStageCanary {
					t.Fatal("failed handoff must leave normal per-test teardown enabled")
				}
				if _, err := os.Stat(marker); err != nil {
					t.Fatal("failed handoff must keep the post-job cleanup marker")
				}
				if scenario != "patch failure" && scenario != "marker failure" && patches != 0 {
					t.Fatal("retention preconditions must be checked before modifying any tags")
				}
			}
		})
	}
}
