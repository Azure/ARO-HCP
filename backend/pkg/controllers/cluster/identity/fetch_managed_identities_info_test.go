// Copyright 2026 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package identity

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clocktesting "k8s.io/utils/clock/testing"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/msi/armmsi"
	"github.com/Azure/msi-dataplane/pkg/dataplane"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
)

const (
	testDataPlaneOperatorName       = "ingress"
	testDataPlaneOperatorResourceID = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/ingress"
)

type fakeUserAssignedIdentitiesClientByName struct {
	getByName map[string]armmsi.UserAssignedIdentitiesClientGetResponse
	errByName map[string]error
}

var _ azureclient.UserAssignedIdentitiesClient = (*fakeUserAssignedIdentitiesClientByName)(nil)

func (f *fakeUserAssignedIdentitiesClientByName) Get(_ context.Context, _, name string, _ *armmsi.UserAssignedIdentitiesClientGetOptions) (armmsi.UserAssignedIdentitiesClientGetResponse, error) {
	key := strings.ToLower(name)
	if err, ok := f.errByName[key]; ok {
		return armmsi.UserAssignedIdentitiesClientGetResponse{}, err
	}
	if resp, ok := f.getByName[key]; ok {
		return resp, nil
	}
	return armmsi.UserAssignedIdentitiesClientGetResponse{}, fmt.Errorf("unexpected Get for identity %s", name)
}

func (f *fakeUserAssignedIdentitiesClientByName) CreateOrUpdate(_ context.Context, _ string, _ string, _ armmsi.Identity, _ *armmsi.UserAssignedIdentitiesClientCreateOrUpdateOptions) (armmsi.UserAssignedIdentitiesClientCreateOrUpdateResponse, error) {
	panic("CreateOrUpdate not implemented in fakeUserAssignedIdentitiesClientByName")
}

func (f *fakeUserAssignedIdentitiesClientByName) Delete(_ context.Context, _ string, _ string, _ *armmsi.UserAssignedIdentitiesClientDeleteOptions) (armmsi.UserAssignedIdentitiesClientDeleteResponse, error) {
	panic("Delete not implemented in fakeUserAssignedIdentitiesClientByName")
}

func newTestUserAssignedIdentitiesClientGetResponse(clientID, principalID, tenantID string) armmsi.UserAssignedIdentitiesClientGetResponse {
	return armmsi.UserAssignedIdentitiesClientGetResponse{
		Identity: armmsi.Identity{
			Properties: &armmsi.UserAssignedIdentityProperties{
				ClientID:    ptr.To(clientID),
				PrincipalID: ptr.To(principalID),
				TenantID:    ptr.To(tenantID),
			},
		},
	}
}

func newTestClusterForManagedIdentities(opts ...func(*coreapi.HCPOpenShiftCluster)) *coreapi.HCPOpenShiftCluster {
	cluster := newTestClusterForFetch()
	cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.DataPlaneOperators = map[string]*azcorearm.ResourceID{
		testDataPlaneOperatorName: metadataapi.Must(azcorearm.ParseResourceID(testDataPlaneOperatorResourceID)),
	}
	for _, opt := range opts {
		opt(cluster)
	}
	return cluster
}

func newTestIdentityToResolve(resourceID string, isControlPlaneOperator, isDataPlaneOperator, isServiceManagedIdentity bool) *identityToResolve {
	return &identityToResolve{
		resourceID:                     metadataapi.Must(azcorearm.ParseResourceID(resourceID)),
		isControlPlaneOperatorIdentity: isControlPlaneOperator,
		isDataPlaneOperatorIdentity:    isDataPlaneOperator,
		isServiceManagedIdentity:       isServiceManagedIdentity,
	}
}

// assertWantStringPointer reports a test failure unless got and want are both
// nil, or both non-nil with equal string values. Pointer addresses are not
// compared.
func assertWantStringPointer(t *testing.T, got, want *string) {
	t.Helper()
	if want == nil {
		assert.Nil(t, got)
		return
	}
	require.NotNil(t, got)
	assert.Equal(t, *want, *got)
}

func assertIdentityMetadataValue(t *testing.T, got, want *coreapi.IdentityMetadataValue) {
	t.Helper()
	if want == nil {
		assert.Nil(t, got)
		return
	}
	require.NotNil(t, got)
	assertWantStringPointer(t, got.ClientID, want.ClientID)
	assertWantStringPointer(t, got.PrincipalID, want.PrincipalID)
	assertWantStringPointer(t, got.TenantID, want.TenantID)
	if want.RetrievalError == nil {
		assert.Nil(t, got.RetrievalError)
		return
	}
	require.NotNil(t, got.RetrievalError)
	assert.Contains(t, *got.RetrievalError, *want.RetrievalError)
}

func assertIdentityMetadataValues(t *testing.T, got, want map[string]*coreapi.IdentityMetadataValue) {
	t.Helper()
	if want == nil {
		assert.Nil(t, got)
		return
	}
	require.NotNil(t, got)
	require.Len(t, got, len(want))
	for wantResourceIDKey, wantValue := range want {
		require.Contains(t, got, wantResourceIDKey)
		assertIdentityMetadataValue(t, got[wantResourceIDKey], wantValue)
	}
}

func assertManagedIdentityDetails(t *testing.T, got, want map[string]*coreapi.ManagedIdentityMetadata) {
	t.Helper()
	if want == nil {
		assert.Nil(t, got)
		return
	}
	require.NotNil(t, got)
	require.Len(t, got, len(want))
	for wantResourceIDKey, wantMeta := range want {
		require.Contains(t, got, wantResourceIDKey)
		gotMeta := got[wantResourceIDKey]
		if wantMeta == nil {
			assert.Nil(t, gotMeta)
			continue
		}
		require.NotNil(t, gotMeta)
		if wantMeta.ResourceID == nil {
			assert.Nil(t, gotMeta.ResourceID)
		} else {
			require.NotNil(t, gotMeta.ResourceID)
			assert.Equal(t, wantMeta.ResourceID.String(), gotMeta.ResourceID.String())
		}
		assertIdentityMetadataValue(t, gotMeta.MetadataFromARMUserAssignedIdentitiesAPI, wantMeta.MetadataFromARMUserAssignedIdentitiesAPI)
		assertIdentityMetadataValue(t, gotMeta.MetadataFromManagedIdentitiesDataplaneService, wantMeta.MetadataFromManagedIdentitiesDataplaneService)
		assertIdentityMetadataValue(t, gotMeta.MetadataFromHardcodedIdentity, wantMeta.MetadataFromHardcodedIdentity)
	}
}

// assertErrorsContainSubstringsInAnyOrder reports a test failure unless each
// string in wantSubstrings is contained in a distinct error in errs. Matching
// does not depend on order. An error that is not matched by any remaining
// substring is an unexpected extra error. TrackError wraps the original
// message, so this compares substrings rather than exact Error() strings.
func assertErrorsContainSubstringsInAnyOrder(t *testing.T, errs []error, wantSubstrings []string) {
	t.Helper()
	remaining := make([]string, 0, len(errs))
	for _, err := range errs {
		require.Error(t, err)
		remaining = append(remaining, err.Error())
	}
	for _, want := range wantSubstrings {
		matched := -1
		for i, errStr := range remaining {
			if strings.Contains(errStr, want) {
				matched = i
				break
			}
		}
		if !assert.GreaterOrEqual(t, matched, 0, "expected an error containing %q, got %v", want, remaining) {
			continue
		}
		remaining = append(remaining[:matched], remaining[matched+1:]...)
	}
	assert.Empty(t, remaining, "unexpected extra errors")
}

func TestIdentityToResolve_appliesToARMUserAssignedIdentitiesAPI(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name     string
		identity identityToResolve
		want     bool
	}{
		{
			name: "no roles",
			want: false,
		},
		{
			name:     "control plane operator",
			identity: identityToResolve{isControlPlaneOperatorIdentity: true},
			want:     true,
		},
		{
			name:     "data plane operator",
			identity: identityToResolve{isDataPlaneOperatorIdentity: true},
			want:     true,
		},
		{
			name:     "service managed identity only",
			identity: identityToResolve{isServiceManagedIdentity: true},
			want:     false,
		},
		{
			name: "control plane and data plane operator",
			identity: identityToResolve{
				isControlPlaneOperatorIdentity: true,
				isDataPlaneOperatorIdentity:    true,
			},
			want: true,
		},
		{
			name: "control plane operator and service managed identity",
			identity: identityToResolve{
				isControlPlaneOperatorIdentity: true,
				isServiceManagedIdentity:       true,
			},
			want: true,
		},
		{
			name: "data plane operator and service managed identity",
			identity: identityToResolve{
				isDataPlaneOperatorIdentity: true,
				isServiceManagedIdentity:    true,
			},
			want: true,
		},
		{
			name: "control plane operator, data plane operator, and service managed identity",
			identity: identityToResolve{
				isControlPlaneOperatorIdentity: true,
				isDataPlaneOperatorIdentity:    true,
				isServiceManagedIdentity:       true,
			},
			want: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, tc.identity.appliesToARMUserAssignedIdentitiesAPI())
		})
	}
}

