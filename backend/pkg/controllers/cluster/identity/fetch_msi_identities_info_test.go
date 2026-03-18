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

package identity

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clocktesting "k8s.io/utils/clock/testing"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
)

const (
	testOperatorName               = "cloud-controller-manager"
	testOperatorIdentityResourceID = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/ccm"
	testServiceManagedIdentityID   = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/smi"
	testOtherOperatorIdentityID    = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/other"
)

func TestDesiredMSIResourceIDsMatchSPC(t *testing.T) {
	t.Parallel()

	syncer := &fetchMSIIdentitiesInfoSyncer{}
	matchingCluster, matchingSPC := newMatchingClusterAndSPC()
	matchingToFetch, err := syncer.collectMSIBasedIdentitiesToFetch(matchingCluster)
	require.NoError(t, err, "collect matching identities")

	testCases := []struct {
		name    string
		toFetch *msiBasedIdentitiesToFetch
		spc     *coreapi.ServiceProviderCluster
		want    bool
	}{
		{
			name:    "matching control plane and service managed identity",
			toFetch: matchingToFetch,
			spc:     matchingSPC,
			want:    true,
		},
		{
			name: "resource ID casing differences still match",
			toFetch: func() *msiBasedIdentitiesToFetch {
				cluster, _ := newMatchingClusterAndSPC()
				cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ControlPlaneOperators[testOperatorName] =
					metadataapi.Must(azcorearm.ParseResourceID(strings.ToUpper(testOperatorIdentityResourceID)))
				cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ServiceManagedIdentity =
					metadataapi.Must(azcorearm.ParseResourceID(strings.ToUpper(testServiceManagedIdentityID)))
				toFetch, err := syncer.collectMSIBasedIdentitiesToFetch(cluster)
				require.NoError(t, err, "collect casing-variant identities")
				return toFetch
			}(),
			spc:  matchingSPC,
			want: true,
		},
		{
			name: "service managed identity resource ID changed",
			toFetch: func() *msiBasedIdentitiesToFetch {
				cluster, _ := newMatchingClusterAndSPC()
				cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ServiceManagedIdentity =
					metadataapi.Must(azcorearm.ParseResourceID(testOtherOperatorIdentityID))
				toFetch, err := syncer.collectMSIBasedIdentitiesToFetch(cluster)
				require.NoError(t, err, "collect diverged SMI identities")
				return toFetch
			}(),
			spc:  matchingSPC,
			want: false,
		},
		{
			name: "control plane operator resource ID changed",
			toFetch: func() *msiBasedIdentitiesToFetch {
				cluster, _ := newMatchingClusterAndSPC()
				cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ControlPlaneOperators[testOperatorName] =
					metadataapi.Must(azcorearm.ParseResourceID(testOtherOperatorIdentityID))
				toFetch, err := syncer.collectMSIBasedIdentitiesToFetch(cluster)
				require.NoError(t, err, "collect diverged control-plane identities")
				return toFetch
			}(),
			spc:  matchingSPC,
			want: false,
		},
		{
			name: "control plane operator name rebound to same resource ID still matches",
			toFetch: func() *msiBasedIdentitiesToFetch {
				cluster, _ := newMatchingClusterAndSPC()
				delete(cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ControlPlaneOperators, testOperatorName)
				cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ControlPlaneOperators["ingress"] =
					metadataapi.Must(azcorearm.ParseResourceID(testOperatorIdentityResourceID))
				toFetch, err := syncer.collectMSIBasedIdentitiesToFetch(cluster)
				require.NoError(t, err, "collect rebound operator identities")
				return toFetch
			}(),
			spc:  matchingSPC,
			want: true,
		},
		{
			name:    "extra stored control plane identity",
			toFetch: matchingToFetch,
			spc: func() *coreapi.ServiceProviderCluster {
				_, spc := newMatchingClusterAndSPC()
				otherLower := strings.ToLower(testOtherOperatorIdentityID)
				spc.Status.MSIManagedIdentities.ControlPlaneOperatorsIdentities[otherLower] = &coreapi.ServiceProviderClusterControlPlaneOperatorIdentity{
					ResourceID: metadataapi.Must(azcorearm.ParseResourceID(otherLower)),
				}
				return spc
			}(),
			want: false,
		},
		{
			name:    "missing stored service managed identity",
			toFetch: matchingToFetch,
			spc: func() *coreapi.ServiceProviderCluster {
				_, spc := newMatchingClusterAndSPC()
				spc.Status.MSIManagedIdentities.ServiceManagedIdentity = nil
				return spc
			}(),
			want: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, syncer.desiredMSIResourceIDsMatchSPC(tc.toFetch, tc.spc))
		})
	}
}

