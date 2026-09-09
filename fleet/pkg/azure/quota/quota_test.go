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

package quota

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	azfake "github.com/Azure/azure-sdk-for-go/sdk/azcore/fake"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v6"
	armcomputefake "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v6/fake"

	"github.com/Azure/ARO-HCP/fleet/pkg/compute"
)

const testRegion = "eastus"
const testSubscriptionID = "22222222-2222-2222-2222-222222222222"

func newTestUsageClient(t *testing.T, usages []*armcompute.Usage, listErr error) *armcompute.UsageClient {
	t.Helper()
	srv := armcomputefake.UsageServer{
		NewListPager: func(location string, options *armcompute.UsageClientListOptions) (resp azfake.PagerResponder[armcompute.UsageClientListResponse]) {
			if listErr != nil {
				resp.AddError(listErr)
				return
			}
			resp.AddPage(http.StatusOK, armcompute.UsageClientListResponse{
				ListUsagesResult: armcompute.ListUsagesResult{Value: usages},
			}, nil)
			return
		},
	}
	transport := armcomputefake.NewUsageServerTransport(&srv)

	client, err := armcompute.NewUsageClient(testSubscriptionID, &azfake.TokenCredential{}, &azcorearm.ClientOptions{
		ClientOptions: policy.ClientOptions{Transport: transport},
	})
	require.NoError(t, err, "NewUsageClient")
	return client
}

func makeUsage(name string, limit int64, currentValue int32) *armcompute.Usage {
	return &armcompute.Usage{
		Name:         &armcompute.UsageName{Value: ptr.To(name)},
		Limit:        ptr.To(limit),
		CurrentValue: ptr.To(currentValue),
	}
}

func TestFetchUsage(t *testing.T) {
	tests := []struct {
		name     string
		usages   []*armcompute.Usage
		listErr  error
		families sets.Set[compute.VMFamily]
		want     map[compute.VMFamily]compute.QuotaUsage
		wantErr  bool
	}{
		{
			name: "filters to requested families",
			usages: []*armcompute.Usage{
				makeUsage("standardEDSv6Family", 100, 40),
				makeUsage("standardDDSv6Family", 50, 10),
			},
			families: sets.New[compute.VMFamily]("standardEDSv6Family"),
			want: map[compute.VMFamily]compute.QuotaUsage{
				"standardEDSv6Family": {Limit: 100, CurrentValue: 40},
			},
		},
		{
			name: "nil CurrentValue treated as zero",
			usages: []*armcompute.Usage{
				{Name: &armcompute.UsageName{Value: ptr.To("standardEDSv6Family")}, Limit: ptr.To(int64(100))},
			},
			families: sets.New[compute.VMFamily]("standardEDSv6Family"),
			want: map[compute.VMFamily]compute.QuotaUsage{
				"standardEDSv6Family": {Limit: 100, CurrentValue: 0},
			},
		},
		{
			name: "entries missing name, name value, or limit are skipped",
			usages: []*armcompute.Usage{
				{Limit: ptr.To(int64(100))},
				{Name: &armcompute.UsageName{}, Limit: ptr.To(int64(100))},
				{Name: &armcompute.UsageName{Value: ptr.To("standardEDSv6Family")}},
			},
			families: sets.New[compute.VMFamily]("standardEDSv6Family"),
			want:     map[compute.VMFamily]compute.QuotaUsage{},
		},
		{
			name:     "no matching families returns empty map",
			usages:   []*armcompute.Usage{makeUsage("standardEDSv6Family", 100, 40)},
			families: sets.New[compute.VMFamily]("standardDDSv6Family"),
			want:     map[compute.VMFamily]compute.QuotaUsage{},
		},
		{
			name:     "propagates pager error",
			listErr:  errors.New("boom"),
			families: sets.New[compute.VMFamily]("standardEDSv6Family"),
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := newTestUsageClient(t, tt.usages, tt.listErr)

			got, err := FetchUsage(context.Background(), client, testRegion, tt.families)
			if tt.wantErr {
				assert.Error(t, err, "expected FetchUsage to propagate pager error")
				return
			}
			require.NoError(t, err, "FetchUsage")
			assert.Equal(t, tt.want, got)
		})
	}
}
