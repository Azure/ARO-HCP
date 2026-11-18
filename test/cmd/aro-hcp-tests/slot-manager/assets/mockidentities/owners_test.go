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

package mockidentities

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/assets"
	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/slots"
	"github.com/Azure/ARO-HCP/test/pkg/leaseenv"
	"github.com/Azure/ARO-HCP/tooling/entra-app-credentials/pkg/owners"
)

func TestOwnerSettings(t *testing.T) {
	valid := uuid.NewString()
	for _, value := range []string{"", "null", "[]", "garbage", valid + "," + strings.ToUpper(valid)} {
		t.Run(value, func(t *testing.T) {
			t.Setenv(OwnerIDsEnv, value)
			t.Setenv(OwnersGroupIDEnv, valid)
			if _, err := ownerSettingsFromEnv(); err == nil {
				t.Fatal("accepted invalid owners")
			}
		})
	}
	t.Setenv(OwnerIDsEnv, valid)
	for _, value := range []string{"", "null", "bad", valid + "," + uuid.NewString()} {
		t.Setenv(OwnersGroupIDEnv, value)
		if _, err := ownerSettingsFromEnv(); err == nil {
			t.Fatal("accepted invalid group")
		}
	}
	t.Setenv(OwnersGroupIDEnv, valid)
	if _, err := ownerSettingsFromEnv(); err != nil {
		t.Fatal(err)
	}
}

func TestOwnerPlansRetryAndOrdering(t *testing.T) {
	owner, old := uuid.NewString(), uuid.NewString()
	for _, failure := range []string{"", "list-sp", "add-sp", "remove-sp", "visibility"} {
		t.Run(failure, func(t *testing.T) {
			current := map[string][]string{"app": {old}, "sp": {old}}
			fail := failure
			var calls []string
			makeRelations := func(name string) ownerRelations {
				return ownerRelations{
					list: func(context.Context) ([]string, error) {
						if fail == "list-"+name {
							return nil, errors.New("list failed")
						}
						if fail == "visibility" {
							return []string{old}, nil
						}
						return slices.Clone(current[name]), nil
					},
					add: func(_ context.Context, id string) error {
						calls = append(calls, "add-"+name)
						if fail == "add-"+name {
							return errors.New("add failed")
						}
						if !slices.Contains(current[name], id) {
							current[name] = append(current[name], id)
						}
						return nil
					},
					remove: func(_ context.Context, id string) error {
						if !slices.Contains(current["app"], owner) || !slices.Contains(current["sp"], owner) {
							t.Fatal("removed before both additions")
						}
						calls = append(calls, "remove-"+name)
						if fail == "remove-"+name {
							return errors.New("remove failed")
						}
						current[name] = slices.DeleteFunc(current[name], func(value string) bool { return value == id })
						return nil
					},
				}
			}
			plans := []ownerPlan{{name: "app", relations: makeRelations("app")}, {name: "sp", relations: makeRelations("sp")}}
			err := reconcileOwnerSets(t.Context(), plans, []string{owner}, true)
			if (err != nil) != (failure != "") {
				t.Fatalf("failure %s: %v", failure, err)
			}
			if failure == "list-sp" && len(calls) > 0 {
				t.Fatal("writes before complete enumeration")
			}
			if failure == "add-sp" || failure == "visibility" {
				for _, call := range calls {
					if strings.HasPrefix(call, "remove") {
						t.Fatal("removal after incomplete additions")
					}
				}
			}
			fail = ""
			if err := reconcileOwnerSets(t.Context(), plans, []string{owner}, true); err != nil {
				t.Fatal(err)
			}
			count := len(calls)
			if err := reconcileOwnerSets(t.Context(), plans, []string{owner}, true); err != nil {
				t.Fatal(err)
			}
			if len(calls) != count {
				t.Fatal("non-idempotent owner reconciliation")
			}
		})
	}
}

