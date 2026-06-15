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
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/blang/semver/v4"
	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"
	"github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/backend/pkg/kubeapplierhelpers"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplier"
	internallistertesting "github.com/Azure/ARO-HCP/internal/database/listertesting"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestHypershiftHostedClusterOperationState(t *testing.T) {
	t.Parallel()

	// From platformImageContentSources in operation_cluster_update_state_calculation.go.
	testClusterUpdatePlatformImageContentSource := "quay.io/openshift-release-dev/ocp-release"

	fixture := newClusterTestFixture()
	emptySPC := &api.ServiceProviderCluster{}

	tests := []struct {
		name                   string
		cluster                *api.HCPOpenShiftCluster
		serviceProviderCluster *api.ServiceProviderCluster
		readDesires            []*kubeapplier.ReadDesire
		wantState              arm.ProvisioningState
		wantMessageSubstr      string
	}{
		{
			name:                   "no ReadDesire returns Updating",
			cluster:                fixture.newCluster(nil),
			serviceProviderCluster: emptySPC,
			readDesires:            nil,
			wantState:              arm.ProvisioningStateUpdating,
			wantMessageSubstr:      "Hypershift HostedCluster has not been observed yet",
		},
		{
			name:                   "empty cluster matches empty HostedCluster",
			cluster:                fixture.newCluster(nil),
			serviceProviderCluster: emptySPC,
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
			serviceProviderCluster: emptySPC,
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
			wantMessageSubstr: "hypershift HostedCluster autoscaling maxNodesTotal is 5, want 10",
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
			serviceProviderCluster: emptySPC,
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
			serviceProviderCluster: emptySPC,
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
			wantMessageSubstr: `hypershift HostedCluster autoscaling maxNodeProvisionTime is "10m", want "15m"`,
		},
		{
			name: "maxNodeProvisionTime matches when converted",
			cluster: func() *api.HCPOpenShiftCluster {
				c := fixture.newCluster(nil)
				c.CustomerProperties.Autoscaling.MaxNodeProvisionTimeSeconds = 900
				return c
			}(),
			serviceProviderCluster: emptySPC,
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
			serviceProviderCluster: emptySPC,
			readDesires: []*kubeapplier.ReadDesire{
				newHostedClusterReadDesire(t, &v1beta1.HostedCluster{
					Spec: testClusterUpdateMatchingHostedClusterSpec(),
				}),
			},
			wantState:         arm.ProvisioningStateUpdating,
			wantMessageSubstr: `missing source "quay.io/foo"`,
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
			serviceProviderCluster: emptySPC,
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
			wantMessageSubstr: `missing source "quay.io/foo"`,
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
			serviceProviderCluster: emptySPC,
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
			serviceProviderCluster: emptySPC,
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
			wantMessageSubstr: `unexpected imageContentSource "quay.io/foo"`,
		},
		{
			name: "allowedCIDRBlocks mismatch returns Updating",
			cluster: func() *api.HCPOpenShiftCluster {
				c := fixture.newCluster(nil)
				c.CustomerProperties.API.AuthorizedCIDRs = []string{"10.0.0.0/8"}
				return c
			}(),
			serviceProviderCluster: emptySPC,
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
			wantMessageSubstr: `is missing "10.0.0.0/8", want [10.0.0.0/8]`,
		},
		{
			name: "temporary - allowedCIDRBlocks match with internal extras returns Succeeded",
			cluster: func() *api.HCPOpenShiftCluster {
				c := fixture.newCluster(nil)
				c.CustomerProperties.API.AuthorizedCIDRs = []string{"10.0.0.0/8"}
				return c
			}(),
			serviceProviderCluster: emptySPC,
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
			serviceProviderCluster: emptySPC,
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
			serviceProviderCluster: emptySPC,
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
			wantMessageSubstr: `allowedCIDRBlocks is [10.0.0.0/8], want unset (allow all)`,
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
			serviceProviderCluster: emptySPC,
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
			serviceProviderCluster: emptySPC,
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
			serviceProviderCluster: emptySPC,
			readDesires: []*kubeapplier.ReadDesire{
				newHostedClusterReadDesire(t, &v1beta1.HostedCluster{
					Spec: func() v1beta1.HostedClusterSpec {
						spec := testClusterUpdateMatchingHostedClusterSpec()
						spec.ControllerAvailabilityPolicy = v1beta1.HighlyAvailable
						spec.InfrastructureAvailabilityPolicy = v1beta1.SingleReplica
						return spec
					}(),
				}),
			},
			wantState:         arm.ProvisioningStateUpdating,
			wantMessageSubstr: `controllerAvailabilityPolicy is "HighlyAvailable", want "SingleReplica"`,
		},
		{
			name: "single replica infrastructure policy mismatch returns Updating",
			cluster: func() *api.HCPOpenShiftCluster {
				c := fixture.newCluster(nil)
				c.ServiceProviderProperties.ExperimentalFeatures.ControlPlaneAvailability = api.SingleReplicaControlPlane
				return c
			}(),
			serviceProviderCluster: emptySPC,
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
			wantMessageSubstr: `infrastructureAvailabilityPolicy is "HighlyAvailable", want "SingleReplica"`,
		},
		{
			name: "default availability policies (highly available) match highly available hypershift side returns Succeeded",
			cluster: func() *api.HCPOpenShiftCluster {
				c := fixture.newCluster(nil)
				return c
			}(),
			serviceProviderCluster: emptySPC,
			readDesires: []*kubeapplier.ReadDesire{
				newHostedClusterReadDesire(t, &v1beta1.HostedCluster{
					Spec: testClusterUpdateMatchingHostedClusterSpec(),
				}),
			},
			wantState: arm.ProvisioningStateSucceeded,
		},
		{
			name: "default availability policies (highly available) mismatch hypershift sidereturns Updating",
			cluster: func() *api.HCPOpenShiftCluster {
				c := fixture.newCluster(nil)
				return c
			}(),
			serviceProviderCluster: emptySPC,
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
			wantMessageSubstr: `controllerAvailabilityPolicy is "SingleReplica", want "HighlyAvailable"`,
		},
		{
			name: "minimal pod sizing annotation match returns Succeeded",
			cluster: func() *api.HCPOpenShiftCluster {
				c := fixture.newCluster(nil)
				c.ServiceProviderProperties.ExperimentalFeatures.ControlPlanePodSizing = api.MinimalControlPlanePodSizing
				return c
			}(),
			serviceProviderCluster: emptySPC,
			readDesires: []*kubeapplier.ReadDesire{
				newHostedClusterReadDesire(t, &v1beta1.HostedCluster{
					ObjectMeta: metav1.ObjectMeta{
						Annotations: map[string]string{
							v1beta1.ClusterSizeOverrideAnnotation: ocm.CSPropertyE2EMinimalControlPlaneSize,
						},
					},
					Spec: testClusterUpdateMatchingHostedClusterSpec(),
				}),
			},
			wantState: arm.ProvisioningStateSucceeded,
		},
		{
			name: "minimal pod sizing set on cluster but missing annotation on hypershift side returns Updating",
			cluster: func() *api.HCPOpenShiftCluster {
				c := fixture.newCluster(nil)
				c.ServiceProviderProperties.ExperimentalFeatures.ControlPlanePodSizing = api.MinimalControlPlanePodSizing
				return c
			}(),
			serviceProviderCluster: emptySPC,
			readDesires: []*kubeapplier.ReadDesire{
				newHostedClusterReadDesire(t, &v1beta1.HostedCluster{
					Spec: testClusterUpdateMatchingHostedClusterSpec(),
				}),
			},
			wantState:         arm.ProvisioningStateUpdating,
			wantMessageSubstr: `is unset, want "e2e_minimal"`,
		},
		{
			name: "default pod sizing with stale annotation returns Updating",
			cluster: func() *api.HCPOpenShiftCluster {
				c := fixture.newCluster(nil)
				return c
			}(),
			serviceProviderCluster: emptySPC,
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
			wantMessageSubstr: `is "e2e_minimal", want unset`,
		},
		{
			name: "SPC level size override matches annotation returns Succeeded",
			cluster: func() *api.HCPOpenShiftCluster {
				c := fixture.newCluster(nil)
				return c
			}(),
			serviceProviderCluster: &api.ServiceProviderCluster{
				Spec: api.ServiceProviderClusterSpec{
					DesiredHostedClusterControlPlaneSize: ptr.To("large"),
				},
			},
			readDesires: []*kubeapplier.ReadDesire{
				newHostedClusterReadDesire(t, &v1beta1.HostedCluster{
					ObjectMeta: metav1.ObjectMeta{
						Annotations: map[string]string{
							v1beta1.ClusterSizeOverrideAnnotation: "large",
						},
					},
					Spec: testClusterUpdateMatchingHostedClusterSpec(),
				}),
			},
			wantState: arm.ProvisioningStateSucceeded,
		},
		{
			name: "SPC level size override mismatch returns Updating",
			cluster: func() *api.HCPOpenShiftCluster {
				c := fixture.newCluster(nil)
				return c
			}(),
			serviceProviderCluster: &api.ServiceProviderCluster{
				Spec: api.ServiceProviderClusterSpec{
					DesiredHostedClusterControlPlaneSize: ptr.To("large"),
				},
			},
			readDesires: []*kubeapplier.ReadDesire{
				newHostedClusterReadDesire(t, &v1beta1.HostedCluster{
					ObjectMeta: metav1.ObjectMeta{
						Annotations: map[string]string{
							v1beta1.ClusterSizeOverrideAnnotation: "medium",
						},
					},
					Spec: testClusterUpdateMatchingHostedClusterSpec(),
				}),
			},
			wantState:         arm.ProvisioningStateUpdating,
			wantMessageSubstr: `is "medium", want "large"`,
		},
		{
			name: "control plane operator image annotation match returns Succeeded",
			cluster: func() *api.HCPOpenShiftCluster {
				c := fixture.newCluster(nil)
				c.ServiceProviderProperties.ExperimentalFeatures.ControlPlaneOperatorImage = "quay.io/openshift/cpo:test"
				return c
			}(),
			serviceProviderCluster: emptySPC,
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
			serviceProviderCluster: emptySPC,
			readDesires: []*kubeapplier.ReadDesire{
				newHostedClusterReadDesire(t, &v1beta1.HostedCluster{
					Spec: testClusterUpdateMatchingHostedClusterSpec(),
				}),
			},
			wantState:         arm.ProvisioningStateUpdating,
			wantMessageSubstr: `is unset, want "quay.io/openshift/cpo:test"`,
		},
		{
			name: "default control plane operator image with stale annotation returns Updating",
			cluster: func() *api.HCPOpenShiftCluster {
				c := fixture.newCluster(nil)
				return c
			}(),
			serviceProviderCluster: emptySPC,
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
			wantMessageSubstr: `is "quay.io/openshift/cpo:test", want unset`,
		},
		{
			name: "kms cluster key version mismatch returns Updating",
			cluster: func() *api.HCPOpenShiftCluster {
				c := fixture.newCluster(nil)
				c.CustomerProperties.Etcd = api.EtcdProfile{
					DataEncryption: api.EtcdDataEncryptionProfile{
						KeyManagementMode: api.EtcdDataEncryptionKeyManagementModeTypeCustomerManaged,
						CustomerManaged: &api.CustomerManagedEncryptionProfile{
							EncryptionType: api.CustomerManagedEncryptionTypeKMS,
							Kms: &api.KmsEncryptionProfile{
								Visibility: api.KeyVaultVisibilityPublic,
								ActiveKey: api.KmsKey{
									Name:      "test-key",
									VaultName: "test-vault",
									Version:   "v2",
								},
							},
						},
					},
				}
				return c
			}(),
			serviceProviderCluster: emptySPC,
			readDesires: []*kubeapplier.ReadDesire{
				newHostedClusterReadDesire(t, &v1beta1.HostedCluster{
					Spec: func() v1beta1.HostedClusterSpec {
						spec := testClusterUpdateMatchingHostedClusterSpec()
						spec.SecretEncryption = &v1beta1.SecretEncryptionSpec{
							Type: v1beta1.KMS,
							KMS: &v1beta1.KMSSpec{
								Azure: &v1beta1.AzureKMSSpec{
									ActiveKey: v1beta1.AzureKMSKey{
										KeyVersion: "v1",
									},
								},
							},
						}
						return spec
					}(),
				}),
			},
			wantState:         arm.ProvisioningStateUpdating,
			wantMessageSubstr: `active key version is: "v1", want: "v2"`,
		},
		{
			name: "kms cluster with nil SecretEncryption returns Updating",
			cluster: func() *api.HCPOpenShiftCluster {
				c := fixture.newCluster(nil)
				c.CustomerProperties.Etcd = api.EtcdProfile{
					DataEncryption: api.EtcdDataEncryptionProfile{
						KeyManagementMode: api.EtcdDataEncryptionKeyManagementModeTypeCustomerManaged,
						CustomerManaged: &api.CustomerManagedEncryptionProfile{
							EncryptionType: api.CustomerManagedEncryptionTypeKMS,
							Kms: &api.KmsEncryptionProfile{
								Visibility: api.KeyVaultVisibilityPublic,
								ActiveKey: api.KmsKey{
									Name:      "test-key",
									VaultName: "test-vault",
									Version:   "v1",
								},
							},
						},
					},
				}
				return c
			}(),
			serviceProviderCluster: emptySPC,
			readDesires: []*kubeapplier.ReadDesire{
				newHostedClusterReadDesire(t, &v1beta1.HostedCluster{
					Spec: func() v1beta1.HostedClusterSpec {
						spec := testClusterUpdateMatchingHostedClusterSpec()
						spec.SecretEncryption = nil
						return spec
					}(),
				}),
			},
			wantState:         arm.ProvisioningStateUpdating,
			wantMessageSubstr: "secret encryption is not set",
		},
		{
			name: "kms cluster key version matching returns Succeeded",
			cluster: func() *api.HCPOpenShiftCluster {
				c := fixture.newCluster(nil)
				c.CustomerProperties.Etcd = api.EtcdProfile{
					DataEncryption: api.EtcdDataEncryptionProfile{
						KeyManagementMode: api.EtcdDataEncryptionKeyManagementModeTypeCustomerManaged,
						CustomerManaged: &api.CustomerManagedEncryptionProfile{
							EncryptionType: api.CustomerManagedEncryptionTypeKMS,
							Kms: &api.KmsEncryptionProfile{
								Visibility: api.KeyVaultVisibilityPublic,
								ActiveKey: api.KmsKey{
									Name:      "test-key",
									VaultName: "test-vault",
									Version:   "v1",
								},
							},
						},
					},
				}
				return c
			}(),
			serviceProviderCluster: emptySPC,
			readDesires: []*kubeapplier.ReadDesire{
				newHostedClusterReadDesire(t, &v1beta1.HostedCluster{
					Spec: func() v1beta1.HostedClusterSpec {
						spec := testClusterUpdateMatchingHostedClusterSpec()
						spec.SecretEncryption = &v1beta1.SecretEncryptionSpec{
							Type: v1beta1.KMS,
							KMS: &v1beta1.KMSSpec{
								Azure: &v1beta1.AzureKMSSpec{
									ActiveKey: v1beta1.AzureKMSKey{
										KeyVersion: "v1",
									},
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
			name: "Platform Managed data encryption returns Updating",
			cluster: func() *api.HCPOpenShiftCluster {
				c := fixture.newCluster(nil)
				c.CustomerProperties.Etcd = api.EtcdProfile{
					DataEncryption: api.EtcdDataEncryptionProfile{
						KeyManagementMode: api.EtcdDataEncryptionKeyManagementModeTypePlatformManaged,
					},
				}
				return c
			}(),
			serviceProviderCluster: emptySPC,
			readDesires: []*kubeapplier.ReadDesire{
				newHostedClusterReadDesire(t, &v1beta1.HostedCluster{
					Spec: func() v1beta1.HostedClusterSpec {
						spec := testClusterUpdateMatchingHostedClusterSpec()
						spec.SecretEncryption = &v1beta1.SecretEncryptionSpec{
							Type: v1beta1.KMS,
							KMS: &v1beta1.KMSSpec{
								Azure: &v1beta1.AzureKMSSpec{
									ActiveKey: v1beta1.AzureKMSKey{
										KeyVersion: "v1",
									},
								},
							},
						}
						return spec
					}(),
				}),
			},
			wantState:         arm.ProvisioningStateUpdating,
			wantMessageSubstr: "support for desired key management mode",
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

			state, err := controller.hypershiftHostedClusterOperationState(ctx, tt.cluster, tt.serviceProviderCluster)
			require.NoError(t, err)
			assert.Equal(t, tt.wantState, state.ProvisioningState)
			if tt.wantMessageSubstr != "" {
				assert.Contains(t, state.Message, tt.wantMessageSubstr)
			}
		})
	}
}

func TestHypershiftHostedClusterAllowedCIDRBlocksSpecMatchesDesired(t *testing.T) {
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
			wantSubstr: `allowedCIDRBlocks is [10.0.0.0/8], want unset (allow all)`,
		},
		{
			name:    "desired blocks missing on observed",
			desired: []string{"10.0.0.0/8"},
			observed: v1beta1.HostedClusterSpec{
				Networking: v1beta1.ClusterNetworking{},
			},
			wantMatch:  false,
			wantSubstr: `allowedCIDRBlocks is unset, want [10.0.0.0/8]`,
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
			wantSubstr: `is missing "10.0.0.0/8"`,
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
			wantSubstr: `is missing "10.0.0.0/8"`,
		},
		{
			name:    "temporary: desired subset present with internal extras",
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
			matches, msg := controller.hypershiftHostedClusterAllowedCIDRBlocksSpecMatchesDesired(tt.desired, &tt.observed)
			assert.Equal(t, tt.wantMatch, matches)
			if tt.wantSubstr != "" {
				assert.Contains(t, msg, tt.wantSubstr)
			}
		})
	}
}

func TestHypershiftHostedClusterAvailabilityPoliciesSpecMatchesDesired(t *testing.T) {
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
			wantSubstr: `hypershift HostedCluster controllerAvailabilityPolicy is "", want "HighlyAvailable"`,
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
			wantSubstr: `hypershift HostedCluster controllerAvailabilityPolicy is "SingleReplica", want "HighlyAvailable"`,
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
			wantSubstr: `hypershift HostedCluster controllerAvailabilityPolicy is "", want "SingleReplica"`,
		},
		{
			name:    "single replica desired rejects highly available infrastructure",
			desired: api.SingleReplicaControlPlane,
			observed: v1beta1.HostedClusterSpec{
				ControllerAvailabilityPolicy:     v1beta1.SingleReplica,
				InfrastructureAvailabilityPolicy: v1beta1.HighlyAvailable,
			},
			wantMatch:  false,
			wantSubstr: `hypershift HostedCluster infrastructureAvailabilityPolicy is "HighlyAvailable", want "SingleReplica"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			matches, msg := controller.hypershiftHostedClusterAvailabilityPoliciesSpecMatchesDesired(tt.desired, &tt.observed)
			assert.Equal(t, tt.wantMatch, matches)
			if tt.wantSubstr != "" {
				assert.Contains(t, msg, tt.wantSubstr)
			}
		})
	}
}

func TestHypershiftHostedClusterSizeOverrideAnnotationMatchesDesired(t *testing.T) {
	t.Parallel()

	controller := &operationClusterUpdate{}

	tests := []struct {
		name                string
		clusterPodSizing    api.ControlPlanePodSizing
		spcControlPlaneSize *string
		observed            map[string]string
		wantMatch           bool
		wantSubstr          string
	}{
		{
			name:             "default desired with no annotations",
			clusterPodSizing: api.DefaultControlPlanePodSizing,
			observed:         nil,
			wantMatch:        true,
		},
		{
			name:             "default desired rejects stale annotation",
			clusterPodSizing: api.DefaultControlPlanePodSizing,
			observed: map[string]string{
				v1beta1.ClusterSizeOverrideAnnotation: ocm.CSPropertyE2EMinimalControlPlaneSize,
			},
			wantMatch:  false,
			wantSubstr: `is "e2e_minimal", want unset`,
		},
		{
			name:             "minimal desired matches annotation",
			clusterPodSizing: api.MinimalControlPlanePodSizing,
			observed: map[string]string{
				v1beta1.ClusterSizeOverrideAnnotation: ocm.CSPropertyE2EMinimalControlPlaneSize,
			},
			wantMatch: true,
		},
		{
			name:             "minimal desired rejects missing annotation",
			clusterPodSizing: api.MinimalControlPlanePodSizing,
			observed:         nil,
			wantMatch:        false,
			wantSubstr:       `is unset, want "e2e_minimal"`,
		},
		{
			name:             "minimal desired rejects different annotation value",
			clusterPodSizing: api.MinimalControlPlanePodSizing,
			observed: map[string]string{
				v1beta1.ClusterSizeOverrideAnnotation: "small",
			},
			wantMatch:  false,
			wantSubstr: `is "small", want "e2e_minimal"`,
		},
		{
			name:                "SPC size takes precedence and matches",
			clusterPodSizing:    api.MinimalControlPlanePodSizing,
			spcControlPlaneSize: ptr.To("large"),
			observed: map[string]string{
				v1beta1.ClusterSizeOverrideAnnotation: "large",
			},
			wantMatch: true,
		},
		{
			name:                "SPC size takes precedence and rejects mismatch",
			clusterPodSizing:    api.MinimalControlPlanePodSizing,
			spcControlPlaneSize: ptr.To("large"),
			observed: map[string]string{
				v1beta1.ClusterSizeOverrideAnnotation: "medium",
			},
			wantMatch:  false,
			wantSubstr: `is "medium", want "large"`,
		},
		{
			name:                "whenSPC size set it rejects missing annotation",
			clusterPodSizing:    "",
			spcControlPlaneSize: ptr.To("large"),
			observed:            nil,
			wantMatch:           false,
			wantSubstr:          `is unset, want "large"`,
		},
		{
			name:             "unrecognized cluster-level pod sizing returns not completed",
			clusterPodSizing: api.ControlPlanePodSizing("unknown"),
			observed:         nil,
			wantMatch:        false,
			wantSubstr:       `unrecognized cluster-level control plane pod sizing: "unknown"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			matches, msg := controller.hypershiftHostedClusterSizeOverrideAnnotationMatchesDesired(tt.clusterPodSizing, tt.spcControlPlaneSize, tt.observed)
			assert.Equal(t, tt.wantMatch, matches)
			if tt.wantSubstr != "" {
				assert.Contains(t, msg, tt.wantSubstr)
			}
		})
	}
}

