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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"
	"github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplier"
	internallistertesting "github.com/Azure/ARO-HCP/internal/database/listertesting"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestHypershiftClusterOperationState(t *testing.T) {
	t.Parallel()

	// From platformImageContentSources in operation_cluster_update_state_calculation.go.
	testClusterUpdatePlatformImageContentSource := "quay.io/openshift-release-dev/ocp-release"

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
					Spec: testClusterUpdateMatchingHostedClusterSpec(),
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
					Spec: func() v1beta1.HostedClusterSpec {
						spec := testClusterUpdateMatchingHostedClusterSpec()
						spec.Autoscaling.MaxNodesTotal = ptr.To[int32](5)
						return spec
					}(),
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
					Spec: func() v1beta1.HostedClusterSpec {
						spec := testClusterUpdateMatchingHostedClusterSpec()
						spec.Autoscaling = v1beta1.ClusterAutoscaling{
							MaxNodesTotal:        ptr.To[int32](10),
							MaxPodGracePeriod:    ptr.To[int32](60),
							MaxNodeProvisionTime: "0m",
							PodPriorityThreshold: ptr.To[int32](-5),
						}
						return spec
					}(),
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
					Spec: func() v1beta1.HostedClusterSpec {
						spec := testClusterUpdateMatchingHostedClusterSpec()
						spec.Autoscaling.MaxNodeProvisionTime = "10m"
						return spec
					}(),
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
					Spec: func() v1beta1.HostedClusterSpec {
						spec := testClusterUpdateMatchingHostedClusterSpec()
						spec.Autoscaling.MaxNodeProvisionTime = "15m"
						return spec
					}(),
				}),
			},
			wantState: arm.ProvisioningStateSucceeded,
		},
		{
			name: "imageContentSources missing desired source returns Updating",
			cluster: func() *api.HCPOpenShiftCluster {
				c := fixture.newCluster(nil)
				c.CustomerProperties.ImageDigestMirrors = []api.ImageDigestMirror{
					{Source: "quay.io/foo", Mirrors: []string{"mirror.io/foo"}},
				}
				return c
			}(),
			readDesires: []*kubeapplier.ReadDesire{
				newHostedClusterReadDesire(t, &v1beta1.HostedCluster{
					Spec: testClusterUpdateMatchingHostedClusterSpec(),
				}),
			},
			wantState:         arm.ProvisioningStateUpdating,
			wantMessageSubstr: "missing source",
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
					Spec: func() v1beta1.HostedClusterSpec {
						spec := testClusterUpdateMatchingHostedClusterSpec()
						spec.ImageContentSources = []v1beta1.ImageContentSource{
							{Source: "quay.io/bar", Mirrors: []string{"mirror.io/foo"}},
						}
						return spec
					}(),
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
					Spec: func() v1beta1.HostedClusterSpec {
						spec := testClusterUpdateMatchingHostedClusterSpec()
						spec.ImageContentSources = []v1beta1.ImageContentSource{
							{Source: "quay.io/foo", Mirrors: []string{"mirror.io/foo", "mirror2.io/foo"}},
						}
						return spec
					}(),
				}),
			},
			wantState: arm.ProvisioningStateSucceeded,
		},
		{
			name: "imageContentSources unset with stale customer source returns Updating",
			cluster: func() *api.HCPOpenShiftCluster {
				c := fixture.newCluster(nil)
				c.CustomerProperties.ImageDigestMirrors = nil
				return c
			}(),
			readDesires: []*kubeapplier.ReadDesire{
				newHostedClusterReadDesire(t, &v1beta1.HostedCluster{
					Spec: func() v1beta1.HostedClusterSpec {
						spec := testClusterUpdateMatchingHostedClusterSpec()
						spec.ImageContentSources = []v1beta1.ImageContentSource{
							{Source: testClusterUpdatePlatformImageContentSource, Mirrors: []string{"platform-mirror"}},
							{Source: "quay.io/foo", Mirrors: []string{"mirror.io/foo"}},
						}
						return spec
					}(),
				}),
			},
			wantState:         arm.ProvisioningStateUpdating,
			wantMessageSubstr: "unexpected imageContentSource",
		},
		{
			name: "allowedCIDRBlocks mismatch returns Updating",
			cluster: func() *api.HCPOpenShiftCluster {
				c := fixture.newCluster(nil)
				c.CustomerProperties.API.AuthorizedCIDRs = []string{"10.0.0.0/8"}
				return c
			}(),
			readDesires: []*kubeapplier.ReadDesire{
				newHostedClusterReadDesire(t, &v1beta1.HostedCluster{
					Spec: v1beta1.HostedClusterSpec{
						Networking: v1beta1.ClusterNetworking{
							APIServer: &v1beta1.APIServerNetworking{
								AllowedCIDRBlocks: []v1beta1.CIDRBlock{"192.168.0.0/16"},
							},
						},
					},
				}),
			},
			wantState:         arm.ProvisioningStateUpdating,
			wantMessageSubstr: "missing",
		},
		{
			name: "allowedCIDRBlocks match with internal extras returns Succeeded",
			cluster: func() *api.HCPOpenShiftCluster {
				c := fixture.newCluster(nil)
				c.CustomerProperties.API.AuthorizedCIDRs = []string{"10.0.0.0/8"}
				return c
			}(),
			readDesires: []*kubeapplier.ReadDesire{
				newHostedClusterReadDesire(t, &v1beta1.HostedCluster{
					Spec: func() v1beta1.HostedClusterSpec {
						spec := testClusterUpdateMatchingHostedClusterSpec()
						spec.Networking = v1beta1.ClusterNetworking{
							APIServer: &v1beta1.APIServerNetworking{
								AllowedCIDRBlocks: []v1beta1.CIDRBlock{
									"10.0.0.0/8",
									"172.16.0.0/32",
								},
							},
						}
						return spec
					}(),
				}),
			},
			wantState: arm.ProvisioningStateSucceeded,
		},
		{
			name: "allowedCIDRBlocks match returns Succeeded",
			cluster: func() *api.HCPOpenShiftCluster {
				c := fixture.newCluster(nil)
				c.CustomerProperties.API.AuthorizedCIDRs = []string{"10.0.0.0/8", "192.168.0.0/16"}
				return c
			}(),
			readDesires: []*kubeapplier.ReadDesire{
				newHostedClusterReadDesire(t, &v1beta1.HostedCluster{
					Spec: func() v1beta1.HostedClusterSpec {
						spec := testClusterUpdateMatchingHostedClusterSpec()
						spec.Networking = v1beta1.ClusterNetworking{
							APIServer: &v1beta1.APIServerNetworking{
								AllowedCIDRBlocks: []v1beta1.CIDRBlock{"192.168.0.0/16", "10.0.0.0/8"},
							},
						}
						return spec
					}(),
				}),
			},
			wantState: arm.ProvisioningStateSucceeded,
		},
		{
			name: "nil authorizedCIDRs with observed blocks returns Updating",
			cluster: func() *api.HCPOpenShiftCluster {
				c := fixture.newCluster(nil)
				c.CustomerProperties.API.AuthorizedCIDRs = nil
				return c
			}(),
			readDesires: []*kubeapplier.ReadDesire{
				newHostedClusterReadDesire(t, &v1beta1.HostedCluster{
					Spec: v1beta1.HostedClusterSpec{
						Networking: v1beta1.ClusterNetworking{
							APIServer: &v1beta1.APIServerNetworking{
								AllowedCIDRBlocks: []v1beta1.CIDRBlock{"10.0.0.0/8"},
							},
						},
					},
				}),
			},
			wantState:         arm.ProvisioningStateUpdating,
			wantMessageSubstr: "unset",
		},
		{
			name: "imageContentSources with extra hypershift sources returns Succeeded",
			cluster: func() *api.HCPOpenShiftCluster {
				c := fixture.newCluster(nil)
				c.CustomerProperties.ImageDigestMirrors = []api.ImageDigestMirror{
					{Source: "quay.io/foo", Mirrors: []string{"mirror.io/foo"}},
				}
				return c
			}(),
			readDesires: []*kubeapplier.ReadDesire{
				newHostedClusterReadDesire(t, &v1beta1.HostedCluster{
					Spec: func() v1beta1.HostedClusterSpec {
						spec := testClusterUpdateMatchingHostedClusterSpec()
						spec.ImageContentSources = []v1beta1.ImageContentSource{
							{Source: testClusterUpdatePlatformImageContentSource, Mirrors: []string{"internal-mirror.example.io"}},
							{Source: "quay.io/foo", Mirrors: []string{"mirror.io/foo"}},
						}
						return spec
					}(),
				}),
			},
			wantState: arm.ProvisioningStateSucceeded,
		},
		{
			name: "single replica availability policies match returns Succeeded",
			cluster: func() *api.HCPOpenShiftCluster {
				c := fixture.newCluster(nil)
				c.ServiceProviderProperties.ExperimentalFeatures.ControlPlaneAvailability = api.SingleReplicaControlPlane
				return c
			}(),
			readDesires: []*kubeapplier.ReadDesire{
				newHostedClusterReadDesire(t, &v1beta1.HostedCluster{
					Spec: func() v1beta1.HostedClusterSpec {
						spec := testClusterUpdateMatchingHostedClusterSpec()
						spec.ControllerAvailabilityPolicy = v1beta1.SingleReplica
						spec.InfrastructureAvailabilityPolicy = v1beta1.SingleReplica
						return spec
					}(),
				}),
			},
			wantState: arm.ProvisioningStateSucceeded,
		},
		{
			name: "single replica controller policy mismatch returns Updating",
			cluster: func() *api.HCPOpenShiftCluster {
				c := fixture.newCluster(nil)
				c.ServiceProviderProperties.ExperimentalFeatures.ControlPlaneAvailability = api.SingleReplicaControlPlane
				return c
			}(),
			readDesires: []*kubeapplier.ReadDesire{
				newHostedClusterReadDesire(t, &v1beta1.HostedCluster{
					Spec: func() v1beta1.HostedClusterSpec {
						spec := testClusterUpdateMatchingHostedClusterSpec()
						spec.ControllerAvailabilityPolicy = ""
						spec.InfrastructureAvailabilityPolicy = v1beta1.SingleReplica
						return spec
					}(),
				}),
			},
			wantState:         arm.ProvisioningStateUpdating,
			wantMessageSubstr: "controllerAvailabilityPolicy",
		},
		{
			name: "single replica infrastructure policy mismatch returns Updating",
			cluster: func() *api.HCPOpenShiftCluster {
				c := fixture.newCluster(nil)
				c.ServiceProviderProperties.ExperimentalFeatures.ControlPlaneAvailability = api.SingleReplicaControlPlane
				return c
			}(),
			readDesires: []*kubeapplier.ReadDesire{
				newHostedClusterReadDesire(t, &v1beta1.HostedCluster{
					Spec: func() v1beta1.HostedClusterSpec {
						spec := testClusterUpdateMatchingHostedClusterSpec()
						spec.ControllerAvailabilityPolicy = v1beta1.SingleReplica
						spec.InfrastructureAvailabilityPolicy = v1beta1.HighlyAvailable
						return spec
					}(),
				}),
			},
			wantState:         arm.ProvisioningStateUpdating,
			wantMessageSubstr: "infrastructureAvailabilityPolicy",
		},
		{
			name: "default availability policies match highly available returns Succeeded",
			cluster: func() *api.HCPOpenShiftCluster {
				c := fixture.newCluster(nil)
				return c
			}(),
			readDesires: []*kubeapplier.ReadDesire{
				newHostedClusterReadDesire(t, &v1beta1.HostedCluster{
					Spec: testClusterUpdateMatchingHostedClusterSpec(),
				}),
			},
			wantState: arm.ProvisioningStateSucceeded,
		},
		{
			name: "default availability policies mismatch returns Updating",
			cluster: func() *api.HCPOpenShiftCluster {
				c := fixture.newCluster(nil)
				return c
			}(),
			readDesires: []*kubeapplier.ReadDesire{
				newHostedClusterReadDesire(t, &v1beta1.HostedCluster{
					Spec: func() v1beta1.HostedClusterSpec {
						spec := testClusterUpdateMatchingHostedClusterSpec()
						spec.ControllerAvailabilityPolicy = v1beta1.SingleReplica
						spec.InfrastructureAvailabilityPolicy = v1beta1.SingleReplica
						return spec
					}(),
				}),
			},
			wantState:         arm.ProvisioningStateUpdating,
			wantMessageSubstr: "controllerAvailabilityPolicy",
		},
		{
			name: "minimal pod sizing annotation match returns Succeeded",
			cluster: func() *api.HCPOpenShiftCluster {
				c := fixture.newCluster(nil)
				c.ServiceProviderProperties.ExperimentalFeatures.ControlPlanePodSizing = api.MinimalControlPlanePodSizing
				return c
			}(),
			readDesires: []*kubeapplier.ReadDesire{
				newHostedClusterReadDesire(t, &v1beta1.HostedCluster{
					ObjectMeta: metav1.ObjectMeta{
						Annotations: map[string]string{
							v1beta1.ClusterSizeOverrideAnnotation: "e2e_minimal",
						},
					},
					Spec: testClusterUpdateMatchingHostedClusterSpec(),
				}),
			},
			wantState: arm.ProvisioningStateSucceeded,
		},
		{
			name: "minimal pod sizing missing annotation returns Updating",
			cluster: func() *api.HCPOpenShiftCluster {
				c := fixture.newCluster(nil)
				c.ServiceProviderProperties.ExperimentalFeatures.ControlPlanePodSizing = api.MinimalControlPlanePodSizing
				return c
			}(),
			readDesires: []*kubeapplier.ReadDesire{
				newHostedClusterReadDesire(t, &v1beta1.HostedCluster{
					Spec: testClusterUpdateMatchingHostedClusterSpec(),
				}),
			},
			wantState:         arm.ProvisioningStateUpdating,
			wantMessageSubstr: "cluster-size-override",
		},
		{
			name: "default pod sizing with stale annotation returns Updating",
			cluster: func() *api.HCPOpenShiftCluster {
				c := fixture.newCluster(nil)
				return c
			}(),
			readDesires: []*kubeapplier.ReadDesire{
				newHostedClusterReadDesire(t, &v1beta1.HostedCluster{
					ObjectMeta: metav1.ObjectMeta{
						Annotations: map[string]string{
							v1beta1.ClusterSizeOverrideAnnotation: "e2e_minimal",
						},
					},
					Spec: testClusterUpdateMatchingHostedClusterSpec(),
				}),
			},
			wantState:         arm.ProvisioningStateUpdating,
			wantMessageSubstr: "cluster-size-override",
		},
		{
			name: "control plane operator image annotation match returns Succeeded",
			cluster: func() *api.HCPOpenShiftCluster {
				c := fixture.newCluster(nil)
				c.ServiceProviderProperties.ExperimentalFeatures.ControlPlaneOperatorImage = "quay.io/openshift/cpo:test"
				return c
			}(),
			readDesires: []*kubeapplier.ReadDesire{
				newHostedClusterReadDesire(t, &v1beta1.HostedCluster{
					ObjectMeta: metav1.ObjectMeta{
						Annotations: map[string]string{
							v1beta1.ControlPlaneOperatorImageAnnotation: "quay.io/openshift/cpo:test",
						},
					},
					Spec: testClusterUpdateMatchingHostedClusterSpec(),
				}),
			},
			wantState: arm.ProvisioningStateSucceeded,
		},
		{
			name: "control plane operator image missing annotation returns Updating",
			cluster: func() *api.HCPOpenShiftCluster {
				c := fixture.newCluster(nil)
				c.ServiceProviderProperties.ExperimentalFeatures.ControlPlaneOperatorImage = "quay.io/openshift/cpo:test"
				return c
			}(),
			readDesires: []*kubeapplier.ReadDesire{
				newHostedClusterReadDesire(t, &v1beta1.HostedCluster{
					Spec: testClusterUpdateMatchingHostedClusterSpec(),
				}),
			},
			wantState:         arm.ProvisioningStateUpdating,
			wantMessageSubstr: "control-plane-operator-image",
		},
		{
			name: "default control plane operator image with stale annotation returns Updating",
			cluster: func() *api.HCPOpenShiftCluster {
				c := fixture.newCluster(nil)
				return c
			}(),
			readDesires: []*kubeapplier.ReadDesire{
				newHostedClusterReadDesire(t, &v1beta1.HostedCluster{
					ObjectMeta: metav1.ObjectMeta{
						Annotations: map[string]string{
							v1beta1.ControlPlaneOperatorImageAnnotation: "quay.io/openshift/cpo:test",
						},
					},
					Spec: testClusterUpdateMatchingHostedClusterSpec(),
				}),
			},
			wantState:         arm.ProvisioningStateUpdating,
			wantMessageSubstr: "control-plane-operator-image",
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
			assert.Equal(t, tt.wantState, state.ProvisioningState)
			if tt.wantMessageSubstr != "" {
				assert.Contains(t, state.Message, tt.wantMessageSubstr)
			}
		})
	}
}

