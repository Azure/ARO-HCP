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

package kubeappliercosmosstorage_test

import (
	"context"
	"strings"
	"testing"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/kubeappliercosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/kubeappliercosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestSummarizeApplyDesiresByController(t *testing.T) {
	const (
		subscriptionID    = "00000000-0000-0000-0000-000000000000"
		resourceGroupName = "test-rg"
		clusterName       = "test-cluster"
	)
	managementClusterResourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/providers/microsoft.redhatopenshift/stamps/1/managementclusters/default"))

	newApplyDesire := func(name string, tags map[string]string) *kubeapplierapi.ApplyDesire {
		resourceID := metadataapi.Must(azcorearm.ParseResourceID(
			kubeapplierapi.ToClusterScopedApplyDesireResourceIDString(subscriptionID, resourceGroupName, clusterName, name)))
		return &kubeapplierapi.ApplyDesire{
			CosmosMetadata: coreapi.CosmosMetadata{
				ResourceID:   resourceID,
				PartitionKey: strings.ToLower(managementClusterResourceID.String()),
			},
			Spec: kubeapplierapi.ApplyDesireSpec{
				ManagementCluster: managementClusterResourceID,
			},
			Tags: tags,
		}
	}
	tagged := func(name, controllerName string) *kubeapplierapi.ApplyDesire {
		return newApplyDesire(name, map[string]string{kubeapplierapi.TagControllerName: controllerName})
	}
	untagged := func(name string) *kubeapplierapi.ApplyDesire {
		return newApplyDesire(name, nil)
	}

	testCases := []struct {
		name          string
		desires       []any
		wantTotal     int
		wantBreakdown string
	}{
		{
			name:          "no ApplyDesires",
			desires:       nil,
			wantTotal:     0,
			wantBreakdown: "",
		},
		{
			name:          "single tagged ApplyDesire",
			desires:       []any{tagged("desire-a", "some-controller")},
			wantTotal:     1,
			wantBreakdown: "1 for controller some-controller",
		},
		{
			name:          "untagged ApplyDesire buckets under unknown",
			desires:       []any{untagged("desire-a")},
			wantTotal:     1,
			wantBreakdown: "1 for controller " + kubeappliercosmosstorage.UnknownApplyDesireController,
		},
		{
			// Sorted by controller NAME, not count: "aaa-controller" has more desires
			// than the "unknown" bucket yet sorts first. Sorting the formatted
			// "%d for controller %s" strings would order by the leading count instead
			// and put "unknown" first.
			name: "breakdown sorted by controller name, not count",
			desires: []any{
				tagged("desire-a", "aaa-controller"),
				tagged("desire-b", "aaa-controller"),
				untagged("desire-c"),
			},
			wantTotal:     3,
			wantBreakdown: "2 for controller aaa-controller, 1 for controller unknown",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))

			client, err := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClientWithResources(ctx, tc.desires)
			require.NoError(t, err)

			applyDesireCRUD, err := client.ApplyDesiresForCluster(subscriptionID, resourceGroupName, clusterName)
			require.NoError(t, err)

			total, breakdown, err := kubeappliercosmosstorage.SummarizeApplyDesiresByController(ctx, applyDesireCRUD)
			require.NoError(t, err)
			assert.Equal(t, tc.wantTotal, total)
			assert.Equal(t, tc.wantBreakdown, breakdown)
		})
	}
}
