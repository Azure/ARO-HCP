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

package controllerutils

import (
	"context"
	"testing"
	"time"

	"github.com/go-logr/logr"

	clocktesting "k8s.io/utils/clock/testing"

	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/api"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
)

// fakeCooldownActiveOperationLister is a minimal ActiveOperationLister whose
// ListActiveOperationsForCluster returns a fixed set, so the cooldown checker's
// key routing can be exercised without an informer.
type fakeCooldownActiveOperationLister struct {
	clusterOps []*api.Operation
}

var _ listers.ActiveOperationLister = (*fakeCooldownActiveOperationLister)(nil)

func (f *fakeCooldownActiveOperationLister) List(context.Context) ([]*api.Operation, error) {
	return nil, nil
}

func (f *fakeCooldownActiveOperationLister) Get(context.Context, string, string) (*api.Operation, error) {
	return nil, nil
}

func (f *fakeCooldownActiveOperationLister) ListActiveOperationsForCluster(context.Context, string, string, string) ([]*api.Operation, error) {
	return f.clusterOps, nil
}

func (f *fakeCooldownActiveOperationLister) ListActiveOperationsForNodePool(context.Context, string, string, string, string) ([]*api.Operation, error) {
	return nil, nil
}

func (f *fakeCooldownActiveOperationLister) ListActiveOperationsForExternalAuth(context.Context, string, string, string, string) ([]*api.Operation, error) {
	return nil, nil
}

// TestActiveOperationBasedChecker_CredentialKeysUseClusterActivity asserts that
// SystemAdminCredentialRequestKey and SystemAdminCredentialRevocationKey are
// routed through the cluster's active operations. When the cluster has an active
// operation (the in-flight RequestCredential/RevokeCredentials operation), the
// checker must use the fast active-operation cooldown so the issuance/revocation
// pipeline reconciles promptly instead of being throttled by the slow
// inactive-operation cooldown.
func TestActiveOperationBasedChecker_CredentialKeysUseClusterActivity(t *testing.T) {
	const (
		activeCooldown   = 10 * time.Second
		inactiveCooldown = 5 * time.Minute
	)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	newChecker := func(activeOps []*api.Operation) (*ActiveOperationBasedChecker, *clocktesting.FakeClock) {
		clk := clocktesting.NewFakeClock(base)
		checker := NewActiveOperationPrioritizingCooldown(
			&fakeCooldownActiveOperationLister{clusterOps: activeOps},
			activeCooldown,
			inactiveCooldown,
		)
		checker.activeOperationTimer.(*controllerutil.TimeBasedCooldownChecker).SetClock(clk)
		checker.inactiveOperationTimer.(*controllerutil.TimeBasedCooldownChecker).SetClock(clk)
		return checker, clk
	}

	keys := []struct {
		name string
		key  any
	}{
		{
			name: "credential request",
			key:  SystemAdminCredentialRequestKey{SubscriptionID: "sub", ResourceGroupName: "rg", HCPClusterName: "cluster", CredentialName: "cred"},
		},
		{
			name: "revocation",
			key:  SystemAdminCredentialRevocationKey{SubscriptionID: "sub", ResourceGroupName: "rg", HCPClusterName: "cluster", RevocationName: "rev"},
		},
	}

	for _, tc := range keys {
		t.Run(tc.name+"/active cluster operation uses fast cadence", func(t *testing.T) {
			checker, clk := newChecker([]*api.Operation{{}})
			ctx := logr.NewContext(context.Background(), logr.Discard())

			if !checker.CanSync(ctx, tc.key) {
				t.Fatalf("first CanSync should be allowed")
			}
			// Past the active cooldown (10s) but far short of the inactive one (5m).
			clk.Step(activeCooldown + time.Second)
			if !checker.CanSync(ctx, tc.key) {
				t.Fatalf("CanSync should be allowed via the active-operation cooldown after %s; the key was throttled, meaning it was not routed through ListActiveOperationsForCluster", activeCooldown+time.Second)
			}
		})

		t.Run(tc.name+"/no active operation uses slow cadence", func(t *testing.T) {
			checker, clk := newChecker(nil)
			ctx := logr.NewContext(context.Background(), logr.Discard())

			if !checker.CanSync(ctx, tc.key) {
				t.Fatalf("first CanSync should be allowed")
			}
			clk.Step(activeCooldown + time.Second)
			if checker.CanSync(ctx, tc.key) {
				t.Fatalf("CanSync should be throttled by the inactive-operation cooldown when no cluster operation is active")
			}
			clk.Step(inactiveCooldown)
			if !checker.CanSync(ctx, tc.key) {
				t.Fatalf("CanSync should be allowed again after the inactive cooldown of %s elapses", inactiveCooldown)
			}
		})
	}
}
