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

package nodepool

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	azfake "github.com/Azure/azure-sdk-for-go/sdk/azcore/fake"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"

	"github.com/Azure/ARO-HCP/fleet/pkg/azure/agentpools"
	"github.com/Azure/ARO-HCP/fleet/pkg/azure/skucache"
	"github.com/Azure/ARO-HCP/fleet/pkg/compute"
	fleetcontrollers "github.com/Azure/ARO-HCP/fleet/pkg/controllers/base"
)

// TestShadowSyncOnceScenario runs the shadow controller's action planning
// against Resource SKUs, quota usages, and the agent pools of a management
// cluster dumped from real systems, one directory per region under
// compute/testdata/scenarios, each with the profile its environment runs. The
// dumps hold the raw ARM responses, trimmed to the fields the planner reads.
// The SKUs and usages are shared with TestResolveDesiredPools_Scenario. Pins
// the projected trace.
func TestShadowSyncOnceScenario(t *testing.T) {
	tests := []struct {
		region  string
		profile string
	}{
		{region: "uksouth", profile: compute.ProfileProduction},
		// Quota less the running vCPUs of the subscription's other clusters.
		{region: "westus3", profile: compute.ProfileProduction},
	}

	for _, tt := range tests {
		t.Run(tt.region, func(t *testing.T) {
			scenario := filepath.Join("..", "..", "compute", "testdata", "scenarios", tt.region)
			bodies := map[string]string{
				"/agentpools": filepath.Join(scenario, "scenario-agentpools.json"),
				"/skus":       filepath.Join(scenario, "scenario-skus.json"),
				"/usages":     filepath.Join(scenario, "scenario-usages.json"),
			}
			transport := shadowTransportFunc(func(req *http.Request) (*http.Response, error) {
				require.Equal(t, http.MethodGet, req.Method, "shadow controller attempted an ARM mutation: %s", req.URL)
				path := strings.ToLower(req.URL.Path)
				body := `{"properties":{"provisioningState":"Succeeded"}}`
				if strings.HasSuffix(path, "/mc") {
					return scenarioResponse(req, body), nil
				}
				for suffix, file := range bodies {
					if strings.HasSuffix(path, suffix) {
						items, err := os.ReadFile(file)
						require.NoError(t, err, "reading %s", file)
						return scenarioResponse(req, fmt.Sprintf(`{"value":%s}`, items)), nil
					}
				}
				t.Fatalf("unexpected Azure read: %s", req.URL)
				return nil, nil
			})

			profile, ok := compute.LookupProfile(tt.profile)
			require.True(t, ok, "profile %q must exist", tt.profile)

			credential := &azfake.TokenCredential{}
			options := &policy.ClientOptions{Transport: transport}
			factory, armOptions := agentpools.NewClientFactory(credential, options)
			syncer := &nodePoolSyncer{
				managementClusterLister: &fakeManagementClusterLister{mc: syncOnceTestManagementCluster(syncOnceTestAKSResourceID)},
				profile:                 profile, zones: []string{"1", "2", "3"}, region: tt.region,
				agentPoolClientFactory: factory, credential: credential, armClientOptions: armOptions,
				skuCache: skucache.NewSKUCache(tt.region, credential, options, nil),
			}

			ctx, reports := shadowReportContext(t)
			require.NoError(t, syncer.SyncOnce(ctx, fleetcontrollers.ManagementClusterKey{StampIdentifier: syncOnceTestStampID}))
			require.Len(t, *reports, 1)
			compareGolden(t, (*reports)[0]["projectedTrace"].(string))
		})
	}
}

func scenarioResponse(req *http.Request, body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}},
		Body: io.NopCloser(strings.NewReader(body)), Request: req,
	}
}
