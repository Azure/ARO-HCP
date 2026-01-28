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
	"embed"
	"io/fs"
	"path"
	"testing"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/test-integration/utils/controllertesthelpers"
	"github.com/Azure/ARO-HCP/test-integration/utils/integrationutils"
)

//go:embed artifacts/*
var artifacts embed.FS

func TestDoNothingController(t *testing.T) {
	integrationutils.WithAndWithoutCosmos(t, testDoNothingController)
}

func testDoNothingController(t *testing.T, withMock bool) {
	testCases := []controllertesthelpers.BasicControllerTest{
		{
			Name: "sync_deleted_cluster",
			ControllerKey: controllerutils.HCPClusterKey{
				SubscriptionID:    "3d2a485a-d467-4375-b0dc-92350913c57e",
				ResourceGroupName: "partialIllustrator",
				HCPClusterName:    "damagingKingdom",
			},
			ArtifactDir: api.Must(fs.Sub(artifacts, path.Join("artifacts"))),
			ControllerInitializerFn: func(ctx context.Context, t *testing.T, input *controllertesthelpers.ControllerInitializationInput) (controller controllerutils.Controller, testMemory map[string]any) {
				return controllers.NewDoNothingExampleController(input.CosmosClient, input.SubscriptionLister), map[string]any{}
			},
			ControllerVerifierFn: func(ctx context.Context, t *testing.T, controller controllerutils.Controller, testMemory map[string]any, input *controllertesthelpers.ControllerInitializationInput) {
			},
		},
		{
			Name: "sync_cluster",
			ControllerKey: controllerutils.HCPClusterKey{
				SubscriptionID:    "4fa75980-6637-4157-9726-84d878a62e83",
				ResourceGroupName: "shrillEffectiveness",
				HCPClusterName:    "lavishUnhappiness",
			},
			ArtifactDir: api.Must(fs.Sub(artifacts, path.Join("artifacts"))),
			ControllerInitializerFn: func(ctx context.Context, t *testing.T, input *controllertesthelpers.ControllerInitializationInput) (controller controllerutils.Controller, testMemory map[string]any) {
				return controllers.NewDoNothingExampleController(input.CosmosClient, input.SubscriptionLister), map[string]any{}
			},
			ControllerVerifierFn: func(ctx context.Context, t *testing.T, controller controllerutils.Controller, testMemory map[string]any, input *controllertesthelpers.ControllerInitializationInput) {
			},
		},
	}

	for _, tc := range testCases {
		tc.WithMock = withMock
		t.Run(tc.Name, tc.RunTest)
	}
}
