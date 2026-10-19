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

package infrastructureidentities

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
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
)

type admissionCredential struct{}

func (admissionCredential) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "unit-test", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

type admissionTransport struct {
	active        atomic.Int32
	mu            sync.Mutex
	scenario      string
	role          bool
	fic           bool
	deletes       []string
	roleLists     int
	identityLists int
	ficLists      int
}

func (a *admissionTransport) Do(request *http.Request) (*http.Response, error) {
	active := a.active.Add(1)
	defer a.active.Add(-1)
	if active != 1 {
		return nil, errors.New("admission issued concurrent ARM requests")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	path := request.URL.Path
	lower := strings.ToLower(path)
	status := http.StatusOK
	var payload any
	switch {
	case request.Method == http.MethodDelete:
		if a.scenario == "role delete failure" && strings.Contains(lower, "/roleassignments/") {
			status = http.StatusForbidden
			payload = map[string]any{"error": map[string]string{"code": "Forbidden", "message": "fake delete failure"}}
			break
		}
		if a.scenario == "fic delete failure" && strings.Contains(lower, "/federatedidentitycredentials/") {
			status = http.StatusForbidden
			payload = map[string]any{"error": map[string]string{"code": "Forbidden", "message": "fake fic delete failure"}}
			break
		}
		a.deletes = append(a.deletes, path)
		payload = map[string]any{}
		if a.scenario == "already deleted" {
			status = http.StatusNotFound
			payload = map[string]any{"error": map[string]string{"code": "ResourceNotFound", "message": "already deleted"}}
		}
	case strings.HasSuffix(lower, "/federatedidentitycredentials"):
		a.ficLists++
		if a.scenario == "fic list failure" {
			return nil, fmt.Errorf("fake fic list failure")
		}
		credentials := []map[string]any{}
		ficIdentity := "/admin-api/"
		if a.scenario == "mixed case" {
			ficIdentity = "/Admin-API/"
		}
		if a.fic && strings.Contains(path, ficIdentity) {
			credentials = append(credentials, map[string]any{"name": "admin-api-westus3-fedcred"})
		}
		payload = map[string]any{"value": credentials}
	case strings.HasSuffix(lower, "/userassignedidentities"):
		a.identityLists++
		if a.scenario == "list failure" {
			return nil, fmt.Errorf("fake identity list failure")
		}
		resourceGroup := resourceGroupFromPath(path)
		names := DefaultTopology().ServiceIdentityNames
		if strings.Contains(resourceGroup, "-mgmt-") {
			names = DefaultTopology().ManagementIdentityNames
		}
		identities := []map[string]any{}
		for i, name := range names {
			id := fmt.Sprintf("00000000-0000-0000-0000-%012d", i+1)
			if i == 0 {
				id = "00000000-0000-0000-0000-0000000000ab"
			}
			if a.scenario == "mixed case" && name == "admin-api" {
				name = "Admin-API"
			}
			switch a.scenario {
			case "bad principal":
				id = "not-a-uuid"
			case "padded identity principal":
				id = " " + id + " "
			}
			identities = append(identities, map[string]any{"name": name, "properties": map[string]string{"principalId": id}})
		}
		if a.scenario != "no extras" {
			identities = append(identities, map[string]any{"name": "unrelated", "properties": map[string]string{"principalId": "ffffffff-ffff-ffff-ffff-ffffffffffff"}})
		}
		switch a.scenario {
		case "missing identity":
			identities = identities[1:]
		case "duplicate identity":
			identities = append(identities, identities[0])
		}
		payload = map[string]any{"value": identities}
	case strings.HasSuffix(lower, "/roleassignments"):
		a.roleLists++
		if a.scenario == "role list failure" {
			return nil, fmt.Errorf("fake role list failure")
		}
		principal := "00000000-0000-0000-0000-0000000000ab"
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

func resourceGroupFromPath(path string) string {
	lower := strings.ToLower(path)
	marker := "/resourcegroups/"
	index := strings.Index(lower, marker)
	if index < 0 {
		return ""
	}
	rest := path[index+len(marker):]
	name, _, _ := strings.Cut(rest, "/")
	return name
}

func admissionSDKClients(t *testing.T, transport *admissionTransport) *admitClients {
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
	return &admitClients{
		identities: factory.NewUserAssignedIdentitiesClient(),
		fics:       factory.NewFederatedIdentityCredentialsClient(),
		roles:      roles,
	}
}

func admittedRequest() assets.LeaseRequest {
	return assets.LeaseRequest{AcquiredSlotState: &slots.AcquiredSlotState{Slot: slots.ExpandedSlot{
		Assets: slots.ResolvedAssets{InfrastructureIdentities: &slots.ResolvedInfrastructureIdentities{
			Allocation: slots.AllocationLeased, ResourceGroups: []string{"bundle-03"}, Identities: DefaultTopology().IdentityInventory(),
		}},
	}}}
}

func TestAdmissionCleansFICsAndRolesWithoutRescan(t *testing.T) {
	t.Parallel()
	for _, scenario := range []string{"clean", "residue", "already deleted", "no extras", "mixed case"} {
		t.Run(scenario, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				dirty := scenario == "residue" || scenario == "already deleted" || scenario == "mixed case"
				transport := &admissionTransport{scenario: scenario, role: dirty, fic: dirty}
				err := admitIdentityLeaseWithClients(t.Context(), admittedRequest(), DefaultTopology(), admissionSDKClients(t, transport), 1)
				if err != nil {
					t.Fatalf("admission failed: %v", err)
				}
				if transport.roleLists != 1 || transport.identityLists != 3 {
					t.Fatalf("admission must load inventory once, got %d role lists and %d identity lists", transport.roleLists, transport.identityLists)
				}
				if transport.ficLists != 30 {
					t.Fatalf("admission must list federated credentials for every expected identity, got %d", transport.ficLists)
				}
				wantDeletes := 0
				if dirty {
					wantDeletes = 2
				}
				if len(transport.deletes) != wantDeletes {
					t.Fatalf("expected %d deletes, got %v", wantDeletes, transport.deletes)
				}
				if scenario == "mixed case" {
					found := false
					for _, path := range transport.deletes {
						if strings.Contains(path, "/Admin-API/") {
							found = true
							break
						}
					}
					if !found {
						t.Fatalf("FIC cleanup must use the Azure identity name, got %v", transport.deletes)
					}
				}
				for _, path := range transport.deletes {
					if strings.HasSuffix(path, "/foreign") {
						t.Fatalf("deleted another principal's role assignment: %s", path)
					}
				}
			})
		})
	}
}

func TestAdmissionFailsClosed(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ scenario, want string }{
		{"missing identity", "missing=["},
		{"duplicate identity", "duplicate identity"},
		{"bad principal", "invalid principal ID"},
		{"padded identity principal", "invalid principal ID"},
		{"list failure", "fake identity list failure"},
		{"fic list failure", "fake fic list failure"},
		{"role list failure", "fake role list failure"},
		{"role delete failure", "failed deleting role assignment"},
		{"fic delete failure", "failed deleting federated identity credential"},
	} {
		t.Run(tc.scenario, func(t *testing.T) {
			transport := &admissionTransport{scenario: tc.scenario, role: true, fic: true}
			err := admitIdentityLeaseWithClients(context.Background(), admittedRequest(), DefaultTopology(), admissionSDKClients(t, transport), 1)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected admission failure containing %q, got %v", tc.want, err)
			}
			if !strings.HasSuffix(tc.scenario, "delete failure") && len(transport.deletes) != 0 {
				t.Fatalf("mutated resources before completing inventory: %v", transport.deletes)
			}
		})
	}
}
