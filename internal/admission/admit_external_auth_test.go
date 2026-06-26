// Copyright 2025 Microsoft Corporation
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

package admission

import (
	"context"
	"testing"

	"k8s.io/apimachinery/pkg/api/operation"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestAdmitExternalAuth(t *testing.T) {
	t.Parallel()

	const (
		existingExternalAuthResourceID = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster/externalAuths/existing"
		otherExternalAuthResourceID    = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster/externalAuths/other"
	)

	externalAuthWithResourceID := func(resourceID string) *api.HCPOpenShiftClusterExternalAuth {
		return api.NewDefaultHCPOpenShiftClusterExternalAuth(api.Must(azcorearm.ParseResourceID(resourceID)))
	}

	newExternalAuth := api.MinimumValidExternalAuthTestCase()
	existingExternalAuth := externalAuthWithResourceID(existingExternalAuthResourceID)
	otherExternalAuth := externalAuthWithResourceID(otherExternalAuthResourceID)

	tests := []struct {
		name             string
		op               operation.Type
		admissionContext *ExternalAuthAdmissionContext
		newObj           *api.HCPOpenShiftClusterExternalAuth
		oldObj           *api.HCPOpenShiftClusterExternalAuth
		expectErrors     []utils.ExpectedError
	}{
		{
			name: "create: no existing external auths allowed",
			op:   operation.Create,
			admissionContext: &ExternalAuthAdmissionContext{
				ClusterExternalAuths: []*api.HCPOpenShiftClusterExternalAuth{},
			},
			newObj:       newExternalAuth,
			expectErrors: []utils.ExpectedError{},
		},
		{
			name: "create: one existing external auth rejected",
			op:   operation.Create,
			admissionContext: &ExternalAuthAdmissionContext{
				ClusterExternalAuths: []*api.HCPOpenShiftClusterExternalAuth{existingExternalAuth},
			},
			newObj: newExternalAuth,
			expectErrors: []utils.ExpectedError{
				{
					FieldPath: "name",
					Message:   "Only one external auth is allowed per cluster",
				},
			},
		},
		{
			name: "create: multiple existing external auths rejected",
			op:   operation.Create,
			admissionContext: &ExternalAuthAdmissionContext{
				ClusterExternalAuths: []*api.HCPOpenShiftClusterExternalAuth{existingExternalAuth, otherExternalAuth},
			},
			newObj: newExternalAuth,
			expectErrors: []utils.ExpectedError{
				{
					FieldPath: "name",
					Message:   "Only one external auth is allowed per cluster",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			errs := AdmitExternalAuth(
				context.Background(),
				tt.admissionContext,
				operation.Operation{Type: tt.op},
				tt.newObj,
				tt.oldObj,
			)
			utils.VerifyErrorsMatch(t, tt.expectErrors, errs)
		})
	}
}
