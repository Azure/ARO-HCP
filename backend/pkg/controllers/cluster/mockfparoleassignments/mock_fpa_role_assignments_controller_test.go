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

package mockfparoleassignments

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
)

func TestHasRenderableKeys(t *testing.T) {
	t.Parallel()

	key := coreapi.RoleAssignmentKey{PrincipalID: testMockFPAPrincipalID, RoleDefinitionResourceID: OwnerRoleDefinitionResourceID}
	stamp := metav1.NewTime(time.Now())

	assert.False(t, hasRenderableKeys(nil), "empty map has nothing to render")
	assert.False(t, hasRenderableKeys(map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus{
		key: {DeconfigureTimestamp: &stamp},
	}), "only a draining key has nothing to render")
	assert.True(t, hasRenderableKeys(map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus{
		key: {},
	}), "a non-draining key is renderable")
}

func TestRoleAssignmentTemplateResources(t *testing.T) {
	t.Parallel()

	desiredKey := coreapi.RoleAssignmentKey{PrincipalID: testMockFPAPrincipalID, RoleDefinitionResourceID: OwnerRoleDefinitionResourceID}
	drainingKey := coreapi.RoleAssignmentKey{PrincipalID: "old-principal", RoleDefinitionResourceID: OwnerRoleDefinitionResourceID}
	stamp := metav1.NewTime(time.Now())

	t.Run("renders the non-draining key and excludes the draining one", func(t *testing.T) {
		t.Parallel()
		assignments := map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus{
			desiredKey:  {ReservedAzureResourceID: mustReservedID(t, "11111111-1111-1111-1111-111111111111")},
			drainingKey: {DeconfigureTimestamp: &stamp, ReservedAzureResourceID: mustReservedID(t, "22222222-2222-2222-2222-222222222222")},
		}
		resources, err := roleAssignmentTemplateResources(assignments)
		require.NoError(t, err)
		require.Len(t, resources, 1)
		assert.Equal(t, "Microsoft.Authorization/roleAssignments", resources[0]["type"])
		assert.Equal(t, "11111111-1111-1111-1111-111111111111", resources[0]["name"])
		props := resources[0]["properties"].(map[string]any)
		assert.Equal(t, testMockFPAPrincipalID, props["principalId"])
		assert.Equal(t, OwnerRoleDefinitionResourceID, props["roleDefinitionId"])
		assert.Equal(t, "ServicePrincipal", props["principalType"])
	})

	t.Run("errors when a non-draining key has no reserved ID", func(t *testing.T) {
		t.Parallel()
		assignments := map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus{
			desiredKey: {},
		}
		_, err := roleAssignmentTemplateResources(assignments)
		require.Error(t, err)
	})

	t.Run("empty when all keys are draining", func(t *testing.T) {
		t.Parallel()
		assignments := map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus{
			drainingKey: {DeconfigureTimestamp: &stamp, ReservedAzureResourceID: mustReservedID(t, "22222222-2222-2222-2222-222222222222")},
		}
		resources, err := roleAssignmentTemplateResources(assignments)
		require.NoError(t, err)
		assert.Empty(t, resources)
	})
}
