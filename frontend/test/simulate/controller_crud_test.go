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

package simulate

import (
	"context"
	"io/fs"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Azure/ARO-HCP/frontend/test/simulate/controllermutation"
	"github.com/Azure/ARO-HCP/internal/api"
)

func TestControllerCRUD(t *testing.T) {
	SkipIfNotSimulationTesting(t)

	ctx := context.Background()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	_, testInfo, err := NewFrontendFromTestingEnv(ctx, t)
	require.NoError(t, err)
	defer testInfo.Cleanup(context.Background())

	//controllerCRUDClient := database.NewControllerCRUD(
	//	testInfo.CosmosResourcesContainer(),
	//	api.ClusterResourceType,
	//	"subscriptionID",
	//	"resourceGroupName",
	//	"parentCluster")
	//
	//clusterResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/subscriptionID/resourceGroups/resourceGroupName/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/parentCluster"))
	//controllerName := "test-controller"
	//controllerResourceID := api.Must(azcorearm.ParseResourceID(clusterResourceID.String() + "/" + api.ControllerResourceTypeName + "/" + controllerName))
	//_, err = controllerCRUDClient.Create(ctx, &api.Controller{
	//	CosmosUID:      "e29415cf-5bde-463e-802f-6c475131d67b",
	//	ExternalID:     clusterResourceID,
	//	ControllerName: "test-controller",
	//	ResourceID:     controllerResourceID,
	//	Status: api.ControllerStatus{
	//		Conditions: []api.Condition{
	//			{
	//				Type:               "Degraded",
	//				Status:             api.ConditionTrue,
	//				LastTransitionTime: time.Now(),
	//				Reason:             "UpdateFailed",
	//				Message:            "Updating cosmos failed for some reason.",
	//			},
	//		},
	//	},
	//}, nil)
	//require.NoError(t, err)
	//
	//return

	controllerCRUDFS, err := fs.Sub(artifacts, "artifacts/ControllerCRUD")
	require.NoError(t, err)

	dirContent := api.Must(fs.ReadDir(controllerCRUDFS, "."))
	for _, dirEntry := range dirContent {
		currTest, err := controllermutation.NewControllerMutationTest(
			ctx,
			testInfo.CosmosResourcesContainer(),
			dirEntry.Name(),
			api.Must(fs.Sub(controllerCRUDFS, dirEntry.Name())),
		)
		require.NoError(t, err)

		t.Run(dirEntry.Name(), currTest.RunTest)
	}
}
