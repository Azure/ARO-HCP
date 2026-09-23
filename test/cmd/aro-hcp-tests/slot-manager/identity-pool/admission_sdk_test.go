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
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v3"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/msi/armmsi"

	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/assets"
	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/slots"
	"github.com/Azure/ARO-HCP/test/util/framework"
)

type admissionCredential struct{}

func (admissionCredential) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "unit-test", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

// This transport never opens a socket. All SDK requests, including deletes and
// subscription-wide role enumeration, are handled by a bounded fake inventory.
type admissionTransport struct {
	mu            sync.Mutex
	scenario      string
	fic, role     bool
	deletes       []string
	roleLists     int
	identityReads []time.Time
	roleReads     []time.Time
	ficReads      int
}

func (a *admissionTransport) Do(request *http.Request) (*http.Response, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	path := request.URL.Path
	status := http.StatusOK
	var payload any
	names := framework.NewDefaultIdentities().ToSlice()
	principal := "00000000-0000-0000-0000-000000000001"
	switch {
	case request.Method == http.MethodDelete:
		if a.scenario == "delete failure" {
			status = http.StatusForbidden
			payload = map[string]any{"error": map[string]string{"code": "Forbidden", "message": "fake delete failure"}}
			break
		}
		a.deletes = append(a.deletes, path)
		if strings.Contains(path, "federatedIdentityCredentials") {
			a.fic = false
		} else if a.scenario != "eventual consistency" {
			a.role = false
		}
		payload = map[string]any{}
	case strings.Contains(path, "/federatedIdentityCredentials/"):
		a.ficReads++
		if a.scenario == "FIC get failure" {
			status = http.StatusForbidden
			payload = map[string]any{"error": map[string]string{"code": "Forbidden", "message": "fake FIC read failure"}}
			break
		}
		if a.fic {
			payload = map[string]any{"name": "previous-run"}
		} else {
			status = http.StatusNotFound
			payload = map[string]any{"error": map[string]string{"code": "ResourceNotFound", "message": "FIC was deleted"}}
		}
	case strings.Contains(path, "/roleAssignments/"):
		a.roleReads = append(a.roleReads, time.Now())
		if a.scenario == "role get failure" {
			status = http.StatusTooManyRequests
			payload = map[string]any{"error": map[string]string{"code": "TooManyRequests", "message": "fake role read throttling"}}
			break
		}
		if a.scenario == "eventual consistency" && len(a.roleReads) == 3 {
			a.role = false
		}
		if a.role {
			payload = map[string]any{"id": path, "properties": map[string]string{"principalId": principal}}
		} else {
			status = http.StatusNotFound
			payload = map[string]any{"error": map[string]string{"code": "RoleAssignmentNotFound", "message": "role assignment was deleted"}}
		}
	case strings.HasSuffix(path, "/userAssignedIdentities"):
		a.identityReads = append(a.identityReads, time.Now())
		if a.scenario == "list failure" {
			return nil, fmt.Errorf("fake identity list failure")
		}
		identities := []map[string]any{}
		for i, name := range names {
			id := fmt.Sprintf("00000000-0000-0000-0000-%012d", i+1)
			if a.scenario == "bad principal" {
				id = "not-a-uuid"
			}
			identities = append(identities, map[string]any{"name": name, "properties": map[string]string{"principalId": id}})
		}
		switch a.scenario {
		case "missing identity":
			identities = identities[1:]
		case "extra identity":
			identities = append(identities, map[string]any{"name": "unexpected", "properties": map[string]string{"principalId": principal}})
		case "duplicate identity":
			identities = append(identities, identities[0])
		}
		payload = map[string]any{"value": identities}
	case strings.HasSuffix(path, "/federatedIdentityCredentials"):
		if a.scenario == "FIC list failure" {
			return nil, fmt.Errorf("fake FIC list failure")
		}
		credentials := []map[string]string{}
		if a.fic && strings.Contains(path, "/"+names[0]+"/") {
			credentials = append(credentials, map[string]string{"name": "previous-run"})
		}
		payload = map[string]any{"value": credentials}
	case strings.HasSuffix(path, "/roleAssignments"):
		a.roleLists++
		if a.scenario == "role list failure" {
			return nil, fmt.Errorf("fake role list failure")
		}

		roles := []map[string]any{
			{"id": "/subscriptions/sub/providers/Microsoft.Authorization/roleAssignments/foreign", "properties": map[string]string{"principalId": "ffffffff-ffff-ffff-ffff-ffffffffffff"}},
		}
		if a.role {
			roles = append(roles, map[string]any{
				"id":         "/subscriptions/sub/resourceGroups/previous-run/providers/Microsoft.Authorization/roleAssignments/11111111-1111-1111-1111-111111111111",
				"properties": map[string]string{"principalId": principal},
			})
		}
		payload = map[string]any{"value": roles}
	default:
		return nil, fmt.Errorf("unexpected SDK request %s %s", request.Method, path)
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return &http.Response{
		StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(strings.NewReader(string(data))), Request: request,
	}, nil
}

func admissionSDKClients(t *testing.T, transport *admissionTransport) (*armmsi.ClientFactory, *armauthorization.RoleAssignmentsClient) {
	t.Helper()
	options := &azcorearm.ClientOptions{ClientOptions: policy.ClientOptions{
		Transport: transport, Retry: policy.RetryOptions{MaxRetries: -1},
	}}
	factory, err := armmsi.NewClientFactory("sub", admissionCredential{}, options)
	if err != nil {
		t.Fatal(err)
	}
	roles, err := armauthorization.NewRoleAssignmentsClient("sub", admissionCredential{}, options)
	if err != nil {
		t.Fatal(err)
	}
	return factory, roles
}

func TestWaitForCleanIdentityLease(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name        string
		scenario    string
		dirty       bool
		checkRole   bool
		checkFIC    bool
		cancelAt    time.Duration
		wantReads   []time.Duration
		wantErr     error
		wantText    string
		wantElapsed time.Duration
	}{
		{name: "already clean"},
		{name: "cancelled before reads", checkRole: true, cancelAt: -time.Second, wantErr: context.Canceled},
		{name: "deleted role", checkRole: true, wantReads: []time.Duration{0}},
		{name: "eventual consistency", scenario: "eventual consistency", dirty: true, checkRole: true, wantReads: []time.Duration{0, 5 * time.Second, 15 * time.Second}, wantElapsed: 15 * time.Second},
		{name: "read error fails immediately even with dirty FIC", scenario: "role get failure", dirty: true, checkRole: true, checkFIC: true, wantReads: []time.Duration{0}, wantText: "fake role read throttling"},
		{name: "FIC read error fails immediately", scenario: "FIC get failure", checkFIC: true, wantText: "fake FIC read failure"},
		{name: "FIC convergence deadline", checkFIC: true, wantErr: context.DeadlineExceeded, wantText: "still exists after preparation", wantElapsed: admissionValidationTimeout},
		{name: "cancel during backoff", dirty: true, checkRole: true, cancelAt: time.Second, wantReads: []time.Duration{0}, wantErr: context.Canceled, wantElapsed: time.Second},
		{name: "convergence deadline", dirty: true, checkRole: true, wantReads: []time.Duration{0, 5 * time.Second, 15 * time.Second, 35 * time.Second, 75 * time.Second}, wantErr: context.DeadlineExceeded, wantText: "still exists after preparation", wantElapsed: admissionValidationTimeout},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				transport := &admissionTransport{scenario: tc.scenario, role: tc.dirty, fic: tc.checkFIC}
				factory, roles := admissionSDKClients(t, transport)
				inventory := &identityLeaseInventory{}
				if tc.checkRole {
					id := "/subscriptions/sub/resourceGroups/previous-run/providers/Microsoft.Authorization/roleAssignments/11111111-1111-1111-1111-111111111111"
					inventory.roleAssignments = []*armauthorization.RoleAssignment{{ID: &id}}
				}
				if tc.checkFIC {
					inventory.federatedCredentials = []federatedCredentialReference{{resourceGroup: "identity-rg", identityName: "identity", name: "previous-run"}}
				}
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				if tc.cancelAt > 0 {
					timer := time.AfterFunc(tc.cancelAt, cancel)
					defer timer.Stop()
				} else if tc.cancelAt < 0 {
					cancel()
				}
				start := time.Now()
				err := waitForCleanIdentityLease(ctx, inventory, factory, roles)
				if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
					t.Fatalf("expected %v, got %v", tc.wantErr, err)
				}
				if tc.wantText != "" && (err == nil || !strings.Contains(err.Error(), tc.wantText)) {
					t.Fatalf("expected error containing %q, got %v", tc.wantText, err)
				}
				if tc.wantErr == nil && tc.wantText == "" && err != nil {
					t.Fatalf("expected clean lease, got %v", err)
				}
				if elapsed := time.Since(start); elapsed != tc.wantElapsed {
					t.Fatalf("expected elapsed %s, got %s", tc.wantElapsed, elapsed)
				}
				if transport.roleLists != 0 || len(transport.identityReads) != 0 {
					t.Fatalf("convergence must not reload inventory, got %d role lists and %d identity lists", transport.roleLists, len(transport.identityReads))
				}
				if len(transport.roleReads) != len(tc.wantReads) {
					t.Fatalf("expected %d targeted reads, got %d", len(tc.wantReads), len(transport.roleReads))
				}
				for i, read := range transport.roleReads {
					if elapsed := read.Sub(start); elapsed != tc.wantReads[i] {
						t.Fatalf("targeted read %d: expected at %s, got %s", i, tc.wantReads[i], elapsed)
					}
				}
			})
		})
	}
}

