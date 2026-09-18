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
	"bytes"
	"context"
	"net/http"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
)

func TestHCPAPIVersionUsageRecordsAPINameAndRendersSortedTable(t *testing.T) {
	usage := newHCPAPIVersionUsage()
	usage.record(newPolicyRequest(t, "2026-09-01-preview", "HcpOpenShiftClustersClient.Get"))
	usage.record(newPolicyRequest(t, "2024-06-10-preview", "NodePoolsClient.Get"))
	usage.record(newPolicyRequest(t, "2026-09-01-preview", "HcpOpenShiftClustersClient.Get"))

	var report bytes.Buffer
	usage.WriteHCPAPIVersionUsage(&report)

	expected := "HCP API version usage (logical SDK calls; transport retries excluded):\n" +
		"API version         Operation                       Count\n" +
		"------------------  ------------------------------  -----\n" +
		"2024-06-10-preview  NodePoolsClient.Get             1\n" +
		"2026-09-01-preview  HcpOpenShiftClustersClient.Get  2\n"
	if report.String() != expected {
		t.Fatalf("unexpected report:\n%s", report.String())
	}
}

func TestHCPAPIVersionUsageFallsBackToHTTPMethodAndResourceType(t *testing.T) {
	usage := newHCPAPIVersionUsage()
	req, err := runtime.NewRequest(context.Background(), http.MethodGet, "https://management.azure.com/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster?api-version=2026-09-01-preview")
	if err != nil {
		t.Fatal(err)
	}
	usage.record(req)

	var report bytes.Buffer
	usage.WriteHCPAPIVersionUsage(&report)
	if want := "GET Microsoft.RedHatOpenShift/hcpOpenShiftClusters"; !bytes.Contains(report.Bytes(), []byte(want)) {
		t.Fatalf("report did not contain fallback operation %q:\n%s", want, report.String())
	}
}

func newPolicyRequest(t *testing.T, apiVersion, operation string) *policy.Request {
	t.Helper()
	ctx := context.WithValue(context.Background(), runtime.CtxAPINameKey{}, operation)
	req, err := runtime.NewRequest(ctx, http.MethodGet, "https://management.azure.com/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster?api-version="+apiVersion)
	if err != nil {
		t.Fatal(err)
	}
	return req
}
