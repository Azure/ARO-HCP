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
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"

	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/assets"
	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/slots"
	"github.com/Azure/ARO-HCP/test/pkg/leaseenv"
	hcpsdk "github.com/Azure/ARO-HCP/test/sdk/v20261001/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
)

func TestAdmissionFiltersBeforeCleanup(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name        string
		mode        string
		minimum     int
		skipCleanup bool
		incomplete  bool
		wantErr     bool
	}{
		{name: "mixed containers", mode: "enforce", minimum: 3},
		{name: "default requires full capacity", mode: "enforce", wantErr: true},
		{name: "insufficient safe capacity", mode: "enforce", minimum: 4, wantErr: true},
		{name: "later inventory failure", mode: "enforce", minimum: 3, incomplete: true, wantErr: true},
		{name: "skip cleanup still filters", mode: "enforce", minimum: 3, skipCleanup: true},
		{name: "skip cleanup fails closed", mode: "enforce", minimum: 3, skipCleanup: true, incomplete: true, wantErr: true},
		{name: "audit permits reuse", mode: "audit"},
		{name: "audit permits incomplete inventory", mode: "audit", incomplete: true},
		{name: "disabled", mode: "disabled"},
		{name: "empty defaults disabled"},
	} {
		t.Run(test.name, func(t *testing.T) {
			groups := []string{"identity-rg-00", "identity-rg-01", "identity-rg-02", "identity-rg-03", "identity-rg-04"}
			transport := &admissionTransport{principals: map[string]string{}}
			for i, group := range groups {
				transport.principals[group] = fmt.Sprintf("aaaaaaaa-0000-0000-0000-%012d", i)
			}
			identityID := func(group string) string {
				return "/subscriptions/sub/resourceGroups/" + group + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/service"
			}
			clusterID := "/subscriptions/sub/resourceGroups/old/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/old"
			cluster := &hcpsdk.HcpOpenShiftCluster{
				ID: to.Ptr(clusterID),
				Properties: &hcpsdk.HcpOpenShiftClusterProperties{
					Platform: &hcpsdk.PlatformProfile{OperatorsAuthentication: &hcpsdk.OperatorsAuthenticationProfile{
						UserAssignedIdentities: &hcpsdk.UserAssignedIdentitiesProfile{
							ServiceManagedIdentity: to.Ptr(identityID(groups[3])),
							ControlPlaneOperators:  map[string]*string{"operator": to.Ptr(identityID("other"))},
							DataPlaneOperators:     map[string]*string{"operator": to.Ptr(identityID("other"))},
						},
					}},
				},
			}
			consumerCalls := 0
			transport.consumerResponse = func(req *http.Request) (any, int, error) {
				consumerCalls++
				if req.URL.Query().Get("api-version") != "2026-10-01" {
					t.Fatalf("unexpected consumer API: %s", req.URL)
				}
				if strings.HasSuffix(req.URL.Path, "/nodePools") {
					return map[string]any{"value": []*hcpsdk.NodePool{{
						ID: to.Ptr(clusterID + "/nodePools/workers"),
						Identity: &hcpsdk.ManagedServiceIdentity{
							Type:                   to.Ptr(hcpsdk.ManagedServiceIdentityTypeUserAssigned),
							UserAssignedIdentities: map[string]*hcpsdk.UserAssignedIdentity{strings.ToUpper(identityID(groups[4])): {}},
						},
					}}}, http.StatusOK, nil
				}
				if req.URL.Query().Get("page") == "2" {
					if test.incomplete {
						return map[string]any{}, http.StatusOK, nil
					}
					return map[string]any{"value": []any{}}, http.StatusOK, nil
				}
				next := *req.URL
				query := next.Query()
				query.Set("page", "2")
				next.RawQuery = query.Encode()
				return map[string]any{"value": []*hcpsdk.HcpOpenShiftCluster{cluster}, "nextLink": next.String()}, http.StatusOK, nil
			}
			state := &slots.AcquiredSlotState{Slot: slots.ExpandedSlot{
				// DEV must obey the same explicit policy as every other environment.
				Environment:   "dev",
				Subscriptions: slots.ResolvedSubscriptions{E2E: slots.ResolvedSubscription{ID: "sub"}},
				Assets:        slots.ResolvedAssets{E2EIdentities: &slots.ResolvedE2EIdentitiesAsset{ResourceGroups: slices.Clone(groups)}},
			}}
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
			clients := admissionSDKClients(t, transport)
			handler := NewHandler(WithAdmitClients(func(subscription string, credential azcore.TokenCredential) (*admitClients, error) {
				if subscription != "sub" || credential == nil {
					t.Fatalf("incorrect admission dependencies: subscription=%q credential=%v", subscription, credential)
				}
				return clients, nil
			}))
			registry, err := assets.NewRegistry(handler)
			if err != nil {
				t.Fatal(err)
			}
			if test.minimum > 0 {
				if err := registry.ApplyOptions([]string{fmt.Sprintf("e2e_identities.minimum-containers=%d", test.minimum)}); err != nil {
					t.Fatal(err)
				}
			}
			request := assets.LeaseRequest{SelectedClusterProfileDir: profile, AcquiredSlotState: state, IdentityConsumerGuardMode: test.mode, SkipAdmissionCleanup: test.skipCleanup}
			err = handler.AdmitLease(t.Context(), request)
			if (err != nil) != test.wantErr {
				t.Fatalf("admission error=%v, want error=%t", err, test.wantErr)
			}
			disabled := test.mode == "" || test.mode == "disabled"
			if (consumerCalls == 0) != disabled {
				t.Fatalf("consumer calls=%d for mode %q", consumerCalls, test.mode)
			}
			if !slices.Equal(state.Slot.IdentityContainerNames(), groups) {
				t.Fatal("filtering changed dedicated lease ownership")
			}
			if test.wantErr {
				if len(transport.deletes) != 0 || transport.identityLists != 0 || transport.roleLists != 0 || len(state.AdmittedIdentityContainers) != 0 {
					t.Fatal("failed guard performed cleanup or admitted identities")
				}
				return
			}
			want := groups
			if test.mode == "enforce" {
				want = groups[:3]
			}
			if !slices.Equal(state.AdmittedIdentityContainers, want) {
				t.Fatalf("admitted=%v, want %v", state.AdmittedIdentityContainers, want)
			}
			if test.skipCleanup {
				if transport.identityLists != 0 || transport.roleLists != 0 || len(transport.deletes) != 0 {
					t.Fatal("skipped cleanup performed mutation inventory or deletes")
				}
			} else {
				if transport.identityLists != len(want) || transport.roleLists != 1 || len(transport.deletes) != len(want) {
					t.Fatalf("identity lists=%d role lists=%d deletes=%v, want cleanup only for %v", transport.identityLists, transport.roleLists, transport.deletes, want)
				}
				for _, excluded := range groups[len(want):] {
					for _, path := range transport.deletes {
						if strings.Contains(path, "/"+excluded+"/") {
							t.Fatalf("deleted unsafe principal's role: %s", path)
						}
					}
				}
			}
			contract := slots.NewRuntimeContractBuilder()
			if err := NewHandler().PublishLease(t.Context(), request, contract); err != nil {
				t.Fatal(err)
			}
			shared := t.TempDir()
			if err := slots.WriteRuntimeContract(shared, contract); err != nil {
				t.Fatal(err)
			}
			envFile, err := slots.EnvFile(shared)
			if err != nil {
				t.Fatal(err)
			}
			mode := test.mode
			if mode == "" {
				mode = "disabled"
			}
			cmd := exec.Command("sh", "-eu", "-c", `. "$1"; printf '%s\n%s' "$`+leaseenv.MSIContainers+`" "$`+leaseenv.IdentityConsumerGuardMode+`"`, "sh", envFile)
			output, err := cmd.CombinedOutput()
			if expected := strings.Join(want, " ") + "\n" + mode; err != nil || string(output) != expected {
				t.Fatalf("sourced admitted identity contract=%q, want %q: %v", output, expected, err)
			}
		})
	}
}
