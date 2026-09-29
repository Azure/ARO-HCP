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

package framework

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/fake"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"

	hcpsdk20240610preview "github.com/Azure/ARO-HCP/test/sdk/v20240610preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
)

const rpCollectionSubscription = "00000000-0000-0000-0000-000000000001"
const rpCollectionGroupPath = "/subscriptions/" + rpCollectionSubscription + "/resourceGroups/rg-a/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters"
const rpCollectionClusterPath = rpCollectionGroupPath + "/cluster-a"

type rpCollectionTestResponse struct {
	status int
	body   string
}

func newRPCollectionTestFactory(test *testing.T, responses map[string]rpCollectionTestResponse, requests *[]string) *hcpsdk20240610preview.ClientFactory {
	test.Helper()
	transport := &fakeTransport{do: func(request *http.Request) (*http.Response, error) {
		require.Equal(test, "example.com", request.URL.Host)
		if err := request.Context().Err(); err != nil {
			return nil, err
		}
		key := request.URL.Path
		if page := request.URL.Query().Get("page"); page != "" {
			key += "?page=" + page
		}
		*requests = append(*requests, key)
		response, ok := responses[key]
		require.True(test, ok, "unexpected request: %s", key)
		status := response.status
		if status == 0 {
			status = http.StatusOK
		}
		return &http.Response{
			StatusCode: status,
			Header:     http.Header{"Content-Type": {"application/json"}},
			Body:       io.NopCloser(strings.NewReader(response.body)),
			Request:    request,
		}, nil
	}}
	factory, err := hcpsdk20240610preview.NewClientFactory(rpCollectionSubscription, &fake.TokenCredential{}, &azcorearm.ClientOptions{
		ClientOptions: policy.ClientOptions{
			Transport: transport,
			Retry:     policy.RetryOptions{MaxRetries: -1},
			Cloud: cloud.Configuration{Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
				cloud.ResourceManager: {Endpoint: "https://example.com", Audience: "https://example.com"},
			}},
		},
	})
	require.NoError(test, err)
	return factory
}

func readRPCollectionArtifact(test *testing.T, outputDir, name string, target any) {
	test.Helper()
	data, err := os.ReadFile(filepath.Join(outputDir, name))
	require.NoError(test, err)
	require.NotContains(test, string(data), "fake_token")
	require.NoError(test, json.Unmarshal(data, target))
}

func TestTrackedRPResourceGroups(test *testing.T) {
	groups, err := trackedRPResourceGroups("")
	require.NoError(test, err)
	require.Empty(test, groups)
	sharedDir := test.TempDir()
	groups, err = trackedRPResourceGroups(sharedDir)
	require.NoError(test, err)
	require.Empty(test, groups)
	for _, name := range []string{"tracked-resource-group_rg-b", "tracked-resource-group_rg-a", "tracked-resource-group_", "unrelated"} {
		require.NoError(test, os.WriteFile(filepath.Join(sharedDir, name), nil, 0644))
	}
	require.NoError(test, os.Mkdir(filepath.Join(sharedDir, "tracked-resource-group_directory"), 0755))
	require.NoError(test, os.Symlink(filepath.Join(sharedDir, "unrelated"), filepath.Join(sharedDir, "tracked-resource-group_symlink")))
	groups, err = trackedRPResourceGroups(sharedDir)
	require.NoError(test, err)
	require.Equal(test, []string{"rg-a", "rg-b"}, groups)
	_, err = trackedRPResourceGroups(filepath.Join(sharedDir, "missing"))
	require.ErrorContains(test, err, "read tracked resource groups")
}

func TestCollectRPResourcesAtEndOfRunWithoutArtifacts(test *testing.T) {
	test.Setenv("ARTIFACT_DIR", "")
	require.NoError(test, CollectRPResourcesAtEndOfRun(context.Background()))
}

