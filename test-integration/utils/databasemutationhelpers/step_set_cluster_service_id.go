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
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/test-integration/utils/integrationutils"
)

type setClusterServiceIDStep struct {
	stepID StepID
	key    ResourceKey

	explicitClusterServiceID string
}

type clusterServiceIDFile struct {
	ClusterServiceID string `json:"clusterServiceID"`
}

func newSetClusterServiceIDStep(stepID StepID, stepDir fs.FS) (*setClusterServiceIDStep, error) {
	keyBytes, err := fs.ReadFile(stepDir, "00-key.json")
	if err != nil {
		return nil, fmt.Errorf("failed to read 00-key.json: %w", err)
	}
	var key ResourceKey
	if err := json.Unmarshal(keyBytes, &key); err != nil {
		return nil, fmt.Errorf("failed to unmarshal 00-key.json: %w", err)
	}
	if key.ResourceID == "" {
		return nil, fmt.Errorf("00-key.json: resourceId is required")
	}

	var explicitClusterServiceID string
	csidBytes, err := fs.ReadFile(stepDir, "cluster-service-id.json")
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("failed to read cluster-service-id.json: %w", err)
	}
	if err == nil {
		var csidFile clusterServiceIDFile
		if err := json.Unmarshal(csidBytes, &csidFile); err != nil {
			return nil, fmt.Errorf("failed to unmarshal cluster-service-id.json: %w", err)
		}
		explicitClusterServiceID = strings.TrimSpace(csidFile.ClusterServiceID)
		if explicitClusterServiceID == "" {
			return nil, fmt.Errorf("cluster-service-id.json: clusterServiceID is required when file is present")
		}
	}

	return &setClusterServiceIDStep{
		stepID:                   stepID,
		key:                      key,
		explicitClusterServiceID: explicitClusterServiceID,
	}, nil
}

var _ IntegrationTestStep = &setClusterServiceIDStep{}

func (l *setClusterServiceIDStep) StepID() StepID {
	return l.stepID
}

func (l *setClusterServiceIDStep) RunTest(ctx context.Context, t *testing.T, stepInput StepInput) {
	t.Helper()

	clusterServiceID := l.explicitClusterServiceID
	if clusterServiceID == "" {
		var err error
		clusterServiceID, err = l.calculateClusterServiceID(ctx, stepInput.ResourcesDBClient)
		require.NoError(t, err)
	}

	err := integrationutils.SetClusterServiceID(ctx, stepInput.ResourcesDBClient, l.key.ResourceID, clusterServiceID)
	require.NoError(t, err)
}

func (l *setClusterServiceIDStep) calculateClusterServiceID(ctx context.Context, resourcesDBClient database.ResourcesDBClient) (string, error) {
	resourceID, err := azcorearm.ParseResourceID(l.key.ResourceID)
	if err != nil {
		return "", err
	}

	switch {
	case strings.EqualFold(resourceID.ResourceType.String(), api.ClusterResourceType.String()):
		return integrationutils.GenerateRandomClusterClusterServiceHREF(), nil
	case strings.EqualFold(resourceID.ResourceType.String(), api.NodePoolResourceType.String()):
		return integrationutils.CalculateClusterServiceIDFromNodePoolResourceID(ctx, resourcesDBClient, l.key.ResourceID)
	case strings.EqualFold(resourceID.ResourceType.String(), api.ExternalAuthResourceType.String()):
		return integrationutils.CalculateClusterServiceIDFromExternalAuthResourceID(ctx, resourcesDBClient, l.key.ResourceID)
	default:
		return "", fmt.Errorf("setClusterServiceID supports clusters, node pools, and external auths only: %s", l.key.ResourceID)
	}
}
