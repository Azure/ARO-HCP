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

package dataplaneworkloads

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clocktesting "k8s.io/utils/clock/testing"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
)

func TestDataPlaneOIDCFederationIntentSyncer_desiredDataPlaneOIDCFederationStatus(t *testing.T) {
	t.Parallel()

	identityA := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/identity-a"))
	identityB := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/identity-b"))

	resolvedA := buildTestResolvedARMManagedIdentityMetadata(identityA, "client-a", "principal-a", "tenant-a")
	keyA := strings.ToLower(identityA.String())
	targetA := coreapi.DataplaneOIDCFederationIdentityInstance{
		ClientID:    "client-a",
		PrincipalID: "principal-a",
		TenantID:    "tenant-a",
	}
	targetARotated := coreapi.DataplaneOIDCFederationIdentityInstance{
		ClientID:    "client-a-rotated",
		PrincipalID: "principal-a-rotated",
		TenantID:    "tenant-a",
	}

	keyB := strings.ToLower(identityB.String())
	ficA := metadataapi.Must(azcorearm.ParseResourceID(identityA.String() + "/federatedIdentityCredentials/fic-a"))
	ficB := metadataapi.Must(azcorearm.ParseResourceID(identityB.String() + "/federatedIdentityCredentials/fic-b"))

	unresolvedA := &coreapi.ManagedIdentityMetadata{
		ResourceID: identityA,
	}

	hardcodedOnlyA := &coreapi.ManagedIdentityMetadata{
		ResourceID: identityA,
		MetadataFromHardcodedIdentity: &coreapi.IdentityMetadataValue{
			ClientID:    ptr.To("hardcoded-client"),
			PrincipalID: ptr.To("hardcoded-principal"),
			TenantID:    ptr.To("hardcoded-tenant"),
		},
	}

	testCCMOperatorName := "cloud-controller-manager"

	since := metav1.NewTime(time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC))
	alreadyDeconfiguringAt := metav1.NewTime(time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC))
	previousDeconfigureAt := metav1.NewTime(time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC))

	testCases := []struct {
		name                            string
		desiredDataPlaneOperators       map[string]*azcorearm.ResourceID
		existingIdentityMetadataDetails map[string]*coreapi.ManagedIdentityMetadata
		existing                        map[coreapi.DataplaneOIDCFederationAssignmentKey]*coreapi.DataplaneOIDCFederationAssignmentStatus
		want                            map[coreapi.DataplaneOIDCFederationAssignmentKey]*coreapi.DataplaneOIDCFederationAssignmentStatus
	}{
		{
			name: "empty operators and empty federation yields nil",
		},
		{
			name: "resolved data-plane identity is added",
			desiredDataPlaneOperators: map[string]*azcorearm.ResourceID{
				testCCMOperatorName: identityA,
			},
			existingIdentityMetadataDetails: map[string]*coreapi.ManagedIdentityMetadata{
				strings.ToLower(identityA.String()): resolvedA,
			},
			want: map[coreapi.DataplaneOIDCFederationAssignmentKey]*coreapi.DataplaneOIDCFederationAssignmentStatus{
				buildTestOIDCAssignmentKey(keyA, testCCMOperatorName): {TargetIdentity: targetA},
			},
		},
		{
			name: "shared data-plane identity creates one assignment per operator",
			desiredDataPlaneOperators: map[string]*azcorearm.ResourceID{
				testCCMOperatorName: identityA,
				"ingress":           identityA,
			},
			existingIdentityMetadataDetails: map[string]*coreapi.ManagedIdentityMetadata{
				strings.ToLower(identityA.String()): resolvedA,
			},
			want: map[coreapi.DataplaneOIDCFederationAssignmentKey]*coreapi.DataplaneOIDCFederationAssignmentStatus{
				buildTestOIDCAssignmentKey(keyA, testCCMOperatorName): {TargetIdentity: targetA},
				buildTestOIDCAssignmentKey(keyA, "ingress"):           {TargetIdentity: targetA},
			},
		},
		{
			name: "one operator leaving a shared identity sets deconfigure timestamp only for that operator",
			desiredDataPlaneOperators: map[string]*azcorearm.ResourceID{
				testCCMOperatorName: identityA,
			},
			existingIdentityMetadataDetails: map[string]*coreapi.ManagedIdentityMetadata{
				strings.ToLower(identityA.String()): resolvedA,
			},
			existing: map[coreapi.DataplaneOIDCFederationAssignmentKey]*coreapi.DataplaneOIDCFederationAssignmentStatus{
				buildTestOIDCAssignmentKey(keyA, testCCMOperatorName): buildTestOIDCAssignmentWithTarget(targetA, buildTestOIDCAssignmentEnsured(targetA, []*azcorearm.ResourceID{ficA})),
				buildTestOIDCAssignmentKey(keyA, "ingress"):           buildTestOIDCAssignmentWithTarget(targetA, buildTestOIDCAssignmentEnsured(targetA, []*azcorearm.ResourceID{ficB})),
			},
			want: map[coreapi.DataplaneOIDCFederationAssignmentKey]*coreapi.DataplaneOIDCFederationAssignmentStatus{
				buildTestOIDCAssignmentKey(keyA, testCCMOperatorName): buildTestOIDCAssignmentWithTarget(targetA, buildTestOIDCAssignmentEnsured(targetA, []*azcorearm.ResourceID{ficA})),
				buildTestOIDCAssignmentKey(keyA, "ingress"): {
					TargetIdentity:       targetA,
					EnsuredIdentity:      ptr.To(targetA),
					AzureResources:       []*azcorearm.ResourceID{ficB},
					DeconfigureTimestamp: &since,
				},
			},
		},
		{
			name: "one operator leaving a shared identity with unresolved ARM sets deconfigure timestamp only for that operator",
			desiredDataPlaneOperators: map[string]*azcorearm.ResourceID{
				testCCMOperatorName: identityA,
			},
			existingIdentityMetadataDetails: map[string]*coreapi.ManagedIdentityMetadata{
				strings.ToLower(identityA.String()): unresolvedA,
			},
			existing: map[coreapi.DataplaneOIDCFederationAssignmentKey]*coreapi.DataplaneOIDCFederationAssignmentStatus{
				buildTestOIDCAssignmentKey(keyA, testCCMOperatorName): buildTestOIDCAssignmentWithTarget(targetA, buildTestOIDCAssignmentEnsured(targetA, []*azcorearm.ResourceID{ficA})),
				buildTestOIDCAssignmentKey(keyA, "ingress"):           buildTestOIDCAssignmentWithTarget(targetA, buildTestOIDCAssignmentEnsured(targetA, []*azcorearm.ResourceID{ficB})),
			},
			want: map[coreapi.DataplaneOIDCFederationAssignmentKey]*coreapi.DataplaneOIDCFederationAssignmentStatus{
				buildTestOIDCAssignmentKey(keyA, testCCMOperatorName): buildTestOIDCAssignmentWithTarget(targetA, buildTestOIDCAssignmentEnsured(targetA, []*azcorearm.ResourceID{ficA})),
				buildTestOIDCAssignmentKey(keyA, "ingress"): {
					TargetIdentity:       targetA,
					EnsuredIdentity:      ptr.To(targetA),
					AzureResources:       []*azcorearm.ResourceID{ficB},
					DeconfigureTimestamp: &since,
				},
			},
		},
		{
			name: "unresolved ARM metadata is not added",
			desiredDataPlaneOperators: map[string]*azcorearm.ResourceID{
				testCCMOperatorName: identityA,
			},
			existingIdentityMetadataDetails: map[string]*coreapi.ManagedIdentityMetadata{
				strings.ToLower(identityA.String()): unresolvedA,
			},
		},
		{
			name: "identity missing from ManagedIdentityDetails is not added",
			desiredDataPlaneOperators: map[string]*azcorearm.ResourceID{
				testCCMOperatorName: identityA,
			},
		},
		{
			name: "hardcoded-identity metadata is not used for data-plane federation",
			desiredDataPlaneOperators: map[string]*azcorearm.ResourceID{
				testCCMOperatorName: identityA,
			},
			existingIdentityMetadataDetails: map[string]*coreapi.ManagedIdentityMetadata{
				strings.ToLower(identityA.String()): hardcodedOnlyA,
			},
		},
		{
			name: "identity with ARM metadata that is not desired by a data plane operator is not added",
			existingIdentityMetadataDetails: map[string]*coreapi.ManagedIdentityMetadata{
				strings.ToLower(identityA.String()): resolvedA,
			},
		},
		{
			name: "still-desired resolved assignment keeps EnsuredIdentity",
			desiredDataPlaneOperators: map[string]*azcorearm.ResourceID{
				testCCMOperatorName: identityA,
			},
			existingIdentityMetadataDetails: map[string]*coreapi.ManagedIdentityMetadata{
				strings.ToLower(identityA.String()): resolvedA,
			},
			existing: map[coreapi.DataplaneOIDCFederationAssignmentKey]*coreapi.DataplaneOIDCFederationAssignmentStatus{
				buildTestOIDCAssignmentKey(keyA, testCCMOperatorName): buildTestOIDCAssignmentWithTarget(targetA, buildTestOIDCAssignmentEnsured(targetA, nil)),
			},
			want: map[coreapi.DataplaneOIDCFederationAssignmentKey]*coreapi.DataplaneOIDCFederationAssignmentStatus{
				buildTestOIDCAssignmentKey(keyA, testCCMOperatorName): buildTestOIDCAssignmentWithTarget(targetA, buildTestOIDCAssignmentEnsured(targetA, nil)),
			},
		},
		{
			name: "desired again clears DeconfigureTimestamp",
			desiredDataPlaneOperators: map[string]*azcorearm.ResourceID{
				testCCMOperatorName: identityA,
			},
			existingIdentityMetadataDetails: map[string]*coreapi.ManagedIdentityMetadata{
				strings.ToLower(identityA.String()): resolvedA,
			},
			existing: map[coreapi.DataplaneOIDCFederationAssignmentKey]*coreapi.DataplaneOIDCFederationAssignmentStatus{
				buildTestOIDCAssignmentKey(keyA, testCCMOperatorName): buildTestOIDCAssignmentWithTarget(targetA, buildTestOIDCAssignmentDeconfigure(&previousDeconfigureAt, nil, nil)),
			},
			want: map[coreapi.DataplaneOIDCFederationAssignmentKey]*coreapi.DataplaneOIDCFederationAssignmentStatus{
				buildTestOIDCAssignmentKey(keyA, testCCMOperatorName): {TargetIdentity: targetA},
			},
		},
		{
			name: "desired again while draining keeps EnsuredIdentity and FIC lists and clears DeconfigureTimestamp",
			desiredDataPlaneOperators: map[string]*azcorearm.ResourceID{
				testCCMOperatorName: identityA,
			},
			existingIdentityMetadataDetails: map[string]*coreapi.ManagedIdentityMetadata{
				strings.ToLower(identityA.String()): resolvedA,
			},
			existing: map[coreapi.DataplaneOIDCFederationAssignmentKey]*coreapi.DataplaneOIDCFederationAssignmentStatus{
				buildTestOIDCAssignmentKey(keyA, testCCMOperatorName): buildTestOIDCAssignmentWithTarget(targetA, &coreapi.DataplaneOIDCFederationAssignmentStatus{
					EnsuredIdentity:      ptr.To(targetA),
					AzureResources:       []*azcorearm.ResourceID{ficA},
					DeconfigureTimestamp: &previousDeconfigureAt,
				}),
			},
			want: map[coreapi.DataplaneOIDCFederationAssignmentKey]*coreapi.DataplaneOIDCFederationAssignmentStatus{
				buildTestOIDCAssignmentKey(keyA, testCCMOperatorName): buildTestOIDCAssignmentWithTarget(targetA, buildTestOIDCAssignmentEnsured(targetA, []*azcorearm.ResourceID{ficA})),
			},
		},
		{
			name: "operator that left DataPlaneOperators gets DeconfigureTimestamp",
			existing: map[coreapi.DataplaneOIDCFederationAssignmentKey]*coreapi.DataplaneOIDCFederationAssignmentStatus{
				buildTestOIDCAssignmentKey(keyA, testCCMOperatorName): buildTestOIDCAssignmentWithTarget(targetA, buildTestOIDCAssignmentEnsured(targetA, []*azcorearm.ResourceID{ficA})),
			},
			want: map[coreapi.DataplaneOIDCFederationAssignmentKey]*coreapi.DataplaneOIDCFederationAssignmentStatus{
				buildTestOIDCAssignmentKey(keyA, testCCMOperatorName): {
					TargetIdentity:       targetA,
					EnsuredIdentity:      ptr.To(targetA),
					AzureResources:       []*azcorearm.ResourceID{ficA},
					DeconfigureTimestamp: &since,
				},
			},
		},
		{
			name: "already draining assignment keeps its DeconfigureTimestamp",
			existing: map[coreapi.DataplaneOIDCFederationAssignmentKey]*coreapi.DataplaneOIDCFederationAssignmentStatus{
				buildTestOIDCAssignmentKey(keyA, testCCMOperatorName): buildTestOIDCAssignmentWithTarget(targetA, buildTestOIDCAssignmentDeconfigure(&alreadyDeconfiguringAt, []*azcorearm.ResourceID{ficA}, nil)),
			},
			want: map[coreapi.DataplaneOIDCFederationAssignmentKey]*coreapi.DataplaneOIDCFederationAssignmentStatus{
				buildTestOIDCAssignmentKey(keyA, testCCMOperatorName): buildTestOIDCAssignmentWithTarget(targetA, buildTestOIDCAssignmentDeconfigure(&alreadyDeconfiguringAt, []*azcorearm.ResourceID{ficA}, nil)),
			},
		},
		{
			name: "assignment with no tracked FICs that left operators is dropped",
			existing: map[coreapi.DataplaneOIDCFederationAssignmentKey]*coreapi.DataplaneOIDCFederationAssignmentStatus{
				buildTestOIDCAssignmentKey(keyA, testCCMOperatorName): {TargetIdentity: targetA},
			},
		},
		{
			name: "assignment with only PendingAzureResources that left operators is stamped",
			existing: map[coreapi.DataplaneOIDCFederationAssignmentKey]*coreapi.DataplaneOIDCFederationAssignmentStatus{
				buildTestOIDCAssignmentKey(keyA, testCCMOperatorName): {
					TargetIdentity:        targetA,
					PendingAzureResources: []*azcorearm.ResourceID{ficA},
				},
			},
			want: map[coreapi.DataplaneOIDCFederationAssignmentKey]*coreapi.DataplaneOIDCFederationAssignmentStatus{
				buildTestOIDCAssignmentKey(keyA, testCCMOperatorName): {
					TargetIdentity:        targetA,
					PendingAzureResources: []*azcorearm.ResourceID{ficA},
					DeconfigureTimestamp:  &since,
				},
			},
		},
		{
			name: "unresolved ARM metadata leaves a still-desired assignment as-is",
			desiredDataPlaneOperators: map[string]*azcorearm.ResourceID{
				testCCMOperatorName: identityA,
			},
			existingIdentityMetadataDetails: map[string]*coreapi.ManagedIdentityMetadata{
				strings.ToLower(identityA.String()): unresolvedA,
			},
			existing: map[coreapi.DataplaneOIDCFederationAssignmentKey]*coreapi.DataplaneOIDCFederationAssignmentStatus{
				buildTestOIDCAssignmentKey(keyA, testCCMOperatorName): buildTestOIDCAssignmentWithTarget(targetA, buildTestOIDCAssignmentEnsured(targetA, nil)),
			},
			want: map[coreapi.DataplaneOIDCFederationAssignmentKey]*coreapi.DataplaneOIDCFederationAssignmentStatus{
				buildTestOIDCAssignmentKey(keyA, testCCMOperatorName): buildTestOIDCAssignmentWithTarget(targetA, buildTestOIDCAssignmentEnsured(targetA, nil)),
			},
		},
		{
			name: "TargetIdentity change clears EnsuredIdentity and FIC lists on the same key",
			desiredDataPlaneOperators: map[string]*azcorearm.ResourceID{
				testCCMOperatorName: identityA,
			},
			existingIdentityMetadataDetails: map[string]*coreapi.ManagedIdentityMetadata{
				strings.ToLower(identityA.String()): buildTestResolvedARMManagedIdentityMetadata(identityA, "client-a-rotated", "principal-a-rotated", "tenant-a"),
			},
			existing: map[coreapi.DataplaneOIDCFederationAssignmentKey]*coreapi.DataplaneOIDCFederationAssignmentStatus{
				buildTestOIDCAssignmentKey(keyA, testCCMOperatorName): buildTestOIDCAssignmentWithTarget(targetA, &coreapi.DataplaneOIDCFederationAssignmentStatus{
					EnsuredIdentity:       ptr.To(targetA),
					AzureResources:        []*azcorearm.ResourceID{ficA},
					PendingAzureResources: []*azcorearm.ResourceID{ficA},
				}),
			},
			want: map[coreapi.DataplaneOIDCFederationAssignmentKey]*coreapi.DataplaneOIDCFederationAssignmentStatus{
				buildTestOIDCAssignmentKey(keyA, testCCMOperatorName): {TargetIdentity: targetARotated},
			},
		},
		{
			name: "identity recreation clears both operators sharing the identity and drops a draining neighbor",
			desiredDataPlaneOperators: map[string]*azcorearm.ResourceID{
				testCCMOperatorName: identityA,
				"ingress":           identityA,
			},
			existingIdentityMetadataDetails: map[string]*coreapi.ManagedIdentityMetadata{
				strings.ToLower(identityA.String()): buildTestResolvedARMManagedIdentityMetadata(identityA, "client-a-rotated", "principal-a-rotated", "tenant-a"),
			},
			existing: map[coreapi.DataplaneOIDCFederationAssignmentKey]*coreapi.DataplaneOIDCFederationAssignmentStatus{
				buildTestOIDCAssignmentKey(keyA, testCCMOperatorName): buildTestOIDCAssignmentWithTarget(targetA, buildTestOIDCAssignmentEnsured(targetA, []*azcorearm.ResourceID{ficA})),
				buildTestOIDCAssignmentKey(keyA, "ingress"):           buildTestOIDCAssignmentWithTarget(targetA, buildTestOIDCAssignmentEnsured(targetA, []*azcorearm.ResourceID{ficB})),
				buildTestOIDCAssignmentKey(keyA, "image-registry"): buildTestOIDCAssignmentWithTarget(targetA, buildTestOIDCAssignmentDeconfigure(
					&previousDeconfigureAt,
					[]*azcorearm.ResourceID{ficA},
					nil,
				)),
			},
			want: map[coreapi.DataplaneOIDCFederationAssignmentKey]*coreapi.DataplaneOIDCFederationAssignmentStatus{
				buildTestOIDCAssignmentKey(keyA, testCCMOperatorName): {TargetIdentity: targetARotated},
				buildTestOIDCAssignmentKey(keyA, "ingress"):           {TargetIdentity: targetARotated},
			},
		},
		{
			name: "identity recreation clears only assignments whose TargetIdentity does not match ARM",
			desiredDataPlaneOperators: map[string]*azcorearm.ResourceID{
				testCCMOperatorName: identityA,
			},
			existingIdentityMetadataDetails: map[string]*coreapi.ManagedIdentityMetadata{
				strings.ToLower(identityA.String()): buildTestResolvedARMManagedIdentityMetadata(identityA, "client-a-rotated", "principal-a-rotated", "tenant-a"),
			},
			existing: map[coreapi.DataplaneOIDCFederationAssignmentKey]*coreapi.DataplaneOIDCFederationAssignmentStatus{
				buildTestOIDCAssignmentKey(keyA, testCCMOperatorName): buildTestOIDCAssignmentWithTarget(targetARotated, buildTestOIDCAssignmentEnsured(targetARotated, []*azcorearm.ResourceID{ficA})),
				buildTestOIDCAssignmentKey(keyA, "image-registry"): buildTestOIDCAssignmentWithTarget(targetA, buildTestOIDCAssignmentDeconfigure(
					&previousDeconfigureAt,
					[]*azcorearm.ResourceID{ficA},
					nil,
				)),
			},
			want: map[coreapi.DataplaneOIDCFederationAssignmentKey]*coreapi.DataplaneOIDCFederationAssignmentStatus{
				buildTestOIDCAssignmentKey(keyA, testCCMOperatorName): buildTestOIDCAssignmentWithTarget(targetARotated, buildTestOIDCAssignmentEnsured(targetARotated, []*azcorearm.ResourceID{ficA})),
			},
		},
		{
			name: "operator moved to a new identity adds that assignment and stamps DeconfigureTimestamp on the old one",
			desiredDataPlaneOperators: map[string]*azcorearm.ResourceID{
				testCCMOperatorName: identityA,
			},
			existingIdentityMetadataDetails: map[string]*coreapi.ManagedIdentityMetadata{
				strings.ToLower(identityA.String()): resolvedA,
			},
			existing: map[coreapi.DataplaneOIDCFederationAssignmentKey]*coreapi.DataplaneOIDCFederationAssignmentStatus{
				buildTestOIDCAssignmentKey(keyB, testCCMOperatorName): buildTestOIDCAssignmentWithTarget(targetA, buildTestOIDCAssignmentEnsured(targetA, []*azcorearm.ResourceID{ficB})),
			},
			want: map[coreapi.DataplaneOIDCFederationAssignmentKey]*coreapi.DataplaneOIDCFederationAssignmentStatus{
				buildTestOIDCAssignmentKey(keyA, testCCMOperatorName): {TargetIdentity: targetA},
				buildTestOIDCAssignmentKey(keyB, testCCMOperatorName): {
					TargetIdentity:       targetA,
					EnsuredIdentity:      ptr.To(targetA),
					AzureResources:       []*azcorearm.ResourceID{ficB},
					DeconfigureTimestamp: &since,
				},
			},
		},
		{
			name: "ARM metadata for a UAMI that is no longer a data-plane operator does not keep federation",
			existingIdentityMetadataDetails: map[string]*coreapi.ManagedIdentityMetadata{
				strings.ToLower(identityA.String()): resolvedA,
			},
			existing: map[coreapi.DataplaneOIDCFederationAssignmentKey]*coreapi.DataplaneOIDCFederationAssignmentStatus{
				buildTestOIDCAssignmentKey(keyA, testCCMOperatorName): buildTestOIDCAssignmentWithTarget(targetA, buildTestOIDCAssignmentEnsured(targetA, []*azcorearm.ResourceID{ficA})),
			},
			want: map[coreapi.DataplaneOIDCFederationAssignmentKey]*coreapi.DataplaneOIDCFederationAssignmentStatus{
				buildTestOIDCAssignmentKey(keyA, testCCMOperatorName): {
					TargetIdentity:       targetA,
					EnsuredIdentity:      ptr.To(targetA),
					AzureResources:       []*azcorearm.ResourceID{ficA},
					DeconfigureTimestamp: &since,
				},
			},
		},
		{
			name: "data-plane identity is federated from ARM metadata even when the same UAMI also has hardcoded identity metadata",
			desiredDataPlaneOperators: map[string]*azcorearm.ResourceID{
				testCCMOperatorName: identityA,
			},
			existingIdentityMetadataDetails: map[string]*coreapi.ManagedIdentityMetadata{
				strings.ToLower(identityA.String()): {
					ResourceID: identityA,
					MetadataFromARMUserAssignedIdentitiesAPI: &coreapi.IdentityMetadataValue{
						ClientID:    ptr.To("client-a"),
						PrincipalID: ptr.To("principal-a"),
						TenantID:    ptr.To("tenant-a"),
					},
					MetadataFromHardcodedIdentity: &coreapi.IdentityMetadataValue{
						ClientID:    ptr.To("hardcoded-client"),
						PrincipalID: ptr.To("hardcoded-principal"),
						TenantID:    ptr.To("hardcoded-tenant"),
					},
				},
			},
			want: map[coreapi.DataplaneOIDCFederationAssignmentKey]*coreapi.DataplaneOIDCFederationAssignmentStatus{
				buildTestOIDCAssignmentKey(keyA, testCCMOperatorName): {TargetIdentity: targetA},
			},
		},
	}

	syncer := &dataPlaneOIDCFederationIntentSyncer{
		clock: clocktesting.NewFakePassiveClock(since.Time),
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := syncer.desiredDataPlaneOIDCFederationStatus(context.Background(), tc.desiredDataPlaneOperators, tc.existingIdentityMetadataDetails, tc.existing)
			require.NoError(t, err)
			assertDataplaneOIDCFederationAssignments(t, got, tc.want)
		})
	}
}

