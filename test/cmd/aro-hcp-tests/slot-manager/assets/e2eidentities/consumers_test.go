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

package e2eidentities

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/go-logr/logr"
	"github.com/go-logr/logr/funcr"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"

	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/assets"
	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/slots"
	hcpsdk "github.com/Azure/ARO-HCP/test/sdk/v20261001preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/framework"
)

func TestAdmissionFiltersCompleteConsumerInventory(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name          string
		mode          string
		minimum       int
		incomplete    bool
		allReferenced bool
		wantErr       bool
	}{
		{name: "mixed containers at minimum capacity", minimum: minimumIdentityContainers},
		{name: "insufficient safe capacity", minimum: 4, wantErr: true},
		{name: "no safe containers", allReferenced: true, wantErr: true},
		{name: "match followed by inventory failure", incomplete: true, wantErr: true},
		{name: "audit retains all containers", mode: "audit"},
		{name: "audit reports incomplete inventory", mode: "audit", incomplete: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			groups := []string{"identity-rg-00", "identity-rg-01", "identity-rg-02", "identity-rg-03", "identity-rg-04"}
			transport := &admissionTransport{fic: true, principals: map[string]string{}}
			for i, group := range groups {
				transport.principals[group] = fmt.Sprintf("aaaaaaaa-0000-0000-0000-%012d", i)
			}
			identityID := func(group string) string {
				return "/subscriptions/sub/resourceGroups/" + group + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/service"
			}
			clusterID := "/subscriptions/sub/resourceGroups/old/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/"
			cluster := func(name, group string) *hcpsdk.HcpOpenShiftCluster {
				return &hcpsdk.HcpOpenShiftCluster{
					ID: to.Ptr(clusterID + name),
					Properties: &hcpsdk.HcpOpenShiftClusterProperties{
						Platform: &hcpsdk.PlatformProfile{OperatorsAuthentication: &hcpsdk.OperatorsAuthenticationProfile{
							UserAssignedIdentities: &hcpsdk.UserAssignedIdentitiesProfile{
								ServiceManagedIdentity: to.Ptr(identityID(group)),
								ControlPlaneOperators:  map[string]*string{"operator": to.Ptr(identityID("other"))},
								DataPlaneOperators:     map[string]*string{"operator": to.Ptr(identityID("other"))},
							},
						}},
					},
				}
			}
			consumerCalls := 0
			transport.consumerResponse = func(req *http.Request) (any, int, error) {
				consumerCalls++
				secondPage := req.URL.Query().Get("page") == "2"
				nodePools := strings.HasSuffix(req.URL.Path, "/nodePools")
				next := *req.URL
				query := next.Query()
				query.Set("page", "2")
				next.RawQuery = query.Encode()
				switch {
				case !nodePools && !secondPage:
					first := cluster("first", groups[3])
					if test.allReferenced {
						for _, group := range groups {
							first.Properties.Platform.OperatorsAuthentication.UserAssignedIdentities.ControlPlaneOperators[group] = to.Ptr(identityID(group))
						}
					}
					return map[string]any{"value": []*hcpsdk.HcpOpenShiftCluster{first}, "nextLink": next.String()}, http.StatusOK, nil
				case !nodePools && test.incomplete:
					return map[string]any{}, http.StatusOK, nil
				case !nodePools:
					return map[string]any{"value": []*hcpsdk.HcpOpenShiftCluster{cluster("second", "other")}}, http.StatusOK, nil
				case strings.Contains(req.URL.Path, "/first/"):
					return map[string]any{"value": []any{}}, http.StatusOK, nil
				case !secondPage:
					return map[string]any{"value": []any{}, "nextLink": next.String()}, http.StatusOK, nil
				default:
					node := &hcpsdk.NodePool{
						ID: to.Ptr(clusterID + "second/nodePools/workers"),
						Identity: &hcpsdk.ManagedServiceIdentity{
							Type:                   to.Ptr(hcpsdk.ManagedServiceIdentityTypeUserAssigned),
							UserAssignedIdentities: map[string]*hcpsdk.UserAssignedIdentity{strings.ToUpper(identityID(groups[4])): {}},
						},
					}
					return map[string]any{"value": []*hcpsdk.NodePool{node}}, http.StatusOK, nil
				}
			}
			factory, roles, hcp := admissionSDKClients(t, transport)
			state := &slots.AcquiredSlotState{Slot: slots.ExpandedSlot{
				Environment:   "stg",
				Subscriptions: slots.ResolvedSubscriptions{E2E: slots.ResolvedSubscription{ID: "sub"}},
				Assets:        slots.ResolvedAssets{E2EIdentities: &slots.ResolvedE2EIdentitiesAsset{ResourceGroups: slices.Clone(groups)}},
			}}
			request := assets.LeaseRequest{AcquiredSlotState: state, IdentityConsumerGuardMode: test.mode, MinimumIdentityContainers: test.minimum}
			var logs strings.Builder
			ctx := logr.NewContext(t.Context(), funcr.New(func(_, message string) { logs.WriteString(message) }, funcr.Options{}))
			err := admitIdentityLeaseWithClients(ctx, request, factory, roles, hcp)
			if (err != nil) != test.wantErr {
				t.Fatalf("admission error=%v, want error=%t", err, test.wantErr)
			}
			wantCalls := 5
			if test.incomplete {
				wantCalls = 3
			}
			if consumerCalls != wantCalls {
				t.Fatalf("must inventory every page once, got %d calls, want %d", consumerCalls, wantCalls)
			}
			if !slices.Equal(state.Slot.IdentityContainerNames(), groups) {
				t.Fatal("filtering changed dedicated lease ownership")
			}
			if test.wantErr {
				if len(transport.deletes) != 0 || transport.identityLists != 0 || len(state.AdmittedIdentityContainers) != 0 {
					t.Fatal("failed admission inventoried cleanup, mutated identities or admitted containers")
				}
				return
			}
			want := groups[:3]
			if test.mode == "audit" {
				want = groups
			}
			if !slices.Equal(state.AdmittedIdentityContainers, want) || transport.identityLists != len(want) || len(transport.deletes) != 2*len(want) {
				t.Fatalf("admitted=%v identityLists=%d deletes=%v, want only %v", state.AdmittedIdentityContainers, transport.identityLists, transport.deletes, want)
			}
			contract := slots.NewRuntimeContractBuilder()
			if err := NewHandler().PublishLease(ctx, request, contract); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(contract.MarshalShell()), "export LEASED_MSI_CONTAINERS='"+strings.Join(want, " ")+"'") {
				t.Fatalf("incorrect runtime export: %s", contract.MarshalShell())
			}
			if test.mode != "audit" {
				for _, excluded := range groups[3:] {
					if !strings.Contains(logs.String(), `"container"="`+excluded+`"`) {
						t.Fatalf("missing exclusion audit for %s", excluded)
					}
					for _, path := range transport.deletes {
						if strings.Contains(path, excluded) {
							t.Fatalf("deleted excluded container FIC or principal role: %s", path)
						}
					}
				}
			}
		})
	}
}
func TestAdmissionProtectsConsumersBeforeAnyDelete(t *testing.T) {
	t.Parallel()
	for _, scenario := range []string{
		"unrelated", "other subscription", "Succeeded", "Failed", "Deleting", "Updating",
		"cluster identity", "data plane", "service", "node pool", "case insensitive",
		"container registry", "missing registry identity", "unrelated registry identity",
		"nil identity type", "empty identity type", "unknown identity type", "unsupported API",
		"later cluster page", "later node pool page", "HCP list failure", "node pool list failure",
		"later cluster list failure", "later node pool list failure", "missing HCP value",
		"nil HCP", "missing HCP ID", "wrong subscription", "missing properties", "missing profile",
		"missing service", "nil operator", "empty operator", "invalid operator", "empty identity map",
		"nil node pool", "missing node pool ID", "wrong node pool parent", "missing node pool value",
		"cancelled", "deadline", "FIC only", "dev",
	} {
		for _, mode := range []string{"enforce", "audit"} {
			for _, skipCleanup := range []bool{false, true} {
				name := scenario + "/" + mode
				if skipCleanup {
					name += "/cleanup disabled"
				}
				t.Run(name, func(t *testing.T) {
					owned := "/subscriptions/sub/resourceGroups/identity-rg-01/providers/Microsoft.ManagedIdentity/userAssignedIdentities/" + framework.NewDefaultIdentities().ToSlice()[0]
					unrelated := strings.Replace(owned, "identity-rg-01", "unrelated", 1)
					clusterID := "/subscriptions/sub/resourceGroups/old/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/old"
					profile := &hcpsdk.UserAssignedIdentitiesProfile{
						ServiceManagedIdentity: to.Ptr(unrelated),
						ControlPlaneOperators:  map[string]*string{"operator": to.Ptr(unrelated)},
						DataPlaneOperators:     map[string]*string{"operator": to.Ptr(unrelated)},
					}
					cluster := &hcpsdk.HcpOpenShiftCluster{
						ID: to.Ptr(clusterID),
						Properties: &hcpsdk.HcpOpenShiftClusterProperties{
							ProvisioningState: to.Ptr(hcpsdk.ProvisioningState(scenario)),
							Platform:          &hcpsdk.PlatformProfile{OperatorsAuthentication: &hcpsdk.OperatorsAuthenticationProfile{UserAssignedIdentities: profile}},
						},
					}
					nodePool := &hcpsdk.NodePool{ID: to.Ptr(clusterID + "/nodePools/workers")}
					identity := &hcpsdk.ManagedServiceIdentity{
						Type:                   to.Ptr(hcpsdk.ManagedServiceIdentityTypeUserAssigned),
						UserAssignedIdentities: map[string]*hcpsdk.UserAssignedIdentity{owned: {}},
					}
					switch scenario {
					case "Succeeded", "Failed", "Deleting", "Updating", "later cluster page", "FIC only":
						profile.ControlPlaneOperators["operator"] = &owned
					case "case insensitive":
						profile.ControlPlaneOperators["operator"] = to.Ptr(strings.ToUpper(owned))
					case "other subscription":
						profile.ControlPlaneOperators["operator"] = to.Ptr(strings.Replace(owned, "/sub/", "/other/", 1))
					case "cluster identity":
						cluster.Identity = identity
					case "data plane":
						profile.DataPlaneOperators["operator"] = &owned
					case "service":
						profile.ServiceManagedIdentity = &owned
					case "node pool", "later node pool page":
						nodePool.Identity = identity
					case "container registry":
						cluster.Properties.Platform.ContainerRegistry = &hcpsdk.ContainerRegistryProfile{ManagedIdentity: &owned}
					case "missing registry identity":
						cluster.Properties.Platform.ContainerRegistry = &hcpsdk.ContainerRegistryProfile{}
					case "unrelated registry identity":
						cluster.Properties.Platform.ContainerRegistry = &hcpsdk.ContainerRegistryProfile{ManagedIdentity: &unrelated}
					case "nil identity type", "empty identity type", "unknown identity type":
						cluster.Identity = &hcpsdk.ManagedServiceIdentity{}
						if scenario != "nil identity type" {
							value := ""
							if scenario == "unknown identity type" {
								value = "unknown"
							}
							cluster.Identity.Type = to.Ptr(hcpsdk.ManagedServiceIdentityType(value))
						}
					case "nil HCP":
						cluster = nil
					case "missing HCP ID":
						cluster.ID = nil
					case "wrong subscription":
						cluster.ID = to.Ptr(strings.Replace(clusterID, "/sub/", "/other/", 1))
					case "missing properties":
						cluster.Properties = nil
					case "missing profile":
						cluster.Properties.Platform.OperatorsAuthentication = nil
					case "missing service":
						profile.ServiceManagedIdentity = nil
					case "nil operator":
						profile.ControlPlaneOperators["operator"] = nil
					case "empty operator":
						profile.ControlPlaneOperators["operator"] = to.Ptr("")
					case "invalid operator":
						profile.ControlPlaneOperators["operator"] = to.Ptr("/not-an-identity")
					case "empty identity map":
						cluster.Identity = &hcpsdk.ManagedServiceIdentity{Type: identity.Type}
					case "nil node pool":
						nodePool = nil
					case "missing node pool ID":
						nodePool.ID = nil
					case "wrong node pool parent":
						nodePool.ID = to.Ptr(clusterID + "-other/nodePools/workers")
					}
					ctx, cancel := context.WithCancel(t.Context())
					defer cancel()
					var logs strings.Builder
					ctx = logr.NewContext(ctx, funcr.New(func(_, message string) { logs.WriteString(message) }, funcr.Options{}))
					consumerCalls := 0
					transport := &admissionTransport{fic: true, role: scenario != "FIC only"}
					transport.consumerResponse = func(req *http.Request) (any, int, error) {
						consumerCalls++
						if req.URL.Query().Get("api-version") != "2026-10-01-preview" {
							t.Fatalf("consumer inventory must expose container registry identities: %s", req.URL)
						}
						if scenario == "unsupported API" {
							return map[string]any{"error": map[string]string{"code": "InvalidApiVersionParameter", "message": "unsupported consumer API version"}}, http.StatusBadRequest, nil
						}
						isNodePool := strings.HasSuffix(req.URL.Path, "/nodePools")
						secondPage := req.URL.Query().Get("page") == "2"
						if scenario == "cancelled" {
							cancel()
							return nil, 0, context.Canceled
						}
						if scenario == "deadline" {
							return nil, 0, context.DeadlineExceeded
						}
						if (!isNodePool && (scenario == "HCP list failure" || (scenario == "later cluster list failure" && secondPage))) ||
							(isNodePool && (scenario == "node pool list failure" || (scenario == "later node pool list failure" && secondPage))) {
							return map[string]any{"error": map[string]string{"code": "Forbidden", "message": "fake consumer list failure"}}, http.StatusForbidden, nil
						}
						if (!isNodePool && scenario == "missing HCP value") || (isNodePool && scenario == "missing node pool value") {
							return map[string]any{}, http.StatusOK, nil
						}
						paginated := (!isNodePool && strings.HasPrefix(scenario, "later cluster")) ||
							(isNodePool && strings.HasPrefix(scenario, "later node pool"))
						if paginated && !secondPage {
							next := *req.URL
							query := next.Query()
							query.Set("page", "2")
							next.RawQuery = query.Encode()
							return map[string]any{"value": []any{}, "nextLink": next.String()}, http.StatusOK, nil
						}
						if isNodePool {
							return map[string]any{"value": []*hcpsdk.NodePool{nodePool}}, http.StatusOK, nil
						}
						return map[string]any{"value": []*hcpsdk.HcpOpenShiftCluster{cluster}}, http.StatusOK, nil
					}
					factory, roles, hcp := admissionSDKClients(t, transport)
					request := assets.LeaseRequest{
						SkipAdmissionCleanup:      skipCleanup,
						IdentityConsumerGuardMode: mode,
						AcquiredSlotState: &slots.AcquiredSlotState{Slot: slots.ExpandedSlot{
							Environment:   "stg",
							Subscriptions: slots.ResolvedSubscriptions{E2E: slots.ResolvedSubscription{ID: "sub"}},
							Assets: slots.ResolvedAssets{E2EIdentities: &slots.ResolvedE2EIdentitiesAsset{
								Allocation: slots.AllocationDedicated, ResourceGroups: []string{"identity-rg-00", "identity-rg-01"},
							}},
						}},
					}
					if scenario == "dev" {
						request.AcquiredSlotState.Slot.Environment = "dev"
					}
					wantSuccess := scenario == "unrelated" || scenario == "other subscription" || scenario == "dev" || scenario == "unrelated registry identity"
					referenced := false
					switch scenario {
					case "Succeeded", "Failed", "Deleting", "Updating", "cluster identity", "data plane", "service", "node pool", "case insensitive", "container registry", "later cluster page", "later node pool page", "FIC only":
						referenced = true
						wantSuccess = true
					}
					if mode == "audit" && scenario != "cancelled" {
						wantSuccess = true
					}
					for attempt := range 2 {
						err := admitIdentityLeaseWithClients(ctx, request, factory, roles, hcp)
						if (err == nil) != wantSuccess {
							t.Fatalf("attempt %d: success=%t, got %v", attempt, wantSuccess, err)
						}
						if scenario == "cancelled" && !errors.Is(err, context.Canceled) {
							t.Fatalf("lost cancellation: %v", err)
						}
						if scenario == "deadline" && mode == "enforce" && !errors.Is(err, context.DeadlineExceeded) {
							t.Fatalf("lost deadline: %v", err)
						}
					}
					if (!wantSuccess || skipCleanup) && len(transport.deletes) != 0 {
						t.Fatalf("unsafe or disabled cleanup issued DELETEs: %v", transport.deletes)
					}
					wantDeletes := 6
					if scenario == "FIC only" {
						wantDeletes = 4
					}
					if referenced && mode == "enforce" {
						wantDeletes -= 2
						if got := request.AcquiredSlotState.AdmittedIdentityContainers; len(got) != 1 || got[0] != "identity-rg-00" {
							t.Fatalf("expected only safe container, got %v", got)
						}
						for _, path := range transport.deletes {
							if strings.Contains(path, "/identity-rg-01/") {
								t.Fatalf("referenced container was mutated: %s", path)
							}
						}
					}
					if wantSuccess && !skipCleanup && len(transport.deletes) != wantDeletes {
						t.Fatalf("cleanup sent %d DELETEs, expected %d: %v", len(transport.deletes), wantDeletes, transport.deletes)
					}
					if scenario != "dev" && consumerCalls == 0 {
						t.Fatal("admission bypassed the consumer check")
					}
					if scenario != "dev" && !strings.Contains(logs.String(), `"phase"="acquisition"`) {
						t.Fatalf("missing acquisition audit: %s", logs.String())
					}
					if scenario == "dev" && consumerCalls != 0 {
						t.Fatal("DEV local clusters cannot be checked through ARM")
					}
					if skipCleanup && (transport.identityLists != 0 || transport.roleLists != 0) {
						t.Fatal("disabled cleanup loaded its mutation inventory")
					}
				})
			}
		}
	}
}