func TestAdmissionConvergenceAvoidsRepeatedInventory(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		transport := &admissionTransport{scenario: "eventual consistency", fic: true, role: true}
		factory, roles := admissionSDKClients(t, transport)
		request := assets.LeaseRequest{State: &slots.AcquiredSlotState{Slot: slots.ExpandedSlot{
			Assets: slots.ResolvedAssets{E2EIdentities: &slots.ResolvedE2EIdentitiesAsset{
				Allocation: slots.AllocationDedicated, ResourceGroups: []string{"identity-rg"},
			}},
		}}}
		ctx := context.Background()
		if err := prepareIdentityLeaseWithClients(ctx, request, factory, roles); err != nil {
			t.Fatalf("preparation failed: %v", err)
		}
		if transport.roleLists != 1 || len(transport.identityReads) != 1 {
			t.Fatalf("preparation must load inventory once, got %d role lists and %d identity lists", transport.roleLists, len(transport.identityReads))
		}
		if len(transport.roleReads) != 3 || transport.ficReads != 1 {
			t.Fatalf("only pending deletions should be rechecked, got %d role reads and %d FIC reads", len(transport.roleReads), transport.ficReads)
		}
		if err := validateCleanIdentityLease(ctx, request, factory, roles); err != nil {
			t.Fatalf("final validation failed: %v", err)
		}
		if transport.roleLists != 2 || len(transport.identityReads) != 2 {
			t.Fatalf("final validation must independently reload inventory, got %d role lists and %d identity lists", transport.roleLists, len(transport.identityReads))
		}
		transport.role = true
		if err := validateCleanIdentityLease(ctx, request, factory, roles); err == nil {
			t.Fatal("fresh validation accepted a role assignment added after deletion convergence")
		}
	})
}