func TestCollectRPResources20240610Pagination(test *testing.T) {
	resourceGroups := []string{"rg-a"}
	clusterListPath := rpCollectionGroupPath
	responses := map[string]rpCollectionTestResponse{
		clusterListPath:                                    {body: fmt.Sprintf(`{"value":[{"id":%q,"name":"cluster-a","properties":{"provisioningState":"Updating"}}],"nextLink":%q}`, rpCollectionClusterPath, "https://example.com"+clusterListPath+"?page=2")},
		clusterListPath + "?page=2":                        {body: fmt.Sprintf(`{"value":[{"id":%q,"name":"cluster-b"}]}`, rpCollectionGroupPath+"/cluster-b")},
		rpCollectionClusterPath + "/nodePools":             {body: fmt.Sprintf(`{"value":[{"name":"pool-1"}],"nextLink":%q}`, "https://example.com"+rpCollectionClusterPath+"/nodePools?page=2")},
		rpCollectionClusterPath + "/nodePools?page=2":      {body: `{"value":[{"name":"pool-2"}]}`},
		rpCollectionClusterPath + "/externalAuths":         {body: fmt.Sprintf(`{"value":[{"name":"auth-1"}],"nextLink":%q}`, "https://example.com"+rpCollectionClusterPath+"/externalAuths?page=2")},
		rpCollectionClusterPath + "/externalAuths?page=2":  {body: `{"value":[{"name":"auth-2"}]}`},
		rpCollectionGroupPath + "/cluster-b/nodePools":     {body: `{"value":[]}`},
		rpCollectionGroupPath + "/cluster-b/externalAuths": {body: `{"value":[]}`},
	}
	var requests []string
	factory := newRPCollectionTestFactory(test, responses, &requests)
	outputDir := filepath.Join(test.TempDir(), "rp-resources")
	require.NoError(test, collectRPResources20240610(context.Background(), factory, resourceGroups, outputDir))
	require.Len(test, requests, 8)
	var clusters []*hcpsdk20240610preview.HcpOpenShiftCluster
	readRPCollectionArtifact(test, outputDir, "clusters.json", &clusters)
	require.Len(test, clusters, 2)
	require.Equal(test, "Updating", string(ptr.Deref(clusters[0].Properties.ProvisioningState, "")))
	var nodePools []collectedNodePool
	readRPCollectionArtifact(test, outputDir, "nodepools.json", &nodePools)
	require.Len(test, nodePools, 2)
	require.Equal(test, "pool-2", ptr.Deref(nodePools[1].NodePool.Name, ""))
	require.Equal(test, "rg-a", nodePools[1].ResourceGroup)
	require.Equal(test, "cluster-a", nodePools[1].Cluster)
	var externalAuths []collectedExternalAuth
	readRPCollectionArtifact(test, outputDir, "externalauths.json", &externalAuths)
	require.Len(test, externalAuths, 2)
	require.Equal(test, "auth-2", ptr.Deref(externalAuths[1].ExternalAuth.Name, ""))
	require.Equal(test, "rg-a", externalAuths[1].ResourceGroup)
	require.Equal(test, "cluster-a", externalAuths[1].Cluster)
}

func TestCollectRPResources20240610PartialFailures(test *testing.T) {
	otherGroupPath := strings.ReplaceAll(rpCollectionGroupPath, "rg-a", "rg-b")
	responses := map[string]rpCollectionTestResponse{
		strings.ReplaceAll(rpCollectionGroupPath, "rg-a", "denied"): {status: http.StatusForbidden, body: `{"error":{"code":"AuthorizationFailed","message":"denied"}}`},
		rpCollectionGroupPath:                         {body: fmt.Sprintf(`{"value":[{"name":"cluster-a"}],"nextLink":%q}`, "https://example.com"+rpCollectionGroupPath+"?page=2")},
		rpCollectionGroupPath + "?page=2":             {status: http.StatusInternalServerError, body: `{"error":{"code":"InternalServerError","message":"cluster list failed"}}`},
		rpCollectionClusterPath + "/nodePools":        {body: fmt.Sprintf(`{"value":[{"name":"pool-1"}],"nextLink":%q}`, "https://example.com"+rpCollectionClusterPath+"/nodePools?page=2")},
		rpCollectionClusterPath + "/nodePools?page=2": {status: http.StatusForbidden, body: `{"error":{"code":"AuthorizationFailed","message":"node list failed"}}`},
		rpCollectionClusterPath + "/externalAuths":    {body: `{"value":[{"name":"auth-1"}]}`},
		otherGroupPath:                                {body: `{"value":[{"name":"cluster-b"}]}`},
		otherGroupPath + "/cluster-b/nodePools":       {body: `{"value":[{"name":"pool-2"}]}`},
		otherGroupPath + "/cluster-b/externalAuths":   {status: http.StatusForbidden, body: `{"error":{"code":"AuthorizationFailed","message":"auth list failed"}}`},
	}
	var requests []string
	factory := newRPCollectionTestFactory(test, responses, &requests)
	outputDir := test.TempDir()
	err := collectRPResources20240610(context.Background(), factory, []string{"denied", "rg-a", "rg-b"}, outputDir)
	require.ErrorContains(test, err, "list clusters in denied")
	require.ErrorContains(test, err, "list clusters in rg-a")
	require.ErrorContains(test, err, "list node pools for rg-a/cluster-a")
	require.ErrorContains(test, err, "list external auths for rg-b/cluster-b")
	var clusters []*hcpsdk20240610preview.HcpOpenShiftCluster
	readRPCollectionArtifact(test, outputDir, "clusters.json", &clusters)
	require.Len(test, clusters, 2)
	var nodePools []collectedNodePool
	readRPCollectionArtifact(test, outputDir, "nodepools.json", &nodePools)
	require.Len(test, nodePools, 2)
	var externalAuths []collectedExternalAuth
	readRPCollectionArtifact(test, outputDir, "externalauths.json", &externalAuths)
	require.Len(test, externalAuths, 1)
}