func TestDataPlaneOIDCFederationIntentSyncer_desiredDataPlaneOIDCFederationStatus_errors(t *testing.T) {
	t.Parallel()

	identityA := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/identity-a"))
	keyA := strings.ToLower(identityA.String())
	syncer := &dataPlaneOIDCFederationIntentSyncer{
		clock: clocktesting.NewFakePassiveClock(time.Time{}),
	}

	testCases := []struct {
		name               string
		dataPlaneOperators map[string]*azcorearm.ResourceID
		details            map[string]*coreapi.ManagedIdentityMetadata
		current            map[coreapi.DataplaneOIDCFederationAssignmentKey]*coreapi.DataplaneOIDCFederationAssignmentStatus
		wantErrorSubstr    string
	}{
		{
			name: "nil federation status",
			current: map[coreapi.DataplaneOIDCFederationAssignmentKey]*coreapi.DataplaneOIDCFederationAssignmentStatus{
				buildTestOIDCAssignmentKey(keyA, "cloud-controller-manager"): nil,
			},
			wantErrorSubstr: "nil status",
		},
		{
			name: "nil ManagedIdentityDetails metadata",
			dataPlaneOperators: map[string]*azcorearm.ResourceID{
				"cloud-controller-manager": identityA,
			},
			details: map[string]*coreapi.ManagedIdentityMetadata{
				strings.ToLower(identityA.String()): nil,
			},
			wantErrorSubstr: "nil metadata",
		},
		{
			name: "empty operator name",
			dataPlaneOperators: map[string]*azcorearm.ResourceID{
				"": identityA,
			},
			wantErrorSubstr: "has an empty name",
		},
		{
			name: "nil operator resource ID",
			dataPlaneOperators: map[string]*azcorearm.ResourceID{
				"cloud-controller-manager": nil,
			},
			wantErrorSubstr: "has a nil resource ID",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := syncer.desiredDataPlaneOIDCFederationStatus(context.Background(), tc.dataPlaneOperators, tc.details, tc.current)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErrorSubstr)
		})
	}
}

