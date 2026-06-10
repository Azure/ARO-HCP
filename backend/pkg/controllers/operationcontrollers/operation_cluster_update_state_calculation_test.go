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

package operationcontrollers

import (
	"context"
	"testing"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"k8s.io/utils/ptr"

	"github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplier"
	internallistertesting "github.com/Azure/ARO-HCP/internal/database/listertesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func observedAutoscalingForZeroDesired() v1beta1.ClusterAutoscaling {
	return v1beta1.ClusterAutoscaling{
		MaxNodesTotal:        ptr.To[int32](0),
		MaxPodGracePeriod:    ptr.To[int32](0),
		MaxNodeProvisionTime: "0m",
		PodPriorityThreshold: ptr.To[int32](0),
	}
}

func TestHypershiftClusterOperationState(t *testing.T) {
	t.Parallel()

	fixture := newClusterTestFixture()

	tests := []struct {
		name              string
		cluster           *api.HCPOpenShiftCluster
		readDesires       []*kubeapplier.ReadDesire
		wantState         arm.ProvisioningState
		wantMessageSubstr string
	}{
		{
			name:              "no ReadDesire returns Updating",
			cluster:           fixture.newCluster(nil),
			readDesires:       nil,
			wantState:         arm.ProvisioningStateUpdating,
			wantMessageSubstr: "not been observed",
		},
		{
			name:    "empty cluster matches empty HostedCluster",
			cluster: fixture.newCluster(nil),
			readDesires: []*kubeapplier.ReadDesire{
				newHostedClusterReadDesire(t, &v1beta1.HostedCluster{
					Spec: v1beta1.HostedClusterSpec{
						Autoscaling: observedAutoscalingForZeroDesired(),
					},
				}),
			},
			wantState: arm.ProvisioningStateSucceeded,
		},
		{
			name: "autoscaling maxNodesTotal mismatch returns Updating",
			cluster: func() *api.HCPOpenShiftCluster {
				c := fixture.newCluster(nil)
				c.CustomerProperties.Autoscaling.MaxNodesTotal = 10
				return c
			}(),
			readDesires: []*kubeapplier.ReadDesire{
				newHostedClusterReadDesire(t, &v1beta1.HostedCluster{
					Spec: v1beta1.HostedClusterSpec{
						Autoscaling: func() v1beta1.ClusterAutoscaling {
							autoscaling := observedAutoscalingForZeroDesired()
							autoscaling.MaxNodesTotal = ptr.To[int32](5)
							return autoscaling
						}(),
					},
				}),
			},
			wantState:         arm.ProvisioningStateUpdating,
			wantMessageSubstr: "maxNodesTotal",
		},
		{
			name: "autoscaling matches returns Succeeded",
			cluster: func() *api.HCPOpenShiftCluster {
				c := fixture.newCluster(nil)
				c.CustomerProperties.Autoscaling.MaxNodesTotal = 10
				c.CustomerProperties.Autoscaling.MaxPodGracePeriodSeconds = 60
				c.CustomerProperties.Autoscaling.PodPriorityThreshold = -5
				return c
			}(),
			readDesires: []*kubeapplier.ReadDesire{
				newHostedClusterReadDesire(t, &v1beta1.HostedCluster{
					Spec: v1beta1.HostedClusterSpec{
						Autoscaling: v1beta1.ClusterAutoscaling{
							MaxNodesTotal:        ptr.To[int32](10),
							MaxPodGracePeriod:    ptr.To[int32](60),
							MaxNodeProvisionTime: "0m",
							PodPriorityThreshold: ptr.To[int32](-5),
						},
					},
				}),
			},
			wantState: arm.ProvisioningStateSucceeded,
		},
		{
			name: "maxNodeProvisionTime mismatch returns Updating",
			cluster: func() *api.HCPOpenShiftCluster {
				c := fixture.newCluster(nil)
				c.CustomerProperties.Autoscaling.MaxNodeProvisionTimeSeconds = 900
				return c
			}(),
			readDesires: []*kubeapplier.ReadDesire{
				newHostedClusterReadDesire(t, &v1beta1.HostedCluster{
					Spec: v1beta1.HostedClusterSpec{
						Autoscaling: func() v1beta1.ClusterAutoscaling {
							autoscaling := observedAutoscalingForZeroDesired()
							autoscaling.MaxNodeProvisionTime = "10m"
							return autoscaling
						}(),
					},
				}),
			},
			wantState:         arm.ProvisioningStateUpdating,
			wantMessageSubstr: "maxNodeProvisionTime",
		},
		{
			name: "maxNodeProvisionTime matches when converted",
			cluster: func() *api.HCPOpenShiftCluster {
				c := fixture.newCluster(nil)
				c.CustomerProperties.Autoscaling.MaxNodeProvisionTimeSeconds = 900
				return c
			}(),
			readDesires: []*kubeapplier.ReadDesire{
				newHostedClusterReadDesire(t, &v1beta1.HostedCluster{
					Spec: v1beta1.HostedClusterSpec{
						Autoscaling: func() v1beta1.ClusterAutoscaling {
							autoscaling := observedAutoscalingForZeroDesired()
							autoscaling.MaxNodeProvisionTime = "15m"
							return autoscaling
						}(),
					},
				}),
			},
			wantState: arm.ProvisioningStateSucceeded,
		},
		{
			name: "imageContentSources count mismatch returns Updating",
			cluster: func() *api.HCPOpenShiftCluster {
				c := fixture.newCluster(nil)
				c.CustomerProperties.ImageDigestMirrors = []api.ImageDigestMirror{
					{Source: "quay.io/foo", Mirrors: []string{"mirror.io/foo"}},
				}
				return c
			}(),
			readDesires: []*kubeapplier.ReadDesire{
				newHostedClusterReadDesire(t, &v1beta1.HostedCluster{
					Spec: v1beta1.HostedClusterSpec{
						Autoscaling: observedAutoscalingForZeroDesired(),
					},
				}),
			},
			wantState:         arm.ProvisioningStateUpdating,
			wantMessageSubstr: "imageContentSources",
		},
		{
			name: "imageContentSources source mismatch returns Updating",
			cluster: func() *api.HCPOpenShiftCluster {
				c := fixture.newCluster(nil)
				c.CustomerProperties.ImageDigestMirrors = []api.ImageDigestMirror{
					{Source: "quay.io/foo", Mirrors: []string{"mirror.io/foo"}},
				}
				return c
			}(),
			readDesires: []*kubeapplier.ReadDesire{
				newHostedClusterReadDesire(t, &v1beta1.HostedCluster{
					Spec: v1beta1.HostedClusterSpec{
						Autoscaling: observedAutoscalingForZeroDesired(),
						ImageContentSources: []v1beta1.ImageContentSource{
							{Source: "quay.io/bar", Mirrors: []string{"mirror.io/foo"}},
						},
					},
				}),
			},
			wantState:         arm.ProvisioningStateUpdating,
			wantMessageSubstr: "source",
		},
		{
			name: "imageContentSources matches returns Succeeded",
			cluster: func() *api.HCPOpenShiftCluster {
				c := fixture.newCluster(nil)
				c.CustomerProperties.ImageDigestMirrors = []api.ImageDigestMirror{
					{Source: "quay.io/foo", Mirrors: []string{"mirror.io/foo", "mirror2.io/foo"}},
				}
				return c
			}(),
			readDesires: []*kubeapplier.ReadDesire{
				newHostedClusterReadDesire(t, &v1beta1.HostedCluster{
					Spec: v1beta1.HostedClusterSpec{
						Autoscaling: observedAutoscalingForZeroDesired(),
						ImageContentSources: []v1beta1.ImageContentSource{
							{Source: "quay.io/foo", Mirrors: []string{"mirror.io/foo", "mirror2.io/foo"}},
						},
					},
				}),
			},
			wantState: arm.ProvisioningStateSucceeded,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			ctx = utils.ContextWithLogger(ctx, testr.New(t))

			controller := &operationClusterUpdate{
				readDesireLister: &internallistertesting.SliceReadDesireLister{
					Desires: tt.readDesires,
				},
			}

			state, err := controller.hypershiftClusterOperationState(ctx, tt.cluster)
			require.NoError(t, err)
			assert.Equal(t, tt.wantState, state.provisioningState)
			if tt.wantMessageSubstr != "" {
				assert.Contains(t, state.message, tt.wantMessageSubstr)
			}
		})
	}
}