func TestDedicatedIdentityAdmissionWithFakeSDK(t *testing.T) {
	t.Parallel()
	for _, scenario := range []string{"success", "missing identity", "extra identity", "duplicate identity", "bad principal", "list failure", "FIC list failure", "role list failure", "delete failure"} {
		t.Run(scenario, func(t *testing.T) {
			transport := &admissionTransport{scenario: scenario, fic: true, role: true}
			factory, roles := admissionSDKClients(t, transport)
			request := assets.LeaseRequest{State: &slots.AcquiredSlotState{Slot: slots.ExpandedSlot{
				Assets: slots.ResolvedAssets{E2EIdentities: &slots.ResolvedE2EIdentitiesAsset{
					Allocation: slots.AllocationDedicated, ResourceGroups: []string{"identity-rg"},
				}},
			}}}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			err := prepareIdentityLeaseWithClients(ctx, request, factory, roles)
			if scenario != "success" {
				if err == nil {
					t.Fatal("incomplete or unclean inventory passed preparation")
				}
				if scenario != "delete failure" && len(transport.deletes) != 0 {
					t.Fatalf("mutated resources before completing inventory: %v", transport.deletes)
				}
				return
			}
			if err != nil {
				t.Fatalf("preparation failed: %v", err)
			}
			if err := validateCleanIdentityLease(ctx, request, factory, roles); err != nil {
				t.Fatalf("independent validation failed: %v", err)
			}
			if len(transport.deletes) != 2 || transport.roleLists != 2 {
				t.Fatalf("expected FIC + child-scope role cleanup and one role list per read, got deletes=%v lists=%d", transport.deletes, transport.roleLists)
			}
			handler := NewHandler()
			contract := slots.NewRuntimeContractBuilder()
			if err := handler.PublishLease(ctx, request, contract); err != nil {
				t.Fatal(err)
			}
			data, err := contract.MarshalShell()
			if err != nil || !strings.Contains(string(data), "LEASED_MSI_CONTAINERS='identity-rg'") {
				t.Fatalf("incorrect dedicated asset contract: %s, %v", data, err)
			}
			transport.fic = true
			if err := validateCleanIdentityLease(ctx, request, factory, roles); err == nil {
				t.Fatal("validation accepted remaining FIC residue")
			}
			transport.fic, transport.role = false, true
			if err := validateCleanIdentityLease(ctx, request, factory, roles); err == nil {
				t.Fatal("validation accepted remaining role-assignment residue")
			}
		})
	}
}