func TestDataPlaneOIDCFederationIntentSyncer_SyncOnce(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	identityA := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/identity-a"))
	resolvedA := buildTestResolvedARMManagedIdentityMetadata(identityA, "client-a", "principal-a", "tenant-a")
	keyA := strings.ToLower(identityA.String())
	ficA := metadataapi.Must(azcorearm.ParseResourceID(identityA.String() + "/federatedIdentityCredentials/fic-a"))
	targetA := coreapi.DataplaneOIDCFederationIdentityInstance{
		ClientID:    "client-a",
		PrincipalID: "principal-a",
		TenantID:    "tenant-a",
	}
	deconfigureAt := metav1.NewTime(now)
	deletionTimestamp := &metav1.Time{Time: now}
	clusterServiceDeletionTimestamp := &metav1.Time{Time: now.Add(2 * time.Minute)}
	clusterServiceID := buildTestClusterServiceID()
	pendingClusterServiceID := buildTestClusterServiceID()

	ensuredWithFIC := map[coreapi.DataplaneOIDCFederationAssignmentKey]*coreapi.DataplaneOIDCFederationAssignmentStatus{
		buildTestOIDCAssignmentKey(keyA, "cloud-controller-manager"): buildTestOIDCAssignmentWithTarget(targetA, buildTestOIDCAssignmentEnsured(targetA, []*azcorearm.ResourceID{ficA})),
	}

	testCases := []struct {
		name                   string
		cluster                *coreapi.HCPOpenShiftCluster
		serviceProviderCluster *coreapi.ServiceProviderCluster
		want                   map[coreapi.DataplaneOIDCFederationAssignmentKey]*coreapi.DataplaneOIDCFederationAssignmentStatus
	}{
		{
			name:    "resolved data-plane identity is added",
			cluster: buildTestOIDCIntentCluster(t, map[string]*azcorearm.ResourceID{"cloud-controller-manager": identityA}),
			serviceProviderCluster: buildTestOIDCIntentServiceProviderCluster(map[string]*coreapi.ManagedIdentityMetadata{
				keyA: resolvedA,
			}, nil),
			want: map[coreapi.DataplaneOIDCFederationAssignmentKey]*coreapi.DataplaneOIDCFederationAssignmentStatus{
				buildTestOIDCAssignmentKey(keyA, "cloud-controller-manager"): {
					TargetIdentity: targetA,
				},
			},
		},
		{
			name:                   "stamps DeconfigureTimestamp when the operator leaves on a live cluster",
			cluster:                buildTestOIDCIntentCluster(t, nil),
			serviceProviderCluster: buildTestOIDCIntentServiceProviderCluster(nil, ensuredWithFIC),
			want: map[coreapi.DataplaneOIDCFederationAssignmentKey]*coreapi.DataplaneOIDCFederationAssignmentStatus{
				buildTestOIDCAssignmentKey(keyA, "cloud-controller-manager"): {
					TargetIdentity:       targetA,
					EnsuredIdentity:      ptr.To(targetA),
					AzureResources:       []*azcorearm.ResourceID{ficA},
					DeconfigureTimestamp: &deconfigureAt,
				},
			},
		},
		{
			name: "new deletion approach deconfigures after CS is confirmed gone even if PendingClusterServiceID is still set",
			cluster: buildTestOIDCIntentCluster(t, map[string]*azcorearm.ResourceID{"cloud-controller-manager": identityA}, func(cluster *coreapi.HCPOpenShiftCluster) {
				cluster.ServiceProviderProperties.UsesNewClusterDeletionApproach = true
				cluster.ServiceProviderProperties.DeletionTimestamp = deletionTimestamp
				cluster.ServiceProviderProperties.ClusterServiceDeletionTimestamp = clusterServiceDeletionTimestamp
				cluster.ServiceProviderProperties.PendingClusterServiceID = pendingClusterServiceID
			}),
			serviceProviderCluster: buildTestOIDCIntentServiceProviderCluster(map[string]*coreapi.ManagedIdentityMetadata{
				keyA: resolvedA,
			}, ensuredWithFIC),
			want: map[coreapi.DataplaneOIDCFederationAssignmentKey]*coreapi.DataplaneOIDCFederationAssignmentStatus{
				buildTestOIDCAssignmentKey(keyA, "cloud-controller-manager"): {
					TargetIdentity:       targetA,
					EnsuredIdentity:      ptr.To(targetA),
					AzureResources:       []*azcorearm.ResourceID{ficA},
					DeconfigureTimestamp: &deconfigureAt,
				},
			},
		},
		{
			name: "new deletion approach does not deconfigure before ClusterServiceDeletionTimestamp is set",
			cluster: buildTestOIDCIntentCluster(t, map[string]*azcorearm.ResourceID{"cloud-controller-manager": identityA}, func(cluster *coreapi.HCPOpenShiftCluster) {
				cluster.ServiceProviderProperties.UsesNewClusterDeletionApproach = true
				cluster.ServiceProviderProperties.DeletionTimestamp = deletionTimestamp
			}),
			serviceProviderCluster: buildTestOIDCIntentServiceProviderCluster(map[string]*coreapi.ManagedIdentityMetadata{
				keyA: resolvedA,
			}, ensuredWithFIC),
			want: map[coreapi.DataplaneOIDCFederationAssignmentKey]*coreapi.DataplaneOIDCFederationAssignmentStatus{
				buildTestOIDCAssignmentKey(keyA, "cloud-controller-manager"): buildTestOIDCAssignmentWithTarget(targetA, buildTestOIDCAssignmentEnsured(targetA, []*azcorearm.ResourceID{ficA})),
			},
		},
		{
			name: "new deletion approach does not deconfigure while ClusterServiceID is still set",
			cluster: buildTestOIDCIntentCluster(t, map[string]*azcorearm.ResourceID{"cloud-controller-manager": identityA}, func(cluster *coreapi.HCPOpenShiftCluster) {
				cluster.ServiceProviderProperties.UsesNewClusterDeletionApproach = true
				cluster.ServiceProviderProperties.DeletionTimestamp = deletionTimestamp
				cluster.ServiceProviderProperties.ClusterServiceDeletionTimestamp = clusterServiceDeletionTimestamp
				cluster.ServiceProviderProperties.ClusterServiceID = clusterServiceID
			}),
			serviceProviderCluster: buildTestOIDCIntentServiceProviderCluster(map[string]*coreapi.ManagedIdentityMetadata{
				keyA: resolvedA,
			}, ensuredWithFIC),
			want: map[coreapi.DataplaneOIDCFederationAssignmentKey]*coreapi.DataplaneOIDCFederationAssignmentStatus{
				buildTestOIDCAssignmentKey(keyA, "cloud-controller-manager"): buildTestOIDCAssignmentWithTarget(targetA, buildTestOIDCAssignmentEnsured(targetA, []*azcorearm.ResourceID{ficA})),
			},
		},
		{
			name: "legacy deletion approach does not deconfigure while ClusterServiceID is already cleared",
			cluster: buildTestOIDCIntentCluster(t, map[string]*azcorearm.ResourceID{"cloud-controller-manager": identityA}, func(cluster *coreapi.HCPOpenShiftCluster) {
				cluster.ServiceProviderProperties.DeletionTimestamp = deletionTimestamp
			}),
			serviceProviderCluster: buildTestOIDCIntentServiceProviderCluster(map[string]*coreapi.ManagedIdentityMetadata{
				keyA: resolvedA,
			}, ensuredWithFIC),
			want: map[coreapi.DataplaneOIDCFederationAssignmentKey]*coreapi.DataplaneOIDCFederationAssignmentStatus{
				buildTestOIDCAssignmentKey(keyA, "cloud-controller-manager"): buildTestOIDCAssignmentWithTarget(targetA, buildTestOIDCAssignmentEnsured(targetA, []*azcorearm.ResourceID{ficA})),
			},
		},
		{
			name: "legacy deletion approach does not deconfigure while ClusterServiceID is still set",
			cluster: buildTestOIDCIntentCluster(t, map[string]*azcorearm.ResourceID{"cloud-controller-manager": identityA}, func(cluster *coreapi.HCPOpenShiftCluster) {
				cluster.ServiceProviderProperties.DeletionTimestamp = deletionTimestamp
				cluster.ServiceProviderProperties.ClusterServiceID = clusterServiceID
			}),
			serviceProviderCluster: buildTestOIDCIntentServiceProviderCluster(map[string]*coreapi.ManagedIdentityMetadata{
				keyA: resolvedA,
			}, ensuredWithFIC),
			want: map[coreapi.DataplaneOIDCFederationAssignmentKey]*coreapi.DataplaneOIDCFederationAssignmentStatus{
				buildTestOIDCAssignmentKey(keyA, "cloud-controller-manager"): buildTestOIDCAssignmentWithTarget(targetA, buildTestOIDCAssignmentEnsured(targetA, []*azcorearm.ResourceID{ficA})),
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			mockResourcesDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{tc.cluster, tc.serviceProviderCluster})
			require.NoError(t, err)

			syncer := &dataPlaneOIDCFederationIntentSyncer{
				clock:                        clocktesting.NewFakePassiveClock(now),
				clusterLister:                &corelistertesting.DBClusterLister{ResourcesDBClient: mockResourcesDB},
				serviceProviderClusterLister: &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: mockResourcesDB},
				resourcesDBClient:            mockResourcesDB,
			}

			err = syncer.SyncOnce(ctx, controllerutils.HCPClusterKey{
				SubscriptionID:    testSubscriptionID,
				ResourceGroupName: testResourceGroupName,
				HCPClusterName:    testClusterName,
			})
			require.NoError(t, err)

			updated, err := mockResourcesDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
			require.NoError(t, err)
			assertDataplaneOIDCFederationAssignments(t, updated.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation, tc.want)
		})
	}
}