func TestAllowedCIDRBlocksMatchDesired(t *testing.T) {
	t.Parallel()

	controller := &operationClusterUpdate{}

	tests := []struct {
		name       string
		desired    []string
		observed   v1beta1.HostedClusterSpec
		wantMatch  bool
		wantSubstr string
	}{
		{
			name:    "nil desired and no observed blocks",
			desired: nil,
			observed: v1beta1.HostedClusterSpec{
				Networking: v1beta1.ClusterNetworking{},
			},
			wantMatch: true,
		},
		{
			name:    "nil desired with observed blocks",
			desired: nil,
			observed: v1beta1.HostedClusterSpec{
				Networking: v1beta1.ClusterNetworking{
					APIServer: &v1beta1.APIServerNetworking{
						AllowedCIDRBlocks: []v1beta1.CIDRBlock{"10.0.0.0/8"},
					},
				},
			},
			wantMatch:  false,
			wantSubstr: "unset",
		},
		{
			name:    "desired blocks missing on observed",
			desired: []string{"10.0.0.0/8"},
			observed: v1beta1.HostedClusterSpec{
				Networking: v1beta1.ClusterNetworking{},
			},
			wantMatch:  false,
			wantSubstr: "unset",
		},
		{
			name:    "desired blocks missing with internal extras present",
			desired: []string{"10.0.0.0/8"},
			observed: v1beta1.HostedClusterSpec{
				Networking: v1beta1.ClusterNetworking{
					APIServer: &v1beta1.APIServerNetworking{
						AllowedCIDRBlocks: []v1beta1.CIDRBlock{"172.16.0.0/32"},
					},
				},
			},
			wantMatch:  false,
			wantSubstr: "missing",
		},
		{
			name:    "desired blocks mismatch",
			desired: []string{"10.0.0.0/8"},
			observed: v1beta1.HostedClusterSpec{
				Networking: v1beta1.ClusterNetworking{
					APIServer: &v1beta1.APIServerNetworking{
						AllowedCIDRBlocks: []v1beta1.CIDRBlock{"192.168.0.0/16"},
					},
				},
			},
			wantMatch:  false,
			wantSubstr: "missing",
		},
		{
			name:    "desired subset present with internal extras",
			desired: []string{"10.0.0.0/8", "192.168.0.0/16"},
			observed: v1beta1.HostedClusterSpec{
				Networking: v1beta1.ClusterNetworking{
					APIServer: &v1beta1.APIServerNetworking{
						AllowedCIDRBlocks: []v1beta1.CIDRBlock{
							"192.168.0.0/16",
							"10.0.0.0/8",
							"172.16.0.0/32",
						},
					},
				},
			},
			wantMatch: true,
		},
		{
			name:    "full match regardless of order",
			desired: []string{"10.0.0.0/8", "192.168.0.0/16"},
			observed: v1beta1.HostedClusterSpec{
				Networking: v1beta1.ClusterNetworking{
					APIServer: &v1beta1.APIServerNetworking{
						AllowedCIDRBlocks: []v1beta1.CIDRBlock{"192.168.0.0/16", "10.0.0.0/8"},
					},
				},
			},
			wantMatch: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			match, msg := controller.allowedCIDRBlocksMatchDesired(tt.desired, tt.observed)
			assert.Equal(t, tt.wantMatch, match)
			if tt.wantSubstr != "" {
				assert.Contains(t, msg, tt.wantSubstr)
			}
		})
	}
}

