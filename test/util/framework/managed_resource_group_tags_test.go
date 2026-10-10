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
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	azfake "github.com/Azure/azure-sdk-for-go/sdk/azcore/fake"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources/fake"
)

func TestJobIDTagPatch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		existing  map[string]*string
		jobID     string
		wantPatch map[string]*string
		wantError bool
	}{
		{name: "outside prow", existing: map[string]*string{}},
		{name: "untagged group", existing: nil, jobID: "123", wantPatch: map[string]*string{ProwJobIDTag: to.Ptr("123")}},
		{name: "other tags kept by merge", existing: map[string]*string{"createdAt": to.Ptr("now")}, jobID: "123", wantPatch: map[string]*string{ProwJobIDTag: to.Ptr("123")}},
		{name: "already tagged", existing: map[string]*string{ProwJobIDTag: to.Ptr("123")}, jobID: "123"},
		{name: "other job", existing: map[string]*string{ProwJobIDTag: to.Ptr("456")}, jobID: "123", wantError: true},
		{name: "already tagged, other casing", existing: map[string]*string{"JOBID.aro-hcp-ci.redhat.com": to.Ptr("123")}, jobID: "123"},
		{name: "other job, other casing", existing: map[string]*string{"JobID.ARO-HCP-CI.redhat.com": to.Ptr("456")}, jobID: "123", wantError: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			patch, err := jobIDTagPatch(tt.existing, tt.jobID)
			if (err != nil) != tt.wantError {
				t.Fatalf("jobIDTagPatch() error = %v, wantError %v", err, tt.wantError)
			}
			if !reflect.DeepEqual(patch, tt.wantPatch) {
				t.Fatalf("jobIDTagPatch() = %v, want %v", patch, tt.wantPatch)
			}
		})
	}
}

