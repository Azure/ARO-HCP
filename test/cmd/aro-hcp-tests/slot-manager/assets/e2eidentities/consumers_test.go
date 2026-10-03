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
	"net/http"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"

	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/assets"
	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/slots"
	hcpsdk "github.com/Azure/ARO-HCP/test/sdk/v20261001preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/framework"
)

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
		for _, skipCleanup := range []bool{false, true} {
			name := scenario
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
					SkipAdmissionCleanup: skipCleanup,
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
				for attempt := range 2 {
					err := admitIdentityLeaseWithClients(ctx, request, factory, roles, hcp)
					if (err == nil) != wantSuccess {
						t.Fatalf("attempt %d: success=%t, got %v", attempt, wantSuccess, err)
					}
					if scenario == "cancelled" && !errors.Is(err, context.Canceled) {
						t.Fatalf("lost cancellation: %v", err)
					}
					if scenario == "deadline" && !errors.Is(err, context.DeadlineExceeded) {
						t.Fatalf("lost deadline: %v", err)
					}
				}
				if (!wantSuccess || skipCleanup) && len(transport.deletes) != 0 {
					t.Fatalf("unsafe or disabled cleanup issued DELETEs: %v", transport.deletes)
				}
				if wantSuccess && !skipCleanup && len(transport.deletes) != 6 {
					t.Fatalf("safe orphan cleanup did not delete two FICs and one role per admission: %v", transport.deletes)
				}
				if scenario != "dev" && consumerCalls == 0 {
					t.Fatal("admission bypassed the consumer check")
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
