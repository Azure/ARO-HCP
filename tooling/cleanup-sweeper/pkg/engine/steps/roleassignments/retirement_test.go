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

package roleassignments

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/microsoft/kiota-abstractions-go/authentication"
	msgraphsdk "github.com/microsoftgraph/msgraph-sdk-go"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"

	"github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/pkg/engine/runner"
)

const (
	retirementSubscription = "11111111-1111-1111-1111-111111111111"
	retirementTenant       = "22222222-2222-2222-2222-222222222222"
	retirementPrincipal    = "33333333-3333-3333-3333-333333333333"
	retirementRG           = "/subscriptions/" + retirementSubscription + "/resourceGroups/run-owned"
	retirementAssignment   = "/subscriptions/" + retirementSubscription + "/providers/Microsoft.Authorization/roleAssignments/44444444-4444-4444-4444-444444444444"
)

type retirementFixture struct {
	group       armresources.ResourceGroup
	groupErr    error
	tenant      string
	identities  []retiringIdentity
	assignments []roleAssignmentRecord
	current     roleAssignmentRecord
	currentErr  error
	active      bool
	activeErr   error
	deleted     bool
	deletedErr  error
	restored    bool
	activeCalls int
	removed     []string
	removeErr   error
}

func newRetirementFixture() *retirementFixture {
	assignment := roleAssignmentRecord{
		ID: retirementAssignment, PrincipalID: retirementPrincipal, Type: ResourceType,
	}
	return &retirementFixture{
		group:  armresources.ResourceGroup{ID: strPtr(retirementRG)},
		tenant: retirementTenant,
		identities: []retiringIdentity{{
			id:          retirementRG + "/providers/" + managedIdentityType + "/owned",
			principalID: retirementPrincipal, tenantID: retirementTenant,
		}},
		assignments: []roleAssignmentRecord{assignment},
		current:     assignment, active: true, deleted: true,
	}
}

func (f *retirementFixture) api() retirementAPI {
	return retirementAPI{
		group:       func(context.Context) (armresources.ResourceGroup, error) { return f.group, f.groupErr },
		tenant:      func(context.Context) (string, error) { return f.tenant, nil },
		identities:  func(context.Context) ([]retiringIdentity, error) { return f.identities, nil },
		assignments: func(context.Context) ([]roleAssignmentRecord, error) { return f.assignments, nil },
		assignment:  func(context.Context, string) (roleAssignmentRecord, error) { return f.current, f.currentErr },
		active: func(context.Context, string) (bool, error) {
			f.activeCalls++
			return f.active, f.activeErr
		},
		deleted: func(context.Context, string) (bool, error) {
			if f.restored {
				f.active = true
			}
			return f.deleted, f.deletedErr
		},
		remove: func(_ context.Context, id string) error {
			f.removed = append(f.removed, id)
			return f.removeErr
		},
	}
}

func (f *retirementFixture) retire() {
	f.groupErr = &azcore.ResponseError{StatusCode: http.StatusNotFound}
	f.active = false
	f.activeCalls = 0
}

func TestCaptureResourceGroupRetirementSafety(t *testing.T) {
	tests := []struct {
		name   string
		change func(*retirementFixture)
	}{
		{"missing group ID", func(f *retirementFixture) { f.group.ID = nil }},
		{"wrong group", func(f *retirementFixture) { f.group.ID = strPtr(retirementRG + "-other") }},
		{"managed group", func(f *retirementFixture) { f.group.ManagedBy = strPtr("cluster") }},
		{"persistent group", func(f *retirementFixture) { f.group.Tags = map[string]*string{"Persist": strPtr("TRUE")} }},
		{"ambiguous persist tag", func(f *retirementFixture) { f.group.Tags = map[string]*string{"persist": nil} }},
		{"group lookup forbidden", func(f *retirementFixture) { f.groupErr = &azcore.ResponseError{StatusCode: 403} }},
		{"missing tenant", func(f *retirementFixture) { f.tenant = "" }},
		{"wrong identity tenant", func(f *retirementFixture) { f.identities[0].tenantID = retirementSubscription }},
		{"pool identity outside group", func(f *retirementFixture) {
			f.identities[0].id = strings.ReplaceAll(f.identities[0].id, "run-owned", "aro-hcp-msi-container-pool")
		}},
		{"wrong identity subscription", func(f *retirementFixture) {
			f.identities[0].id = strings.ReplaceAll(f.identities[0].id, retirementSubscription, retirementTenant)
		}},
		{"unexpected identity type", func(f *retirementFixture) {
			f.identities[0].id = retirementRG + "/providers/Microsoft.Compute/virtualMachines/vm"
		}},
		{"malformed principal", func(f *retirementFixture) { f.identities[0].principalID = "not-a-guid" }},
		{"missing principal", func(f *retirementFixture) { f.identities[0].principalID = "" }},
		{"principal not positively resolved", func(f *retirementFixture) { f.active = false }},
		{"directory forbidden", func(f *retirementFixture) { f.activeErr = errors.New("403") }},
		{"malformed directory response", func(f *retirementFixture) { f.activeErr = errors.New("missing or unexpected ID") }},
		{"wrong assignment subscription", func(f *retirementFixture) {
			f.assignments[0].ID = strings.ReplaceAll(retirementAssignment, retirementSubscription, retirementTenant)
		}},
		{"wrong assignment type", func(f *retirementFixture) { f.assignments[0].ID = retirementRG }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			f := newRetirementFixture()
			test.change(f)
			if _, err := captureResourceGroupRetirement(context.Background(), retirementRG, f.api()); err == nil {
				t.Fatal("expected unsafe ownership evidence to fail capture")
			}
			if len(f.removed) != 0 {
				t.Fatal("capture must never delete assignments")
			}
		})
	}
}