func TestAvailabilityPoliciesMatchDesired(t *testing.T) {
	t.Parallel()

	controller := &operationClusterUpdate{}

	tests := []struct {
		name       string
		desired    api.ControlPlaneAvailability
		observed   v1beta1.HostedClusterSpec
		wantMatch  bool
		wantSubstr string
	}{
		{
			name:       "default desired rejects unset policies",
			desired:    api.DefaultControlPlaneAvailability,
			observed:   v1beta1.HostedClusterSpec{},
			wantMatch:  false,
			wantSubstr: "controllerAvailabilityPolicy",
		},
		{
			name:    "default desired matches highly available policies",
			desired: api.DefaultControlPlaneAvailability,
			observed: v1beta1.HostedClusterSpec{
				ControllerAvailabilityPolicy:     v1beta1.HighlyAvailable,
				InfrastructureAvailabilityPolicy: v1beta1.HighlyAvailable,
			},
			wantMatch: true,
		},
		{
			name:    "default desired rejects single replica controller",
			desired: api.DefaultControlPlaneAvailability,
			observed: v1beta1.HostedClusterSpec{
				ControllerAvailabilityPolicy:     v1beta1.SingleReplica,
				InfrastructureAvailabilityPolicy: v1beta1.SingleReplica,
			},
			wantMatch:  false,
			wantSubstr: "controllerAvailabilityPolicy",
		},
		{
			name:    "single replica desired matches both policies",
			desired: api.SingleReplicaControlPlane,
			observed: v1beta1.HostedClusterSpec{
				ControllerAvailabilityPolicy:     v1beta1.SingleReplica,
				InfrastructureAvailabilityPolicy: v1beta1.SingleReplica,
			},
			wantMatch: true,
		},
		{
			name:    "single replica desired rejects unset controller policy",
			desired: api.SingleReplicaControlPlane,
			observed: v1beta1.HostedClusterSpec{
				InfrastructureAvailabilityPolicy: v1beta1.SingleReplica,
			},
			wantMatch:  false,
			wantSubstr: "controllerAvailabilityPolicy",
		},
		{
			name:    "single replica desired rejects highly available infrastructure",
			desired: api.SingleReplicaControlPlane,
			observed: v1beta1.HostedClusterSpec{
				ControllerAvailabilityPolicy:     v1beta1.SingleReplica,
				InfrastructureAvailabilityPolicy: v1beta1.HighlyAvailable,
			},
			wantMatch:  false,
			wantSubstr: "infrastructureAvailabilityPolicy",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			match, msg := controller.availabilityPoliciesMatchDesired(tt.desired, tt.observed)
			assert.Equal(t, tt.wantMatch, match)
			if tt.wantSubstr != "" {
				assert.Contains(t, msg, tt.wantSubstr)
			}
		})
	}
}

