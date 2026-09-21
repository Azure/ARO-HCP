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

package backendapihelpers

import (
	"testing"

	"github.com/stretchr/testify/assert"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
)

func TestIsDataplaneOIDCFederationAssignmentEnsured(t *testing.T) {
	t.Parallel()

	identity := coreapi.DataplaneOIDCFederationIdentityInstance{
		ClientID:    "client-a",
		PrincipalID: "principal-a",
		TenantID:    "tenant-a",
	}
	rotated := identity
	rotated.ClientID = "client-b"

	assert.False(t, IsDataplaneOIDCFederationAssignmentEnsured(&coreapi.DataplaneOIDCFederationAssignmentStatus{TargetIdentity: identity}))
	assert.True(t, IsDataplaneOIDCFederationAssignmentEnsured(&coreapi.DataplaneOIDCFederationAssignmentStatus{
		TargetIdentity:  identity,
		EnsuredIdentity: ptr.To(identity),
	}))
	assert.False(t, IsDataplaneOIDCFederationAssignmentEnsured(&coreapi.DataplaneOIDCFederationAssignmentStatus{
		TargetIdentity:  identity,
		EnsuredIdentity: ptr.To(rotated),
	}))
	assert.False(t, IsDataplaneOIDCFederationAssignmentEnsured(&coreapi.DataplaneOIDCFederationAssignmentStatus{
		TargetIdentity:       identity,
		EnsuredIdentity:      ptr.To(identity),
		DeconfigureTimestamp: ptr.To(metav1.Now()),
	}))
}

func TestIsDataplaneOIDCFederationAssignmentsTargetIdentityEnsured(t *testing.T) {
	t.Parallel()

	identityResourceID := "/subscriptions/00000000-0000-0000-0000-000000000000/resourcegroups/test-rg/providers/microsoft.managedidentity/userassignedidentities/identity-a"
	identity := coreapi.DataplaneOIDCFederationIdentityInstance{
		ClientID:    "client-a",
		PrincipalID: "principal-a",
		TenantID:    "tenant-a",
	}
	rotated := identity
	rotated.ClientID = "client-b"
	diskKey := coreapi.DataplaneOIDCFederationAssignmentKey{IdentityResourceID: identityResourceID, OperatorName: "azure-disk-csi-driver"}
	fileKey := coreapi.DataplaneOIDCFederationAssignmentKey{IdentityResourceID: identityResourceID, OperatorName: "azure-file-csi-driver"}

	assert.False(t, IsDataplaneOIDCFederationAssignmentsTargetIdentityEnsured(nil, identityResourceID))
	assert.False(t, IsDataplaneOIDCFederationAssignmentsTargetIdentityEnsured(map[coreapi.DataplaneOIDCFederationAssignmentKey]*coreapi.DataplaneOIDCFederationAssignmentStatus{}, identityResourceID))
	assert.True(t, IsDataplaneOIDCFederationAssignmentsTargetIdentityEnsured(map[coreapi.DataplaneOIDCFederationAssignmentKey]*coreapi.DataplaneOIDCFederationAssignmentStatus{
		diskKey: {
			TargetIdentity:  identity,
			EnsuredIdentity: ptr.To(identity),
		},
	}, identityResourceID))
	assert.False(t, IsDataplaneOIDCFederationAssignmentsTargetIdentityEnsured(map[coreapi.DataplaneOIDCFederationAssignmentKey]*coreapi.DataplaneOIDCFederationAssignmentStatus{
		diskKey: {
			TargetIdentity:  identity,
			EnsuredIdentity: ptr.To(identity),
		},
		fileKey: {
			TargetIdentity: identity,
		},
	}, identityResourceID))
	assert.False(t, IsDataplaneOIDCFederationAssignmentsTargetIdentityEnsured(map[coreapi.DataplaneOIDCFederationAssignmentKey]*coreapi.DataplaneOIDCFederationAssignmentStatus{
		diskKey: {
			TargetIdentity:  identity,
			EnsuredIdentity: ptr.To(rotated),
		},
	}, identityResourceID))
	assert.False(t, IsDataplaneOIDCFederationAssignmentsTargetIdentityEnsured(map[coreapi.DataplaneOIDCFederationAssignmentKey]*coreapi.DataplaneOIDCFederationAssignmentStatus{
		diskKey: {
			TargetIdentity:       identity,
			EnsuredIdentity:      ptr.To(identity),
			DeconfigureTimestamp: ptr.To(metav1.Now()),
		},
	}, identityResourceID))
	assert.True(t, IsDataplaneOIDCFederationAssignmentsTargetIdentityEnsured(map[coreapi.DataplaneOIDCFederationAssignmentKey]*coreapi.DataplaneOIDCFederationAssignmentStatus{
		diskKey: {
			TargetIdentity:  identity,
			EnsuredIdentity: ptr.To(identity),
		},
		fileKey: {
			TargetIdentity:       identity,
			EnsuredIdentity:      ptr.To(identity),
			DeconfigureTimestamp: ptr.To(metav1.Now()),
		},
	}, identityResourceID))
}
