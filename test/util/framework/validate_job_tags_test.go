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
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	"github.com/stretchr/testify/require"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	azfake "github.com/Azure/azure-sdk-for-go/sdk/azcore/fake"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"

	hcpsdk20240610preview "github.com/Azure/ARO-HCP/test/sdk/v20240610preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
)

func TestValidateResourceJobTags(t *testing.T) {
	expected := map[string]*string{ProwJobNameTag: to.Ptr("job"), ProwJobIDTag: to.Ptr("123")}
	for _, tc := range []struct {
		name string
		tags map[string]*string
		want []string
	}{
		{name: "correct", tags: expected},
		{name: "case insensitive keys", tags: map[string]*string{strings.ToUpper(ProwJobNameTag): to.Ptr("job"), strings.ToUpper(ProwJobIDTag): to.Ptr("123")}},
		{name: "missing", want: []string{ProwJobNameTag, ProwJobIDTag, "missing tag"}},
		{name: "nil", tags: map[string]*string{ProwJobNameTag: nil, ProwJobIDTag: nil}, want: []string{"missing tag"}},
		{name: "wrong", tags: map[string]*string{ProwJobNameTag: to.Ptr("JOB"), ProwJobIDTag: to.Ptr("456")}, want: []string{`is "JOB", expected "job"`, `is "456", expected "123"`}},
		{name: "unrelated tags", tags: map[string]*string{"customer": to.Ptr("value")}, want: []string{ProwJobNameTag, ProwJobIDTag}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateResourceJobTags("cluster-resource-id", tc.tags, expected)
			if len(tc.want) == 0 {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, "cluster-resource-id")
			for _, message := range tc.want {
				require.ErrorContains(t, err, message)
			}
		})
	}
	require.NoError(t, validateResourceJobTags("resource", nil, nil), "unconfigured tags are not required")
	require.NoError(t, validateResourceJobTags("resource", map[string]*string{ProwJobIDTag: to.Ptr("123")}, map[string]*string{ProwJobIDTag: to.Ptr("123")}), "job name is optional when only ID is configured")
}

func TestValidateHCPJobTagsListsOnlyHCPsAndNodePools(t *testing.T) {
	base := "/subscriptions/" + fakeSubscriptionID + "/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters"
	clusterID := base + "/cluster"
	nodePoolID := clusterID + "/nodePools/pool"
	var requests []string
	factory, err := hcpsdk20240610preview.NewClientFactory(fakeSubscriptionID, &azfake.TokenCredential{}, &azcorearm.ClientOptions{
		ClientOptions: azcore.ClientOptions{Transport: &fakeTransport{do: func(request *http.Request) (*http.Response, error) {
			require.Equal(t, http.MethodGet, request.Method, "validation must be read-only")
			requests = append(requests, request.URL.Path)
			switch request.URL.Path {
			case base:
				return jobIDResponse(fmt.Sprintf(`{"value":[{"id":%q,"name":"cluster"}],"nextLink":"https://management.azure.com/cluster-page-2"}`, clusterID))
			case clusterID + "/nodePools":
				return jobIDResponse(`{"value":[],"nextLink":"https://management.azure.com/nodepool-page-2"}`)
			case "/nodepool-page-2":
				return jobIDResponse(fmt.Sprintf(`{"value":[{"id":%q,"name":"pool","tags":{"jobID.aro-hcp-ci.redhat.com":"wrong"}}]}`, nodePoolID))
			case "/cluster-page-2":
				return jobIDResponse(`{"value":[]}`)
			default:
				t.Fatalf("unexpected resource request: %s", request.URL)
				return nil, nil
			}
		}}, Retry: policy.RetryOptions{MaxRetries: -1}},
	})
	require.NoError(t, err)
	err = validateHCPJobTags20240610(t.Context(), factory, []string{"rg"}, map[string]*string{ProwJobIDTag: to.Ptr("123")})
	require.ErrorContains(t, err, clusterID+`: missing tag "`+ProwJobIDTag+`"`)
	require.ErrorContains(t, err, nodePoolID+`: tag "`+ProwJobIDTag+`" is "wrong", expected "123"`)
	require.Len(t, requests, 4, "all cluster and node-pool pages must be checked")
}

