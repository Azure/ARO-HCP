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

	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
)

func validCleanup() *fleetapi.StuckClusterCleanup {
	return &fleetapi.StuckClusterCleanup{
		Spec: fleetapi.StuckClusterCleanupSpec{
			Mode:          fleetapi.StuckClusterCleanupModeInspect,
			NamespaceName: "ocm-production-abc123-mycluster",
			RequestReason: "AROSLSRE-2477 stuck namespace sweep",
		},
	}
}

func TestValidateStuckClusterCleanupCreate(t *testing.T) {
	for _, tt := range []struct {
		name    string
		mutate  func(*fleetapi.StuckClusterCleanup)
		wantErr bool
	}{
		{
			name:   "valid inspect request",
			mutate: func(*fleetapi.StuckClusterCleanup) {},
		},
		{
			name:   "valid execute request",
			mutate: func(c *fleetapi.StuckClusterCleanup) { c.Spec.Mode = fleetapi.StuckClusterCleanupModeExecute },
		},
		{
			name:    "missing mode",
			mutate:  func(c *fleetapi.StuckClusterCleanup) { c.Spec.Mode = "" },
			wantErr: true,
		},
		{
			name:    "unknown mode",
			mutate:  func(c *fleetapi.StuckClusterCleanup) { c.Spec.Mode = "Delete" },
			wantErr: true,
		},
		{
			name:    "missing namespace name",
			mutate:  func(c *fleetapi.StuckClusterCleanup) { c.Spec.NamespaceName = "" },
			wantErr: true,
		},
		{
			name:    "namespace name is not a DNS label",
			mutate:  func(c *fleetapi.StuckClusterCleanup) { c.Spec.NamespaceName = "Not A Namespace" },
			wantErr: true,
		},
		{
			name:    "missing request reason",
			mutate:  func(c *fleetapi.StuckClusterCleanup) { c.Spec.RequestReason = "" },
			wantErr: true,
		},
		{
			// The UID is established by observing the namespace. Letting a
			// caller assert it would let them pin an identity the controller is
			// supposed to discover.
			name:    "caller supplied namespace UID",
			mutate:  func(c *fleetapi.StuckClusterCleanup) { c.Spec.NamespaceUID = "11111111-2222-3333-4444-555555555555" },
			wantErr: true,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cleanup := validCleanup()
			tt.mutate(cleanup)

			errs := ValidateStuckClusterCleanupCreate(context.Background(), cleanup)
			if gotErr := len(errs) > 0; gotErr != tt.wantErr {
				t.Fatalf("ValidateStuckClusterCleanupCreate() error = %v, wantErr %v", errs.ToAggregate(), tt.wantErr)
			}
		})
	}
}

func TestValidateStuckClusterCleanupUpdate(t *testing.T) {
	for _, tt := range []struct {
		name    string
		mutate  func(*fleetapi.StuckClusterCleanup)
		wantErr bool
	}{
		{
			name:   "no change",
			mutate: func(*fleetapi.StuckClusterCleanup) {},
		},
		{
			// The controller pinning the UID for the first time is the one
			// mutation the spec is allowed to take.
			name:   "controller pins the UID for the first time",
			mutate: func(c *fleetapi.StuckClusterCleanup) { c.Spec.NamespaceUID = "11111111-2222-3333-4444-555555555555" },
		},
		{
			name:    "mode changed",
			mutate:  func(c *fleetapi.StuckClusterCleanup) { c.Spec.Mode = fleetapi.StuckClusterCleanupModeExecute },
			wantErr: true,
		},
		{
			name:    "namespace name changed",
			mutate:  func(c *fleetapi.StuckClusterCleanup) { c.Spec.NamespaceName = "some-other-namespace" },
			wantErr: true,
		},
		{
			name:    "request reason changed",
			mutate:  func(c *fleetapi.StuckClusterCleanup) { c.Spec.RequestReason = "different reason" },
			wantErr: true,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			oldCleanup := validCleanup()
			newCleanup := oldCleanup.DeepCopy()
			tt.mutate(newCleanup)

			errs := ValidateStuckClusterCleanupUpdate(context.Background(), newCleanup, oldCleanup)
			if gotErr := len(errs) > 0; gotErr != tt.wantErr {
				t.Fatalf("ValidateStuckClusterCleanupUpdate() error = %v, wantErr %v", errs.ToAggregate(), tt.wantErr)
			}
		})
	}
}

// A pinned UID must not be repointed: a different UID means the namespace was
// recreated, so the request no longer refers to what the operator asked about.
func TestValidateStuckClusterCleanupUpdateRejectsRepinnedUID(t *testing.T) {
	oldCleanup := validCleanup()
	oldCleanup.Spec.NamespaceUID = "11111111-2222-3333-4444-555555555555"

	newCleanup := oldCleanup.DeepCopy()
	newCleanup.Spec.NamespaceUID = "99999999-8888-7777-6666-555555555555"

	errs := ValidateStuckClusterCleanupUpdate(context.Background(), newCleanup, oldCleanup)
	if len(errs) == 0 {
		t.Fatal("expected repinning a already-pinned namespaceUID to be rejected, got no error")
	}
}