func TestIdentityToResolve_registeredWithManagedIdentitiesDataplane(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name     string
		identity identityToResolve
		want     bool
	}{
		{
			name: "no roles",
			want: false,
		},
		{
			name:     "control plane operator",
			identity: identityToResolve{isControlPlaneOperatorIdentity: true},
			want:     true,
		},
		{
			name:     "data plane operator",
			identity: identityToResolve{isDataPlaneOperatorIdentity: true},
			want:     false,
		},
		{
			name:     "service managed identity",
			identity: identityToResolve{isServiceManagedIdentity: true},
			want:     true,
		},
		{
			name: "control plane and data plane operator",
			identity: identityToResolve{
				isControlPlaneOperatorIdentity: true,
				isDataPlaneOperatorIdentity:    true,
			},
			want: true,
		},
		{
			name: "control plane operator and service managed identity",
			identity: identityToResolve{
				isControlPlaneOperatorIdentity: true,
				isServiceManagedIdentity:       true,
			},
			want: true,
		},
		{
			name: "data plane operator and service managed identity",
			identity: identityToResolve{
				isDataPlaneOperatorIdentity: true,
				isServiceManagedIdentity:    true,
			},
			want: true,
		},
		{
			name: "control plane operator, data plane operator, and service managed identity",
			identity: identityToResolve{
				isControlPlaneOperatorIdentity: true,
				isDataPlaneOperatorIdentity:    true,
				isServiceManagedIdentity:       true,
			},
			want: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, tc.identity.registeredWithManagedIdentitiesDataplane())
		})
	}
}

func TestFetchManagedIdentitiesInfoSyncer_collectIdentitiesToResolve(t *testing.T) {
	t.Parallel()

	lowerControlPlaneOperatorIdentityResourceIDStr := strings.ToLower(testControlPlaneOperatorIdentityResourceID)
	lowerServiceManagedIdentityResourceIDStr := strings.ToLower(testServiceManagedIdentityID)
	lowerDataPlaneOperatorIdentityResourceIDStr := strings.ToLower(testDataPlaneOperatorResourceID)

	type wantIdentity struct {
		isControlPlaneOperatorIdentity bool
		isDataPlaneOperatorIdentity    bool
		isServiceManagedIdentity       bool
	}

	testCases := []struct {
		name              string
		cluster           *coreapi.HCPOpenShiftCluster
		wantErrorsSubstrs []string
		wantIdentities    map[string]wantIdentity
	}{
		{
			name:    "collects control plane, data plane, and service managed identity",
			cluster: newTestClusterForManagedIdentities(),
			wantIdentities: map[string]wantIdentity{
				lowerControlPlaneOperatorIdentityResourceIDStr: {isControlPlaneOperatorIdentity: true},
				lowerServiceManagedIdentityResourceIDStr:       {isServiceManagedIdentity: true},
				lowerDataPlaneOperatorIdentityResourceIDStr:    {isDataPlaneOperatorIdentity: true},
			},
		},
		{
			name: "deduplicates shared resource IDs and marks both control-plane and data-plane when the same UAMI is used for both",
			cluster: newTestClusterForManagedIdentities(func(c *coreapi.HCPOpenShiftCluster) {
				c.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.DataPlaneOperators["shared"] =
					metadataapi.Must(azcorearm.ParseResourceID(strings.ToUpper(testControlPlaneOperatorIdentityResourceID)))
			}),
			wantIdentities: map[string]wantIdentity{
				lowerControlPlaneOperatorIdentityResourceIDStr: {
					isControlPlaneOperatorIdentity: true,
					isDataPlaneOperatorIdentity:    true,
				},
				lowerServiceManagedIdentityResourceIDStr:    {isServiceManagedIdentity: true},
				lowerDataPlaneOperatorIdentityResourceIDStr: {isDataPlaneOperatorIdentity: true},
			},
		},
		{
			name: "deduplicates shared resource IDs and marks both data-plane and SMI when the same UAMI is used for both",
			cluster: newTestClusterForManagedIdentities(func(c *coreapi.HCPOpenShiftCluster) {
				c.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.DataPlaneOperators[testDataPlaneOperatorName] =
					metadataapi.Must(azcorearm.ParseResourceID(strings.ToUpper(testServiceManagedIdentityID)))
			}),
			wantIdentities: map[string]wantIdentity{
				lowerControlPlaneOperatorIdentityResourceIDStr: {isControlPlaneOperatorIdentity: true},
				lowerServiceManagedIdentityResourceIDStr: {
					isDataPlaneOperatorIdentity: true,
					isServiceManagedIdentity:    true,
				},
			},
		},
		{
			name: "nil service managed identity is accumulated as an error and other identities are still collected",
			cluster: newTestClusterForManagedIdentities(func(c *coreapi.HCPOpenShiftCluster) {
				c.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ServiceManagedIdentity = nil
			}),
			wantErrorsSubstrs: []string{"unexpected nil identity Resource ID for service managed identity"},
			wantIdentities: map[string]wantIdentity{
				lowerControlPlaneOperatorIdentityResourceIDStr: {isControlPlaneOperatorIdentity: true},
				lowerDataPlaneOperatorIdentityResourceIDStr:    {isDataPlaneOperatorIdentity: true},
			},
		},
		{
			name: "nil control plane operator identity is accumulated as an error and other identities are still collected",
			cluster: newTestClusterForManagedIdentities(func(c *coreapi.HCPOpenShiftCluster) {
				c.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ControlPlaneOperators[testControlPlaneOperatorName] = nil
			}),
			wantErrorsSubstrs: []string{fmt.Sprintf("unexpected nil identity Resource ID for control plane operator %q", testControlPlaneOperatorName)},
			wantIdentities: map[string]wantIdentity{
				lowerServiceManagedIdentityResourceIDStr:    {isServiceManagedIdentity: true},
				lowerDataPlaneOperatorIdentityResourceIDStr: {isDataPlaneOperatorIdentity: true},
			},
		},
		{
			name: "nil data plane operator identity is accumulated as an error and other identities are still collected",
			cluster: newTestClusterForManagedIdentities(func(c *coreapi.HCPOpenShiftCluster) {
				c.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.DataPlaneOperators[testDataPlaneOperatorName] = nil
			}),
			wantErrorsSubstrs: []string{fmt.Sprintf("unexpected nil identity Resource ID for data plane operator %q", testDataPlaneOperatorName)},
			wantIdentities: map[string]wantIdentity{
				lowerControlPlaneOperatorIdentityResourceIDStr: {isControlPlaneOperatorIdentity: true},
				lowerServiceManagedIdentityResourceIDStr:       {isServiceManagedIdentity: true},
			},
		},
		{
			name: "empty control plane operator name is accumulated as an error and other identities are still collected",
			cluster: newTestClusterForManagedIdentities(func(c *coreapi.HCPOpenShiftCluster) {
				c.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ControlPlaneOperators[""] =
					metadataapi.Must(azcorearm.ParseResourceID(testOtherOperatorIdentityID))
			}),
			wantErrorsSubstrs: []string{"unexpected empty operator name for control plane operator"},
			wantIdentities: map[string]wantIdentity{
				lowerControlPlaneOperatorIdentityResourceIDStr: {isControlPlaneOperatorIdentity: true},
				lowerServiceManagedIdentityResourceIDStr:       {isServiceManagedIdentity: true},
				lowerDataPlaneOperatorIdentityResourceIDStr:    {isDataPlaneOperatorIdentity: true},
			},
		},
		{
			name: "empty data plane operator name is accumulated as an error and other identities are still collected",
			cluster: newTestClusterForManagedIdentities(func(c *coreapi.HCPOpenShiftCluster) {
				c.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.DataPlaneOperators[""] =
					metadataapi.Must(azcorearm.ParseResourceID(testOtherOperatorIdentityID))
			}),
			wantErrorsSubstrs: []string{"unexpected empty operator name for data plane operator"},
			wantIdentities: map[string]wantIdentity{
				lowerControlPlaneOperatorIdentityResourceIDStr: {isControlPlaneOperatorIdentity: true},
				lowerServiceManagedIdentityResourceIDStr:       {isServiceManagedIdentity: true},
				lowerDataPlaneOperatorIdentityResourceIDStr:    {isDataPlaneOperatorIdentity: true},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			syncer := &fetchManagedIdentitiesInfoSyncer{}
			got, errs := syncer.collectIdentitiesToResolve(tc.cluster)
			assertErrorsContainSubstringsInAnyOrder(t, errs, tc.wantErrorsSubstrs)
			require.Len(t, got, len(tc.wantIdentities))
			for resourceIDKey, want := range tc.wantIdentities {
				require.Contains(t, got, resourceIDKey)
				assert.Equal(t, want.isControlPlaneOperatorIdentity, got[resourceIDKey].isControlPlaneOperatorIdentity)
				assert.Equal(t, want.isDataPlaneOperatorIdentity, got[resourceIDKey].isDataPlaneOperatorIdentity)
				assert.Equal(t, want.isServiceManagedIdentity, got[resourceIDKey].isServiceManagedIdentity)
			}
		})
	}
}