func TestValidateHCPJobTagsListFailures(t *testing.T) {
	for _, nodePools := range []bool{false, true} {
		for _, status := range []int{http.StatusNotFound, http.StatusForbidden} {
			t.Run(fmt.Sprintf("nodepools=%t/status=%d", nodePools, status), func(t *testing.T) {
				factory, err := hcpsdk20240610preview.NewClientFactory(fakeSubscriptionID, &azfake.TokenCredential{}, &azcorearm.ClientOptions{
					ClientOptions: azcore.ClientOptions{Transport: &fakeTransport{do: func(request *http.Request) (*http.Response, error) {
						if nodePools && !strings.HasSuffix(request.URL.Path, "/nodePools") {
							return jobIDResponse(`{"value":[{"name":"cluster","tags":{"jobID.aro-hcp-ci.redhat.com":"123"}}]}`)
						}
						response, _ := jobIDResponse(`{"error":{"code":"TestError","message":"list failed"}}`)
						response.StatusCode = status
						return response, nil
					}}, Retry: policy.RetryOptions{MaxRetries: -1}},
				})
				require.NoError(t, err)
				err = validateHCPJobTags20240610(t.Context(), factory, []string{"rg"}, map[string]*string{ProwJobIDTag: to.Ptr("123")})
				if status == http.StatusNotFound {
					require.NoError(t, err, "resources already deleted by the test are skipped")
				} else {
					require.ErrorContains(t, err, "for job-tag validation")
				}
			})
		}
	}
}

func TestValidateCreatedHCPJobTagsNoMetadataOrResources(t *testing.T) {
	t.Setenv(JobNameEnvvar, "")
	t.Setenv(JobIDEnvvar, "")
	// No clients: these cases must return without contacting Azure.
	(&perItOrDescribeTestContext{knownResourceGroups: []string{"rg"}}).validateCreatedHCPJobTags(t.Context())
	t.Setenv(JobIDEnvvar, "123")
	(&perItOrDescribeTestContext{}).validateCreatedHCPJobTags(t.Context())
}

func TestJobTagValidationFailureStillRunsCleanup(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "cleanup-ran")
	command := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestJobTagValidationCleanupChild$")
	command.Env = append(os.Environ(), "ARO_HCP_JOB_TAG_CLEANUP_MARKER="+marker)
	output, err := command.CombinedOutput()
	var exitError *exec.ExitError
	require.ErrorAs(t, err, &exitError, "missing tags must fail the spec: %s", output)
	require.Contains(t, string(output), "rg/hcpOpenShiftClusters/untagged")
	require.Contains(t, string(output), "missing tag")
	require.Contains(t, string(output), "framework.WithJobTags")
	_, err = os.Stat(marker)
	require.NoError(t, err, "resource cleanup must run after tag validation fails: %s", output)
}

func TestJobTagValidationCleanupChild(t *testing.T) {
	marker := os.Getenv("ARO_HCP_JOB_TAG_CLEANUP_MARKER")
	if marker == "" {
		t.Skip("subprocess only")
	}
	t.Setenv(JobNameEnvvar, "job")
	t.Setenv(JobIDEnvvar, "123")
	factory, err := hcpsdk20240610preview.NewClientFactory(fakeSubscriptionID, &azfake.TokenCredential{}, &azcorearm.ClientOptions{
		ClientOptions: azcore.ClientOptions{Transport: &fakeTransport{do: func(request *http.Request) (*http.Response, error) {
			if strings.HasSuffix(request.URL.Path, "/nodePools") {
				return jobIDResponse(`{"value":[]}`)
			}
			return jobIDResponse(`{"value":[{"name":"untagged"}]}`)
		}}},
	})
	require.NoError(t, err)
	tc := &perItOrDescribeTestContext{knownResourceGroups: []string{"rg"}, clientFactory20240610: factory}
	gomega.RegisterFailHandler(ginkgo.Fail)
	ginkgo.Describe("job-tag validation", func() {
		ginkgo.It("fails without blocking subsequent cleanup nodes", func(ctx context.Context) {
			ginkgo.DeferCleanup(func() error { return os.WriteFile(marker, nil, 0600) })
			ginkgo.DeferCleanup(tc.validateCreatedHCPJobTags, ginkgo.NodeTimeout(time.Minute))
		})
	})
	ginkgo.RunSpecs(t, "Job tag cleanup")
}
