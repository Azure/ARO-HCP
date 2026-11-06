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
	"net/http"
	"testing"

	azfake "github.com/Azure/azure-sdk-for-go/sdk/azcore/fake"

	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	hcpsdk20240610preview "github.com/Azure/ARO-HCP/test/sdk/v20240610preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	hcpfake20240610preview "github.com/Azure/ARO-HCP/test/sdk/v20240610preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp/fake"
)

func TestAPIVersionAvailable(t *testing.T) {
	for _, tc := range []struct {
		name      string
		status    int
		errorCode string
		available bool
		wantErr   bool
	}{
		{name: "available", status: http.StatusOK, available: true},
		{name: "not found", status: http.StatusNotFound},
		{name: "provider not registered", status: http.StatusBadRequest, errorCode: "NoRegisteredProviderFound"},
		{name: "unexpected error", status: http.StatusBadRequest, errorCode: "InvalidRequest", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := hcpfake20240610preview.HcpOpenShiftClustersServer{
				NewListByResourceGroupPager: func(_ string, _ *hcpsdk20240610preview.HcpOpenShiftClustersClientListByResourceGroupOptions) (resp azfake.PagerResponder[hcpsdk20240610preview.HcpOpenShiftClustersClientListByResourceGroupResponse]) {
					if tc.status == http.StatusOK {
						resp.AddPage(http.StatusOK, hcpsdk20240610preview.HcpOpenShiftClustersClientListByResourceGroupResponse{}, nil)
					} else {
						resp.AddResponseError(tc.status, tc.errorCode)
					}
					return
				},
			}
			factory, err := hcpsdk20240610preview.NewClientFactory(fakeSubscriptionID, &azfake.TokenCredential{}, fakeClientOptions(hcpfake20240610preview.NewHcpOpenShiftClustersServerTransport(&srv)))
			if err != nil {
				t.Fatalf("failed to create fake client factory: %v", err)
			}
			frameworkContext := &perItOrDescribeTestContext{clientFactory20240610: factory}
			available, err := frameworkContext.APIVersionAvailable(context.Background(), "rg", metadataapi.APIVersionV20240610Preview)
			if available != tc.available || (err != nil) != tc.wantErr {
				t.Fatalf("APIVersionAvailable() = (%t, %v), want available=%t, error=%t", available, err, tc.available, tc.wantErr)
			}
		})
	}
}