func TestCollectRPResources20240610InvalidClusters(test *testing.T) {
	responses := map[string]rpCollectionTestResponse{
		rpCollectionGroupPath: {body: fmt.Sprintf(`{"value":[null,{"id":%q}]}`, rpCollectionClusterPath)},
	}
	var requests []string
	factory := newRPCollectionTestFactory(test, responses, &requests)
	outputDir := test.TempDir()
	err := collectRPResources20240610(context.Background(), factory, []string{"rg-a"}, outputDir)
	require.ErrorContains(test, err, "has no resource group or name")
	var clusters []*hcpsdk20240610preview.HcpOpenShiftCluster
	readRPCollectionArtifact(test, outputDir, "clusters.json", &clusters)
	require.Len(test, clusters, 1)
}

func TestCollectRPResources20240610Canceled(test *testing.T) {
	var requests []string
	factory := newRPCollectionTestFactory(test, nil, &requests)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	outputDir := test.TempDir()
	require.ErrorIs(test, collectRPResources20240610(ctx, factory, []string{"rg-a"}, outputDir), context.Canceled)
	for _, name := range []string{"clusters.json", "nodepools.json", "externalauths.json"} {
		data, err := os.ReadFile(filepath.Join(outputDir, name))
		require.NoError(test, err)
		require.JSONEq(test, `[]`, string(data))
	}
}

func TestCollectRPResources20240610WriteFailure(test *testing.T) {
	responses := map[string]rpCollectionTestResponse{rpCollectionGroupPath: {body: `{"value":[]}`}}
	var requests []string
	factory := newRPCollectionTestFactory(test, responses, &requests)
	outputDir := test.TempDir()
	require.NoError(test, os.Mkdir(filepath.Join(outputDir, "clusters.json"), 0755))
	require.ErrorContains(test, collectRPResources20240610(context.Background(), factory, []string{"rg-a"}, outputDir), "write clusters.json")
	for _, name := range []string{"nodepools.json", "externalauths.json"} {
		data, err := os.ReadFile(filepath.Join(outputDir, name))
		require.NoError(test, err)
		require.JSONEq(test, `[]`, string(data))
	}
}

func captureRPCollectionStderr(test *testing.T, run func()) string {
	test.Helper()
	stderr, err := os.CreateTemp(test.TempDir(), "stderr")
	require.NoError(test, err)
	originalStderr := os.Stderr
	os.Stderr = stderr
	defer func() {
		os.Stderr = originalStderr
		require.NoError(test, stderr.Close())
	}()
	run()
	data, err := os.ReadFile(stderr.Name())
	require.NoError(test, err)
	return string(data)
}