func TestClusterSizeOverrideAnnotationMatchesDesired(t *testing.T) {
	t.Parallel()

	controller := &operationClusterUpdate{}

	tests := []struct {
		name       string
		desired    api.ControlPlanePodSizing
		observed   map[string]string
		wantMatch  bool
		wantSubstr string
	}{
		{
			name:      "default desired with no annotations",
			desired:   api.DefaultControlPlanePodSizing,
			observed:  nil,
			wantMatch: true,
		},
		{
			name:    "default desired rejects stale annotation",
			desired: api.DefaultControlPlanePodSizing,
			observed: map[string]string{
				v1beta1.ClusterSizeOverrideAnnotation: "e2e_minimal",
			},
			wantMatch:  false,
			wantSubstr: "cluster-size-override",
		},
		{
			name:    "minimal desired matches annotation",
			desired: api.MinimalControlPlanePodSizing,
			observed: map[string]string{
				v1beta1.ClusterSizeOverrideAnnotation: "e2e_minimal",
			},
			wantMatch: true,
		},
		{
			name:       "minimal desired rejects missing annotation",
			desired:    api.MinimalControlPlanePodSizing,
			observed:   nil,
			wantMatch:  false,
			wantSubstr: "unset",
		},
		{
			name:    "minimal desired rejects wrong annotation value",
			desired: api.MinimalControlPlanePodSizing,
			observed: map[string]string{
				v1beta1.ClusterSizeOverrideAnnotation: "small",
			},
			wantMatch:  false,
			wantSubstr: "e2e_minimal",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			match, msg := controller.clusterSizeOverrideAnnotationMatchesDesired(tt.desired, tt.observed)
			assert.Equal(t, tt.wantMatch, match)
			if tt.wantSubstr != "" {
				assert.Contains(t, msg, tt.wantSubstr)
			}
		})
	}
}

