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

package validation

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
)

func validRequest() *fleetapi.NamespaceDeletion {
	return &fleetapi.NamespaceDeletion{
		Spec: fleetapi.NamespaceDeletionSpec{
			NamespaceName:     "ocm-production-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			HostedClusterName: "hcp1",
			RequestReason:     "AROSLSRE-2477 sweep",
		},
	}
}

func TestValidateNamespaceDeletionCreate(t *testing.T) {
	for _, tc := range []struct {
		name    string
		mutate  func(*fleetapi.NamespaceDeletion)
		wantErr string
	}{
		{name: "valid", mutate: func(*fleetapi.NamespaceDeletion) {}},
		{
			name:    "namespaceName required",
			mutate:  func(r *fleetapi.NamespaceDeletion) { r.Spec.NamespaceName = "" },
			wantErr: "spec.namespaceName",
		},
		{
			name:    "hostedClusterName required",
			mutate:  func(r *fleetapi.NamespaceDeletion) { r.Spec.HostedClusterName = "" },
			wantErr: "spec.hostedClusterName",
		},
		{
			name:    "requestReason required",
			mutate:  func(r *fleetapi.NamespaceDeletion) { r.Spec.RequestReason = "" },
			wantErr: "spec.requestReason",
		},
		{
			name:    "caller cannot pin the UID",
			mutate:  func(r *fleetapi.NamespaceDeletion) { r.Spec.NamespaceUID = "deadbeef" },
			wantErr: "spec.namespaceUID",
		},
		{
			// The opt-in is a legitimate thing to ask for at creation; it is
			// only adding it later that is forbidden.
			name:   "deleteHostedCluster is allowed at creation",
			mutate: func(r *fleetapi.NamespaceDeletion) { r.Spec.DeleteHostedCluster = true },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := validRequest()
			tc.mutate(request)

			errs := ValidateNamespaceDeletionCreate(context.Background(), request)

			if tc.wantErr == "" {
				require.Empty(t, errs, "expected no validation errors, got %v", errs)
				return
			}
			require.NotEmpty(t, errs, "expected a validation error mentioning %q", tc.wantErr)
			require.Contains(t, errs.ToAggregate().Error(), tc.wantErr)
		})
	}
}

func TestValidateNamespaceDeletionUpdateImmutability(t *testing.T) {
	for _, tc := range []struct {
		name    string
		mutate  func(*fleetapi.NamespaceDeletion)
		wantErr string
	}{
		{
			name: "namespaceName is immutable",
			mutate: func(r *fleetapi.NamespaceDeletion) {
				r.Spec.NamespaceName = "ocm-production-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
			},
			wantErr: "namespaceName is immutable",
		},
		{
			name:    "hostedClusterName is immutable",
			mutate:  func(r *fleetapi.NamespaceDeletion) { r.Spec.HostedClusterName = "hcp2" },
			wantErr: "hostedClusterName is immutable",
		},
		{
			name:    "requestReason is immutable",
			mutate:  func(r *fleetapi.NamespaceDeletion) { r.Spec.RequestReason = "something else" },
			wantErr: "requestReason is immutable",
		},
		{
			// This is the important one. deleteHostedCluster authorizes
			// originating a destructive operation, so it must not be
			// acquirable after a request has already been accepted without it.
			name:    "deleteHostedCluster cannot be added after creation",
			mutate:  func(r *fleetapi.NamespaceDeletion) { r.Spec.DeleteHostedCluster = true },
			wantErr: "deleteHostedCluster is immutable",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			old := validRequest()
			updated := old.DeepCopy()
			tc.mutate(updated)

			errs := ValidateNamespaceDeletionUpdate(context.Background(), updated, old)

			require.NotEmpty(t, errs, "expected a validation error mentioning %q", tc.wantErr)
			require.Contains(t, errs.ToAggregate().Error(), tc.wantErr)
		})
	}
}

func TestValidateNamespaceDeletionUpdateAllowsControllerToPinUID(t *testing.T) {
	old := validRequest()
	updated := old.DeepCopy()
	updated.Spec.NamespaceUID = "11111111-1111-1111-1111-111111111111"

	require.Empty(t, ValidateNamespaceDeletionUpdate(context.Background(), updated, old),
		"the controller must be able to pin the UID on first observation")
}

func TestValidateNamespaceDeletionUpdateRejectsRepinnedUID(t *testing.T) {
	old := validRequest()
	old.Spec.NamespaceUID = "11111111-1111-1111-1111-111111111111"
	updated := old.DeepCopy()
	updated.Spec.NamespaceUID = "22222222-2222-2222-2222-222222222222"

	errs := ValidateNamespaceDeletionUpdate(context.Background(), updated, old)

	require.NotEmpty(t, errs, "repointing a pinned UID silently retargets the request and must be rejected")
	require.Contains(t, errs.ToAggregate().Error(), "namespaceUID is immutable once the controller has pinned it")
}
