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

package do_nothing

import (
	"context"
	"io/fs"
	"path"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/mismatchcontrollers"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/test-integration/utils/controllertesthelpers"
	"github.com/Azure/ARO-HCP/test-integration/utils/integrationutils"
)

func TestNodePoolMismatchController(t *testing.T) {
	integrationutils.SkipIfNotSimulationTesting(t)

	testCases := []controllertesthelpers.BasicControllerTest{
		{
			Name: "remove_orphaned_nodepool_descendents",
			ControllerKey: controllerutils.HCPClusterKey{
				SubscriptionID:    "a433a095-1277-44f1-8453-8d61a4d848c2",
				ResourceGroupName: "unimportantPostponement",
				HCPClusterName:    "monstrousPrecinct",
			},
			ArtifactDir: api.Must(fs.Sub(artifacts, path.Join("artifacts/nodepool"))),
			ControllerInitializerFn: func(ctx context.Context, t *testing.T, input *controllertesthelpers.ControllerInitializationInput) (controller controllerutils.Controller, testMemory map[string]any) {
				return controllerutils.NewClusterWatchingController(
						"CosmosMatchingNodePools", input.CosmosClient, input.SubscriptionLister, 60*time.Minute,
						mismatchcontrollers.NewCosmosNodePoolMatchingController(input.CosmosClient, input.ClusterServiceClient)),
					map[string]any{}
			},
			ControllerVerifierFn: func(ctx context.Context, t *testing.T, controller controllerutils.Controller, testMemory map[string]any, input *controllertesthelpers.ControllerInitializationInput) {
				clusterResourceID := api.Must(azcorearm.ParseResourceID(strings.ToLower("/subscriptions/a433a095-1277-44f1-8453-8d61a4d848c2/resourceGroups/unimportantPostponement/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/monstrousPrecinct/nodepools/basic")))
				crud, err := input.CosmosClient.UntypedCRUD(*clusterResourceID)
				require.NoError(t, err)
				_, err = crud.Get(ctx, clusterResourceID)
				require.Error(t, err)
				allItems, err := crud.ListRecursive(ctx, nil)
				require.NoError(t, err)
				for _, curr := range allItems.Items(ctx) {
					if curr.ID == "|subscriptions|a433a095-1277-44f1-8453-8d61a4d848c2|resourcegroups|unimportantpostponement|providers|microsoft.redhatopenshift|hcpopenshiftclusters|monstrousprecinct|hcpopenshiftcontrollers|cosmosmatchingnodepools" {
						// we create an instance to indicate we deleted a thing.  We'll clean it up in a separate controller later that does NOT report.
						// we want this one to report in case it cannot cleanup, so we'll leave the standard logic.
						continue
					}
					t.Errorf("got an item: %v", curr)
				}
				require.Empty(t, allItems.GetError())
			},
		},
		{
			Name: "present_nodepool",
			ControllerKey: controllerutils.HCPClusterKey{
				SubscriptionID:    "a433a095-1277-44f1-8453-8d61a4d848c2",
				ResourceGroupName: "unimportantPostponement",
				HCPClusterName:    "monstrousPrecinct",
			},
			ArtifactDir: api.Must(fs.Sub(artifacts, path.Join("artifacts/nodepool"))),
			ControllerInitializerFn: func(ctx context.Context, t *testing.T, input *controllertesthelpers.ControllerInitializationInput) (controller controllerutils.Controller, testMemory map[string]any) {
				return controllerutils.NewClusterWatchingController(
						"CosmosMatchingNodePools", input.CosmosClient, input.SubscriptionLister, 60*time.Minute,
						mismatchcontrollers.NewCosmosNodePoolMatchingController(input.CosmosClient, input.ClusterServiceClient)),
					map[string]any{}

			},
			ControllerVerifierFn: func(ctx context.Context, t *testing.T, controller controllerutils.Controller, testMemory map[string]any, input *controllertesthelpers.ControllerInitializationInput) {
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.Name, tc.RunTest)
	}

}
