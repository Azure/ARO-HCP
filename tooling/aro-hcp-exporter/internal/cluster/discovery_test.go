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

package cluster

import (
	"context"
	"fmt"
	"testing"

	"github.com/go-viper/mapstructure/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Azure/ARO-HCP/tooling/aro-hcp-exporter/pkg/graphquery"
)

type mockQuerier struct {
	rows []clusterRow
	err  error
}

func (m *mockQuerier) ExecuteConvertRequest(_ context.Context, request graphquery.ResourceGraphRequest) error {
	if m.err != nil {
		return m.err
	}
	return mapstructure.Decode(m.rows, request.Output)
}

func TestDiscover(t *testing.T) {
	tests := []struct {
		name            string
		querier         *mockQuerier
		region          string
		clusterTypes    []string
		wantNames       []string
		wantSubIDs      []string
		wantErrContains string
	}{
		{
			name: "returns discovered clusters",
			querier: &mockQuerier{
				rows: []clusterRow{
					{Name: "svc-1", SubscriptionId: "sub-a"},
					{Name: "mgmt-1", SubscriptionId: "sub-b"},
				},
			},
			region:       "eastus",
			clusterTypes: []string{"svc-cluster", "mgmt-cluster"},
			wantNames:    []string{"svc-1", "mgmt-1"},
			wantSubIDs:   []string{"sub-a", "sub-b"},
		},
		{
			name: "deduplicates subscription IDs",
			querier: &mockQuerier{
				rows: []clusterRow{
					{Name: "cluster-a", SubscriptionId: "sub-1"},
					{Name: "cluster-b", SubscriptionId: "sub-2"},
					{Name: "cluster-c", SubscriptionId: "sub-1"},
				},
			},
			region:       "eastus",
			clusterTypes: []string{"svc-cluster"},
			wantNames:    []string{"cluster-a", "cluster-b", "cluster-c"},
			wantSubIDs:   []string{"sub-1", "sub-2"},
		},
		{
			name: "no clusters found includes region and types in error",
			querier: &mockQuerier{
				rows: []clusterRow{},
			},
			region:          "westeurope",
			clusterTypes:    []string{"svc-cluster"},
			wantErrContains: `no clusters found in region "westeurope" matching clusterTypes [svc-cluster]`,
		},
		{
			name: "client error is propagated",
			querier: &mockQuerier{
				err: fmt.Errorf("network timeout"),
			},
			region:          "eastus",
			clusterTypes:    []string{"svc-cluster"},
			wantErrContains: "network timeout",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := Discover(context.Background(), tt.querier, tt.region, tt.clusterTypes)

			if tt.wantErrContains != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErrContains)
				return
			}

			require.NoError(t, err)
			assert.ElementsMatch(t, tt.wantNames, result.ClusterNames)
			assert.ElementsMatch(t, tt.wantSubIDs, result.SubscriptionIDs)
		})
	}
}

func TestBuildKQLQuery(t *testing.T) {
	tests := []struct {
		name         string
		region       string
		clusterTypes []string
		wantContains []string
	}{
		{
			name:         "single cluster type",
			region:       "eastus",
			clusterTypes: []string{"svc-cluster"},
			wantContains: []string{
				"| where location =~ 'eastus'",
				"| where tags['clusterType'] in~ ('svc-cluster')",
				"| project name, subscriptionId",
			},
		},
		{
			name:         "multiple cluster types",
			region:       "westus2",
			clusterTypes: []string{"svc-cluster", "mgmt-cluster"},
			wantContains: []string{
				"| where location =~ 'westus2'",
				"| where tags['clusterType'] in~ ('svc-cluster', 'mgmt-cluster')",
			},
		},
		{
			name:         "region is passed through as-is",
			region:       "eastus",
			clusterTypes: []string{"svc-cluster"},
			wantContains: []string{
				"| where location =~ 'eastus'",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			query := buildKQLQuery(tt.region, tt.clusterTypes)

			assert.Contains(t, query, "| where type =~ 'Microsoft.ContainerService/managedClusters'")
			for _, want := range tt.wantContains {
				assert.Contains(t, query, want)
			}
		})
	}
}