func TestHypershiftHostedClusterControlPlaneOperatorImageAnnotationMatchesDesired(t *testing.T) {
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
			wantSubstr: `is "quay.io/openshift/cpo:test", want unset`,
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
			wantSubstr: `is unset, want "quay.io/openshift/cpo:test"`,
		},
		{
			name:    "desired image rejects wrong annotation value",
			desired: "quay.io/openshift/cpo:test",
			observed: map[string]string{
				v1beta1.ControlPlaneOperatorImageAnnotation: "quay.io/openshift/cpo:other",
			},
			wantMatch:  false,
			wantSubstr: `is "quay.io/openshift/cpo:other", want "quay.io/openshift/cpo:test"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			matches, msg := controller.hypershiftHostedClusterControlPlaneOperatorImageAnnotationMatchesDesired(tt.desired, tt.observed)
			assert.Equal(t, tt.wantMatch, matches)
			if tt.wantSubstr != "" {
				assert.Contains(t, msg, tt.wantSubstr)
			}
		})
	}
}

func TestHypershiftHostedClusterAutoscalingSpecMatchesDesired(t *testing.T) {
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
			wantSubstr: `maxNodesTotal is unset, want 0`,
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
			wantSubstr: `maxPodGracePeriod is unset, want 0`,
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
			wantSubstr: `maxNodesTotal is 5, want 10`,
		},
		{
			name:       "maxNodesTotal desired nonzero observed nil",
			desired:    api.ClusterAutoscalingProfile{MaxNodesTotal: 10},
			observed:   testClusterUpdateMatchingHostedClusterSpec().Autoscaling,
			wantMatch:  false,
			wantSubstr: `maxNodesTotal is 0, want 10`,
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
			wantSubstr: `maxNodeProvisionTime has an invalid duration: "not-a-duration", want "15m"`,
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
			wantSubstr: `maxPodGracePeriod is 30, want 60`,
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
			wantSubstr: `podPriorityThreshold is 0, want -10`,
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
			matches, msg := controller.hypershiftHostedClusterAutoscalingSpecMatchesDesired(tt.desired, &tt.observed)
			assert.Equal(t, tt.wantMatch, matches)
			if tt.wantSubstr != "" {
				assert.Contains(t, msg, tt.wantSubstr)
			}
		})
	}
}

func TestHypershiftHostedClusterImageContentSourcesSpecMatchesDesired(t *testing.T) {
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
			wantSubstr: `missing source "a"`,
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
			name:    "desired empty and observed has stale customer sources",
			desired: []api.ImageDigestMirror{},
			observed: []v1beta1.ImageContentSource{
				{Source: testClusterUpdatePlatformImageContentSource, Mirrors: []string{"platform-mirror"}},
				{Source: "quay.io/foo", Mirrors: []string{"mirror.io/foo"}},
			},
			wantMatch:  false,
			wantSubstr: `unexpected imageContentSource "quay.io/foo"`,
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
			wantSubstr: `unexpected imageContentSource "quay.io/bar"`,
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
			wantSubstr: `missing source "a"`,
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
			wantSubstr: `for source "a" mirrors do not match`,
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
			matches, msg := controller.hypershiftHostedClusterImageContentSourcesSpecMatchesDesired(tt.desired, tt.observed)
			assert.Equal(t, tt.wantMatch, matches)
			if tt.wantSubstr != "" {
				assert.Contains(t, msg, tt.wantSubstr)
			}
		})
	}
}

func TestHypershiftHostedClusterEtcdSecretEncryptionSpecMatchesDesired(t *testing.T) {
	t.Parallel()

	controller := &operationClusterUpdate{}

	etcdDesired := api.EtcdDataEncryptionProfile{
		KeyManagementMode: api.EtcdDataEncryptionKeyManagementModeTypeCustomerManaged,
		CustomerManaged: &api.CustomerManagedEncryptionProfile{
			EncryptionType: api.CustomerManagedEncryptionTypeKMS,
			Kms: &api.KmsEncryptionProfile{
				Visibility: api.KeyVaultVisibilityPublic,
				ActiveKey: api.KmsKey{
					Name:      "test-key",
					VaultName: "test-vault",
					Version:   "v1",
				},
			},
		},
	}

	tests := []struct {
		name       string
		desired    api.EtcdDataEncryptionProfile
		observed   *v1beta1.SecretEncryptionSpec
		wantMatch  bool
		wantSubstr string
	}{
		{
			name:       "customer managed KMS with nil observed returns mismatch",
			desired:    etcdDesired,
			observed:   nil,
			wantMatch:  false,
			wantSubstr: "secret encryption is not set",
		},
		{
			name:    "customer managed KMS with observed type not KMS returns mismatch",
			desired: etcdDesired,
			observed: &v1beta1.SecretEncryptionSpec{
				Type: v1beta1.AESCBC,
			},
			wantMatch:  false,
			wantSubstr: `secret encryption is: "aescbc"`,
		},
		{
			name:    "customer managed KMS with nil KMS field returns mismatch",
			desired: etcdDesired,
			observed: &v1beta1.SecretEncryptionSpec{
				Type: v1beta1.KMS,
			},
			wantMatch:  false,
			wantSubstr: "kms secret encryption configuration unset",
		},
		{
			name:    "customer managed KMS with nil Azure field returns mismatch",
			desired: etcdDesired,
			observed: &v1beta1.SecretEncryptionSpec{
				Type: v1beta1.KMS,
				KMS:  &v1beta1.KMSSpec{},
			},
			wantMatch:  false,
			wantSubstr: "azure configuration unset",
		},
		{
			name:    "customer managed KMS key version mismatch",
			desired: etcdDesired,
			observed: &v1beta1.SecretEncryptionSpec{
				Type: v1beta1.KMS,
				KMS: &v1beta1.KMSSpec{
					Azure: &v1beta1.AzureKMSSpec{
						ActiveKey: v1beta1.AzureKMSKey{
							KeyVersion: "v2",
						},
					},
				},
			},
			wantMatch:  false,
			wantSubstr: `active key version is: "v2", want: "v1"`,
		},
		{
			name:    "customer managed KMS key version match",
			desired: etcdDesired,
			observed: &v1beta1.SecretEncryptionSpec{
				Type: v1beta1.KMS,
				KMS: &v1beta1.KMSSpec{
					Azure: &v1beta1.AzureKMSSpec{
						ActiveKey: v1beta1.AzureKMSKey{
							KeyVersion: "v1",
						},
					},
				},
			},
			wantMatch: true,
		},
		{
			name: "platform managed",
			desired: api.EtcdDataEncryptionProfile{
				KeyManagementMode: api.EtcdDataEncryptionKeyManagementModeTypePlatformManaged,
			},
			observed: &v1beta1.SecretEncryptionSpec{
				Type: v1beta1.KMS,
				KMS: &v1beta1.KMSSpec{
					Azure: &v1beta1.AzureKMSSpec{
						ActiveKey: v1beta1.AzureKMSKey{
							KeyVersion: "v1",
						},
					},
				},
			},
			wantMatch:  false,
			wantSubstr: `support for desired key management mode "PlatformManaged" for updates is not implemented`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			matches, msg := controller.hypershiftHostedClusterEtcdSecretEncryptionSpecMatchesDesired(tt.desired, tt.observed)
			assert.Equal(t, tt.wantMatch, matches)
			if tt.wantSubstr != "" {
				assert.Contains(t, msg, tt.wantSubstr)
			}
		})
	}
}

func TestClusterServiceClusterSpecOperationState(t *testing.T) {
	t.Parallel()

	newCSClusterWithCIDRBlockAllowAccess := func(t *testing.T, mode string, cidrs ...string) *arohcpv1alpha1.Cluster {
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
	newCSCluster := func(t *testing.T) *arohcpv1alpha1.Cluster {
		t.Helper()
		cluster, err := arohcpv1alpha1.NewCluster().Build()
		require.NoError(t, err)
		return cluster
	}
	newCSClusterWithAllowAll := func(t *testing.T) *arohcpv1alpha1.Cluster {
		t.Helper()
		return newCSClusterWithCIDRBlockAllowAccess(t, ocm.CSCIDRBlockAllowAccessModeAllowAll)
	}
	newCSClusterWithNodeDrainTimeoutAndAllowAll := func(t *testing.T, minutes int32) *arohcpv1alpha1.Cluster {
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
	newCSClusterWithAuthorizedCIDRs := func(t *testing.T, cidrs ...string) *arohcpv1alpha1.Cluster {
		t.Helper()
		return newCSClusterWithCIDRBlockAllowAccess(t, ocm.CSCIDRBlockAllowAccessModeAllowList, cidrs...)
	}

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
			csCluster: newCSClusterWithNodeDrainTimeoutAndAllowAll(t, 30),
			wantState: arm.ProvisioningStateSucceeded,
		},
		{
			name: "zero desired with unset CS value returns Succeeded",
			cluster: &api.HCPOpenShiftCluster{
				CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{},
			},
			csCluster: newCSClusterWithAllowAll(t),
			wantState: arm.ProvisioningStateSucceeded,
		},
		{
			name: "mismatch returns Updating",
			cluster: &api.HCPOpenShiftCluster{
				CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
					NodeDrainTimeoutMinutes: 60,
				},
			},
			csCluster:         newCSClusterWithNodeDrainTimeoutAndAllowAll(t, 30),
			wantState:         arm.ProvisioningStateUpdating,
			wantMessageSubstr: `nodeDrainGracePeriod is 30 minutes, want 60`,
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
			csCluster: newCSClusterWithAuthorizedCIDRs(t, "10.0.0.0/8", "192.168.0.0/16"),
			wantState: arm.ProvisioningStateSucceeded,
		},
		{
			name: "nil desired with unset CS CIDR config returns Updating",
			cluster: &api.HCPOpenShiftCluster{
				CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
					API: api.CustomerAPIProfile{},
				},
			},
			csCluster:         newCSCluster(t),
			wantState:         arm.ProvisioningStateUpdating,
			wantMessageSubstr: `k8sAPIServerAuthorizedCIDRs is unset, want allow_all`,
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
			csCluster:         newCSClusterWithAuthorizedCIDRs(t, "10.0.0.0/8"),
			wantState:         arm.ProvisioningStateUpdating,
			wantMessageSubstr: `allow_list is [10.0.0.0/8], want [203.0.113.0/24]`,
		},
		{
			name: "explicit allow_all CS config with nil desired returns Succeeded",
			cluster: &api.HCPOpenShiftCluster{
				CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
					API: api.CustomerAPIProfile{},
				},
			},
			csCluster: newCSClusterWithCIDRBlockAllowAccess(t, ocm.CSCIDRBlockAllowAccessModeAllowAll),
			wantState: arm.ProvisioningStateSucceeded,
		},
		{
			name: "allow_all CS config with stale values and nil desired returns Succeeded",
			cluster: &api.HCPOpenShiftCluster{
				CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
					API: api.CustomerAPIProfile{},
				},
			},
			csCluster: newCSClusterWithCIDRBlockAllowAccess(t, ocm.CSCIDRBlockAllowAccessModeAllowAll, "10.0.0.0/8"),
			wantState: arm.ProvisioningStateSucceeded,
		},
		{
			name: "allow_list CS config with nil desired returns Updating",
			cluster: &api.HCPOpenShiftCluster{
				CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
					API: api.CustomerAPIProfile{},
				},
			},
			csCluster:         newCSClusterWithCIDRBlockAllowAccess(t, ocm.CSCIDRBlockAllowAccessModeAllowList, "10.0.0.0/8"),
			wantState:         arm.ProvisioningStateUpdating,
			wantMessageSubstr: `k8sAPIServerAuthorizedCIDRs is allow_list [10.0.0.0/8], want allow_all`,
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
			csCluster:         newCSClusterWithCIDRBlockAllowAccess(t, ocm.CSCIDRBlockAllowAccessModeAllowAll),
			wantState:         arm.ProvisioningStateUpdating,
			wantMessageSubstr: `k8sAPIServerAuthorizedCIDRs is allow_all, want allow_list`,
		},
		{
			name: "matching container registry pull MI returns Succeeded",
			cluster: &api.HCPOpenShiftCluster{
				CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
					Platform: api.CustomerPlatformProfile{
						ContainerRegistryPullManagedIdentity: api.NewTestUserAssignedIdentity("cr-pull-mi"),
					},
				},
			},
			csCluster: func() *arohcpv1alpha1.Cluster {
				c, err := arohcpv1alpha1.NewCluster().
					API(arohcpv1alpha1.NewClusterAPI().
						CIDRBlockAccess(arohcpv1alpha1.NewCIDRBlockAccess().
							Allow(arohcpv1alpha1.NewCIDRBlockAllowAccess().Mode(ocm.CSCIDRBlockAllowAccessModeAllowAll)))).
					Azure(arohcpv1alpha1.NewAzure().
						ContainerRegistry(arohcpv1alpha1.NewAzureContainerRegistry().
							Credentials(arohcpv1alpha1.NewAzureContainerRegistryCredentials().
								Type(arohcpv1alpha1.AzureContainerRegistryCredentialTypeManagedIdentity).
								ManagedIdentity(arohcpv1alpha1.NewAzureUserAssignedManagedIdentity().
									ResourceID(api.NewTestUserAssignedIdentity("cr-pull-mi").String()))))).
					Build()
				require.NoError(t, err)
				return c
			}(),
			wantState: arm.ProvisioningStateSucceeded,
		},
		{
			name: "container registry pull MI mismatch returns Updating",
			cluster: &api.HCPOpenShiftCluster{
				CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
					Platform: api.CustomerPlatformProfile{
						ContainerRegistryPullManagedIdentity: api.NewTestUserAssignedIdentity("new-mi"),
					},
				},
			},
			csCluster: func() *arohcpv1alpha1.Cluster {
				c, err := arohcpv1alpha1.NewCluster().
					API(arohcpv1alpha1.NewClusterAPI().
						CIDRBlockAccess(arohcpv1alpha1.NewCIDRBlockAccess().
							Allow(arohcpv1alpha1.NewCIDRBlockAllowAccess().Mode(ocm.CSCIDRBlockAllowAccessModeAllowAll)))).
					Azure(arohcpv1alpha1.NewAzure().
						ContainerRegistry(arohcpv1alpha1.NewAzureContainerRegistry().
							Credentials(arohcpv1alpha1.NewAzureContainerRegistryCredentials().
								Type(arohcpv1alpha1.AzureContainerRegistryCredentialTypeManagedIdentity).
								ManagedIdentity(arohcpv1alpha1.NewAzureUserAssignedManagedIdentity().
									ResourceID(api.NewTestUserAssignedIdentity("old-mi").String()))))).
					Build()
				require.NoError(t, err)
				return c
			}(),
			wantState:         arm.ProvisioningStateUpdating,
			wantMessageSubstr: `containerRegistryPullManagedIdentity`,
		},
		{
			name: "nil desired with unset CS container registry returns Succeeded",
			cluster: &api.HCPOpenShiftCluster{
				CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{},
			},
			csCluster: newCSClusterWithAllowAll(t),
			wantState: arm.ProvisioningStateSucceeded,
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

func readyControlPlaneClusterAutoscaler() *v1beta1.ControlPlaneComponent {
	return &v1beta1.ControlPlaneComponent{
		Status: v1beta1.ControlPlaneComponentStatus{
			Conditions: []metav1.Condition{
				{Type: string(v1beta1.ControlPlaneComponentAvailable), Status: metav1.ConditionTrue},
				{Type: string(v1beta1.ControlPlaneComponentRolloutComplete), Status: metav1.ConditionTrue},
			},
		},
	}
}

func newControlPlaneClusterAutoscalerReadDesire(t *testing.T, controlPlaneComponent *v1beta1.ControlPlaneComponent, conditions ...metav1.Condition) *kubeapplier.ReadDesire {
	t.Helper()
	raw, err := json.Marshal(controlPlaneComponent)
	require.NoError(t, err)
	if conditions == nil {
		conditions = []metav1.Condition{
			{Type: kubeapplier.ConditionTypeSuccessful, Status: metav1.ConditionTrue, Reason: kubeapplier.ConditionReasonNoErrors},
		}
	}

	resourceID := api.Must(azcorearm.ParseResourceID(
		kubeapplier.ToClusterScopedReadDesireResourceIDString(
			testSubscriptionID, testResourceGroupName, testClusterName, kubeapplierhelpers.ReadDesireNameReadonlyHypershiftControlPlaneComponentClusterAutoscaler)))

	return &kubeapplier.ReadDesire{
		CosmosMetadata: api.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(resourceID.SubscriptionID),
		},
		Status: kubeapplier.ReadDesireStatus{
			Conditions:  conditions,
			KubeContent: &kruntime.RawExtension{Raw: raw},
		},
	}
}

func TestIsControlPlaneClusterAutoscalerReady(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                  string
		controlPlaneComponent *v1beta1.ControlPlaneComponent
		want                  bool
	}{
		{
			name:                  "both Available and RolloutComplete true is ready",
			controlPlaneComponent: readyControlPlaneClusterAutoscaler(),
			want:                  true,
		},
		{
			name: "Available false is not ready",
			controlPlaneComponent: &v1beta1.ControlPlaneComponent{
				Status: v1beta1.ControlPlaneComponentStatus{
					Conditions: []metav1.Condition{
						{Type: string(v1beta1.ControlPlaneComponentAvailable), Status: metav1.ConditionFalse},
						{Type: string(v1beta1.ControlPlaneComponentRolloutComplete), Status: metav1.ConditionTrue},
					},
				},
			},
			want: false,
		},
		{
			name: "RolloutComplete is false",
			controlPlaneComponent: &v1beta1.ControlPlaneComponent{
				Status: v1beta1.ControlPlaneComponentStatus{
					Conditions: []metav1.Condition{
						{Type: string(v1beta1.ControlPlaneComponentAvailable), Status: metav1.ConditionTrue},
						{Type: string(v1beta1.ControlPlaneComponentRolloutComplete), Status: metav1.ConditionFalse},
					},
				},
			},
			want: false,
		},
		{
			name: "Available condition missing is not ready",
			controlPlaneComponent: &v1beta1.ControlPlaneComponent{
				Status: v1beta1.ControlPlaneComponentStatus{
					Conditions: []metav1.Condition{
						{Type: string(v1beta1.ControlPlaneComponentRolloutComplete), Status: metav1.ConditionTrue},
					},
				},
			},
			want: false,
		},
		{
			name: "RolloutComplete condition missing is not ready",
			controlPlaneComponent: &v1beta1.ControlPlaneComponent{
				Status: v1beta1.ControlPlaneComponentStatus{
					Conditions: []metav1.Condition{
						{Type: string(v1beta1.ControlPlaneComponentAvailable), Status: metav1.ConditionTrue},
					},
				},
			},
			want: false,
		},
		{
			name: "Available condition unknown is not ready",
			controlPlaneComponent: &v1beta1.ControlPlaneComponent{
				Status: v1beta1.ControlPlaneComponentStatus{
					Conditions: []metav1.Condition{
						{Type: string(v1beta1.ControlPlaneComponentAvailable), Status: metav1.ConditionUnknown},
						{Type: string(v1beta1.ControlPlaneComponentRolloutComplete), Status: metav1.ConditionTrue},
					},
				},
			},
			want: false,
		},
		{
			name: "RolloutComplete condition unknown is not ready",
			controlPlaneComponent: &v1beta1.ControlPlaneComponent{
				Status: v1beta1.ControlPlaneComponentStatus{
					Conditions: []metav1.Condition{
						{Type: string(v1beta1.ControlPlaneComponentAvailable), Status: metav1.ConditionTrue},
						{Type: string(v1beta1.ControlPlaneComponentRolloutComplete), Status: metav1.ConditionUnknown},
					},
				},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, (&operationClusterUpdate{}).isControlPlaneClusterAutoscalerReady(tt.controlPlaneComponent))
		})
	}
}

func TestControlPlaneClusterAutoscalerNotReadyMessage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                  string
		controlPlaneComponent *v1beta1.ControlPlaneComponent
		wantExact             string
		wantContains          []string
	}{
		{
			name: "Available false with reason and message includes both",
			controlPlaneComponent: &v1beta1.ControlPlaneComponent{
				Status: v1beta1.ControlPlaneComponentStatus{
					Conditions: []metav1.Condition{
						{Type: string(v1beta1.ControlPlaneComponentAvailable), Status: metav1.ConditionFalse, Reason: "PodsNotReady", Message: "waiting for pods"},
					},
				},
			},
			wantContains: []string{clusterAutoscalerNotAvailableMsg, "PodsNotReady", "waiting for pods"},
		},
		{
			name: "Available false without message returns base message only",
			controlPlaneComponent: &v1beta1.ControlPlaneComponent{
				Status: v1beta1.ControlPlaneComponentStatus{
					Conditions: []metav1.Condition{
						{Type: string(v1beta1.ControlPlaneComponentAvailable), Status: metav1.ConditionFalse, Reason: "PodsNotReady"},
					},
				},
			},
			wantExact: clusterAutoscalerNotAvailableMsg,
		},
		{
			name: "Available condition missing returns base not available message",
			controlPlaneComponent: &v1beta1.ControlPlaneComponent{
				Status: v1beta1.ControlPlaneComponentStatus{
					Conditions: []metav1.Condition{
						{Type: string(v1beta1.ControlPlaneComponentRolloutComplete), Status: metav1.ConditionTrue},
					},
				},
			},
			wantExact: clusterAutoscalerNotAvailableMsg,
		},
		{
			name: "Available true and Rollout false with reason and message includes both",
			controlPlaneComponent: &v1beta1.ControlPlaneComponent{
				Status: v1beta1.ControlPlaneComponentStatus{
					Conditions: []metav1.Condition{
						{Type: string(v1beta1.ControlPlaneComponentAvailable), Status: metav1.ConditionTrue},
						{Type: string(v1beta1.ControlPlaneComponentRolloutComplete), Status: metav1.ConditionFalse, Reason: "RolloutInProgress", Message: "waiting for rollout"},
					},
				},
			},
			wantContains: []string{clusterAutoscalerRolloutNotCompleteMsg, "RolloutInProgress", "waiting for rollout"},
		},
		{
			name: "Available true and Rollout false without message returns base message only",
			controlPlaneComponent: &v1beta1.ControlPlaneComponent{
				Status: v1beta1.ControlPlaneComponentStatus{
					Conditions: []metav1.Condition{
						{Type: string(v1beta1.ControlPlaneComponentAvailable), Status: metav1.ConditionTrue},
						{Type: string(v1beta1.ControlPlaneComponentRolloutComplete), Status: metav1.ConditionFalse, Reason: "RolloutInProgress"},
					},
				},
			},
			wantExact: clusterAutoscalerRolloutNotCompleteMsg,
		},
		{
			name: "Rollout condition missing returns base rollout not complete message",
			controlPlaneComponent: &v1beta1.ControlPlaneComponent{
				Status: v1beta1.ControlPlaneComponentStatus{
					Conditions: []metav1.Condition{
						{Type: string(v1beta1.ControlPlaneComponentAvailable), Status: metav1.ConditionTrue},
					},
				},
			},
			wantExact: clusterAutoscalerRolloutNotCompleteMsg,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			msg := (&operationClusterUpdate{}).controlPlaneClusterAutoscalerNotReadyMessage(tt.controlPlaneComponent)
			if tt.wantExact != "" {
				assert.Equal(t, tt.wantExact, msg)
			}
			for _, want := range tt.wantContains {
				assert.Contains(t, msg, want)
			}
		})
	}
}

func TestHypershiftControlPlaneClusterAutoscalerState(t *testing.T) {
	t.Parallel()

	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
	fixture := newClusterTestFixture()

	tests := []struct {
		name                                          string
		activeVersions                                []api.HCPClusterActiveVersion
		cachedControlPlaneClusterAutoscalerReadDesire *kubeapplier.ReadDesire
		wantState                                     arm.ProvisioningState
		wantMessage                                   string
	}{
		{
			name:           "skips autoscaler gate when lowest active version is below 4.20",
			activeVersions: []api.HCPClusterActiveVersion{{Version: ptr.To(semver.MustParse("4.19.0"))}},
			wantState:      arm.ProvisioningStateSucceeded,
			wantMessage:    `lowest active control plane version "4.19.0" does not support ControlPlaneComponent cluster-autoscaler (requires 4.20+)`,
		},
		{
			name:        "waits when active versions are not yet reported",
			wantState:   arm.ProvisioningStateUpdating,
			wantMessage: "control plane active versions not yet reported",
		},
		{
			name:           "waits when autoscaler ReadDesire is absent on 4.20+",
			activeVersions: []api.HCPClusterActiveVersion{{Version: ptr.To(semver.MustParse("4.20.0"))}},
			wantState:      arm.ProvisioningStateUpdating,
			wantMessage:    "cluster autoscaler state not cached yet",
		},
		{
			name: "nightly pre-release control plane version satisfies the 4.20+ autoscaler gate",
			activeVersions: []api.HCPClusterActiveVersion{{
				Version: ptr.To(semver.MustParse("4.20.0-0.nightly-2026-01-01-000000")),
			}},
			cachedControlPlaneClusterAutoscalerReadDesire: newControlPlaneClusterAutoscalerReadDesire(t, readyControlPlaneClusterAutoscaler()),
			wantState:   arm.ProvisioningStateSucceeded,
			wantMessage: "",
		},
		{
			name:           "succeeds when autoscaler ControlPlaneComponent is ready",
			activeVersions: []api.HCPClusterActiveVersion{{Version: ptr.To(semver.MustParse("4.20.0"))}},
			cachedControlPlaneClusterAutoscalerReadDesire: newControlPlaneClusterAutoscalerReadDesire(t, readyControlPlaneClusterAutoscaler()),
			wantState:   arm.ProvisioningStateSucceeded,
			wantMessage: "",
		},
		{
			name:           "updates when autoscaler ControlPlaneComponent is not ready",
			activeVersions: []api.HCPClusterActiveVersion{{Version: ptr.To(semver.MustParse("4.20.0"))}},
			cachedControlPlaneClusterAutoscalerReadDesire: newControlPlaneClusterAutoscalerReadDesire(t, &v1beta1.ControlPlaneComponent{
				Status: v1beta1.ControlPlaneComponentStatus{
					Conditions: []metav1.Condition{
						{Type: string(v1beta1.ControlPlaneComponentAvailable), Status: metav1.ConditionFalse, Reason: "PodsNotReady", Message: "waiting for pods"},
					},
				},
			}),
			wantState:   arm.ProvisioningStateUpdating,
			wantMessage: "cluster autoscaler not available: PodsNotReady: waiting for pods",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cluster := fixture.newCluster(nil)

			spcResourceID := api.Must(azcorearm.ParseResourceID(fmt.Sprintf("%s/%s/%s",
				fixture.clusterResourceID.String(),
				api.ServiceProviderClusterResourceTypeName,
				api.ServiceProviderClusterResourceName,
			)))
			spc := &api.ServiceProviderCluster{
				CosmosMetadata: api.CosmosMetadata{ResourceID: spcResourceID, PartitionKey: strings.ToLower(spcResourceID.SubscriptionID)},
				Status: api.ServiceProviderClusterStatus{
					ControlPlaneVersion: api.ServiceProviderClusterStatusVersion{
						ActiveVersions: tt.activeVersions,
					},
				},
			}

			var readDesires []*kubeapplier.ReadDesire
			if tt.cachedControlPlaneClusterAutoscalerReadDesire != nil {
				readDesires = append(readDesires, tt.cachedControlPlaneClusterAutoscalerReadDesire)
			}

			controller := &operationClusterUpdate{
				readDesireLister: &internallistertesting.SliceReadDesireLister{Desires: readDesires},
			}

			got, err := controller.hypershiftControlPlaneClusterAutoscalerState(ctx, cluster, spc)
			require.NoError(t, err)
			assert.Equal(t, tt.wantState, got.ProvisioningState)
			assert.Equal(t, tt.wantMessage, got.Message)
		})
	}
}