func TestAutoscalingSpecMatchesDesired(t *testing.T) {
	t.Parallel()

	controller := &operationClusterUpdate{}

	tests := []struct {
		name       string
		desired    api.ClusterAutoscalingProfile
		observed   v1beta1.ClusterAutoscaling
		wantMatch  bool
		wantSubstr string
	}{
		{
			name:    "maxNodesTotal zero exact match",
			desired: api.ClusterAutoscalingProfile{},
			observed: observedAutoscalingForZeroDesired(),
			wantMatch: true,
		},
		{
			name:    "maxNodesTotal desired zero observed nil",
			desired: api.ClusterAutoscalingProfile{},
			observed: func() v1beta1.ClusterAutoscaling {
				autoscaling := observedAutoscalingForZeroDesired()
				autoscaling.MaxNodesTotal = nil
				return autoscaling
			}(),
			wantMatch:  false,
			wantSubstr: "maxNodesTotal",
		},
		{
			name:    "maxPodGracePeriod desired zero observed nil",
			desired: api.ClusterAutoscalingProfile{MaxPodGracePeriodSeconds: 0},
			observed: func() v1beta1.ClusterAutoscaling {
				autoscaling := observedAutoscalingForZeroDesired()
				autoscaling.MaxPodGracePeriod = nil
				return autoscaling
			}(),
			wantMatch:  false,
			wantSubstr: "maxPodGracePeriod",
		},
		{
			name:    "maxNodesTotal mismatch",
			desired: api.ClusterAutoscalingProfile{MaxNodesTotal: 10},
			observed: func() v1beta1.ClusterAutoscaling {
				autoscaling := observedAutoscalingForZeroDesired()
				autoscaling.MaxNodesTotal = ptr.To[int32](5)
				return autoscaling
			}(),
			wantMatch:  false,
			wantSubstr: "maxNodesTotal",
		},
		{
			name:    "maxNodesTotal desired nonzero observed nil",
			desired: api.ClusterAutoscalingProfile{MaxNodesTotal: 10},
			observed: observedAutoscalingForZeroDesired(),
			wantMatch:  false,
			wantSubstr: "maxNodesTotal",
		},
		{
			name:    "maxNodeProvisionTime equivalent duration match",
			desired: api.ClusterAutoscalingProfile{MaxNodeProvisionTimeSeconds: 900},
			observed: func() v1beta1.ClusterAutoscaling {
				autoscaling := observedAutoscalingForZeroDesired()
				autoscaling.MaxNodeProvisionTime = "900s"
				return autoscaling
			}(),
			wantMatch: true,
		},
		{
			name:    "maxNodeProvisionTime invalid duration",
			desired: api.ClusterAutoscalingProfile{MaxNodeProvisionTimeSeconds: 900},
			observed: func() v1beta1.ClusterAutoscaling {
				autoscaling := observedAutoscalingForZeroDesired()
				autoscaling.MaxNodeProvisionTime = "not-a-duration"
				return autoscaling
			}(),
			wantMatch:  false,
			wantSubstr: "not a valid duration",
		},
		{
			name:    "maxPodGracePeriod mismatch",
			desired: api.ClusterAutoscalingProfile{MaxPodGracePeriodSeconds: 60},
			observed: func() v1beta1.ClusterAutoscaling {
				autoscaling := observedAutoscalingForZeroDesired()
				autoscaling.MaxPodGracePeriod = ptr.To[int32](30)
				return autoscaling
			}(),
			wantMatch:  false,
			wantSubstr: "maxPodGracePeriod",
		},
		{
			name:    "podPriorityThreshold mismatch",
			desired: api.ClusterAutoscalingProfile{PodPriorityThreshold: -10},
			observed: func() v1beta1.ClusterAutoscaling {
				autoscaling := observedAutoscalingForZeroDesired()
				autoscaling.PodPriorityThreshold = ptr.To[int32](0)
				return autoscaling
			}(),
			wantMatch:  false,
			wantSubstr: "podPriorityThreshold",
		},
		{
			name:    "all match",
			desired: api.ClusterAutoscalingProfile{MaxNodesTotal: 10, MaxPodGracePeriodSeconds: 60, PodPriorityThreshold: -5},
			observed: v1beta1.ClusterAutoscaling{
				MaxNodesTotal:        ptr.To[int32](10),
				MaxPodGracePeriod:    ptr.To[int32](60),
				MaxNodeProvisionTime: "0m",
				PodPriorityThreshold: ptr.To[int32](-5),
			},
			wantMatch: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			match, msg := controller.autoscalingSpecMatchesDesired(tt.desired, tt.observed)
			assert.Equal(t, tt.wantMatch, match)
			if tt.wantSubstr != "" {
				assert.Contains(t, msg, tt.wantSubstr)
			}
		})
	}
}

