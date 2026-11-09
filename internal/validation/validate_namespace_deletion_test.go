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
	"time"

	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
)

func validRequest() *fleetapi.NamespaceDeletion {
	return &fleetapi.NamespaceDeletion{
		Spec: fleetapi.NamespaceDeletionSpec{
			ManagementClusterName:      fleetapi.ManagementClusterResourceName,
			HostedClusterNamespaceName: "ocm-production-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			HostedClusterName:          "hcp1",
			RequestReason:              "AROSLSRE-2477 sweep",
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
			name:    "managementClusterName required",
			mutate:  func(r *fleetapi.NamespaceDeletion) { r.Spec.ManagementClusterName = "" },
			wantErr: "spec.managementClusterName",
		},
		{
			name:    "hostedClusterNamespaceName required",
			mutate:  func(r *fleetapi.NamespaceDeletion) { r.Spec.HostedClusterNamespaceName = "" },
			wantErr: "spec.hostedClusterNamespaceName",
		},
		{
			name:    "hostedClusterName required",
			mutate:  func(r *fleetapi.NamespaceDeletion) { r.Spec.HostedClusterName = "" },
			wantErr: "spec.hostedClusterName",
		},
		{
			name: "control plane namespace is not a valid request target",
			mutate: func(r *fleetapi.NamespaceDeletion) {
				r.Spec.HostedClusterNamespaceName += "-hcp1"
			},
			wantErr: "is a control plane namespace",
		},
		{
			name:    "requestReason required",
			mutate:  func(r *fleetapi.NamespaceDeletion) { r.Spec.RequestReason = "" },
			wantErr: "spec.requestReason",
		},
		{
			name:    "caller cannot pin the UID",
			mutate:  func(r *fleetapi.NamespaceDeletion) { r.Status.NamespaceUID = "deadbeef" },
			wantErr: "status.namespaceUID",
		},
		{
			name:    "caller cannot set delete-issued timestamp",
			mutate:  func(r *fleetapi.NamespaceDeletion) { now := metav1.Now(); r.Status.HostedClusterDeleteIssuedAt = &now },
			wantErr: "status.hostedClusterDeleteIssuedAt",
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
			name: "hostedClusterNamespaceName is immutable",
			mutate: func(r *fleetapi.NamespaceDeletion) {
				r.Spec.HostedClusterNamespaceName = "ocm-production-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
			},
			wantErr: "hostedClusterNamespaceName is immutable",
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
			name:    "managementClusterName is immutable",
			mutate:  func(r *fleetapi.NamespaceDeletion) { r.Spec.ManagementClusterName = "other" },
			wantErr: "managementClusterName is immutable",
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
	updated.Status.NamespaceUID = "11111111-1111-1111-1111-111111111111"

	require.Empty(t, ValidateNamespaceDeletionUpdate(context.Background(), updated, old),
		"the controller must be able to pin the UID on first observation")
}

func TestValidateNamespaceDeletionUpdateRejectsRepinnedUID(t *testing.T) {
	old := validRequest()
	old.Status.NamespaceUID = "11111111-1111-1111-1111-111111111111"
	updated := old.DeepCopy()
	updated.Status.NamespaceUID = "22222222-2222-2222-2222-222222222222"

	errs := ValidateNamespaceDeletionUpdate(context.Background(), updated, old)

	require.NotEmpty(t, errs, "repointing a pinned UID silently retargets the request and must be rejected")
	require.Contains(t, errs.ToAggregate().Error(), "namespaceUID is immutable once the controller has pinned it")
}

func TestValidateNamespaceDeletionUpdateRejectsChangedDeleteIssuedAt(t *testing.T) {
	old := validRequest()
	issuedAt := metav1.NewTime(time.Now())
	old.Status.HostedClusterDeleteIssuedAt = &issuedAt
	updated := old.DeepCopy()
	changed := metav1.NewTime(issuedAt.Add(time.Minute))
	updated.Status.HostedClusterDeleteIssuedAt = &changed

	errs := ValidateNamespaceDeletionUpdate(context.Background(), updated, old)
	require.Contains(t, errs.ToAggregate().Error(), "hostedClusterDeleteIssuedAt is immutable once set")
}