func TestDataPlaneOIDCFederationIntentSyncer_targetIdentityFromARMUserAssignedIdentities(t *testing.T) {
	t.Parallel()

	syncer := &dataPlaneOIDCFederationIntentSyncer{}
	identity := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/Test-RG/providers/Microsoft.ManagedIdentity/userAssignedIdentities/Identity-A"))

	testCases := []struct {
		name     string
		metadata *coreapi.ManagedIdentityMetadata
		want     coreapi.DataplaneOIDCFederationIdentityInstance
		wantOK   bool
	}{
		{
			name: "resolved ARM user assigned identities metadata",
			metadata: &coreapi.ManagedIdentityMetadata{
				ResourceID: identity,
				MetadataFromARMUserAssignedIdentitiesAPI: &coreapi.IdentityMetadataValue{
					ClientID:    ptr.To("arm-client"),
					PrincipalID: ptr.To("arm-principal"),
					TenantID:    ptr.To("arm-tenant"),
				},
				MetadataFromHardcodedIdentity: &coreapi.IdentityMetadataValue{
					ClientID:    ptr.To("hardcoded-client"),
					PrincipalID: ptr.To("hardcoded-principal"),
					TenantID:    ptr.To("hardcoded-tenant"),
				},
			},
			want: coreapi.DataplaneOIDCFederationIdentityInstance{
				ClientID:    "arm-client",
				PrincipalID: "arm-principal",
				TenantID:    "arm-tenant",
			},
			wantOK: true,
		},
		{
			name: "missing ARM user assigned identities metadata",
			metadata: &coreapi.ManagedIdentityMetadata{
				ResourceID: identity,
				MetadataFromHardcodedIdentity: &coreapi.IdentityMetadataValue{
					ClientID:    ptr.To("hardcoded-client"),
					PrincipalID: ptr.To("hardcoded-principal"),
					TenantID:    ptr.To("hardcoded-tenant"),
				},
			},
		},
		{
			name: "ARM retrieval error is unresolved",
			metadata: &coreapi.ManagedIdentityMetadata{
				ResourceID: identity,
				MetadataFromARMUserAssignedIdentitiesAPI: &coreapi.IdentityMetadataValue{
					RetrievalError: ptr.To("ResourceNotFound"),
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := syncer.targetIdentityFromARMUserAssignedIdentities(tc.metadata)
			assert.Equal(t, tc.wantOK, ok)
			assert.Equal(t, tc.want, got)
		})
	}
}

func buildTestResolvedARMManagedIdentityMetadata(resourceID *azcorearm.ResourceID, clientID, principalID, tenantID string) *coreapi.ManagedIdentityMetadata {
	return &coreapi.ManagedIdentityMetadata{
		ResourceID: resourceID,
		MetadataFromARMUserAssignedIdentitiesAPI: &coreapi.IdentityMetadataValue{
			ClientID:    ptr.To(clientID),
			PrincipalID: ptr.To(principalID),
			TenantID:    ptr.To(tenantID),
		},
	}
}

func buildTestOIDCIntentCluster(t *testing.T, operators map[string]*azcorearm.ResourceID, opts ...func(*coreapi.HCPOpenShiftCluster)) *coreapi.HCPOpenShiftCluster {
	t.Helper()
	cluster := buildTestClusterWithIdentities(t, testClusterName, nil, operators)
	for _, opt := range opts {
		opt(cluster)
	}
	return cluster
}

func buildTestOIDCIntentServiceProviderCluster(
	details map[string]*coreapi.ManagedIdentityMetadata,
	federation map[coreapi.DataplaneOIDCFederationAssignmentKey]*coreapi.DataplaneOIDCFederationAssignmentStatus,
) *coreapi.ServiceProviderCluster {
	serviceProviderCluster := buildTestServiceProviderClusterWithIdentities(testClusterName, nil, nil)
	if details != nil {
		copied := make(map[string]*coreapi.ManagedIdentityMetadata, len(details))
		for key, metadata := range details {
			copied[key] = metadata.DeepCopy()
		}
		serviceProviderCluster.Status.ManagedIdentityDetails = copied
	}
	if federation != nil {
		copied := make(map[coreapi.DataplaneOIDCFederationAssignmentKey]*coreapi.DataplaneOIDCFederationAssignmentStatus, len(federation))
		for key, status := range federation {
			copied[key] = status.DeepCopy()
		}
		serviceProviderCluster.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation = copied
	}
	return serviceProviderCluster
}
