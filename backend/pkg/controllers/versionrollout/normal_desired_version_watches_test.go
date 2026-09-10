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

package versionrollout

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"k8s.io/client-go/tools/cache"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
)

type candidateQueue struct{ channels []string }

func (q *candidateQueue) Enqueue(key any) {
	q.channels = append(q.channels, key.(controllerutils.ControlPlaneVersionRolloutKey).YStreamChannel)
}

type candidateNotifier struct {
	handler cache.ResourceEventHandler
	options cache.HandlerOptions
}

func (n *candidateNotifier) AddEventHandlerWithOptions(handler cache.ResourceEventHandler, options cache.HandlerOptions) (cache.ResourceEventHandlerRegistration, error) {
	n.handler = handler
	n.options = options
	return nil, nil
}

func registerCandidateHandlers(t *testing.T, c *normalClusterDesiredVersionSyncer, q *candidateQueue) (cache.ResourceEventHandler, cache.ResourceEventHandler) {
	t.Helper()
	clusters, serviceProviderClusters := &candidateNotifier{}, &candidateNotifier{}
	require.NoError(t, c.watchVersionCandidates(clusters, serviceProviderClusters, q))
	require.NotNil(t, clusters.handler)
	require.NotNil(t, serviceProviderClusters.handler)
	require.NotNil(t, clusters.options.ResyncPeriod)
	require.NotNil(t, serviceProviderClusters.options.ResyncPeriod)
	require.Zero(t, *clusters.options.ResyncPeriod)
	require.Zero(t, *serviceProviderClusters.options.ResyncPeriod)
	return clusters.handler, serviceProviderClusters.handler
}

func TestNormalVersionCandidateClusterEvents(t *testing.T) {
	for _, tc := range []struct {
		name, oldGroup, oldVersion, newGroup, newVersion string
		want                                             []string
	}{
		{name: "initial add", newGroup: "stable", newVersion: "4.21", want: []string{"stable-4.21"}},
		{name: "resync", oldGroup: "stable", oldVersion: "4.21", newGroup: "stable", newVersion: "4.21"},
		{name: "patch version only", oldGroup: "stable", oldVersion: "4.21.1", newGroup: "stable", newVersion: "4.21.2"},
		{name: "minor changed", oldGroup: "stable", oldVersion: "4.21", newGroup: "stable", newVersion: "4.22", want: []string{"stable-4.22"}},
		{name: "group changed", oldGroup: "stable", oldVersion: "4.21", newGroup: "candidate", newVersion: "4.21", want: []string{"candidate-4.21"}},
		{name: "invalid to valid", oldGroup: "stable", oldVersion: "invalid", newGroup: "stable", newVersion: "4.21", want: []string{"stable-4.21"}},
		{name: "valid to invalid", oldGroup: "stable", oldVersion: "4.21", newGroup: "stable", newVersion: "invalid"},
		{name: "invalid add", newVersion: "4.21"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := &candidateQueue{}
			c := &normalClusterDesiredVersionSyncer{}
			h, _ := registerCandidateHandlers(t, c, q)
			cluster := newTestCluster("c1", tc.newGroup, tc.newVersion)
			if tc.oldVersion == "" {
				h.OnAdd(cluster, true)
			} else {
				h.OnUpdate(newTestCluster("c1", tc.oldGroup, tc.oldVersion), cluster)
			}
			h.OnDelete(cluster)
			require.Equal(t, tc.want, q.channels)
		})
	}
}

func TestNormalVersionCandidateServiceProviderClusterEvents(t *testing.T) {
	cluster := newTestCluster("c1", "stable", "4.21")
	db, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(context.Background(), []any{cluster})
	require.NoError(t, err)
	c := &normalClusterDesiredVersionSyncer{clusterLister: &corelistertesting.DBClusterLister{ResourcesDBClient: db}}
	for _, tc := range []struct {
		name                                          string
		setDesired, update, missingCluster, missingID bool
		want                                          []string
	}{
		{name: "add without desired", want: []string{"stable-4.21"}},
		{name: "update without desired", update: true, want: []string{"stable-4.21"}},
		{name: "add with desired", setDesired: true},
		{name: "update with desired", update: true, setDesired: true},
		{name: "missing cluster", missingCluster: true},
		{name: "missing resource ID", missingID: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := &candidateQueue{}
			_, h := registerCandidateHandlers(t, c, q)
			name := "c1"
			if tc.missingCluster {
				name = "missing"
			}
			serviceProviderCluster := newTestServiceProviderCluster(name, nil, nil, nil)
			if tc.setDesired {
				serviceProviderCluster.Spec.ControlPlaneVersion.DesiredVersion = v("4.21.1")
			}
			if tc.missingID {
				serviceProviderCluster.ResourceID = nil
			}
			if tc.update {
				h.OnUpdate(newTestServiceProviderCluster(name, nil, nil, nil), serviceProviderCluster)
			} else {
				h.OnAdd(serviceProviderCluster, true)
			}
			h.OnDelete(serviceProviderCluster)
			require.Equal(t, tc.want, q.channels)
		})
	}
}