func TestControlPlaneOperatorImageAnnotationMatchesDesired(t *testing.T) {
	t.Parallel()

	controller := &operationClusterUpdate{}

	tests := []struct {
		name       string
		desired    string
		observed   map[string]string
		wantMatch  bool
		wantSubstr string
	}{
		{
			name:      "default desired with no annotations",
			desired:   "",
			observed:  nil,
			wantMatch: true,
		},
		{
			name:    "default desired rejects stale annotation",
			desired: "",
			observed: map[string]string{
				v1beta1.ControlPlaneOperatorImageAnnotation: "quay.io/openshift/cpo:test",
			},
			wantMatch:  false,
			wantSubstr: "control-plane-operator-image",
		},
		{
			name:    "desired image matches annotation",
			desired: "quay.io/openshift/cpo:test",
			observed: map[string]string{
				v1beta1.ControlPlaneOperatorImageAnnotation: "quay.io/openshift/cpo:test",
			},
			wantMatch: true,
		},
		{
			name:       "desired image rejects missing annotation",
			desired:    "quay.io/openshift/cpo:test",
			observed:   nil,
			wantMatch:  false,
			wantSubstr: "unset",
		},
		{
			name:    "desired image rejects wrong annotation value",
			desired: "quay.io/openshift/cpo:test",
			observed: map[string]string{
				v1beta1.ControlPlaneOperatorImageAnnotation: "quay.io/openshift/cpo:other",
			},
			wantMatch:  false,
			wantSubstr: "quay.io/openshift/cpo:test",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			match, msg := controller.controlPlaneOperatorImageAnnotationMatchesDesired(tt.desired, tt.observed)
			assert.Equal(t, tt.wantMatch, match)
			if tt.wantSubstr != "" {
				assert.Contains(t, msg, tt.wantSubstr)
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
			name:      "maxNodesTotal zero exact match",
			desired:   api.ClusterAutoscalingProfile{},
			observed:  testClusterUpdateMatchingHostedClusterSpec().Autoscaling,
			wantMatch: true,
		},
		{
			name:    "maxNodesTotal desired zero observed nil",
			desired: api.ClusterAutoscalingProfile{},
			observed: func() v1beta1.ClusterAutoscaling {
				autoscaling := testClusterUpdateMatchingHostedClusterSpec().Autoscaling
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
				autoscaling := testClusterUpdateMatchingHostedClusterSpec().Autoscaling
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
				autoscaling := testClusterUpdateMatchingHostedClusterSpec().Autoscaling
				autoscaling.MaxNodesTotal = ptr.To[int32](5)
				return autoscaling
			}(),
			wantMatch:  false,
			wantSubstr: "maxNodesTotal",
		},
		{
			name:       "maxNodesTotal desired nonzero observed nil",
			desired:    api.ClusterAutoscalingProfile{MaxNodesTotal: 10},
			observed:   testClusterUpdateMatchingHostedClusterSpec().Autoscaling,
			wantMatch:  false,
			wantSubstr: "maxNodesTotal",
		},
		{
			name:    "maxNodeProvisionTime equivalent duration match",
			desired: api.ClusterAutoscalingProfile{MaxNodeProvisionTimeSeconds: 900},
			observed: func() v1beta1.ClusterAutoscaling {
				autoscaling := testClusterUpdateMatchingHostedClusterSpec().Autoscaling
				autoscaling.MaxNodeProvisionTime = "900s"
				return autoscaling
			}(),
			wantMatch: true,
		},
		{
			name:    "maxNodeProvisionTime invalid duration",
			desired: api.ClusterAutoscalingProfile{MaxNodeProvisionTimeSeconds: 900},
			observed: func() v1beta1.ClusterAutoscaling {
				autoscaling := testClusterUpdateMatchingHostedClusterSpec().Autoscaling
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
				autoscaling := testClusterUpdateMatchingHostedClusterSpec().Autoscaling
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
				autoscaling := testClusterUpdateMatchingHostedClusterSpec().Autoscaling
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

	// From platformImageContentSources in operation_cluster_update_state_calculation.go.
	testClusterUpdatePlatformImageContentSource := "quay.io/openshift-release-dev/ocp-release"

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
			name: "missing desired source",
			desired: []api.ImageDigestMirror{
				{Source: "a"},
			},
			observed:   nil,
			wantMatch:  false,
			wantSubstr: "missing source",
		},
		{
			name: "extra platform observed sources",
			desired: []api.ImageDigestMirror{
				{Source: "a", Mirrors: []string{"m1"}},
			},
			observed: []v1beta1.ImageContentSource{
				{Source: testClusterUpdatePlatformImageContentSource, Mirrors: []string{"platform-mirror"}},
				{Source: "a", Mirrors: []string{"m1"}},
			},
			wantMatch: true,
		},
		{
			name:      "desired empty observed has only platform sources",
			desired:   []api.ImageDigestMirror{},
			observed:  []v1beta1.ImageContentSource{{Source: testClusterUpdatePlatformImageContentSource, Mirrors: []string{"platform-mirror"}}},
			wantMatch: true,
		},
		{
			name:    "desired empty observed has stale customer source",
			desired: []api.ImageDigestMirror{},
			observed: []v1beta1.ImageContentSource{
				{Source: testClusterUpdatePlatformImageContentSource, Mirrors: []string{"platform-mirror"}},
				{Source: "quay.io/foo", Mirrors: []string{"mirror.io/foo"}},
			},
			wantMatch:  false,
			wantSubstr: "unexpected imageContentSource",
		},
		{
			name: "unset customer source still present",
			desired: []api.ImageDigestMirror{
				{Source: "quay.io/foo", Mirrors: []string{"mirror.io/foo"}},
			},
			observed: []v1beta1.ImageContentSource{
				{Source: testClusterUpdatePlatformImageContentSource, Mirrors: []string{"platform-mirror"}},
				{Source: "quay.io/foo", Mirrors: []string{"mirror.io/foo"}},
				{Source: "quay.io/bar", Mirrors: []string{"mirror.io/bar"}},
			},
			wantMatch:  false,
			wantSubstr: "unexpected imageContentSource",
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
		{
			name: "full match regardless of order",
			desired: []api.ImageDigestMirror{
				{Source: "a", Mirrors: []string{"m1"}},
				{Source: "b", Mirrors: []string{"m2", "m3"}},
			},
			observed: []v1beta1.ImageContentSource{
				{Source: testClusterUpdatePlatformImageContentSource, Mirrors: []string{"platform-mirror"}},
				{Source: "b", Mirrors: []string{"m2", "m3"}},
				{Source: "a", Mirrors: []string{"m1"}},
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

func TestClusterServiceClusterOperationState(t *testing.T) {
	t.Parallel()

	controller := &operationClusterUpdate{}

	tests := []struct {
		name              string
		cluster           *api.HCPOpenShiftCluster
		csCluster         *arohcpv1alpha1.Cluster
		wantState         arm.ProvisioningState
		wantMessageSubstr string
	}{
		{
			name: "matching node drain timeout returns Succeeded",
			cluster: &api.HCPOpenShiftCluster{
				CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
					NodeDrainTimeoutMinutes: 30,
				},
			},
			csCluster: testCSClusterWithNodeDrainTimeoutAndAllowAll(t, 30),
			wantState: arm.ProvisioningStateSucceeded,
		},
		{
			name: "zero desired with unset CS value returns Succeeded",
			cluster: &api.HCPOpenShiftCluster{
				CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{},
			},
			csCluster: testCSClusterWithAllowAll(t),
			wantState: arm.ProvisioningStateSucceeded,
		},
		{
			name: "mismatch returns Updating",
			cluster: &api.HCPOpenShiftCluster{
				CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
					NodeDrainTimeoutMinutes: 60,
				},
			},
			csCluster:         testCSClusterWithNodeDrainTimeoutAndAllowAll(t, 30),
			wantState:         arm.ProvisioningStateUpdating,
			wantMessageSubstr: "nodeDrainGracePeriod",
		},
		{
			name: "matching authorized CIDRs returns Succeeded",
			cluster: &api.HCPOpenShiftCluster{
				CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
					API: api.CustomerAPIProfile{
						AuthorizedCIDRs: []string{"10.0.0.0/8", "192.168.0.0/16"},
					},
				},
			},
			csCluster: testCSClusterWithAuthorizedCIDRs(t, "10.0.0.0/8", "192.168.0.0/16"),
			wantState: arm.ProvisioningStateSucceeded,
		},
		{
			name: "nil desired with unset CS CIDR config returns Updating",
			cluster: &api.HCPOpenShiftCluster{
				CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
					API: api.CustomerAPIProfile{},
				},
			},
			csCluster:         testCSCluster(t),
			wantState:         arm.ProvisioningStateUpdating,
			wantMessageSubstr: "want allow_all",
		},
		{
			name: "authorized CIDR mismatch returns Updating",
			cluster: &api.HCPOpenShiftCluster{
				CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
					API: api.CustomerAPIProfile{
						AuthorizedCIDRs: []string{"203.0.113.0/24"},
					},
				},
			},
			csCluster:         testCSClusterWithAuthorizedCIDRs(t, "10.0.0.0/8"),
			wantState:         arm.ProvisioningStateUpdating,
			wantMessageSubstr: "k8sAPIServerAuthorizedCIDRs",
		},
		{
			name: "explicit allow_all CS config with nil desired returns Succeeded",
			cluster: &api.HCPOpenShiftCluster{
				CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
					API: api.CustomerAPIProfile{},
				},
			},
			csCluster: testCSClusterWithCIDRBlockAllowAccess(t, ocm.CSCIDRBlockAllowAccessModeAllowAll),
			wantState: arm.ProvisioningStateSucceeded,
		},
		{
			name: "allow_list CS config with nil desired returns Updating",
			cluster: &api.HCPOpenShiftCluster{
				CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
					API: api.CustomerAPIProfile{},
				},
			},
			csCluster:         testCSClusterWithCIDRBlockAllowAccess(t, ocm.CSCIDRBlockAllowAccessModeAllowList, "10.0.0.0/8"),
			wantState:         arm.ProvisioningStateUpdating,
			wantMessageSubstr: "want allow_all",
		},
		{
			name: "allow_all CS config with desired CIDR list returns Updating",
			cluster: &api.HCPOpenShiftCluster{
				CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
					API: api.CustomerAPIProfile{
						AuthorizedCIDRs: []string{"203.0.113.0/24"},
					},
				},
			},
			csCluster:         testCSClusterWithCIDRBlockAllowAccess(t, ocm.CSCIDRBlockAllowAccessModeAllowAll),
			wantState:         arm.ProvisioningStateUpdating,
			wantMessageSubstr: "want allow_list",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			state, err := controller.clusterServiceClusterSpecOperationState(tt.cluster, tt.csCluster)
			require.NoError(t, err)
			assert.Equal(t, tt.wantState, state.ProvisioningState)
			if tt.wantMessageSubstr != "" {
				assert.Contains(t, state.Message, tt.wantMessageSubstr)
			}
		})
	}
}