func TestGraphOwnerReconciliation(t *testing.T) {
	name := "aro-hcp-slot-test-msi"
	applicationID, clientID, principalID, old, owner := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	appPath := "applications/" + applicationID + "/owners"
	spPath := "servicePrincipals/" + principalID + "/owners"
	list := func(ids ...string) string {
		values := []string{}
		for _, id := range ids {
			values = append(values, fmt.Sprintf(`{"@odata.type":"#microsoft.graph.user","id":%q}`, id))
		}
		return `{"value":[` + strings.Join(values, ",") + `]}`
	}
	app := graphExchange{method: "GET", path: "applications(uniqueName='" + name + "')", response: fmt.Sprintf(`{"id":%q,"appId":%q,"displayName":%q}`, applicationID, clientID, name)}
	sp := graphExchange{method: "GET", path: "servicePrincipals(appId='" + clientID + "')", response: fmt.Sprintf(`{"id":%q,"appId":%q,"accountEnabled":true}`, principalID, clientID)}
	principal := leaseenv.MockPrincipal{ClientID: clientID, PrincipalID: principalID, CertificateName: name}
	for _, scenario := range []string{"repair", "exact", "validate-drift", "page-error", "incomplete", "null-object", "missing-id", "mismatch-app", "mismatch-sp", "null-app", "group-pages"} {
		t.Run(scenario, func(t *testing.T) {
			exchanges := []graphExchange{app, sp}
			apply := scenario != "validate-drift"
			wantError := scenario != "repair" && scenario != "exact" && scenario != "group-pages"
			switch scenario {
			case "mismatch-app":
				exchanges = []graphExchange{{method: "GET", path: app.path, response: fmt.Sprintf(`{"id":%q,"appId":%q,"displayName":%q}`, applicationID, uuid.NewString(), name)}}
			case "null-app":
				exchanges = []graphExchange{{method: "GET", path: app.path, status: 204}}
			case "mismatch-sp":
				exchanges[1].response = fmt.Sprintf(`{"id":%q,"appId":%q,"accountEnabled":true}`, uuid.NewString(), clientID)
			case "incomplete", "null-object", "missing-id":
				response := `{}`
				if scenario == "null-object" {
					response = `{"value":[null]}`
				}
				if scenario == "missing-id" {
					response = `{"value":[{}]}`
				}
				exchanges = append(exchanges, graphExchange{method: "GET", path: appPath, response: response})
			case "page-error":
				exchanges = append(exchanges, graphExchange{method: "GET", path: appPath, response: fmt.Sprintf(`{"value":[],"@odata.nextLink":%q}`, directoryObjectsURL[:strings.Index(directoryObjectsURL, "directoryObjects")]+appPath+"?page=2")}, graphExchange{method: "GET", path: appPath, query: map[string]string{"page": "2"}, status: 403, response: `{"error":{"code":"Authorization_RequestDenied","message":"denied"}}`})
			default:
				switch scenario {
				case "group-pages":
					exchanges = append(exchanges, graphExchange{method: "GET", path: appPath, response: fmt.Sprintf(`{"value":[],"@odata.nextLink":%q}`, "https://graph.microsoft.com/v1.0/"+appPath+"?page=2")}, graphExchange{method: "GET", path: appPath, query: map[string]string{"page": "2"}, response: list(owner)}, graphExchange{method: "GET", path: spPath, response: list(owner)})
				case "exact":
					exchanges = append(exchanges, graphExchange{method: "GET", path: appPath, response: list(strings.ToUpper(owner))}, graphExchange{method: "GET", path: spPath, response: list(owner)})
				default:
					exchanges = append(exchanges, graphExchange{method: "GET", path: appPath, response: list(old)}, graphExchange{method: "GET", path: spPath, response: list(old)})
					if scenario == "repair" {
						reference := map[string]string{"@odata.id": directoryObjectsURL + owner}
						exchanges = append(exchanges, graphExchange{method: "POST", path: appPath + "/$ref", status: 204, body: reference}, graphExchange{method: "POST", path: spPath + "/$ref", status: 204, body: reference}, graphExchange{method: "GET", path: appPath, response: list(old, owner)}, graphExchange{method: "GET", path: spPath, response: list(old, owner)}, graphExchange{method: "DELETE", path: appPath + "/" + old + "/$ref", status: 204}, graphExchange{method: "DELETE", path: spPath + "/" + old + "/$ref", status: 204}, graphExchange{method: "GET", path: appPath, response: list(owner)}, graphExchange{method: "GET", path: spPath, response: list(owner)})
					}
				}
			}
			store := &azureStore{graph: graphTestClient(t, exchanges)}
			if err := store.Owners(t.Context(), name, principal, []string{owner}, apply); (err != nil) != wantError {
				t.Fatalf("%s: %v", scenario, err)
			}
		})
	}
}

func TestReconcileBundleOwnersCoversAllThreeIdentities(t *testing.T) {
	store := &fakeStore{principals: map[string]leaseenv.MockPrincipal{}, certificates: map[string]bool{}, owner: true}
	slot := testSlot()
	bundle, err := reconcile(t.Context(), store, slot, true)
	if err != nil {
		t.Fatal(err)
	}
	desired := []string{uuid.NewString()}
	if err := reconcileBundleOwners(t.Context(), store, slot, bundle, desired, false); err == nil {
		t.Fatal("validate accepted drift")
	}
	if err := reconcileBundleOwners(t.Context(), store, slot, bundle, desired, true); err != nil {
		t.Fatal(err)
	}
	for _, name := range IdentityNames(slot) {
		if !slices.Equal(store.owners[name], desired) {
			t.Fatal("missing bundle owner update")
		}
	}
	if err := reconcileBundleOwners(t.Context(), store, slot, bundle, desired, false); err != nil {
		t.Fatal(err)
	}
}

