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

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
)

func TestDesiredDataPlaneOperatorResourceIDsMatchSPC(t *testing.T) {
	t.Parallel()

	identityA := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/identity-a"))
	identityB := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/identity-b"))
	mixedCaseIdentityA := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/Test-RG/providers/Microsoft.ManagedIdentity/userAssignedIdentities/Identity-A"))

	testCases := []struct {
		name               string
		desiredResourceIDs map[string]struct{}
		spcIdentities      map[string]*coreapi.ServiceProviderClusterDataPlaneOperatorManagedIdentity
		expectedMatch      bool
	}{
		{
			name:               "both empty match",
			desiredResourceIDs: map[string]struct{}{},
			spcIdentities:      map[string]*coreapi.ServiceProviderClusterDataPlaneOperatorManagedIdentity{},
			expectedMatch:      true,
		},
		{
			name: "matching resource ID",
			desiredResourceIDs: map[string]struct{}{
				strings.ToLower(identityA.String()): {},
			},
			spcIdentities: map[string]*coreapi.ServiceProviderClusterDataPlaneOperatorManagedIdentity{
				strings.ToLower(identityA.String()): {
					ResourceID: identityA,
				},
			},
			expectedMatch: true,
		},
		{
			name: "matching ignores resource ID casing when already lowercased as key",
			desiredResourceIDs: map[string]struct{}{
				strings.ToLower(mixedCaseIdentityA.String()): {},
			},
			spcIdentities: map[string]*coreapi.ServiceProviderClusterDataPlaneOperatorManagedIdentity{
				strings.ToLower(identityA.String()): {
					ResourceID: identityA,
				},
			},
			expectedMatch: true,
		},
		{
			name: "unique identity count mismatch",
			desiredResourceIDs: map[string]struct{}{
				strings.ToLower(identityA.String()): {},
			},
			spcIdentities: map[string]*coreapi.ServiceProviderClusterDataPlaneOperatorManagedIdentity{
				strings.ToLower(identityA.String()): {
					ResourceID: identityA,
				},
				strings.ToLower(identityB.String()): {
					ResourceID: identityB,
				},
			},
			expectedMatch: false,
		},
		{
			name: "resource ID mismatch",
			desiredResourceIDs: map[string]struct{}{
				strings.ToLower(identityA.String()): {},
			},
			spcIdentities: map[string]*coreapi.ServiceProviderClusterDataPlaneOperatorManagedIdentity{
				strings.ToLower(identityB.String()): {
					ResourceID: identityB,
				},
			},
			expectedMatch: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			syncer := &fetchDataPlaneOperatorsManagedIdentitiesInfoSyncer{}
			spc := &coreapi.ServiceProviderCluster{}
			spc.Status.DataPlaneOperatorsManagedIdentities.Identities = tc.spcIdentities

			assert.Equal(t, tc.expectedMatch, syncer.desiredDataPlaneOperatorResourceIDsMatchSPC(tc.desiredResourceIDs, spc))
		})
	}
}

func TestUniqueDataPlaneOperatorResourceIDs(t *testing.T) {
	t.Parallel()

	identityA := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/identity-a"))
	mixedCaseIdentityA := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/Test-RG/providers/Microsoft.ManagedIdentity/userAssignedIdentities/Identity-A"))

	syncer := &fetchDataPlaneOperatorsManagedIdentitiesInfoSyncer{}

	t.Run("dedupes shared identity across operators", func(t *testing.T) {
		t.Parallel()
		unique := syncer.uniqueDataPlaneOperatorResourceIDs(map[string]*azcorearm.ResourceID{
			"operator-a": identityA,
			"operator-b": identityA,
		})
		require.NotNil(t, unique)
		assert.Equal(t, map[string]struct{}{
			strings.ToLower(identityA.String()): {},
		}, unique)
	})

	t.Run("lowercases resource ID keys", func(t *testing.T) {
		t.Parallel()
		unique := syncer.uniqueDataPlaneOperatorResourceIDs(map[string]*azcorearm.ResourceID{
			"operator-a": mixedCaseIdentityA,
		})
		require.NotNil(t, unique)
		assert.Equal(t, map[string]struct{}{
			strings.ToLower(identityA.String()): {},
		}, unique)
	})

	t.Run("nil resource ID returns nil", func(t *testing.T) {
		t.Parallel()
		unique := syncer.uniqueDataPlaneOperatorResourceIDs(map[string]*azcorearm.ResourceID{
			"operator-a": nil,
		})
		assert.Nil(t, unique)
	})
}

func TestFetchDataPlaneOperatorsManagedIdentitiesInfoNeedsWork(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	identityA := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/identity-a"))
	identityB := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/identity-b"))

	matchingDesired := map[string]struct{}{
		strings.ToLower(identityA.String()): {},
	}
	matchingSPCIdentities := map[string]*coreapi.ServiceProviderClusterDataPlaneOperatorManagedIdentity{
		strings.ToLower(identityA.String()): {
			ResourceID: identityA,
		},
	}

	testCases := []struct {
		name                string
		desiredResourceIDs  map[string]struct{}
		spcIdentities       map[string]*coreapi.ServiceProviderClusterDataPlaneOperatorManagedIdentity
		earliestRecheckTime *metav1.Time
		expectedNeedsWork   bool
	}{
		{
			name:                "matching identities with future recheck skips work",
			desiredResourceIDs:  matchingDesired,
			spcIdentities:       matchingSPCIdentities,
			earliestRecheckTime: &metav1.Time{Time: now.Add(time.Hour)},
			expectedNeedsWork:   false,
		},
		{
			name:                "matching identities with past recheck needs work",
			desiredResourceIDs:  matchingDesired,
			spcIdentities:       matchingSPCIdentities,
			earliestRecheckTime: &metav1.Time{Time: now.Add(-time.Hour)},
			expectedNeedsWork:   true,
		},
		{
			name:                "matching identities with nil recheck needs work",
			desiredResourceIDs:  matchingDesired,
			spcIdentities:       matchingSPCIdentities,
			earliestRecheckTime: nil,
			expectedNeedsWork:   true,
		},
		{
			name: "mismatched identities ignore future recheck",
			desiredResourceIDs: map[string]struct{}{
				strings.ToLower(identityB.String()): {},
			},
			spcIdentities:       matchingSPCIdentities,
			earliestRecheckTime: &metav1.Time{Time: now.Add(time.Hour)},
			expectedNeedsWork:   true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			syncer := &fetchDataPlaneOperatorsManagedIdentitiesInfoSyncer{
				clock: clocktesting.NewFakePassiveClock(now),
			}
			spc := &coreapi.ServiceProviderCluster{}
			spc.Status.DataPlaneOperatorsManagedIdentities.Identities = tc.spcIdentities
			spc.Status.DataPlaneOperatorsManagedIdentities.EarliestRecheckTime = tc.earliestRecheckTime

			require.Equal(t, tc.expectedNeedsWork, syncer.needsWork(spc, tc.desiredResourceIDs))
		})
	}
}