func TestFetchManagedIdentitiesInfoSyncer_needsWork(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	lowerControlPlaneOperatorIdentityStr := strings.ToLower(testControlPlaneOperatorIdentityResourceID)
	matchingDesired := map[string]*identityToResolve{
		lowerControlPlaneOperatorIdentityStr: {
			resourceID:                     metadataapi.Must(azcorearm.ParseResourceID(testControlPlaneOperatorIdentityResourceID)),
			isControlPlaneOperatorIdentity: true,
		},
	}
	matchingStored := map[string]*coreapi.ManagedIdentityMetadata{
		lowerControlPlaneOperatorIdentityStr: {
			ResourceID: metadataapi.Must(azcorearm.ParseResourceID(testControlPlaneOperatorIdentityResourceID)),
		},
	}

	testCases := []struct {
		name                             string
		desired                          map[string]*identityToResolve
		stored                           map[string]*coreapi.ManagedIdentityMetadata
		earliestRecheckTimesByController map[string]*metav1.Time
		want                             bool
	}{
		{
			name:    "matching identities with future recheck skips work",
			desired: matchingDesired,
			stored:  matchingStored,
			earliestRecheckTimesByController: map[string]*metav1.Time{
				FetchManagedIdentitiesInfoControllerName: {Time: now.Add(time.Hour)},
			},
			want: false,
		},
		{
			name:    "matching identities with past recheck needs work",
			desired: matchingDesired,
			stored:  matchingStored,
			earliestRecheckTimesByController: map[string]*metav1.Time{
				FetchManagedIdentitiesInfoControllerName: {Time: now.Add(-time.Hour)},
			},
			want: true,
		},
		{
			name:    "matching identities with no recheck map needs work",
			desired: matchingDesired,
			stored:  matchingStored,
			want:    true,
		},
		{
			name:                             "matching identities with no recheck map entry needs work",
			desired:                          matchingDesired,
			stored:                           matchingStored,
			earliestRecheckTimesByController: map[string]*metav1.Time{},
			want:                             true,
		},
		{
			name:    "matching identities with explicit nil recheck entry needs work",
			desired: matchingDesired,
			stored:  matchingStored,
			earliestRecheckTimesByController: map[string]*metav1.Time{
				FetchManagedIdentitiesInfoControllerName: nil,
			},
			want: true,
		},
		{
			name: "mismatched identities ignore future recheck",
			desired: map[string]*identityToResolve{
				strings.ToLower(testDataPlaneOperatorResourceID): {
					resourceID: metadataapi.Must(azcorearm.ParseResourceID(testDataPlaneOperatorResourceID)),
				},
			},
			stored: matchingStored,
			earliestRecheckTimesByController: map[string]*metav1.Time{
				FetchManagedIdentitiesInfoControllerName: {Time: now.Add(time.Hour)},
			},
			want: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			syncer := &fetchManagedIdentitiesInfoSyncer{
				clock: clocktesting.NewFakePassiveClock(now),
			}
			serviceProviderCluster := &coreapi.ServiceProviderCluster{}
			serviceProviderCluster.Status.ManagedIdentityDetails = tc.stored
			serviceProviderCluster.Spec.EarliestRecheckTimesByController = tc.earliestRecheckTimesByController
			assert.Equal(t, tc.want, syncer.needsWork(serviceProviderCluster, tc.desired))
		})
	}
}

func TestFetchManagedIdentitiesInfoSyncer_resolveManagedIdentitiesMetadataFromHardcodedIdentity(t *testing.T) {
	t.Parallel()

	lowerControlPlaneOperatorIdentityResourceIDStr := strings.ToLower(testControlPlaneOperatorIdentityResourceID)
	lowerServiceManagedIdentityResourceIDStr := strings.ToLower(testServiceManagedIdentityID)
	lowerDataPlaneOperatorIdentityResourceIDStr := strings.ToLower(testDataPlaneOperatorResourceID)
	hardcodedWant := &coreapi.IdentityMetadataValue{
		ClientID:    ptr.To("hardcoded-client"),
		PrincipalID: ptr.To("hardcoded-principal"),
		TenantID:    ptr.To("hardcoded-tenant"),
	}

	testCases := []struct {
		name              string
		desiredIdentities map[string]*identityToResolve
		want              map[string]*coreapi.IdentityMetadataValue
	}{
		{
			name: "fills control plane operators and the service managed identity and omits data-plane-only identities",
			desiredIdentities: map[string]*identityToResolve{
				lowerControlPlaneOperatorIdentityResourceIDStr: newTestIdentityToResolve(testControlPlaneOperatorIdentityResourceID, true, false, false),
				lowerServiceManagedIdentityResourceIDStr:       newTestIdentityToResolve(testServiceManagedIdentityID, false, false, true),
				lowerDataPlaneOperatorIdentityResourceIDStr:    newTestIdentityToResolve(testDataPlaneOperatorResourceID, false, true, false),
			},
			want: map[string]*coreapi.IdentityMetadataValue{
				lowerControlPlaneOperatorIdentityResourceIDStr: hardcodedWant,
				lowerServiceManagedIdentityResourceIDStr:       hardcodedWant,
			},
		},
		{
			name:              "returns an empty map when no identity is registered with the dataplane",
			desiredIdentities: map[string]*identityToResolve{},
			want:              map[string]*coreapi.IdentityMetadataValue{},
		},
		{
			name: "omits a data-plane-only identity",
			desiredIdentities: map[string]*identityToResolve{
				lowerDataPlaneOperatorIdentityResourceIDStr: newTestIdentityToResolve(testDataPlaneOperatorResourceID, false, true, false),
			},
			want: map[string]*coreapi.IdentityMetadataValue{},
		},
		{
			name: "fills an identity that is both a control-plane operator and a data-plane operator",
			desiredIdentities: map[string]*identityToResolve{
				lowerControlPlaneOperatorIdentityResourceIDStr: newTestIdentityToResolve(testControlPlaneOperatorIdentityResourceID, true, true, false),
			},
			want: map[string]*coreapi.IdentityMetadataValue{
				lowerControlPlaneOperatorIdentityResourceIDStr: hardcodedWant,
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			syncer := &fetchManagedIdentitiesInfoSyncer{
				hardcodedIdentity: &azureclient.HardcodedIdentity{
					ClientID:    "hardcoded-client",
					PrincipalID: "hardcoded-principal",
					TenantID:    "hardcoded-tenant",
				},
			}
			got := syncer.resolveManagedIdentitiesMetadataFromHardcodedIdentity(tc.desiredIdentities)
			require.NotNil(t, got)
			assertIdentityMetadataValues(t, got, tc.want)
		})
	}
}

