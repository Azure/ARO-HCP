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

package databasemutationhelpers

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"testing"

	"github.com/stretchr/testify/require"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/clusterupdate"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/listertesting"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/test-integration/utils/integrationutils"
)

type syncClusterClusterServiceUpdateDispatchStep struct {
	stepID StepID
	key    controllerutils.HCPClusterKey
}

func newSyncClusterClusterServiceUpdateDispatchStep(stepID StepID, stepDir fs.FS) (*syncClusterClusterServiceUpdateDispatchStep, error) {
	keyBytes, err := fs.ReadFile(stepDir, "00-key.json")
	if err != nil {
		return nil, fmt.Errorf("failed to read 00-key.json: %w", err)
	}
	var resourceKey ResourceKey
	if err := json.Unmarshal(keyBytes, &resourceKey); err != nil {
		return nil, fmt.Errorf("failed to unmarshal 00-key.json: %w", err)
	}

	resourceID, err := azcorearm.ParseResourceID(resourceKey.ResourceID)
	if err != nil {
		return nil, fmt.Errorf("failed to parse resource ID %q: %w", resourceKey.ResourceID, err)
	}

	return &syncClusterClusterServiceUpdateDispatchStep{
		stepID: stepID,
		key: controllerutils.HCPClusterKey{
			SubscriptionID:    resourceID.SubscriptionID,
			ResourceGroupName: resourceID.ResourceGroupName,
			HCPClusterName:    resourceID.Name,
		},
	}, nil
}

var _ IntegrationTestStep = &syncClusterClusterServiceUpdateDispatchStep{}

func (s *syncClusterClusterServiceUpdateDispatchStep) StepID() StepID {
	return s.stepID
}

func (s *syncClusterClusterServiceUpdateDispatchStep) RunTest(ctx context.Context, t *testing.T, stepInput StepInput) {
	require.NotNil(t, stepInput.ClusterServiceMockInfo, "syncClusterClusterServiceUpdateDispatch requires a ClusterServiceMock")

	ctx = utils.ContextWithLogger(ctx, integrationutils.DefaultLogger(t))

	cluster, err := stepInput.ResourcesDBClient.HCPClusters(s.key.SubscriptionID, s.key.ResourceGroupName).Get(ctx, s.key.HCPClusterName)
	require.NoError(t, err)

	clusterLister := &listertesting.SliceClusterLister{Clusters: []*api.HCPOpenShiftCluster{cluster}}
	activeOperationLister := &listertesting.SliceActiveOperationLister{}
	subscriptionLister := &listertesting.DBSubscriptionLister{ResourcesDBClient: stepInput.ResourcesDBClient}
	syncer := clusterupdate.NewClusterClusterServiceUpdateDispatchSyncer(
		stepInput.ResourcesDBClient,
		stepInput.ClusterServiceMockInfo.MockClusterServiceClient,
		activeOperationLister,
		clusterLister,
		subscriptionLister,
	)

	err = syncer.SyncOnce(ctx, s.key)
	require.NoError(t, err)
}