func testCSCluster(t *testing.T) *arohcpv1alpha1.Cluster {
	t.Helper()
	cluster, err := arohcpv1alpha1.NewCluster().Build()
	require.NoError(t, err)
	return cluster
}

func testCSClusterWithAllowAll(t *testing.T) *arohcpv1alpha1.Cluster {
	t.Helper()
	return testCSClusterWithCIDRBlockAllowAccess(t, ocm.CSCIDRBlockAllowAccessModeAllowAll)
}

func testCSClusterWithNodeDrainTimeoutAndAllowAll(t *testing.T, minutes int32) *arohcpv1alpha1.Cluster {
	t.Helper()
	allowAccess := arohcpv1alpha1.NewCIDRBlockAllowAccess().Mode(ocm.CSCIDRBlockAllowAccessModeAllowAll)
	cluster, err := arohcpv1alpha1.NewCluster().
		NodeDrainGracePeriod(arohcpv1alpha1.NewValue().
			Unit("minutes").
			Value(float64(minutes))).
		API(arohcpv1alpha1.NewClusterAPI().
			CIDRBlockAccess(arohcpv1alpha1.NewCIDRBlockAccess().
				Allow(allowAccess))).
		Build()
	require.NoError(t, err)
	return cluster
}

func testCSClusterWithAuthorizedCIDRs(t *testing.T, cidrs ...string) *arohcpv1alpha1.Cluster {
	t.Helper()
	return testCSClusterWithCIDRBlockAllowAccess(t, ocm.CSCIDRBlockAllowAccessModeAllowList, cidrs...)
}

func testCSClusterWithCIDRBlockAllowAccess(t *testing.T, mode string, cidrs ...string) *arohcpv1alpha1.Cluster {
	t.Helper()
	allowAccess := arohcpv1alpha1.NewCIDRBlockAllowAccess().Mode(mode)
	if len(cidrs) > 0 {
		allowAccess = allowAccess.Values(cidrs...)
	}
	cluster, err := arohcpv1alpha1.NewCluster().
		API(arohcpv1alpha1.NewClusterAPI().
			CIDRBlockAccess(arohcpv1alpha1.NewCIDRBlockAccess().
				Allow(allowAccess))).
		Build()
	require.NoError(t, err)
	return cluster
}
