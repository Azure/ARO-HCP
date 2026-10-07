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
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clocktesting "k8s.io/utils/clock/testing"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
)

func TestMembershipUsesRolloutProfile(t *testing.T) {
	t.Parallel()
	clock := clocktesting.NewFakeClock(statusTestNow)
	deleting := newTestCluster("deleting", "stable", "4.21")
	deleting.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: clock.Now()}
	clusters := []*coreapi.Cluster{
		newTestCluster("desired", "stable", "4.22"),
		newTestCluster("active", "stable", "4.22"),
		newTestCluster("other-minor", "stable", "4.21"),
		newTestCluster("other-group", "fast", "4.21"),
		newTestCluster("missing-group", "", "4.21"),
		newTestCluster("unknown", "stable", "4.21"),
		deleting,
	}
	spcs := []*coreapi.ServiceProviderCluster{
		newTestServiceProviderCluster("desired", v("4.21.2"), []coreapi.ServiceProviderClusterActiveVersion{completed("4.20.9")}, nil),
		newTestServiceProviderCluster("active", nil, []coreapi.ServiceProviderClusterActiveVersion{partial("4.22.1"), completed("4.21.2")}, nil),
		newTestServiceProviderCluster("other-minor", v("4.22.1"), []coreapi.ServiceProviderClusterActiveVersion{completed("4.21.2")}, nil),
		newTestServiceProviderCluster("other-group", v("4.21.2"), nil, nil),
		newTestServiceProviderCluster("missing-group", v("4.21.2"), nil, nil),
		newTestServiceProviderCluster("unknown", nil, nil, nil),
		newTestServiceProviderCluster("deleting", v("4.21.2"), nil, nil),
		newTestServiceProviderCluster("orphan", v("4.21.2"), nil, nil),
	}
	want := []*coreapi.ServiceProviderCluster{spcs[0].DeepCopy(), spcs[1].DeepCopy()}
	got, err := serviceProviderClustersForProfile(t.Context(),
		&corelistertesting.SliceServiceProviderClusterLister{ServiceProviderClusters: spcs},
		&corelistertesting.SliceClusterLister{Clusters: clusters},
		coreapi.VersionProfile{ID: "4.21", ChannelGroup: "stable"})
	require.NoError(t, err)
	require.Empty(t, cmp.Diff(want, got, cmp.AllowUnexported(azcorearm.ResourceID{}, azcorearm.ResourceType{})), "desired minor takes precedence over active and requested versions; completed active minor is the fallback")
}
