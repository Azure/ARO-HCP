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
	"testing"

	"github.com/stretchr/testify/require"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/mismatchcontrollers"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/test-integration/utils/controllertesthelpers"
	"github.com/Azure/ARO-HCP/test-integration/utils/integrationutils"
)

func TestDeleteOrphanedCosmosResourcesController(t *testing.T) {
	defer integrationutils.VerifyNoNewGoLeaks(t)
	integrationutils.WithAndWithoutCosmos(t, testDeleteOrphanedCosmosResourcesController)
}

func testDeleteOrphanedCosmosResourcesController(t *testing.T, withMock bool) {
	testCases := []controllertesthelpers.BasicControllerTest{
		{
			Name: "all_parents_exist",
			ControllerKey: controllerutils.HCPClusterKey{
				SubscriptionID:    "a433a095-1277-44f1-8453-8d61a4d848c2",
				ResourceGroupName: "unimportantPostponement",
				HCPClusterName:    "monstrousPrecinct",
			},
			ArtifactDir: api.Must(fs.Sub(artifacts, path.Join("artifacts/delete_orphaned_cosmos"))),
			ControllerInitializerFn: func(ctx context.Context, t *testing.T, input *controllertesthelpers.ControllerInitializationInput) (controller controllerutils.Controller, testMemory map[string]any) {
				return newSubscriptionKeyWrapper(
					mismatchcontrollers.NewDeleteOrphanedCosmosResourcesController(input.CosmosClient, input.SubscriptionLister),
				), map[string]any{}
			},
			ControllerVerifierFn: func(ctx context.Context, t *testing.T, controller controllerutils.Controller, testMemory map[string]any, input *controllertesthelpers.ControllerInitializationInput) {
			},
		},
		{
			Name: "old_style_id_deleted",
			ControllerKey: controllerutils.HCPClusterKey{
				SubscriptionID:    "a433a095-1277-44f1-8453-8d61a4d848c2",
				ResourceGroupName: "unimportantPostponement",
				HCPClusterName:    "monstrousPrecinct",
			},
			ArtifactDir: api.Must(fs.Sub(artifacts, path.Join("artifacts/delete_orphaned_cosmos"))),
			ControllerInitializerFn: func(ctx context.Context, t *testing.T, input *controllertesthelpers.ControllerInitializationInput) (controller controllerutils.Controller, testMemory map[string]any) {
				return newSubscriptionKeyWrapper(
					mismatchcontrollers.NewDeleteOrphanedCosmosResourcesController(input.CosmosClient, input.SubscriptionLister),
				), map[string]any{}
			},
			ControllerVerifierFn: func(ctx context.Context, t *testing.T, controller controllerutils.Controller, testMemory map[string]any, input *controllertesthelpers.ControllerInitializationInput) {
				// The old-style ID document should be deleted
				subscriptionResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/a433a095-1277-44f1-8453-8d61a4d848c2"))
				crud, err := input.CosmosClient.UntypedCRUD(*subscriptionResourceID)
				require.NoError(t, err)

				allItems, err := crud.ListRecursive(ctx, nil)
				require.NoError(t, err)

				for _, item := range allItems.Items(ctx) {
					if item.ID == "|subscriptions|a433a095-1277-44f1-8453-8d61a4d848c2" ||
						item.ID == "|subscriptions|a433a095-1277-44f1-8453-8d61a4d848c2|resourcegroups|unimportantpostponement|providers|microsoft.redhatopenshift|hcpopenshiftclusters|monstrousprecinct" {
						continue
					}
					t.Errorf("unexpected resource found: %v", item.ID)
				}
				require.NoError(t, allItems.GetError())
			},
		},
		{
			Name: "controller_under_missing_nodepool_deleted",
			ControllerKey: controllerutils.HCPClusterKey{
				SubscriptionID:    "a433a095-1277-44f1-8453-8d61a4d848c2",
				ResourceGroupName: "unimportantPostponement",
				HCPClusterName:    "monstrousPrecinct",
			},
			ArtifactDir: api.Must(fs.Sub(artifacts, path.Join("artifacts/delete_orphaned_cosmos"))),
			ControllerInitializerFn: func(ctx context.Context, t *testing.T, input *controllertesthelpers.ControllerInitializationInput) (controller controllerutils.Controller, testMemory map[string]any) {
				return newSubscriptionKeyWrapper(
					mismatchcontrollers.NewDeleteOrphanedCosmosResourcesController(input.CosmosClient, input.SubscriptionLister),
				), map[string]any{}
			},
			ControllerVerifierFn: func(ctx context.Context, t *testing.T, controller controllerutils.Controller, testMemory map[string]any, input *controllertesthelpers.ControllerInitializationInput) {
				// Controllers under missing nodepool should be deleted
				// Controllers under existing cluster should NOT be deleted
				subscriptionResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/a433a095-1277-44f1-8453-8d61a4d848c2"))
				crud, err := input.CosmosClient.UntypedCRUD(*subscriptionResourceID)
				require.NoError(t, err)

				allItems, err := crud.ListRecursive(ctx, nil)
				require.NoError(t, err)

				for _, item := range allItems.Items(ctx) {
					if item.ID == "|subscriptions|a433a095-1277-44f1-8453-8d61a4d848c2" ||
						item.ID == "|subscriptions|a433a095-1277-44f1-8453-8d61a4d848c2|resourcegroups|unimportantpostponement|providers|microsoft.redhatopenshift|hcpopenshiftclusters|monstrousprecinct" ||
						item.ID == "|subscriptions|a433a095-1277-44f1-8453-8d61a4d848c2|resourcegroups|unimportantpostponement|providers|microsoft.redhatopenshift|hcpopenshiftclusters|monstrousprecinct|hcpopenshiftcontrollers|clustercontroller" {
						continue
					}
					t.Errorf("unexpected resource found: %v", item.ID)
				}
				require.NoError(t, allItems.GetError())
			},
		},
		{
			Name: "controller_under_missing_cluster_deleted",
			ControllerKey: controllerutils.HCPClusterKey{
				SubscriptionID:    "a433a095-1277-44f1-8453-8d61a4d848c2",
				ResourceGroupName: "unimportantPostponement",
				HCPClusterName:    "missingcluster",
			},
			ArtifactDir: api.Must(fs.Sub(artifacts, path.Join("artifacts/delete_orphaned_cosmos"))),
			ControllerInitializerFn: func(ctx context.Context, t *testing.T, input *controllertesthelpers.ControllerInitializationInput) (controller controllerutils.Controller, testMemory map[string]any) {
				return newSubscriptionKeyWrapper(
					mismatchcontrollers.NewDeleteOrphanedCosmosResourcesController(input.CosmosClient, input.SubscriptionLister),
				), map[string]any{}
			},
			ControllerVerifierFn: func(ctx context.Context, t *testing.T, controller controllerutils.Controller, testMemory map[string]any, input *controllertesthelpers.ControllerInitializationInput) {
				// All controllers under missing cluster should be deleted
				subscriptionResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/a433a095-1277-44f1-8453-8d61a4d848c2"))
				crud, err := input.CosmosClient.UntypedCRUD(*subscriptionResourceID)
				require.NoError(t, err)

				allItems, err := crud.ListRecursive(ctx, nil)
				require.NoError(t, err)

				for _, item := range allItems.Items(ctx) {
					// No resources other than this subscription should exist
					if item.ID != "|subscriptions|a433a095-1277-44f1-8453-8d61a4d848c2" {
						t.Errorf("resource under missing cluster should have been deleted: %s", item.ID)
					}
				}
				require.NoError(t, allItems.GetError())
			},
		},
	}

	for _, tc := range testCases {
		tc.WithMock = withMock
		t.Run(tc.Name, tc.RunTest)
	}
}

// subscriptionKeyWrapper wraps a controller that expects a subscription ID string
// and adapts it to accept an HCPClusterKey by extracting the subscription ID.
type subscriptionKeyWrapper struct {
	delegate controllerutils.Controller
}

func newSubscriptionKeyWrapper(delegate controllerutils.Controller) controllerutils.Controller {
	return &subscriptionKeyWrapper{delegate: delegate}
}

func (w *subscriptionKeyWrapper) SyncOnce(ctx context.Context, keyObj any) error {
	// Extract subscription ID from HCPClusterKey
	key := keyObj.(controllerutils.HCPClusterKey)
	return w.delegate.SyncOnce(ctx, key.SubscriptionID)
}

func (w *subscriptionKeyWrapper) Run(ctx context.Context, threadiness int) {
	w.delegate.Run(ctx, threadiness)
}
