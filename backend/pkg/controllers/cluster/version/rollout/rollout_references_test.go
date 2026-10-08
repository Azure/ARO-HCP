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

package rollout

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	clocktesting "k8s.io/utils/clock/testing"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
)

func TestCollectRolloutReferences(t *testing.T) {
	for _, group := range []string{"stable", "fast", "candidate", "nightly"} {
		for _, tc := range []struct {
			name       string
			change     func(*coreapi.Cluster, *coreapi.ServiceProviderCluster)
			additional []string
			wantError  bool
		}{
			{"requested", func(c *coreapi.Cluster, _ *coreapi.ServiceProviderCluster) {
				c.CustomerProperties.Version.ID = "4.19.1"
			}, []string{"4.19"}, false},
			{"desired", func(_ *coreapi.Cluster, s *coreapi.ServiceProviderCluster) {
				s.Spec.ControlPlaneVersion.DesiredVersion = v("4.19.1")
			}, []string{"4.19"}, false},
			{"every active version", func(_ *coreapi.Cluster, s *coreapi.ServiceProviderCluster) {
				s.Status.ControlPlaneVersion.ActiveVersions = []coreapi.ServiceProviderClusterActiveVersion{{Version: v("4.18.1")}, {Version: nil}, {Version: v("4.19.1")}}
			}, []string{"4.18", "4.19"}, false},
			{"pinned exact not threshold", func(_ *coreapi.Cluster, s *coreapi.ServiceProviderCluster) {
				s.Spec.PinnedVersion.ExactVersion = v("4.19.1")
				s.Spec.PinnedVersion.UntilExactVersion = v("4.18.1")
			}, []string{"4.19"}, false},
			{"threshold only", func(_ *coreapi.Cluster, s *coreapi.ServiceProviderCluster) {
				s.Spec.PinnedVersion.UntilExactVersion = v("4.18.1")
			}, nil, false},
			{"exact override", func(c *coreapi.Cluster, _ *coreapi.ServiceProviderCluster) {
				c.ServiceProviderProperties.ExperimentalFeatures.ControlPlaneExactVersion = v("4.19.1")
			}, []string{"4.19"}, false},
			{"every customer active version", func(c *coreapi.Cluster, _ *coreapi.ServiceProviderCluster) {
				c.Status.ActiveVersions = []coreapi.HCPClusterActiveVersion{{Version: "4.18"}, {Version: "4.19.1"}}
			}, []string{"4.18", "4.19"}, false},
			{"invalid active retains other references", func(c *coreapi.Cluster, _ *coreapi.ServiceProviderCluster) {
				c.Status.ActiveVersions = []coreapi.HCPClusterActiveVersion{{Version: "invalid"}, {Version: "4.19"}}
			}, []string{"4.19"}, true},
			{"case insensitive parent", func(c *coreapi.Cluster, s *coreapi.ServiceProviderCluster) {
				id, err := azcorearm.ParseResourceID(strings.ToUpper(c.ID.String()))
				require.NoError(t, err)
				c.ID = id
				s.Spec.ControlPlaneVersion.DesiredVersion = v("4.19.1")
			}, []string{"4.19"}, false},
		} {
			t.Run(group+"/"+tc.name, func(t *testing.T) {
				clock := clocktesting.NewFakeClock(statusTestNow)
				cluster := newTestCluster("c1", group, "4.21")
				cluster.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: clock.Now()}
				cluster.Status.ActiveVersions = []coreapi.HCPClusterActiveVersion{{Version: "4.21"}}
				spc := newTestServiceProviderCluster("c1", nil, nil, nil)
				tc.change(cluster, spc)
				beforeCluster, beforeSPC := cluster.DeepCopy(), spc.DeepCopy()
				refs, err := collectRolloutReferences([]*coreapi.Cluster{cluster}, []*coreapi.ServiceProviderCluster{spc})
				require.Equal(t, tc.wantError, err != nil)
				want := sets.New(coreapi.VersionProfile{ChannelGroup: group, ID: "4.21"})
				for _, minor := range tc.additional {
					want.Insert(coreapi.VersionProfile{ChannelGroup: group, ID: minor})
				}
				require.Empty(t, cmp.Diff(want, refs), "deleting documents still retain all dependencies")
				require.Empty(t, cmp.Diff(beforeCluster, cluster, cmp.AllowUnexported(azcorearm.ResourceID{}, azcorearm.ResourceType{})), "clusters must not be mutated")
				require.Empty(t, cmp.Diff(beforeSPC, spc, cmp.AllowUnexported(azcorearm.ResourceID{}, azcorearm.ResourceType{})), "SPCs must not be mutated")
			})
		}
	}
}

func TestCollectRolloutReferencesUncertainty(t *testing.T) {
	for _, tc := range []struct {
		name      string
		cluster   *coreapi.Cluster
		spc       *coreapi.ServiceProviderCluster
		want      sets.Set[coreapi.VersionProfile]
		wantError string
	}{
		{"empty", nil, nil, sets.New[coreapi.VersionProfile](), ""},
		{"nightly prerelease", newTestCluster("c1", "nightly", "4.21.0-0.nightly"), newTestServiceProviderCluster("c1", v("4.19.0-0.nightly"), nil, nil), sets.New(coreapi.VersionProfile{ChannelGroup: "nightly", ID: "4.19"}, coreapi.VersionProfile{ChannelGroup: "nightly", ID: "4.21"}), ""},
		{"invalid nightly retains known references", newTestCluster("c1", "nightly", "invalid"), newTestServiceProviderCluster("c1", v("4.19.1"), nil, nil), sets.New(coreapi.VersionProfile{ChannelGroup: "nightly", ID: "4.19"}), "invalid version"},
		{"unknown group", newTestCluster("c1", "unknown", "4.21"), nil, sets.New[coreapi.VersionProfile](), "unsupported"},
		{"missing group", newTestCluster("c1", "", "4.21"), newTestServiceProviderCluster("c1", v("4.19.1"), nil, nil), sets.New[coreapi.VersionProfile](), "cannot determine channel group"},
		{"missing cluster ID", &coreapi.Cluster{}, nil, sets.New[coreapi.VersionProfile](), "cluster without resource ID"},
		{"missing SPC ID", nil, &coreapi.ServiceProviderCluster{}, sets.New[coreapi.VersionProfile](), "service provider cluster without parent"},
		{"orphan", nil, newTestServiceProviderCluster("orphan", v("4.19.1"), nil, nil), sets.New[coreapi.VersionProfile](), "cannot determine channel group"},
		{"known references with orphan", newTestCluster("healthy", "fast", "4.21"), newTestServiceProviderCluster("orphan", v("4.19.1"), nil, nil), sets.New(coreapi.VersionProfile{ChannelGroup: "fast", ID: "4.21"}), "cannot determine channel group"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var clusters []*coreapi.Cluster
			var spcs []*coreapi.ServiceProviderCluster
			if tc.cluster != nil {
				clusters = append(clusters, tc.cluster)
			}
			if tc.spc != nil {
				spcs = append(spcs, tc.spc)
			}
			refs, err := collectRolloutReferences(clusters, spcs)
			if tc.wantError == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, tc.wantError, "uncertainty must be surfaced so retirement fails closed")
			}
			require.Empty(t, cmp.Diff(tc.want, refs), "collected references must match")
		})
	}
}