func TestCaptureResourceGroupRetirementExcludesUnownedPrincipals(t *testing.T) {
	f := newRetirementFixture()
	f.assignments = append(f.assignments, roleAssignmentRecord{
		ID: retirementAssignment + "-shared", PrincipalID: retirementTenant,
	})
	retirement, err := captureResourceGroupRetirement(context.Background(), retirementRG, f.api())
	if err != nil {
		t.Fatal(err)
	}
	if len(retirement.assignments) != 1 || retirement.assignments[0].ID != retirementAssignment {
		t.Fatalf("expected only owned assignment, got %+v", retirement.assignments)
	}
	if len(f.removed) != 0 {
		t.Fatal("capture deleted an assignment")
	}
}

func TestResourceGroupRetirementCleanup(t *testing.T) {
	tests := []struct {
		name        string
		change      func(*retirementFixture)
		wantError   bool
		wantDeletes int
	}{
		{"soft deleted owned principal", func(*retirementFixture) {}, false, 1},
		{"permanently absent owned principal", func(f *retirementFixture) { f.deleted = false }, false, 1},
		{"active principal", func(f *retirementFixture) { f.active = true }, true, 0},
		{"restored between lookups", func(f *retirementFixture) { f.restored = true }, true, 0},
		{"group still alive", func(f *retirementFixture) { f.groupErr = nil }, true, 0},
		{"group lookup forbidden", func(f *retirementFixture) { f.groupErr = &azcore.ResponseError{StatusCode: 403} }, true, 0},
		{"assignment changed principal", func(f *retirementFixture) { f.current.PrincipalID = retirementTenant }, true, 0},
		{"assignment missing principal", func(f *retirementFixture) { f.current.PrincipalID = "" }, true, 0},
		{"assignment changed ID", func(f *retirementFixture) { f.current.ID += "-other" }, true, 0},
		{"assignment read forbidden", func(f *retirementFixture) { f.currentErr = &azcore.ResponseError{StatusCode: 403} }, true, 0},
		{"assignment already gone", func(f *retirementFixture) { f.currentErr = &azcore.ResponseError{StatusCode: 404} }, false, 0},
		{"active lookup forbidden", func(f *retirementFixture) { f.activeErr = errors.New("403") }, true, 0},
		{"deleted lookup forbidden", func(f *retirementFixture) { f.deletedErr = errors.New("403") }, true, 0},
		{"deleted lookup malformed", func(f *retirementFixture) { f.deletedErr = errors.New("missing ID") }, true, 0},
		{"delete forbidden", func(f *retirementFixture) { f.removeErr = &azcore.ResponseError{StatusCode: 403} }, true, 1},
		{"delete already gone", func(f *retirementFixture) { f.removeErr = &azcore.ResponseError{StatusCode: 404} }, false, 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			f := newRetirementFixture()
			retirement, err := captureResourceGroupRetirement(context.Background(), retirementRG, f.api())
			if err != nil {
				t.Fatal(err)
			}
			f.retire()
			test.change(f)
			err = retirement.Cleanup(context.Background())
			if (err != nil) != test.wantError || len(f.removed) != test.wantDeletes {
				t.Fatalf("error=%v deleted=%v, want error=%t deletes=%d", err, f.removed, test.wantError, test.wantDeletes)
			}
			if test.wantDeletes > 0 && f.activeCalls != 2 {
				t.Fatalf("expected two active-directory checks, got %d", f.activeCalls)
			}
			if test.name == "active principal" && !errors.Is(err, runner.ErrTargetRetained) {
				t.Fatal("directory propagation should be retriable")
			}
			if test.name == "restored between lookups" && errors.Is(err, runner.ErrTargetRetained) {
				t.Fatal("an observed restoration race must stop rather than retry")
			}
		})
	}
}

