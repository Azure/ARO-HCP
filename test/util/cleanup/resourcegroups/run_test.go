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

package resourcegroups

import (
	"context"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	azfake "github.com/Azure/azure-sdk-for-go/sdk/azcore/fake"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
)

type discoveryTransport func(*http.Request) (*http.Response, error)

func (transport discoveryTransport) Do(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func TestDiscoverResourceGroups(t *testing.T) {
	for _, testCase := range []struct {
		name        string
		tracked     []string
		jobID       string
		empty       bool
		failTracked bool
		failJob     bool
		include     sets.Set[string]
		want        []string
	}{
		{name: "union and case insensitive dedup", tracked: []string{"TRACKED", "Both", "both", "missing"}, jobID: "123", want: []string{"both", "tagged", "tracked"}},
		{name: "only tracked", tracked: []string{"TRACKED", "tracked"}, want: []string{"tracked"}},
		{name: "only job", jobID: "123", want: []string{"both", "tagged"}},
		{name: "zero matches", jobID: "123", empty: true, want: []string{}},
		{name: "location filter", tracked: []string{"TRACKED"}, jobID: "123", include: sets.New("westus3"), want: []string{"both", "tracked"}},
		{name: "tracked discovery failure", tracked: []string{"TRACKED"}, jobID: "123", failTracked: true},
		{name: "job discovery failure after tracked", tracked: []string{"TRACKED"}, jobID: "123", failJob: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			jobCalls := 0
			failure := errors.New("discovery failure")
			client, err := armresources.NewResourceGroupsClient("subscription", &azfake.TokenCredential{}, &azcorearm.ClientOptions{ClientOptions: azcore.ClientOptions{
				Retry: policy.RetryOptions{MaxRetries: -1},
				Transport: discoveryTransport(func(request *http.Request) (*http.Response, error) {
					if request.Method != http.MethodGet {
						t.Fatalf("unexpected deletion during discovery: %s", request.Method)
					}
					body := `{"value":[{"name":"tracked","location":"westus3"},{"name":"BOTH","location":"westus3"}]}`
					if request.URL.Query().Get("$filter") != "" {
						jobCalls++
						if testCase.failJob {
							return nil, failure
						}
						body = `{"value":[{"name":"both","location":"westus3","tags":{"jobID.aro-hcp-ci.redhat.com":"123"}},{"name":"TAGGED","location":"eastus","tags":{"jobID.aro-hcp-ci.redhat.com":"123"}}]}`
					} else if testCase.failTracked {
						return nil, failure
					}
					if testCase.empty {
						body = `{"value":[]}`
					}
					return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
				}),
			}})
			if err != nil {
				t.Fatal(err)
			}
			options := &Options{completedOptions: &completedOptions{ResourceGroups: testCase.tracked, JobID: testCase.jobID, IncludeLocations: testCase.include}}
			groups, err := options.discoverResourceGroups(context.Background(), client)
			if testCase.failJob || testCase.failTracked {
				if !errors.Is(err, failure) || groups != nil {
					t.Fatalf("must fail without partial cleanup: %v, %v", groups, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(groups, testCase.want) {
				t.Fatalf("groups=%v want=%v", groups, testCase.want)
			}
			if testCase.jobID == "" && jobCalls != 0 {
				t.Fatal("absent job ID must not trigger tag discovery")
			}
			if len(groups) == 0 {
				if err := options.cleanupResourceGroups(context.Background(), nil, groups); err != nil {
					t.Fatalf("zero matches must be a successful no-op: %v", err)
				}
			}
		})
	}
}