func TestFetchManagedIdentitiesInfoSyncer_resolveManagedIdentitiesMetadataFromManagedIdentitiesDataplaneService(t *testing.T) {
	t.Parallel()

	lowerOperator := strings.ToLower(testControlPlaneOperatorIdentityResourceID)
	lowerSMI := strings.ToLower(testServiceManagedIdentityID)
	lowerDataPlane := strings.ToLower(testDataPlaneOperatorResourceID)
	operatorAndSMI := map[string]*identityToResolve{
		lowerOperator:  newTestIdentityToResolve(testControlPlaneOperatorIdentityResourceID, true, false, false),
		lowerSMI:       newTestIdentityToResolve(testServiceManagedIdentityID, false, false, true),
		lowerDataPlane: newTestIdentityToResolve(testDataPlaneOperatorResourceID, false, true, false),
	}

	testCases := []struct {
		name                                                       string
		cluster                                                    *coreapi.HCPOpenShiftCluster
		desiredIdentities                                          map[string]*identityToResolve
		mockManagedIdentitiesDataplaneCreds                        *dataplane.ManagedIdentityCredentials
		mockManagedIdentitiesDataplaneResponseErr                  error
		mockManagedIdentitiesDataplaneClientBuilderErr             error
		want                                                       map[string]*coreapi.IdentityMetadataValue
		wantErrorsSubstrs                                          []string
		wantManagedIdentitiesDataplaneCallsNum                     int
		wantManagedIdentitiesDataplaneRequestedIdentityResourceIDs []string
	}{
		{
			name:    "returns nil when no identity is registered with the dataplane",
			cluster: newTestClusterForManagedIdentities(),
			desiredIdentities: map[string]*identityToResolve{
				lowerDataPlane: newTestIdentityToResolve(testDataPlaneOperatorResourceID, false, true, false),
			},
			wantManagedIdentitiesDataplaneCallsNum: 0,
		},
		{
			name: "returns an error when the dataplane identity URL is empty",
			cluster: newTestClusterForManagedIdentities(func(c *coreapi.HCPOpenShiftCluster) {
				c.ServiceProviderProperties.ManagedIdentitiesDataPlaneIdentityURL = ""
			}),
			desiredIdentities:                      operatorAndSMI,
			wantErrorsSubstrs:                      []string{"ManagedIdentitiesDataPlaneIdentityURL is empty"},
			wantManagedIdentitiesDataplaneCallsNum: 0,
		},
		{
			name:              "returns an error when the dataplane client cannot be built",
			cluster:           newTestClusterForManagedIdentities(),
			desiredIdentities: operatorAndSMI,
			mockManagedIdentitiesDataplaneClientBuilderErr: errors.New("simulated dataplane client build failure"),
			wantErrorsSubstrs:                      []string{"failed to get Managed Identities Data Plane Client"},
			wantManagedIdentitiesDataplaneCallsNum: 0,
		},
		{
			name:              "returns an error when the dataplane request fails",
			cluster:           newTestClusterForManagedIdentities(),
			desiredIdentities: operatorAndSMI,
			mockManagedIdentitiesDataplaneResponseErr: errFakeDataplane,
			wantErrorsSubstrs:                         []string{"failed to get Managed Identities Data Plane Credentials"},
			wantManagedIdentitiesDataplaneCallsNum:    1,
		},
		{
			name:              "returns an error when the credential count does not match the request",
			cluster:           newTestClusterForManagedIdentities(),
			desiredIdentities: operatorAndSMI,
			mockManagedIdentitiesDataplaneCreds: &dataplane.ManagedIdentityCredentials{
				ExplicitIdentities: []dataplane.UserAssignedIdentityCredentials{
					uaCred(testControlPlaneOperatorIdentityResourceID, ptr.To("op-dp-client"), ptr.To("op-dp-principal")),
				},
			},
			wantErrorsSubstrs:                      []string{"unexpected number of returned Managed Identities Data Plane Credentials"},
			wantManagedIdentitiesDataplaneCallsNum: 1,
		},
		{
			name:              "fills managed identities dataplane-registered identities, matches mixed-case resource IDs, and omits data plane operator only identities",
			cluster:           newTestClusterForManagedIdentities(),
			desiredIdentities: operatorAndSMI,
			mockManagedIdentitiesDataplaneCreds: &dataplane.ManagedIdentityCredentials{
				ExplicitIdentities: []dataplane.UserAssignedIdentityCredentials{
					func() dataplane.UserAssignedIdentityCredentials {
						cred := uaCred(strings.ToUpper(testServiceManagedIdentityID), ptr.To("smi-dp-client"), ptr.To("smi-dp-principal"))
						cred.TenantID = ptr.To("smi-dp-tenant")
						return cred
					}(),
					func() dataplane.UserAssignedIdentityCredentials {
						cred := uaCred(strings.ToUpper(testControlPlaneOperatorIdentityResourceID), ptr.To("op-dp-client"), ptr.To("op-dp-principal"))
						cred.TenantID = ptr.To("op-dp-tenant")
						return cred
					}(),
				},
			},
			want: map[string]*coreapi.IdentityMetadataValue{
				lowerOperator: {
					ClientID:    ptr.To("op-dp-client"),
					PrincipalID: ptr.To("op-dp-principal"),
					TenantID:    ptr.To("op-dp-tenant"),
				},
				lowerSMI: {
					ClientID:    ptr.To("smi-dp-client"),
					PrincipalID: ptr.To("smi-dp-principal"),
					TenantID:    ptr.To("smi-dp-tenant"),
				},
			},
			wantManagedIdentitiesDataplaneCallsNum: 1,
			wantManagedIdentitiesDataplaneRequestedIdentityResourceIDs: []string{
				metadataapi.Must(azcorearm.ParseResourceID(testControlPlaneOperatorIdentityResourceID)).String(),
				metadataapi.Must(azcorearm.ParseResourceID(testServiceManagedIdentityID)).String(),
			},
		},
		{
			name:              "omits a credential with a nil resource ID and accumulates errors for the missing requested identity",
			cluster:           newTestClusterForManagedIdentities(),
			desiredIdentities: operatorAndSMI,
			mockManagedIdentitiesDataplaneCreds: &dataplane.ManagedIdentityCredentials{
				ExplicitIdentities: []dataplane.UserAssignedIdentityCredentials{
					func() dataplane.UserAssignedIdentityCredentials {
						cred := uaCred(testControlPlaneOperatorIdentityResourceID, ptr.To("op-dp-client"), ptr.To("op-dp-principal"))
						cred.TenantID = ptr.To("op-dp-tenant")
						return cred
					}(),
					{ResourceID: nil, ClientID: ptr.To("smi-dp-client"), ObjectID: ptr.To("smi-dp-principal")},
				},
			},
			want: map[string]*coreapi.IdentityMetadataValue{
				lowerOperator: {
					ClientID:    ptr.To("op-dp-client"),
					PrincipalID: ptr.To("op-dp-principal"),
					TenantID:    ptr.To("op-dp-tenant"),
				},
			},
			wantErrorsSubstrs: []string{
				"Resource ID is nil or empty",
				fmt.Sprintf("requested resource id credentials not found in managed identities data plane response. Requested resource ID %q", lowerSMI),
			},
			wantManagedIdentitiesDataplaneCallsNum: 1,
		},
		{
			name:              "accumulates an error when a requested identity is missing from the response",
			cluster:           newTestClusterForManagedIdentities(),
			desiredIdentities: operatorAndSMI,
			mockManagedIdentitiesDataplaneCreds: &dataplane.ManagedIdentityCredentials{
				ExplicitIdentities: []dataplane.UserAssignedIdentityCredentials{
					uaCred(testServiceManagedIdentityID, ptr.To("smi-dp-client"), ptr.To("smi-dp-principal")),
					uaCred(testServiceManagedIdentityID, ptr.To("smi-dp-client"), ptr.To("smi-dp-principal")),
				},
			},
			want: map[string]*coreapi.IdentityMetadataValue{
				lowerSMI: {
					ClientID:    ptr.To("smi-dp-client"),
					PrincipalID: ptr.To("smi-dp-principal"),
				},
			},
			wantErrorsSubstrs:                      []string{fmt.Sprintf("requested resource id credentials not found in managed identities data plane response. Requested resource ID %q", lowerOperator)},
			wantManagedIdentitiesDataplaneCallsNum: 1,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fakeClient := &fakeManagedIdentitiesDataplaneClient{creds: tc.mockManagedIdentitiesDataplaneCreds, err: tc.mockManagedIdentitiesDataplaneResponseErr}
			syncer := &fetchManagedIdentitiesInfoSyncer{
				fpaMIdataplaneClientBuilder: &fakeFPAMIDataplaneClientBuilder{client: fakeClient, buildErr: tc.mockManagedIdentitiesDataplaneClientBuilderErr},
			}
			got, errs := syncer.resolveManagedIdentitiesMetadataFromManagedIdentitiesDataplaneService(context.Background(), tc.cluster, tc.desiredIdentities)
			assertErrorsContainSubstringsInAnyOrder(t, errs, tc.wantErrorsSubstrs)
			assert.Equal(t, tc.wantManagedIdentitiesDataplaneCallsNum, fakeClient.callCount)
			if tc.wantManagedIdentitiesDataplaneCallsNum > 0 && tc.mockManagedIdentitiesDataplaneClientBuilderErr == nil && tc.mockManagedIdentitiesDataplaneResponseErr == nil {
				assert.Equal(t, testMIDataplaneURL, syncer.fpaMIdataplaneClientBuilder.(*fakeFPAMIDataplaneClientBuilder).lastURL)
			}
			if len(tc.wantManagedIdentitiesDataplaneRequestedIdentityResourceIDs) > 0 {
				assert.ElementsMatch(t, tc.wantManagedIdentitiesDataplaneRequestedIdentityResourceIDs, fakeClient.lastReq.IdentityIDs)
			}
			assertIdentityMetadataValues(t, got, tc.want)
		})
	}
}