func TestCaptureResourceGroupRetirementRejectsSharedGroups(t *testing.T) {
	for _, group := range []string{"aro-hcp-msi-container-pool", "env-shared-resources"} {
		t.Run(group, func(t *testing.T) {
			f := newRetirementFixture()
			id := strings.ReplaceAll(retirementRG, "run-owned", group)
			f.group.ID = &id
			if _, err := captureResourceGroupRetirement(context.Background(), id, f.api()); err == nil {
				t.Fatal("expected shared resource group to be rejected")
			}
		})
	}
}

type retirementRoundTripper func(*http.Request) (*http.Response, error)

func (f retirementRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestRetirementGraphResponseValidation(t *testing.T) {
	for _, test := range []struct {
		name                 string
		status               int
		body                 string
		wantFound, wantError bool
	}{
		{"valid object", 200, `{"id":"` + retirementPrincipal + `"}`, true, false},
		{"missing ID", 200, `{}`, false, true},
		{"empty ID", 200, `{"id":""}`, false, true},
		{"unexpected ID", 200, `{"id":"` + retirementTenant + `"}`, false, true},
		{"missing response", 204, "", false, true},
		{"not found", 404, `{"error":{"code":"Request_ResourceNotFound","message":"gone"}}`, false, false},
		{"forbidden", 403, `{"error":{"code":"Authorization_RequestDenied","message":"denied"}}`, false, true},
		{"server error", 500, `{"error":{"code":"InternalServerError","message":"failed"}}`, false, true},
	} {
		for _, deleted := range []bool{false, true} {
			name := "active/"
			if deleted {
				name = "deleted/"
			}
			t.Run(name+test.name, func(t *testing.T) {
				client := &http.Client{Transport: retirementRoundTripper(func(request *http.Request) (*http.Response, error) {
					if request.Method != http.MethodGet {
						t.Fatalf("directory mutation attempted: %s", request.Method)
					}
					wantPath := "/v1.0/servicePrincipals/" + retirementPrincipal
					if deleted {
						wantPath = "/v1.0/directory/deletedItems/" + retirementPrincipal
					}
					if request.URL.Path != wantPath {
						t.Fatalf("unexpected Graph path %q, want %q", request.URL.Path, wantPath)
					}
					return &http.Response{
						StatusCode:    test.status,
						ContentLength: int64(len(test.body)),
						Header:        http.Header{"Content-Type": []string{"application/json"}},
						Body:          io.NopCloser(strings.NewReader(test.body)),
						Request:       request,
					}, nil
				})}
				adapter, err := msgraphsdk.NewGraphRequestAdapterWithParseNodeFactoryAndSerializationWriterFactoryAndHttpClient(
					&authentication.AnonymousAuthenticationProvider{}, nil, nil, client,
				)
				if err != nil {
					t.Fatal(err)
				}
				graph := msgraphsdk.NewGraphServiceClient(adapter)
				var found bool
				if deleted {
					found, err = newGraphDeletedPrincipalLookup(graph)(context.Background(), retirementPrincipal)
				} else {
					found, err = newGraphRetiringPrincipalLookup(graph)(context.Background(), retirementPrincipal)
				}
				if found != test.wantFound || (err != nil) != test.wantError {
					t.Fatalf("found=%t error=%v, want found=%t error=%t", found, err, test.wantFound, test.wantError)
				}
			})
		}
	}

}

func TestResourceGroupRetirementRetriesCapturedAssignmentsOnly(t *testing.T) {
	f := newRetirementFixture()
	retirement, err := captureResourceGroupRetirement(context.Background(), retirementRG, f.api())
	if err != nil {
		t.Fatal(err)
	}
	f.retire()
	f.active = true
	if err := retirement.Cleanup(context.Background()); !errors.Is(err, runner.ErrTargetRetained) {
		t.Fatalf("expected retention while Graph still reports active, got %v", err)
	}
	if len(f.removed) != 0 {
		t.Fatal("active principal's assignment was deleted")
	}
	f.active = false
	if err := retirement.Cleanup(context.Background()); err != nil {
		t.Fatalf("expected cleanup after propagation, got %v", err)
	}
	if len(f.removed) != 1 || f.removed[0] != retirementAssignment {
		t.Fatalf("expected the captured assignment only, got %v", f.removed)
	}
}
