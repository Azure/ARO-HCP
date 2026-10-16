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
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	azfake "github.com/Azure/azure-sdk-for-go/sdk/azcore/fake"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
)

func jobIDTestClient(t *testing.T, handler func(*http.Request) (*http.Response, error)) *armresources.ResourceGroupsClient {
	t.Helper()
	client, err := armresources.NewResourceGroupsClient(fakeSubscriptionID, &azfake.TokenCredential{}, &azcorearm.ClientOptions{
		ClientOptions: azcore.ClientOptions{Transport: &fakeTransport{do: handler}, Retry: policy.RetryOptions{MaxRetries: -1}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func jobIDResponse(body string) (*http.Response, error) {
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
}

func TestCreateResourceGroupInitialJobIDTag(t *testing.T) {
	for _, testCase := range []struct {
		name, value string
		unset       bool
	}{
		{name: "set", value: "123"},
		{name: "exact value", value: " 123 "},
		{name: "empty"},
		{name: "whitespace", value: " \t\n"},
		{name: "absent", unset: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Setenv("BUILD_ID", testCase.value)
			if testCase.unset {
				if err := os.Unsetenv("BUILD_ID"); err != nil {
					t.Fatal(err)
				}
			}
			calls := 0
			client := jobIDTestClient(t, func(request *http.Request) (*http.Response, error) {
				calls++
				if request.Method != http.MethodPut {
					t.Fatalf("expected initial PUT, got %s", request.Method)
				}
				var group armresources.ResourceGroup
				if err := json.NewDecoder(request.Body).Decode(&group); err != nil {
					t.Fatal(err)
				}
				tag, exists := group.Tags[ProwJobIDTag]
				if strings.TrimSpace(testCase.value) == "" {
					if exists {
						t.Fatal("job ID tag must be absent")
					}
				} else if tag == nil || *tag != testCase.value {
					t.Fatalf("incorrect job tag: %v", tag)
				}
				if group.Tags["e2e.aro-hcp-ci.redhat.com"] == nil || *group.Tags["e2e.aro-hcp-ci.redhat.com"] != "true" {
					t.Fatal("missing e2e tag")
				}
				if group.Tags["deleteAfter.aro-hcp-ci.redhat.com"] == nil {
					t.Fatal("missing expiration tag")
				}
				if _, err := time.Parse(time.RFC3339, *group.Tags["deleteAfter.aro-hcp-ci.redhat.com"]); err != nil {
					t.Fatal(err)
				}
				return jobIDResponse(`{"name":"group","location":"westus3"}`)
			})
			if _, err := CreateResourceGroup(context.Background(), client, "group", "westus3", time.Hour, time.Minute); err != nil {
				t.Fatal(err)
			}
			if calls != 1 {
				t.Fatalf("expected one initial request, got %d", calls)
			}
		})
	}
}

func TestListResourceGroupsByJobID(t *testing.T) {
	for _, jobID := range []string{"123", "AbC", "job' or tagValue eq 'other", " 123 "} {
		t.Run(jobID, func(t *testing.T) {
			calls := 0
			client := jobIDTestClient(t, func(request *http.Request) (*http.Response, error) {
				calls++
				if calls == 1 {
					want := "tagName eq 'jobID.aro-hcp-ci.redhat.com' and tagValue eq '" + strings.ReplaceAll(jobID, "'", "''") + "'"
					if got := request.URL.Query().Get("$filter"); got != want {
						t.Fatalf("filter = %q, want %q", got, want)
					}
				}
				groups := []any{nil, map[string]any{"name": "untagged"}, map[string]any{"name": "nil-tag", "tags": map[string]any{ProwJobIDTag: nil}}}
				for _, value := range []string{jobID, jobID + "0", "prefix" + jobID, "other", strings.ToLower(jobID)} {
					if value == strings.ToLower(jobID) && value == jobID && len(groups) > 3 {
						continue
					}
					groups = append(groups, map[string]any{"name": "group", "tags": map[string]string{ProwJobIDTag: value}})
				}
				page := map[string]any{"value": groups}
				if calls == 1 {
					page["nextLink"] = "https://management.azure.com/next"
				}
				body, err := json.Marshal(page)
				if err != nil {
					t.Fatal(err)
				}
				return jobIDResponse(string(body))
			})
			groups, err := ListResourceGroupsByJobID(context.Background(), client, jobID)
			if err != nil {
				t.Fatal(err)
			}
			if calls != 2 || len(groups) != 2 {
				t.Fatalf("expected two pages and two exact matches; calls=%d groups=%d", calls, len(groups))
			}
		})
	}
}

func TestListResourceGroupsByJobIDRejectsBlank(t *testing.T) {
	for _, jobID := range []string{"", " \n\t"} {
		if _, err := ListResourceGroupsByJobID(context.Background(), nil, jobID); err == nil || !strings.Contains(err.Error(), "must not be blank") {
			t.Fatalf("expected blank ID rejection, got %v", err)
		}
	}
}

func TestListResourceGroupsByJobIDFailure(t *testing.T) {
	for _, failPage := range []int{1, 2} {
		calls := 0
		failure := errors.New("discovery failed")
		client := jobIDTestClient(t, func(request *http.Request) (*http.Response, error) {
			calls++
			if calls == failPage {
				return nil, failure
			}
			return jobIDResponse(`{"value":[{"name":"group","tags":{"jobID.aro-hcp-ci.redhat.com":"123"}}],"nextLink":"https://management.azure.com/next"}`)
		})
		groups, err := ListResourceGroupsByJobID(context.Background(), client, "123")
		if !errors.Is(err, failure) || groups != nil {
			t.Fatalf("must discard partial results and propagate failure: groups=%v err=%v", groups, err)
		}
	}
}