func TestFetchManagedIdentitiesInfoSyncer_resolveManagedIdentitiesMetadataFromARMUserAssignedIdentitiesAPI(t *testing.T) {
	t.Parallel()

	lowerControlPlaneOperatorIdentityResourceIDStr := strings.ToLower(testControlPlaneOperatorIdentityResourceID)
	lowerServiceManagedIdentityResourceIDStr := strings.ToLower(testServiceManagedIdentityID)
	lowerDataPlaneOperatorIdentityResourceIDStr := strings.ToLower(testDataPlaneOperatorResourceID)
	controlPlaneOperatorName := metadataapi.Must(azcorearm.ParseResourceID(testControlPlaneOperatorIdentityResourceID)).Name
	dataPlaneOperatorName := metadataapi.Must(azcorearm.ParseResourceID(testDataPlaneOperatorResourceID)).Name
	serviceManagedIdentityName := metadataapi.Must(azcorearm.ParseResourceID(testServiceManagedIdentityID)).Name
	defaultDesiredIdentitiesToResolve := map[string]*identityToResolve{
		lowerControlPlaneOperatorIdentityResourceIDStr: newTestIdentityToResolve(testControlPlaneOperatorIdentityResourceID, true, false, false),
		lowerServiceManagedIdentityResourceIDStr:       newTestIdentityToResolve(testServiceManagedIdentityID, false, false, true),
		lowerDataPlaneOperatorIdentityResourceIDStr:    newTestIdentityToResolve(testDataPlaneOperatorResourceID, false, true, false),
	}

	testCases := []struct {
		name                                               string
		cluster                                            *coreapi.HCPOpenShiftCluster
		desiredIdentities                                  map[string]*identityToResolve
		mockARMUserAssignedIdentitiesClient                *fakeUserAssignedIdentitiesClientByName
		mockARMUserAssignedIdentitiesClientBuilderErr      error
		want                                               map[string]*coreapi.IdentityMetadataValue
		wantErrorsSubstrs                                  []string
		wantARMUserAssignedIdentitiesClientBuilderCallsNum int
	}{
		{
			name:    "returns nil when no identity applies to ARM",
			cluster: newTestClusterForManagedIdentities(),
			desiredIdentities: map[string]*identityToResolve{
				lowerServiceManagedIdentityResourceIDStr: newTestIdentityToResolve(testServiceManagedIdentityID, false, false, true),
			},
		},
		{
			name: "returns an error when ServiceManagedIdentity is nil",
			cluster: newTestClusterForManagedIdentities(func(c *coreapi.HCPOpenShiftCluster) {
				c.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ServiceManagedIdentity = nil
			}),
			desiredIdentities: map[string]*identityToResolve{
				lowerControlPlaneOperatorIdentityResourceIDStr: newTestIdentityToResolve(testControlPlaneOperatorIdentityResourceID, true, false, false),
			},
			wantErrorsSubstrs: []string{"ServiceManagedIdentity is nil"},
		},
		{
			name:              "returns an error when the User Assigned Identities client cannot be built",
			cluster:           newTestClusterForManagedIdentities(),
			desiredIdentities: defaultDesiredIdentitiesToResolve,
			mockARMUserAssignedIdentitiesClientBuilderErr: errors.New("simulated client build failure"),
			wantErrorsSubstrs: []string{"failed to get User Assigned Identities Client"},
			wantARMUserAssignedIdentitiesClientBuilderCallsNum: 1,
		},
		{
			name:              "fills control-plane and data-plane operators and omits SMI-only identities",
			cluster:           newTestClusterForManagedIdentities(),
			desiredIdentities: defaultDesiredIdentitiesToResolve,
			mockARMUserAssignedIdentitiesClient: &fakeUserAssignedIdentitiesClientByName{
				getByName: map[string]armmsi.UserAssignedIdentitiesClientGetResponse{
					strings.ToLower(controlPlaneOperatorName): newTestUserAssignedIdentitiesClientGetResponse("op-arm-client", "op-arm-principal", "op-arm-tenant"),
					strings.ToLower(dataPlaneOperatorName):    newTestUserAssignedIdentitiesClientGetResponse("dp-arm-client", "dp-arm-principal", "dp-arm-tenant"),
				},
			},
			want: map[string]*coreapi.IdentityMetadataValue{
				lowerControlPlaneOperatorIdentityResourceIDStr: {
					ClientID:    ptr.To("op-arm-client"),
					PrincipalID: ptr.To("op-arm-principal"),
					TenantID:    ptr.To("op-arm-tenant"),
				},
				lowerDataPlaneOperatorIdentityResourceIDStr: {
					ClientID:    ptr.To("dp-arm-client"),
					PrincipalID: ptr.To("dp-arm-principal"),
					TenantID:    ptr.To("dp-arm-tenant"),
				},
			},
			wantARMUserAssignedIdentitiesClientBuilderCallsNum: 1,
		},
		{
			name:    "queries ARM for an SMI that is also a data-plane operator",
			cluster: newTestClusterForManagedIdentities(),
			desiredIdentities: map[string]*identityToResolve{
				lowerServiceManagedIdentityResourceIDStr: newTestIdentityToResolve(testServiceManagedIdentityID, false, true, true),
			},
			mockARMUserAssignedIdentitiesClient: &fakeUserAssignedIdentitiesClientByName{
				getByName: map[string]armmsi.UserAssignedIdentitiesClientGetResponse{
					strings.ToLower(serviceManagedIdentityName): newTestUserAssignedIdentitiesClientGetResponse("smi-arm-client", "smi-arm-principal", "smi-arm-tenant"),
				},
			},
			want: map[string]*coreapi.IdentityMetadataValue{
				lowerServiceManagedIdentityResourceIDStr: {
					ClientID:    ptr.To("smi-arm-client"),
					PrincipalID: ptr.To("smi-arm-principal"),
					TenantID:    ptr.To("smi-arm-tenant"),
				},
			},
			wantARMUserAssignedIdentitiesClientBuilderCallsNum: 1,
		},
		{
			name:              "ResourceNotFound stores RetrievalError and is not accumulated",
			cluster:           newTestClusterForManagedIdentities(),
			desiredIdentities: defaultDesiredIdentitiesToResolve,
			mockARMUserAssignedIdentitiesClient: &fakeUserAssignedIdentitiesClientByName{
				getByName: map[string]armmsi.UserAssignedIdentitiesClientGetResponse{
					strings.ToLower(controlPlaneOperatorName): newTestUserAssignedIdentitiesClientGetResponse("op-arm-client", "op-arm-principal", "op-arm-tenant"),
				},
				errByName: map[string]error{
					strings.ToLower(dataPlaneOperatorName): resourceNotFoundResponseError(),
				},
			},
			want: map[string]*coreapi.IdentityMetadataValue{
				lowerControlPlaneOperatorIdentityResourceIDStr: {
					ClientID:    ptr.To("op-arm-client"),
					PrincipalID: ptr.To("op-arm-principal"),
					TenantID:    ptr.To("op-arm-tenant"),
				},
				lowerDataPlaneOperatorIdentityResourceIDStr: {RetrievalError: ptr.To("ResourceNotFound")},
			},
			wantARMUserAssignedIdentitiesClientBuilderCallsNum: 1,
		},
		{
			name:              "other Get failures store RetrievalError and are accumulated",
			cluster:           newTestClusterForManagedIdentities(),
			desiredIdentities: defaultDesiredIdentitiesToResolve,
			mockARMUserAssignedIdentitiesClient: &fakeUserAssignedIdentitiesClientByName{
				getByName: map[string]armmsi.UserAssignedIdentitiesClientGetResponse{
					strings.ToLower(controlPlaneOperatorName): newTestUserAssignedIdentitiesClientGetResponse("op-arm-client", "op-arm-principal", "op-arm-tenant"),
				},
				errByName: map[string]error{
					strings.ToLower(dataPlaneOperatorName): errors.New("simulated azure Get failure"),
				},
			},
			want: map[string]*coreapi.IdentityMetadataValue{
				lowerControlPlaneOperatorIdentityResourceIDStr: {
					ClientID:    ptr.To("op-arm-client"),
					PrincipalID: ptr.To("op-arm-principal"),
					TenantID:    ptr.To("op-arm-tenant"),
				},
				lowerDataPlaneOperatorIdentityResourceIDStr: {RetrievalError: ptr.To("simulated azure Get failure")},
			},
			wantErrorsSubstrs: []string{"failed to get User Assigned Managed Identity"},
			wantARMUserAssignedIdentitiesClientBuilderCallsNum: 1,
		},
		{
			name:              "nil Properties stores RetrievalError and is accumulated",
			cluster:           newTestClusterForManagedIdentities(),
			desiredIdentities: defaultDesiredIdentitiesToResolve,
			mockARMUserAssignedIdentitiesClient: &fakeUserAssignedIdentitiesClientByName{
				getByName: map[string]armmsi.UserAssignedIdentitiesClientGetResponse{
					strings.ToLower(controlPlaneOperatorName): newTestUserAssignedIdentitiesClientGetResponse("op-arm-client", "op-arm-principal", "op-arm-tenant"),
					strings.ToLower(dataPlaneOperatorName): {
						Identity: armmsi.Identity{},
					},
				},
			},
			want: map[string]*coreapi.IdentityMetadataValue{
				lowerControlPlaneOperatorIdentityResourceIDStr: {
					ClientID:    ptr.To("op-arm-client"),
					PrincipalID: ptr.To("op-arm-principal"),
					TenantID:    ptr.To("op-arm-tenant"),
				},
				lowerDataPlaneOperatorIdentityResourceIDStr: {RetrievalError: ptr.To("Properties is nil")},
			},
			wantErrorsSubstrs: []string{"Properties is nil"},
			wantARMUserAssignedIdentitiesClientBuilderCallsNum: 1,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctrl := gomock.NewController(t)
			smiClientBuilder := azureclient.NewMockServiceManagedIdentityClientBuilder(ctrl)
			if tc.wantARMUserAssignedIdentitiesClientBuilderCallsNum > 0 {
				smiClientBuilder.EXPECT().
					UserAssignedIdentitiesClient(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
					Return(tc.mockARMUserAssignedIdentitiesClient, tc.mockARMUserAssignedIdentitiesClientBuilderErr).
					Times(tc.wantARMUserAssignedIdentitiesClientBuilderCallsNum)
			}
			syncer := &fetchManagedIdentitiesInfoSyncer{
				smiClientBuilder: smiClientBuilder,
			}
			got, errs := syncer.resolveManagedIdentitiesMetadataFromARMUserAssignedIdentitiesAPI(context.Background(), tc.cluster, tc.desiredIdentities)
			assertErrorsContainSubstringsInAnyOrder(t, errs, tc.wantErrorsSubstrs)
			assertIdentityMetadataValues(t, got, tc.want)
		})
	}
}