func TestNeedsWorkIgnoresEarliestRecheckWhenIdentitiesDiverge(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	future := metav1.NewTime(now.Add(6 * time.Hour))
	past := metav1.NewTime(now.Add(-time.Hour))

	syncer := &fetchMSIIdentitiesInfoSyncer{
		clock: clocktesting.NewFakePassiveClock(now),
	}

	matchingCluster, matchingSPC := newMatchingClusterAndSPC()
	matchingSPC.Status.MSIManagedIdentities.EarliestRecheckTime = &future
	matchingToFetch, err := syncer.collectMSIBasedIdentitiesToFetch(matchingCluster)
	require.NoError(t, err, "collect matching identities")

	divergedCluster, _ := newMatchingClusterAndSPC()
	divergedCluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ServiceManagedIdentity =
		metadataapi.Must(azcorearm.ParseResourceID(testOtherOperatorIdentityID))
	divergedToFetch, err := syncer.collectMSIBasedIdentitiesToFetch(divergedCluster)
	require.NoError(t, err, "collect diverged identities")

	testCases := []struct {
		name    string
		toFetch *msiBasedIdentitiesToFetch
		spc     *coreapi.ServiceProviderCluster
		want    bool
	}{
		{
			name:    "matching identities with future recheck skips work",
			toFetch: matchingToFetch,
			spc:     matchingSPC,
			want:    false,
		},
		{
			name:    "matching identities with past recheck needs work",
			toFetch: matchingToFetch,
			spc: func() *coreapi.ServiceProviderCluster {
				_, spc := newMatchingClusterAndSPC()
				spc.Status.MSIManagedIdentities.EarliestRecheckTime = &past
				return spc
			}(),
			want: true,
		},
		{
			name:    "diverged identities ignore future recheck",
			toFetch: divergedToFetch,
			spc:     matchingSPC,
			want:    true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, syncer.needsWork(tc.spc, tc.toFetch))
		})
	}
}

func newMatchingClusterAndSPC() (*coreapi.HCPOpenShiftCluster, *coreapi.ServiceProviderCluster) {
	operatorResourceID := metadataapi.Must(azcorearm.ParseResourceID(testOperatorIdentityResourceID))
	serviceManagedIdentity := metadataapi.Must(azcorearm.ParseResourceID(testServiceManagedIdentityID))
	lowerOperatorResourceIDStr := strings.ToLower(testOperatorIdentityResourceID)
	lowerServiceManagedIdentityStr := strings.ToLower(testServiceManagedIdentityID)

	cluster := &coreapi.HCPOpenShiftCluster{
		CustomerProperties: coreapi.HCPOpenShiftClusterCustomerProperties{
			Platform: coreapi.CustomerPlatformProfile{
				OperatorsAuthentication: coreapi.OperatorsAuthenticationProfile{
					UserAssignedIdentities: coreapi.UserAssignedIdentitiesProfile{
						ControlPlaneOperators: map[string]*azcorearm.ResourceID{
							testOperatorName: operatorResourceID,
						},
						ServiceManagedIdentity: serviceManagedIdentity,
					},
				},
			},
		},
	}

	spc := &coreapi.ServiceProviderCluster{
		Status: coreapi.ServiceProviderClusterStatus{
			MSIManagedIdentities: coreapi.ServiceProviderClusterMSIManagedIdentities{
				ControlPlaneOperatorsIdentities: map[string]*coreapi.ServiceProviderClusterControlPlaneOperatorIdentity{
					lowerOperatorResourceIDStr: {
						ResourceID:  metadataapi.Must(azcorearm.ParseResourceID(lowerOperatorResourceIDStr)),
						ClientID:    ptr.To("client-id"),
						PrincipalID: ptr.To("principal-id"),
					},
				},
				ServiceManagedIdentity: &coreapi.ServiceProviderClusterServiceManagedIdentity{
					ResourceID:  metadataapi.Must(azcorearm.ParseResourceID(lowerServiceManagedIdentityStr)),
					ClientID:    ptr.To("smi-client-id"),
					PrincipalID: ptr.To("smi-principal-id"),
				},
			},
		},
	}

	return cluster, spc
}