func TestGraphOwnerEnumerationRejectsMalformedObjectIDs(t *testing.T) {
	applicationID, owner := uuid.NewString(), uuid.NewString()
	for _, malformed := range []string{" ", uuid.NewString() + "," + uuid.NewString(), ","} {
		t.Run(malformed, func(t *testing.T) {
			store := &azureStore{graph: graphTestClient(t, []graphExchange{{
				method: "GET", path: "applications/" + applicationID + "/owners",
				response: fmt.Sprintf(`{"value":[{"id":%q},{"id":%q}]}`, owner, malformed),
			}})}
			if _, err := store.applicationOwners(applicationID).list(t.Context()); err == nil {
				t.Fatal("accepted a malformed directory object ID as a CSV fragment")
			}
		})
	}
}

func TestOwnerPreflightSkipsUnrelatedPools(t *testing.T) {
	handler := &Handler{owners: func() (ownerSettings, error) {
		t.Fatal("owner preflight for unrelated pools")
		return ownerSettings{}, nil
	}}
	for _, pools := range [][]slots.Pool{nil, {{}}, {{SlotAssets: slots.SlotAssets{MockIdentities: &slots.MockIdentitiesAsset{Allocation: slots.AllocationDedicated}}}}} {
		for _, apply := range []bool{false, true} {
			if err := handler.pools(t.Context(), assets.PoolRequest{Pools: pools}, apply); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestMakeOwnerSettingsRouting(t *testing.T) {
	root, err := filepath.Abs("../../../../../..")
	if err != nil {
		t.Fatal(err)
	}
	temp := t.TempDir()
	stub := filepath.Join(temp, "binary")
	if err := os.WriteFile(stub, []byte("#!/bin/bash\nif [[ \"$*\" == *e2e_identities* ]]; then exit 0; fi\n[[ -n ${ENTRA_APP_OWNER_IDS:-} && -n ${ENTRA_APP_OWNERS_GROUP_ID:-} ]] || exit 23\nprintf '%s\\n%s\\n' \"$ENTRA_APP_OWNER_IDS\" \"$ENTRA_APP_OWNERS_GROUP_ID\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"apply-pool-assets", "validate-pool-assets"} {
		for _, scenario := range []string{"config", "override", "missing-tool", "missing-config", "unrelated"} {
			t.Run(target+scenario, func(t *testing.T) {
				args := []string{"-s", "--no-print-directory", "-C", filepath.Join(root, "test"), "-o", stub, "ARO_HCP_TESTS=" + stub, "ENVIRONMENT=dev", target, "ASSET=mock_identities"}
				if scenario == "override" {
					args = append(args, "ENTRA_APP_OWNER_IDS="+uuid.NewString(), "ENTRA_APP_OWNERS_GROUP_ID="+uuid.NewString(), "YQ=false")
				}
				if scenario == "missing-tool" {
					args = append(args, "YQ=/nonexistent/yq")
				}
				if scenario == "missing-config" {
					args = append(args, "MOCK_IDENTITY_CONFIG="+filepath.Join(temp, "missing"))
				}
				if scenario == "unrelated" {
					args = append(args, "ASSET=e2e_identities", "YQ=/nonexistent/yq")
				}
				command := exec.Command("make", args...)
				command.Env = slices.DeleteFunc(os.Environ(), func(value string) bool {
					return strings.HasPrefix(value, "ENTRA_APP_") || strings.HasPrefix(value, "MAKEFLAGS=") || strings.HasPrefix(value, "MAKEOVERRIDES=")
				})
				output, err := command.CombinedOutput()
				if (err != nil) != (scenario == "missing-tool" || scenario == "missing-config") {
					t.Fatalf("make routing: %v: %s", err, output)
				}
				if err == nil && scenario != "unrelated" {
					lines := strings.Split(strings.TrimSpace(string(output)), "\n")
					if len(lines) != 2 {
						t.Fatalf("unexpected output %s", output)
					}
					if _, err := owners.ParseIDs(lines[0]); err != nil {
						t.Fatal(err)
					}
					if _, err := uuid.Parse(lines[1]); err != nil {
						t.Fatal(err)
					}
				}
			})
		}
	}
}