func TestImageContentSourcesMatchDesired(t *testing.T) {
	t.Parallel()

	controller := &operationClusterUpdate{}

	tests := []struct {
		name       string
		desired    []api.ImageDigestMirror
		observed   []v1beta1.ImageContentSource
		wantMatch  bool
		wantSubstr string
	}{
		{
			name:      "both nil",
			desired:   nil,
			observed:  nil,
			wantMatch: true,
		},
		{
			name:      "both empty",
			desired:   []api.ImageDigestMirror{},
			observed:  []v1beta1.ImageContentSource{},
			wantMatch: true,
		},
		{
			name: "length mismatch",
			desired: []api.ImageDigestMirror{
				{Source: "a"},
			},
			observed:   nil,
			wantMatch:  false,
			wantSubstr: "imageContentSources",
		},
		{
			name: "source mismatch",
			desired: []api.ImageDigestMirror{
				{Source: "a", Mirrors: []string{"m1"}},
			},
			observed: []v1beta1.ImageContentSource{
				{Source: "b", Mirrors: []string{"m1"}},
			},
			wantMatch:  false,
			wantSubstr: "source",
		},
		{
			name: "mirrors mismatch",
			desired: []api.ImageDigestMirror{
				{Source: "a", Mirrors: []string{"m1", "m2"}},
			},
			observed: []v1beta1.ImageContentSource{
				{Source: "a", Mirrors: []string{"m1", "m3"}},
			},
			wantMatch:  false,
			wantSubstr: "mirrors",
		},
		{
			name: "full match",
			desired: []api.ImageDigestMirror{
				{Source: "a", Mirrors: []string{"m1"}},
				{Source: "b", Mirrors: []string{"m2", "m3"}},
			},
			observed: []v1beta1.ImageContentSource{
				{Source: "a", Mirrors: []string{"m1"}},
				{Source: "b", Mirrors: []string{"m2", "m3"}},
			},
			wantMatch: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			match, msg := controller.imageContentSourcesMatchDesired(tt.desired, tt.observed)
			assert.Equal(t, tt.wantMatch, match)
			if tt.wantSubstr != "" {
				assert.Contains(t, msg, tt.wantSubstr)
			}
		})
	}
}