func TestCollectRPResourcesAtEndOfRunWithoutTrackedGroups(test *testing.T) {
	for _, scope := range []string{"unset", "empty", "unrelated"} {
		test.Run(scope, func(test *testing.T) {
			outputDir := test.TempDir()
			test.Setenv("ARTIFACT_DIR", outputDir)
			sharedDir := ""
			if scope != "unset" {
				sharedDir = test.TempDir()
			}
			if scope == "unrelated" {
				require.NoError(test, os.WriteFile(filepath.Join(sharedDir, "unrelated"), nil, 0644))
			}
			test.Setenv("SHARED_DIR", sharedDir)
			stderr := captureRPCollectionStderr(test, func() {
				require.NoError(test, CollectRPResourcesAtEndOfRun(context.Background()))
			})
			require.Contains(test, stderr, "WARNING: no tracked resource groups")
			entries, err := os.ReadDir(outputDir)
			require.NoError(test, err)
			require.Empty(test, entries)
		})
	}
}

func TestIsRPResourceNotFound(test *testing.T) {
	for _, scenario := range []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil"},
		{name: "not an Azure response", err: fmt.Errorf("status 404")},
		{name: "missing", err: &azcore.ResponseError{StatusCode: http.StatusNotFound}, want: true},
		{name: "wrapped missing", err: fmt.Errorf("list failed: %w", &azcore.ResponseError{StatusCode: http.StatusNotFound}), want: true},
		{name: "forbidden is not missing", err: &azcore.ResponseError{StatusCode: http.StatusForbidden, ErrorCode: "ResourceNotFound"}},
	} {
		test.Run(scenario.name, func(test *testing.T) {
			require.Equal(test, scenario.want, isRPResourceNotFound(scenario.err))
		})
	}
}

func TestCollectRPResources20240610DeletedResources(test *testing.T) {
	otherGroupPath := strings.ReplaceAll(rpCollectionGroupPath, "rg-a", "rg-b")
	lastGroupPath := strings.ReplaceAll(rpCollectionGroupPath, "rg-a", "rg-c")
	notFound := rpCollectionTestResponse{status: http.StatusNotFound, body: `{"error":{"code":"ResourceNotFound","message":"deleted"}}`}
	responses := map[string]rpCollectionTestResponse{
		strings.ReplaceAll(rpCollectionGroupPath, "rg-a", "gone"): notFound,
		rpCollectionGroupPath:                       {body: `{"value":[{"name":"cluster-a"}]}`},
		rpCollectionClusterPath + "/nodePools":      notFound,
		rpCollectionClusterPath + "/externalAuths":  {body: `{"value":[{"name":"auth-a"}]}`},
		otherGroupPath:                              {body: `{"value":[{"name":"cluster-b"}]}`},
		otherGroupPath + "/cluster-b/nodePools":     {body: `{"value":[]}`},
		otherGroupPath + "/cluster-b/externalAuths": notFound,
		lastGroupPath:                               {body: `{"value":[{"name":"cluster-c"}]}`},
		lastGroupPath + "/cluster-c/nodePools":      {body: `{"value":[{"name":"pool-c"}]}`},
		lastGroupPath + "/cluster-c/externalAuths":  {body: `{"value":[{"name":"auth-c"}]}`},
	}
	var requests []string
	factory := newRPCollectionTestFactory(test, responses, &requests)
	outputDir := test.TempDir()
	stderr := captureRPCollectionStderr(test, func() {
		require.NoError(test, collectRPResources20240610(context.Background(), factory, []string{"gone", "rg-a", "rg-b", "rg-c"}, outputDir))
	})
	require.Empty(test, stderr)
	require.Len(test, requests, len(responses))
	var nodePools []collectedNodePool
	readRPCollectionArtifact(test, outputDir, "nodepools.json", &nodePools)
	require.Len(test, nodePools, 1)
	require.Equal(test, "rg-c", nodePools[0].ResourceGroup)
	var externalAuths []collectedExternalAuth
	readRPCollectionArtifact(test, outputDir, "externalauths.json", &externalAuths)
	require.Len(test, externalAuths, 2)
	require.Equal(test, "cluster-a", externalAuths[0].Cluster)
	require.Equal(test, "auth-a", ptr.Deref(externalAuths[0].ExternalAuth.Name, ""))
	require.Equal(test, "cluster-c", externalAuths[1].Cluster)
}
