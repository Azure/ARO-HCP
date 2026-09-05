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

package coreapihelpers

import (
	"testing"

	"github.com/stretchr/testify/assert"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
)

func TestIsDataplaneOIDCFederationAssignmentTargetIdentityEnsured(t *testing.T) {
	t.Parallel()

	identity := coreapi.DataplaneOIDCFederationIdentityInstance{
		ClientID:    "client-a",
		PrincipalID: "principal-a",
		TenantID:    "tenant-a",
	}
	rotated := identity
	rotated.ClientID = "client-b"

	assert.False(t, IsDataplaneOIDCFederationAssignmentTargetIdentityEnsured(&coreapi.DataplaneOIDCFederationAssignmentStatus{TargetIdentity: identity}))
	assert.True(t, IsDataplaneOIDCFederationAssignmentTargetIdentityEnsured(&coreapi.DataplaneOIDCFederationAssignmentStatus{
		TargetIdentity:  identity,
		EnsuredIdentity: ptr.To(identity),
	}))
	assert.False(t, IsDataplaneOIDCFederationAssignmentTargetIdentityEnsured(&coreapi.DataplaneOIDCFederationAssignmentStatus{
		TargetIdentity:  identity,
		EnsuredIdentity: ptr.To(rotated),
	}))
	assert.False(t, IsDataplaneOIDCFederationAssignmentTargetIdentityEnsured(&coreapi.DataplaneOIDCFederationAssignmentStatus{
		TargetIdentity:       identity,
		EnsuredIdentity:      ptr.To(identity),
		DeconfigureTimestamp: ptr.To(metav1.Now()),
	}))
}
