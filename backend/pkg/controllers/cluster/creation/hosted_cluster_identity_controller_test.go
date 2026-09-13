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

package creation

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/Azure/ARO-HCP/internal/ocm"
)

func TestNormalizeClusterServiceName(t *testing.T) {
	assert.Equal(t, "hello-world", normalizeClusterServiceName("Hello world"))
	assert.Equal(t, "abcdefghijklmno", normalizeClusterServiceName("ABCDEFGHIJKLMNOP"))
	assert.Equal(t, "cluster", normalizeClusterServiceName("-Cluster-"))
}

func TestCalculateHostedClusterName(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockCS := ocm.NewMockClusterServiceClientSpec(ctrl)
	mockCS.EXPECT().ListClusters(gomock.Any()).Return(ocm.NewSimpleClusterListIterator(nil, nil))
	syncer := &hostedClusterIdentitySyncer{clustersServiceClient: mockCS}

	got, err := syncer.calculateHostedClusterName(context.Background(), "My Cluster", "abcdefghijklmnopqrstuv", "SUB", "RG")
	require.NoError(t, err)
	assert.Equal(t, "my-cluster", got)
}

func TestCalculateHostedClusterNameFallsBackToID(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockCS := ocm.NewMockClusterServiceClientSpec(ctrl)
	mockCS.EXPECT().ListClusters(gomock.Any()).Return(ocm.NewSimpleClusterListIterator(nil, nil))
	syncer := &hostedClusterIdentitySyncer{clustersServiceClient: mockCS}

	got, err := syncer.calculateHostedClusterName(context.Background(), "this-name-is-longer-than-fifteen", "abcdefghijklmno-rest", "SUB", "RG")
	require.NoError(t, err)
	assert.Equal(t, "abcdefghijklmno", got)
}

func TestShortenClusterServiceEnvironment(t *testing.T) {
	assert.Equal(t, "int", shortenClusterServiceEnvironment("integration"))
	assert.Equal(t, "production", shortenClusterServiceEnvironment("production"))
}
