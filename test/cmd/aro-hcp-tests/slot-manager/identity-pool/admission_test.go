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

package identitypool

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"

	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/assets"
	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/slots"
)

func TestLeaseCredentialUsesSelectedProfileWithoutAzureLogin(t *testing.T) {
	t.Setenv("AZURE_CLIENT_ID", "")
	t.Setenv("AZURE_TENANT_ID", "")
	t.Setenv("AZURE_CLIENT_SECRET", "")
	t.Setenv("AZURE_TOKEN_CREDENTIALS", "")
	t.Setenv("PATH", t.TempDir())
	t.Setenv("CLUSTER_PROFILE_DIR", t.TempDir())
	profile := t.TempDir()
	for name, value := range map[string]string{
		"tenant":        "00000000-0000-0000-0000-000000000001",
		"client-id":     "00000000-0000-0000-0000-000000000002",
		"client-secret": "fake-unit-test-secret",
	} {
		if err := os.WriteFile(filepath.Join(profile, name), []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	request := assets.LeaseRequest{
		SelectedClusterProfileDir: profile,
		State: &slots.AcquiredSlotState{Slot: slots.ExpandedSlot{
			Subscriptions: slots.ResolvedSubscriptions{E2E: slots.ResolvedSubscription{ID: "customer-subscription"}},
		}},
	}
	credential, subscription, err := leaseCredential(request)
	if err != nil {
		t.Fatalf("mounted profile must supply credentials without Azure CLI or environment credentials: %v", err)
	}
	if _, ok := credential.(*azidentity.ClientSecretCredential); !ok || subscription != "customer-subscription" {
		t.Fatalf("expected explicit selected-profile credential and E2E subscription, got %T, %q", credential, subscription)
	}
	if err := os.Remove(filepath.Join(profile, "client-secret")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := leaseCredential(request); !errors.Is(err, os.ErrNotExist) || !strings.Contains(err.Error(), "client-secret") {
		t.Fatalf("missing mounted credential must fail explicitly, got %v", err)
	}
}

func TestRunSerialPanicFailsClosed(t *testing.T) {
	previous := utilruntime.ReallyCrash
	utilruntime.ReallyCrash = false
	defer func() { utilruntime.ReallyCrash = previous }()

	err := runSerial(t.Context(), []func(context.Context) error{
		func(context.Context) error { panic("inventory unavailable") },
	})
	if err == nil || !strings.Contains(err.Error(), "panicked: inventory unavailable") {
		t.Fatalf("panic must become admission error, got %v", err)
	}
}

func TestRunOperationPreservesCrashPolicy(t *testing.T) {
	previous := utilruntime.ReallyCrash
	utilruntime.ReallyCrash = true
	defer func() { utilruntime.ReallyCrash = previous }()
	defer func() {
		if value := recover(); value == nil {
			t.Error("ReallyCrash=true must propagate the panic")
		}
	}()
	_ = runOperation(context.Background(), func(context.Context) error { panic("crash policy") })
}

func TestRunSerialCancellation(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		var active atomic.Int32
		var started atomic.Int32
		operations := make([]func(context.Context) error, 100)
		for i := range operations {
			operations[i] = func(ctx context.Context) error {
				started.Add(1)
				active.Add(1)
				defer active.Add(-1)
				<-ctx.Done()
				return ctx.Err()
			}
		}
		done := make(chan error, 1)
		go func() { done <- runSerial(ctx, operations) }()
		synctest.Wait()
		if active.Load() != 1 {
			t.Fatalf("expected one blocked operation, got %d", active.Load())
		}
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) || active.Load() != 0 {
			t.Fatalf("cancellation failed: err=%v active=%d", err, active.Load())
		}
		if started.Load() != 1 {
			t.Fatalf("started more operations after cancellation: %d", started.Load())
		}
	})
}

func TestRunSerialRunsEveryOperationAndJoinsErrors(t *testing.T) {
	t.Parallel()

	calls := 0
	expectedErr := errors.New("operation failed")
	var operations []func(context.Context) error
	for _, err := range []error{nil, expectedErr, nil} {
		operations = append(operations, func(context.Context) error {
			calls++
			return err
		})
	}
	err := runSerial(context.Background(), operations)
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected joined error to preserve operation failure, got %v", err)
	}
	if calls != len(operations) {
		t.Fatalf("expected all operations to run, got %d of %d", calls, len(operations))
	}
}
