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
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"k8s.io/apimachinery/pkg/api/operation"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation/field"
	clocktesting "k8s.io/utils/clock/testing"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/apitesting/coreapitesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestMutateExternalAuthOperationDeadlines(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	oldDeadline := metav1.NewTime(now.Add(-time.Hour))
	for _, op := range []operation.Type{operation.Create, operation.Update} {
		for _, existing := range []bool{false, true} {
			t.Run(fmt.Sprint(op)+"/existing="+fmt.Sprint(existing), func(t *testing.T) {
				old := &coreapi.ExternalAuth{}
				if existing {
					old.ServiceProviderProperties = coreapi.ExternalAuthServiceProviderProperties{
						CreateOperationCompletionDeadline: &oldDeadline,
						UpdateOperationCompletionDeadline: &oldDeadline,
						DeleteOperationCompletionDeadline: &oldDeadline,
					}
				}
				obj := old.DeepCopy()
				ctx := &ExternalAuthAdmissionContext{Clock: clocktesting.NewFakePassiveClock(now)}
				require.Empty(t, MutateExternalAuth(t.Context(), ctx, operation.Operation{Type: op}, obj, old))
				expected := old.ServiceProviderProperties
				deadline := metav1.NewTime(now.Add(30 * time.Minute))
				switch op {
				case operation.Create:
					expected.CreateOperationCompletionDeadline = &deadline
				case operation.Update:
					expected.UpdateOperationCompletionDeadline = &deadline
				}
				require.Equal(t, expected, obj.ServiceProviderProperties, "only the accepted operation's deadline should change")
				if existing {
					require.Equal(t, oldDeadline, *old.ServiceProviderProperties.CreateOperationCompletionDeadline)
					require.Equal(t, oldDeadline, *old.ServiceProviderProperties.UpdateOperationCompletionDeadline)
				}
			})
		}
	}
}

func TestAdmitExternalAuthNotDeleting(t *testing.T) {
	for _, tc := range []struct {
		name     string
		op       operation.Type
		deleting bool
	}{
		{name: "create", op: operation.Create},
		{name: "failed update", op: operation.Update},
		{name: "failed deletion update", op: operation.Update, deleting: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var oldObj *coreapi.ExternalAuth
			if tc.op == operation.Update {
				oldObj = &coreapi.ExternalAuth{Properties: coreapi.ExternalAuthProperties{ProvisioningState: coreapi.ProvisioningStateFailed}}
				if tc.deleting {
					oldObj.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: time.Now()}
				}
			}
			// The stored object, not the incoming object, carries deletion intent.
			errs := AdmitExternalAuth(t.Context(), &ExternalAuthAdmissionContext{}, operation.Operation{Type: tc.op}, &coreapi.ExternalAuth{}, oldObj)
			if tc.deleting {
				require.Equal(t, field.ErrorList{field.Forbidden(field.NewPath(""), "external auth is being deleted and cannot be updated")}, errs)
			} else {
				require.Empty(t, errs)
			}
		})
	}
}

func TestAdmitExternalAuth(t *testing.T) {
	t.Parallel()

	const (
		existingExternalAuthResourceID = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster/externalAuths/existing"
		otherExternalAuthResourceID    = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster/externalAuths/other"
	)

	externalAuthWithResourceID := func(resourceID string) *coreapi.ExternalAuth {
		return coreapi.NewDefaultExternalAuth(metadataapi.Must(azcorearm.ParseResourceID(resourceID)))
	}

	newExternalAuth := coreapitesting.MinimumValidExternalAuthTestCase()
	existingExternalAuth := externalAuthWithResourceID(existingExternalAuthResourceID)
	otherExternalAuth := externalAuthWithResourceID(otherExternalAuthResourceID)

	tests := []struct {
		name             string
		op               operation.Type
		admissionContext *ExternalAuthAdmissionContext
		newObj           *coreapi.ExternalAuth
		oldObj           *coreapi.ExternalAuth
		expectErrors     []utils.ExpectedError
	}{
		{
			name: "create: no existing external auths allowed",
			op:   operation.Create,
			admissionContext: &ExternalAuthAdmissionContext{
				ExternalAuths: []*coreapi.ExternalAuth{},
			},
			newObj:       newExternalAuth,
			expectErrors: []utils.ExpectedError{},
		},
		{
			name: "create: one existing external auth rejected",
			op:   operation.Create,
			admissionContext: &ExternalAuthAdmissionContext{
				ExternalAuths: []*coreapi.ExternalAuth{existingExternalAuth},
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
				ExternalAuths: []*coreapi.ExternalAuth{existingExternalAuth, otherExternalAuth},
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