func TestManagedByHCPClusterIn(t *testing.T) {
	t.Parallel()

	const clusterID = "/subscriptions/sub/resourceGroups/Customer-RG/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster"
	tests := []struct {
		name      string
		managedBy *string
		parent    string
		want      bool
	}{
		{name: "unmanaged", parent: "customer-rg"},
		{name: "cluster in parent, any case", managedBy: to.Ptr(clusterID), parent: "customer-rg", want: true},
		{name: "cluster in other group", managedBy: to.Ptr(clusterID), parent: "customer"},
		{name: "other resource type", managedBy: to.Ptr("/subscriptions/sub/resourceGroups/customer-rg/providers/Microsoft.Solutions/applications/app"), parent: "customer-rg"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := managedByHCPClusterIn(tt.managedBy, tt.parent); got != tt.want {
				t.Fatalf("managedByHCPClusterIn() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDeploysHCPCluster(t *testing.T) {
	t.Parallel()

	if !deploysHCPCluster([]byte(`{"resources":[{"type":"Microsoft.Resources/deployments","properties":{"template":{"resources":[{"type":"Microsoft.RedHatOpenShift/hcpOpenShiftClusters"}]}}}]}`)) {
		t.Fatal("expected a nested module declaring an HCP cluster to be detected")
	}
	if deploysHCPCluster([]byte(`{"resources":[{"type":"Microsoft.Network/virtualNetworks"}]}`)) {
		t.Fatal("expected a template without an HCP cluster not to be detected")
	}
}

func TestCreatedResourceGroup(t *testing.T) {
	t.Parallel()

	tc := &perItOrDescribeTestContext{knownResourceGroups: []string{"Customer-RG"}}
	if !tc.createdResourceGroup("customer-rg") {
		t.Fatal("expected a resource group created by the test to match regardless of case")
	}
	if tc.createdResourceGroup("shared-rg") {
		t.Fatal("expected a resource group the test did not create not to match")
	}
}

func TestProwJobIDMatchesResourceGroupTag(t *testing.T) {
	for _, tt := range []struct {
		buildID string
		want    string
	}{
		{buildID: "", want: ""},
		{buildID: " \t", want: ""},
		{buildID: "2097011220782518272", want: "2097011220782518272"},
	} {
		t.Run(tt.buildID, func(t *testing.T) {
			t.Setenv("BUILD_ID", tt.buildID)
			if got := prowJobID(); got != tt.want {
				t.Fatalf("prowJobID() = %q, want %q", got, tt.want)
			}
		})
	}
}

const testClusterPath = "/subscriptions/sub/resourceGroups/customer-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster"

type recordingTagger struct {
	mu    sync.Mutex
	calls []string
	err   error
}

func (r *recordingTagger) tag(_ context.Context, subscriptionID, parentResourceGroupName, managedResourceGroupName string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, subscriptionID+"/"+parentResourceGroupName+"/"+managedResourceGroupName)
	return r.err == nil, r.err
}

type staticTransport struct {
	status int
	body   string
}

func (s staticTransport) Do(req *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: s.status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(s.body)),
		Request:    req,
	}, nil
}

func sendThroughPolicy(t *testing.T, p policy.Policy, method, path string, status int, body string) string {
	t.Helper()
	pipeline := runtime.NewPipeline("test", "v0", runtime.PipelineOptions{PerCall: []policy.Policy{p}}, &policy.ClientOptions{
		Retry:     policy.RetryOptions{MaxRetries: -1},
		Transport: staticTransport{status: status, body: body},
	})
	req, err := runtime.NewRequest(context.Background(), method, "https://management.example"+path)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := pipeline.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	received, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(received)
}

func TestManagedResourceGroupJobIDPolicy(t *testing.T) {
	t.Parallel()

	const succeeded = `{"properties":{"provisioningState":"Succeeded","platform":{"managedResourceGroup":"cluster-managed"}}}`
	const provisioning = `{"properties":{"provisioningState":"Provisioning","platform":{"managedResourceGroup":"cluster-managed"}}}`

	tests := []struct {
		name      string
		method    string
		path      string
		status    int
		body      string
		wantCalls int
	}{
		{name: "succeeded cluster", method: http.MethodGet, path: testClusterPath, status: http.StatusOK, body: succeeded, wantCalls: 1},
		{name: "provisioning cluster", method: http.MethodGet, path: testClusterPath, status: http.StatusOK, body: provisioning},
		{name: "create request", method: http.MethodPut, path: testClusterPath, status: http.StatusOK, body: succeeded},
		{name: "failed read", method: http.MethodGet, path: testClusterPath, status: http.StatusNotFound, body: `{}`},
		{name: "node pool", method: http.MethodGet, path: testClusterPath + "/nodePools/pool", status: http.StatusOK, body: succeeded},
		{name: "cluster list", method: http.MethodGet, path: "/subscriptions/sub/resourceGroups/customer-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters", status: http.StatusOK, body: succeeded},
		{name: "no managed resource group", method: http.MethodGet, path: testClusterPath, status: http.StatusOK, body: `{"properties":{"provisioningState":"Succeeded"}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tagger := &recordingTagger{}
			p := &managedResourceGroupJobIDPolicy{tagger: tagger}

			if got := sendThroughPolicy(t, p, tt.method, tt.path, tt.status, tt.body); got != tt.body {
				t.Fatalf("policy altered the response body: %q", got)
			}
			if len(tagger.calls) != tt.wantCalls {
				t.Fatalf("tag calls = %v, want %d", tagger.calls, tt.wantCalls)
			}
			if tt.wantCalls == 1 && tagger.calls[0] != "sub/customer-rg/cluster-managed" {
				t.Fatalf("tagged %q, want sub/customer-rg/cluster-managed", tagger.calls[0])
			}
		})
	}

	t.Run("tags each managed resource group once", func(t *testing.T) {
		t.Parallel()
		tagger := &recordingTagger{}
		p := &managedResourceGroupJobIDPolicy{tagger: tagger}
		for range 3 {
			sendThroughPolicy(t, p, http.MethodGet, testClusterPath, http.StatusOK, succeeded)
		}
		if len(tagger.calls) != 1 {
			t.Fatalf("tag calls = %v, want 1", tagger.calls)
		}
	})

	t.Run("retries after a failure", func(t *testing.T) {
		t.Parallel()
		tagger := &recordingTagger{err: errors.New("throttled")}
		p := &managedResourceGroupJobIDPolicy{tagger: tagger}
		sendThroughPolicy(t, p, http.MethodGet, testClusterPath, http.StatusOK, succeeded)
		sendThroughPolicy(t, p, http.MethodGet, testClusterPath, http.StatusOK, succeeded)
		if len(tagger.calls) != 2 {
			t.Fatalf("tag calls = %v, want 2", tagger.calls)
		}
	})
}

func TestJobManagedResourceGroupTagger(t *testing.T) {
	t.Parallel()

	const (
		jobID     = "2097011220782518272"
		clusterID = testClusterPath
	)
	tests := []struct {
		name        string
		parentTags  map[string]*string
		managedBy   *string
		managedTags map[string]*string
		wantTagged  bool
	}{
		{name: "job resource group", parentTags: map[string]*string{ProwJobIDTag: to.Ptr(jobID)}, managedBy: to.Ptr(clusterID), wantTagged: true},
		{name: "job resource group, other key casing", parentTags: map[string]*string{"JOBID.aro-hcp-ci.redhat.com": to.Ptr(jobID)}, managedBy: to.Ptr(clusterID), wantTagged: true},
		{name: "untagged parent", managedBy: to.Ptr(clusterID)},
		{name: "parent of another job", parentTags: map[string]*string{ProwJobIDTag: to.Ptr("456")}, managedBy: to.Ptr(clusterID)},
		{name: "owned by another resource group", parentTags: map[string]*string{ProwJobIDTag: to.Ptr(jobID)}, managedBy: to.Ptr("/subscriptions/sub/resourceGroups/other/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster")},
		{name: "already tagged", parentTags: map[string]*string{ProwJobIDTag: to.Ptr(jobID)}, managedBy: to.Ptr(clusterID), managedTags: map[string]*string{ProwJobIDTag: to.Ptr(jobID)}},
		{name: "attributed to another job", parentTags: map[string]*string{ProwJobIDTag: to.Ptr(jobID)}, managedBy: to.Ptr(clusterID), managedTags: map[string]*string{ProwJobIDTag: to.Ptr("456")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var patched *armresources.TagsPatchResource
			var patchedScope string
			server := &fake.ServerFactory{
				ResourceGroupsServer: fake.ResourceGroupsServer{
					Get: func(_ context.Context, name string, _ *armresources.ResourceGroupsClientGetOptions) (resp azfake.Responder[armresources.ResourceGroupsClientGetResponse], errResp azfake.ErrorResponder) {
						group := armresources.ResourceGroup{ID: to.Ptr("/subscriptions/sub/resourceGroups/" + name), Name: to.Ptr(name)}
						switch name {
						case "customer-rg":
							group.Tags = tt.parentTags
						case "cluster-managed":
							group.ManagedBy = tt.managedBy
							group.Tags = tt.managedTags
						default:
							errResp.SetResponseError(http.StatusNotFound, "ResourceGroupNotFound")
							return
						}
						resp.SetResponse(http.StatusOK, armresources.ResourceGroupsClientGetResponse{ResourceGroup: group}, nil)
						return
					},
				},
				TagsServer: fake.TagsServer{
					UpdateAtScope: func(_ context.Context, scope string, parameters armresources.TagsPatchResource, _ *armresources.TagsClientUpdateAtScopeOptions) (resp azfake.Responder[armresources.TagsClientUpdateAtScopeResponse], errResp azfake.ErrorResponder) {
						patched, patchedScope = &parameters, scope
						resp.SetResponse(http.StatusOK, armresources.TagsClientUpdateAtScopeResponse{}, nil)
						return
					},
				},
			}
			tagger := &jobManagedResourceGroupTagger{
				jobID: jobID,
				clientFactory: func(subscriptionID string) (*armresources.ClientFactory, error) {
					return armresources.NewClientFactory(subscriptionID, &azfake.TokenCredential{}, &azcorearm.ClientOptions{ClientOptions: azcore.ClientOptions{
						Retry:     policy.RetryOptions{MaxRetries: -1},
						Transport: fake.NewServerFactoryTransport(server),
					}})
				},
			}

			tagged, err := tagger.tag(context.Background(), "sub", "customer-rg", "cluster-managed")
			if err != nil {
				t.Fatal(err)
			}
			if tagged != tt.wantTagged || (patched != nil) != tt.wantTagged {
				t.Fatalf("tagged=%v patched=%v, want %v", tagged, patched != nil, tt.wantTagged)
			}
			if !tt.wantTagged {
				return
			}
			if !strings.EqualFold(strings.Trim(patchedScope, "/"), "subscriptions/sub/resourceGroups/cluster-managed") {
				t.Fatalf("patched scope %q", patchedScope)
			}
			if patched.Operation == nil || *patched.Operation != armresources.TagsPatchOperationMerge {
				t.Fatalf("expected a merge so existing tags are kept, got %v", patched.Operation)
			}
			if !reflect.DeepEqual(patched.Properties.Tags, map[string]*string{ProwJobIDTag: to.Ptr(jobID)}) {
				t.Fatalf("patched tags %v", patched.Properties.Tags)
			}
		})
	}
}