func TestFetchManagedIdentitiesInfoSyncer_SyncOnce(t *testing.T) {
	now := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	lowerControlPlaneOperatorIdentityResourceIDStr := strings.ToLower(testControlPlaneOperatorIdentityResourceID)
	lowerServiceManagedIdentityResourceIDStr := strings.ToLower(testServiceManagedIdentityID)
	lowerDataPlaneOperatorIdentityResourceIDStr := strings.ToLower(testDataPlaneOperatorResourceID)
	controlPlaneOperatorName := metadataapi.Must(azcorearm.ParseResourceID(testControlPlaneOperatorIdentityResourceID)).Name
	dataPlaneOperatorName := metadataapi.Must(azcorearm.ParseResourceID(testDataPlaneOperatorResourceID)).Name
	serviceManagedIdentityName := metadataapi.Must(azcorearm.ParseResourceID(testServiceManagedIdentityID)).Name
	controlplaneOperatorIdentityResourceID := metadataapi.Must(azcorearm.ParseResourceID(testControlPlaneOperatorIdentityResourceID))
	serviceManagedIdentityResourceID := metadataapi.Must(azcorearm.ParseResourceID(testServiceManagedIdentityID))
	dataPlaneOperatorIdentityResourceID := metadataapi.Must(azcorearm.ParseResourceID(testDataPlaneOperatorResourceID))

	testHardcodedIdentity := &azureclient.HardcodedIdentity{
		ClientID:    "hardcoded-client",
		PrincipalID: "hardcoded-principal",
		TenantID:    "hardcoded-tenant",
	}
	hardcodedIdentityMetadata := &coreapi.IdentityMetadataValue{
		ClientID:    ptr.To("hardcoded-client"),
		PrincipalID: ptr.To("hardcoded-principal"),
		TenantID:    ptr.To("hardcoded-tenant"),
	}
	controlPlaneOperatorIdentityMIDataplaneMetadata := &coreapi.IdentityMetadataValue{
		ClientID:    ptr.To("op-dp-client"),
		PrincipalID: ptr.To("op-dp-principal"),
		TenantID:    ptr.To("op-dp-tenant"),
	}
	serviceManagedIdentityMIDataplaneMetadata := &coreapi.IdentityMetadataValue{
		ClientID:    ptr.To("smi-dp-client"),
		PrincipalID: ptr.To("smi-dp-principal"),
		TenantID:    ptr.To("smi-dp-tenant"),
	}
	controlPlaneOperatorIdentityARMMetadata := &coreapi.IdentityMetadataValue{
		ClientID:    ptr.To("op-arm-client"),
		PrincipalID: ptr.To("op-arm-principal"),
		TenantID:    ptr.To("op-arm-tenant"),
	}
	dataPlaneOperatorIdentityARMMetadata := &coreapi.IdentityMetadataValue{
		ClientID:    ptr.To("dp-arm-client"),
		PrincipalID: ptr.To("dp-arm-principal"),
		TenantID:    ptr.To("dp-arm-tenant"),
	}
	serviceManagedIdentityARMMetadata := &coreapi.IdentityMetadataValue{
		ClientID:    ptr.To("smi-arm-client"),
		PrincipalID: ptr.To("smi-arm-principal"),
		TenantID:    ptr.To("smi-arm-tenant"),
	}
	emptyIdentityMetadata := &coreapi.IdentityMetadataValue{}
	defaultMIDataplaneCredentialsResponse := &dataplane.ManagedIdentityCredentials{
		ExplicitIdentities: []dataplane.UserAssignedIdentityCredentials{
			func() dataplane.UserAssignedIdentityCredentials {
				cred := uaCred(strings.ToUpper(testServiceManagedIdentityID), ptr.To("smi-dp-client"), ptr.To("smi-dp-principal"))
				cred.TenantID = ptr.To("smi-dp-tenant")
				return cred
			}(),
			func() dataplane.UserAssignedIdentityCredentials {
				cred := uaCred(strings.ToUpper(testControlPlaneOperatorIdentityResourceID), ptr.To("op-dp-client"), ptr.To("op-dp-principal"))
				cred.TenantID = ptr.To("op-dp-tenant")
				return cred
			}(),
		},
	}
	defaultFakeUserAssignedIdentitiesClientByName := &fakeUserAssignedIdentitiesClientByName{
		getByName: map[string]armmsi.UserAssignedIdentitiesClientGetResponse{
			strings.ToLower(controlPlaneOperatorName): newTestUserAssignedIdentitiesClientGetResponse("op-arm-client", "op-arm-principal", "op-arm-tenant"),
			strings.ToLower(dataPlaneOperatorName):    newTestUserAssignedIdentitiesClientGetResponse("dp-arm-client", "dp-arm-principal", "dp-arm-tenant"),
		},
	}

	testCases := []struct {
		name                                               string
		cluster                                            *coreapi.HCPOpenShiftCluster
		serviceProviderCluster                             *coreapi.ServiceProviderCluster
		hardcodedIdentity                                  *azureclient.HardcodedIdentity
		mockManagedIdentitiesDataplaneCreds                *dataplane.ManagedIdentityCredentials
		mockManagedIdentitiesDataplaneResponseErr          error
		mockARMUserAssignedIdentitiesClient                *fakeUserAssignedIdentitiesClientByName
		mockARMUserAssignedIdentitiesClientBuilderErr      error
		wantManagedIdentitiesDataplaneCallsNum             int
		wantARMUserAssignedIdentitiesClientBuilderCallsNum int
		wantErrorsSubstrs                                  []string
		wantEarliestRecheckTimeSet                         bool
		want                                               map[string]*coreapi.ManagedIdentityMetadata
	}{
		{
			name:                                   "real dataplane and ARM fill applicable sources and skip ARM for SMI",
			cluster:                                newTestClusterForManagedIdentities(),
			serviceProviderCluster:                 newTestServiceProviderCluster(),
			mockManagedIdentitiesDataplaneCreds:    defaultMIDataplaneCredentialsResponse,
			mockARMUserAssignedIdentitiesClient:    defaultFakeUserAssignedIdentitiesClientByName,
			wantManagedIdentitiesDataplaneCallsNum: 1,
			wantARMUserAssignedIdentitiesClientBuilderCallsNum: 1,
			wantEarliestRecheckTimeSet:                         true,
			want: map[string]*coreapi.ManagedIdentityMetadata{
				lowerControlPlaneOperatorIdentityResourceIDStr: {
					ResourceID: controlplaneOperatorIdentityResourceID,
					MetadataFromManagedIdentitiesDataplaneService: controlPlaneOperatorIdentityMIDataplaneMetadata,
					MetadataFromARMUserAssignedIdentitiesAPI:      controlPlaneOperatorIdentityARMMetadata,
				},
				lowerServiceManagedIdentityResourceIDStr: {
					ResourceID: serviceManagedIdentityResourceID,
					MetadataFromManagedIdentitiesDataplaneService: serviceManagedIdentityMIDataplaneMetadata,
				},
				lowerDataPlaneOperatorIdentityResourceIDStr: {
					ResourceID:                               dataPlaneOperatorIdentityResourceID,
					MetadataFromARMUserAssignedIdentitiesAPI: dataPlaneOperatorIdentityARMMetadata,
				},
			},
		},
		{
			name: "SMI that is also a data-plane operator is queried via ARM and dataplane",
			cluster: newTestClusterForManagedIdentities(func(c *coreapi.HCPOpenShiftCluster) {
				c.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.DataPlaneOperators[testDataPlaneOperatorName] =
					metadataapi.Must(azcorearm.ParseResourceID(testServiceManagedIdentityID))
			}),
			serviceProviderCluster:              newTestServiceProviderCluster(),
			mockManagedIdentitiesDataplaneCreds: defaultMIDataplaneCredentialsResponse,
			mockARMUserAssignedIdentitiesClient: &fakeUserAssignedIdentitiesClientByName{
				getByName: map[string]armmsi.UserAssignedIdentitiesClientGetResponse{
					strings.ToLower(controlPlaneOperatorName):   newTestUserAssignedIdentitiesClientGetResponse("op-arm-client", "op-arm-principal", "op-arm-tenant"),
					strings.ToLower(serviceManagedIdentityName): newTestUserAssignedIdentitiesClientGetResponse("smi-arm-client", "smi-arm-principal", "smi-arm-tenant"),
				},
			},
			wantManagedIdentitiesDataplaneCallsNum:             1,
			wantARMUserAssignedIdentitiesClientBuilderCallsNum: 1,
			wantEarliestRecheckTimeSet:                         true,
			want: map[string]*coreapi.ManagedIdentityMetadata{
				lowerControlPlaneOperatorIdentityResourceIDStr: {
					ResourceID: controlplaneOperatorIdentityResourceID,
					MetadataFromManagedIdentitiesDataplaneService: controlPlaneOperatorIdentityMIDataplaneMetadata,
					MetadataFromARMUserAssignedIdentitiesAPI:      controlPlaneOperatorIdentityARMMetadata,
				},
				lowerServiceManagedIdentityResourceIDStr: {
					ResourceID: serviceManagedIdentityResourceID,
					MetadataFromManagedIdentitiesDataplaneService: serviceManagedIdentityMIDataplaneMetadata,
					MetadataFromARMUserAssignedIdentitiesAPI:      serviceManagedIdentityARMMetadata,
				},
			},
		},
		{
			name:                                "hardcoded environment fills hardcoded metadata and still queries ARM",
			cluster:                             newTestClusterForManagedIdentities(),
			serviceProviderCluster:              newTestServiceProviderCluster(),
			hardcodedIdentity:                   testHardcodedIdentity,
			mockARMUserAssignedIdentitiesClient: defaultFakeUserAssignedIdentitiesClientByName,
			wantARMUserAssignedIdentitiesClientBuilderCallsNum: 1,
			wantEarliestRecheckTimeSet:                         true,
			want: map[string]*coreapi.ManagedIdentityMetadata{
				lowerControlPlaneOperatorIdentityResourceIDStr: {
					ResourceID:                               controlplaneOperatorIdentityResourceID,
					MetadataFromHardcodedIdentity:            hardcodedIdentityMetadata,
					MetadataFromARMUserAssignedIdentitiesAPI: controlPlaneOperatorIdentityARMMetadata,
				},
				lowerServiceManagedIdentityResourceIDStr: {
					ResourceID:                    serviceManagedIdentityResourceID,
					MetadataFromHardcodedIdentity: hardcodedIdentityMetadata,
				},
				lowerDataPlaneOperatorIdentityResourceIDStr: {
					ResourceID:                               dataPlaneOperatorIdentityResourceID,
					MetadataFromARMUserAssignedIdentitiesAPI: dataPlaneOperatorIdentityARMMetadata,
				},
			},
		},
		{
			name:                   "MI Dataplane service failure still persists ARM metadata and returns the error",
			cluster:                newTestClusterForManagedIdentities(),
			serviceProviderCluster: newTestServiceProviderCluster(),
			mockManagedIdentitiesDataplaneResponseErr:          errFakeDataplane,
			mockARMUserAssignedIdentitiesClient:                defaultFakeUserAssignedIdentitiesClientByName,
			wantManagedIdentitiesDataplaneCallsNum:             1,
			wantARMUserAssignedIdentitiesClientBuilderCallsNum: 1,
			wantErrorsSubstrs:                                  []string{"simulated Managed Identities Data Plane failure"},
			want: map[string]*coreapi.ManagedIdentityMetadata{
				lowerControlPlaneOperatorIdentityResourceIDStr: {
					ResourceID: controlplaneOperatorIdentityResourceID,
					MetadataFromManagedIdentitiesDataplaneService: emptyIdentityMetadata,
					MetadataFromARMUserAssignedIdentitiesAPI:      controlPlaneOperatorIdentityARMMetadata,
				},
				lowerServiceManagedIdentityResourceIDStr: {
					ResourceID: serviceManagedIdentityResourceID,
					MetadataFromManagedIdentitiesDataplaneService: emptyIdentityMetadata,
				},
				lowerDataPlaneOperatorIdentityResourceIDStr: {
					ResourceID:                               dataPlaneOperatorIdentityResourceID,
					MetadataFromARMUserAssignedIdentitiesAPI: dataPlaneOperatorIdentityARMMetadata,
				},
			},
		},
		{
			name:                   "one ARM Get failure still persists the other identities and MI dataplane service metadata",
			cluster:                newTestClusterForManagedIdentities(),
			serviceProviderCluster: newTestServiceProviderCluster(),
			mockManagedIdentitiesDataplaneCreds: &dataplane.ManagedIdentityCredentials{
				ExplicitIdentities: []dataplane.UserAssignedIdentityCredentials{
					uaCred(testControlPlaneOperatorIdentityResourceID, ptr.To("op-dp-client"), ptr.To("op-dp-principal")),
					uaCred(testServiceManagedIdentityID, ptr.To("smi-dp-client"), ptr.To("smi-dp-principal")),
				},
			},
			mockARMUserAssignedIdentitiesClient: &fakeUserAssignedIdentitiesClientByName{
				getByName: map[string]armmsi.UserAssignedIdentitiesClientGetResponse{
					strings.ToLower(controlPlaneOperatorName): newTestUserAssignedIdentitiesClientGetResponse("op-arm-client", "op-arm-principal", "op-arm-tenant"),
				},
				errByName: map[string]error{
					strings.ToLower(dataPlaneOperatorName): errors.New("simulated azure Get failure"),
				},
			},
			wantManagedIdentitiesDataplaneCallsNum:             1,
			wantARMUserAssignedIdentitiesClientBuilderCallsNum: 1,
			wantErrorsSubstrs: []string{"simulated azure Get failure"},
			want: map[string]*coreapi.ManagedIdentityMetadata{
				lowerControlPlaneOperatorIdentityResourceIDStr: {
					ResourceID: controlplaneOperatorIdentityResourceID,
					MetadataFromManagedIdentitiesDataplaneService: &coreapi.IdentityMetadataValue{
						ClientID:    ptr.To("op-dp-client"),
						PrincipalID: ptr.To("op-dp-principal"),
					},
					MetadataFromARMUserAssignedIdentitiesAPI: controlPlaneOperatorIdentityARMMetadata,
				},
				lowerServiceManagedIdentityResourceIDStr: {
					ResourceID: serviceManagedIdentityResourceID,
					MetadataFromManagedIdentitiesDataplaneService: &coreapi.IdentityMetadataValue{
						ClientID:    ptr.To("smi-dp-client"),
						PrincipalID: ptr.To("smi-dp-principal"),
					},
				},
				lowerDataPlaneOperatorIdentityResourceIDStr: {
					ResourceID: dataPlaneOperatorIdentityResourceID,
					MetadataFromARMUserAssignedIdentitiesAPI: &coreapi.IdentityMetadataValue{
						RetrievalError: ptr.To("simulated azure Get failure"),
					},
				},
			},
		},
		{
			name:                   "ARM ResourceNotFound stores RetrievalError and is not a sync failure",
			cluster:                newTestClusterForManagedIdentities(),
			serviceProviderCluster: newTestServiceProviderCluster(),
			mockManagedIdentitiesDataplaneCreds: &dataplane.ManagedIdentityCredentials{
				ExplicitIdentities: []dataplane.UserAssignedIdentityCredentials{
					uaCred(testControlPlaneOperatorIdentityResourceID, ptr.To("op-dp-client"), ptr.To("op-dp-principal")),
					uaCred(testServiceManagedIdentityID, ptr.To("smi-dp-client"), ptr.To("smi-dp-principal")),
				},
			},
			mockARMUserAssignedIdentitiesClient: &fakeUserAssignedIdentitiesClientByName{
				getByName: map[string]armmsi.UserAssignedIdentitiesClientGetResponse{
					strings.ToLower(controlPlaneOperatorName): newTestUserAssignedIdentitiesClientGetResponse("op-arm-client", "op-arm-principal", "op-arm-tenant"),
				},
				errByName: map[string]error{
					strings.ToLower(dataPlaneOperatorName): resourceNotFoundResponseError(),
				},
			},
			wantManagedIdentitiesDataplaneCallsNum:             1,
			wantARMUserAssignedIdentitiesClientBuilderCallsNum: 1,
			wantEarliestRecheckTimeSet:                         true,
			want: map[string]*coreapi.ManagedIdentityMetadata{
				lowerControlPlaneOperatorIdentityResourceIDStr: {
					ResourceID: controlplaneOperatorIdentityResourceID,
					MetadataFromManagedIdentitiesDataplaneService: &coreapi.IdentityMetadataValue{
						ClientID:    ptr.To("op-dp-client"),
						PrincipalID: ptr.To("op-dp-principal"),
					},
					MetadataFromARMUserAssignedIdentitiesAPI: controlPlaneOperatorIdentityARMMetadata,
				},
				lowerServiceManagedIdentityResourceIDStr: {
					ResourceID: serviceManagedIdentityResourceID,
					MetadataFromManagedIdentitiesDataplaneService: &coreapi.IdentityMetadataValue{
						ClientID:    ptr.To("smi-dp-client"),
						PrincipalID: ptr.To("smi-dp-principal"),
					},
				},
				lowerDataPlaneOperatorIdentityResourceIDStr: {
					ResourceID: dataPlaneOperatorIdentityResourceID,
					MetadataFromARMUserAssignedIdentitiesAPI: &coreapi.IdentityMetadataValue{
						RetrievalError: ptr.To("ResourceNotFound"),
					},
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()

			mockDB := corecosmosstoragetesting.NewMockResourcesDBClient()
			serviceProviderClusterCRUD := mockDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName)
			_, err := serviceProviderClusterCRUD.Create(ctx, tc.serviceProviderCluster, nil)
			require.NoError(t, err)
			storedServiceProviderCluster, err := serviceProviderClusterCRUD.Get(ctx, coreapi.ServiceProviderClusterResourceName)
			require.NoError(t, err)

			fakeDataplaneClient := &fakeManagedIdentitiesDataplaneClient{creds: tc.mockManagedIdentitiesDataplaneCreds, err: tc.mockManagedIdentitiesDataplaneResponseErr}
			ctrl := gomock.NewController(t)
			smiClientBuilder := azureclient.NewMockServiceManagedIdentityClientBuilder(ctrl)
			if tc.wantARMUserAssignedIdentitiesClientBuilderCallsNum > 0 {
				smiClientBuilder.EXPECT().
					UserAssignedIdentitiesClient(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
					Return(tc.mockARMUserAssignedIdentitiesClient, tc.mockARMUserAssignedIdentitiesClientBuilderErr).
					Times(tc.wantARMUserAssignedIdentitiesClientBuilderCallsNum)
			} else {
				smiClientBuilder.EXPECT().
					UserAssignedIdentitiesClient(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
					Times(0)
			}

			syncer := &fetchManagedIdentitiesInfoSyncer{
				clock:                        clocktesting.NewFakePassiveClock(now),
				clusterLister:                &corelistertesting.SliceClusterLister{Clusters: []*coreapi.HCPOpenShiftCluster{tc.cluster}},
				serviceProviderClusterLister: &corelistertesting.SliceServiceProviderClusterLister{ServiceProviderClusters: []*coreapi.ServiceProviderCluster{storedServiceProviderCluster}},
				resourcesDBClient:            mockDB,
				hardcodedIdentity:            tc.hardcodedIdentity,
				smiClientBuilder:             smiClientBuilder,
			}
			if tc.hardcodedIdentity == nil {
				syncer.fpaMIdataplaneClientBuilder = &fakeFPAMIDataplaneClientBuilder{client: fakeDataplaneClient}
			}

			err = syncer.SyncOnce(ctx, controllerutils.HCPClusterKey{
				SubscriptionID:    testSubscriptionID,
				ResourceGroupName: testResourceGroupName,
				HCPClusterName:    testClusterName,
			})
			if len(tc.wantErrorsSubstrs) == 0 {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
				assertErrorsContainSubstringsInAnyOrder(t, []error{err}, tc.wantErrorsSubstrs)
			}
			assert.Equal(t, tc.wantManagedIdentitiesDataplaneCallsNum, fakeDataplaneClient.callCount)

			updated, err := serviceProviderClusterCRUD.Get(ctx, coreapi.ServiceProviderClusterResourceName)
			require.NoError(t, err)
			recheck := updated.Spec.EarliestRecheckTimesByController[FetchManagedIdentitiesInfoControllerName]
			if tc.wantEarliestRecheckTimeSet {
				require.NotNil(t, recheck)
				assert.True(t, recheck.After(now))
			} else {
				assert.Nil(t, recheck)
			}
			assertManagedIdentityDetails(t, updated.Status.ManagedIdentityDetails, tc.want)
		})
	}

	t.Run("matching identities with future recheck skip source queries", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()

		cluster := newTestClusterForManagedIdentities()
		futureRecheck := metav1.NewTime(now.Add(6 * time.Hour))
		serviceProviderCluster := newTestServiceProviderCluster()
		serviceProviderCluster.Spec.EarliestRecheckTimesByController = map[string]*metav1.Time{
			FetchManagedIdentitiesInfoControllerName: &futureRecheck,
		}
		serviceProviderCluster.Status.ManagedIdentityDetails = map[string]*coreapi.ManagedIdentityMetadata{
			lowerControlPlaneOperatorIdentityResourceIDStr: {ResourceID: metadataapi.Must(azcorearm.ParseResourceID(testControlPlaneOperatorIdentityResourceID))},
			lowerServiceManagedIdentityResourceIDStr:       {ResourceID: metadataapi.Must(azcorearm.ParseResourceID(testServiceManagedIdentityID))},
			lowerDataPlaneOperatorIdentityResourceIDStr:    {ResourceID: metadataapi.Must(azcorearm.ParseResourceID(testDataPlaneOperatorResourceID))},
		}

		mockDB := corecosmosstoragetesting.NewMockResourcesDBClient()
		serviceProviderClusterCRUD := mockDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName)
		_, err := serviceProviderClusterCRUD.Create(ctx, serviceProviderCluster, nil)
		require.NoError(t, err)
		storedServiceProviderCluster, err := serviceProviderClusterCRUD.Get(ctx, coreapi.ServiceProviderClusterResourceName)
		require.NoError(t, err)

		fakeDataplaneClient := &fakeManagedIdentitiesDataplaneClient{}
		ctrl := gomock.NewController(t)
		smiClientBuilder := azureclient.NewMockServiceManagedIdentityClientBuilder(ctrl)
		smiClientBuilder.EXPECT().
			UserAssignedIdentitiesClient(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Times(0)

		syncer := &fetchManagedIdentitiesInfoSyncer{
			clock:                        clocktesting.NewFakePassiveClock(now),
			clusterLister:                &corelistertesting.SliceClusterLister{Clusters: []*coreapi.HCPOpenShiftCluster{cluster}},
			serviceProviderClusterLister: &corelistertesting.SliceServiceProviderClusterLister{ServiceProviderClusters: []*coreapi.ServiceProviderCluster{storedServiceProviderCluster}},
			resourcesDBClient:            mockDB,
			fpaMIdataplaneClientBuilder:  &fakeFPAMIDataplaneClientBuilder{client: fakeDataplaneClient},
			smiClientBuilder:             smiClientBuilder,
		}

		err = syncer.SyncOnce(ctx, controllerutils.HCPClusterKey{
			SubscriptionID:    testSubscriptionID,
			ResourceGroupName: testResourceGroupName,
			HCPClusterName:    testClusterName,
		})
		require.NoError(t, err)
		assert.Equal(t, 0, fakeDataplaneClient.callCount)
	})

	t.Run("collect errors return without replacing stored identities", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()

		cluster := newTestClusterForManagedIdentities(func(c *coreapi.HCPOpenShiftCluster) {
			c.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ServiceManagedIdentity = nil
		})
		storedOperator := &coreapi.ManagedIdentityMetadata{
			ResourceID: metadataapi.Must(azcorearm.ParseResourceID(testControlPlaneOperatorIdentityResourceID)),
		}
		serviceProviderCluster := newTestServiceProviderCluster()
		serviceProviderCluster.Status.ManagedIdentityDetails = map[string]*coreapi.ManagedIdentityMetadata{
			lowerControlPlaneOperatorIdentityResourceIDStr: storedOperator,
		}

		mockDB := corecosmosstoragetesting.NewMockResourcesDBClient()
		serviceProviderClusterCRUD := mockDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName)
		_, err := serviceProviderClusterCRUD.Create(ctx, serviceProviderCluster, nil)
		require.NoError(t, err)
		storedServiceProviderCluster, err := serviceProviderClusterCRUD.Get(ctx, coreapi.ServiceProviderClusterResourceName)
		require.NoError(t, err)

		fakeDataplaneClient := &fakeManagedIdentitiesDataplaneClient{}
		ctrl := gomock.NewController(t)
		smiClientBuilder := azureclient.NewMockServiceManagedIdentityClientBuilder(ctrl)
		smiClientBuilder.EXPECT().
			UserAssignedIdentitiesClient(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Times(0)

		syncer := &fetchManagedIdentitiesInfoSyncer{
			clock:                        clocktesting.NewFakePassiveClock(now),
			clusterLister:                &corelistertesting.SliceClusterLister{Clusters: []*coreapi.HCPOpenShiftCluster{cluster}},
			serviceProviderClusterLister: &corelistertesting.SliceServiceProviderClusterLister{ServiceProviderClusters: []*coreapi.ServiceProviderCluster{storedServiceProviderCluster}},
			resourcesDBClient:            mockDB,
			fpaMIdataplaneClientBuilder:  &fakeFPAMIDataplaneClientBuilder{client: fakeDataplaneClient},
			smiClientBuilder:             smiClientBuilder,
		}

		err = syncer.SyncOnce(ctx, controllerutils.HCPClusterKey{
			SubscriptionID:    testSubscriptionID,
			ResourceGroupName: testResourceGroupName,
			HCPClusterName:    testClusterName,
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "service managed identity")
		assert.Equal(t, 0, fakeDataplaneClient.callCount)

		updated, err := serviceProviderClusterCRUD.Get(ctx, coreapi.ServiceProviderClusterResourceName)
		require.NoError(t, err)
		require.Len(t, updated.Status.ManagedIdentityDetails, 1)
		require.Contains(t, updated.Status.ManagedIdentityDetails, lowerControlPlaneOperatorIdentityResourceIDStr)
		assert.Nil(t, updated.Spec.EarliestRecheckTimesByController[FetchManagedIdentitiesInfoControllerName])
	})
}
