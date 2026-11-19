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

package slotmanager

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/assets"
	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/assets/mockidentities"
	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/slots"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armdeployments"
)

const externalTestSubscription = "da9096ac-59c8-4508-a64c-86fbf9817b70"

func TestExternalSubscriptionFlagsRejectUnsafeInputBeforeIO(t *testing.T) {
	for _, test := range []struct {
		name string
		args []string
		want string
	}{
		{name: "name is not UUID", args: []string{"--subscription-id", "Hypershift Managed Azure"}, want: "canonical UUID"},
		{name: "empty UUID", want: "canonical UUID"},
		{name: "zero UUID", args: []string{"--subscription-id", "00000000-0000-0000-0000-000000000000"}, want: "canonical UUID"},
		{name: "prepare needs apply", args: []string{"--subscription-id", externalTestSubscription, "--repo-root", "/does-not-exist", "--prepare-identities"}, want: "requires --apply"},
		{name: "explicit source", args: []string{"--subscription-id", externalTestSubscription}, want: "--repo-root is required"},
	} {
		t.Run(test.name, func(t *testing.T) {
			command, err := newReconcileExternalSubscriptionCommand()
			if err != nil {
				t.Fatal(err)
			}
			command.SetOut(io.Discard)
			command.SetErr(io.Discard)
			command.SetArgs(test.args)
			err = command.Execute()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("got %v, want %q", err, test.want)
			}
		})
	}
}

func externalTestPool(name string) slots.Pool {
	return slots.Pool{Name: name, DeployEnv: "ci01", Subscriptions: slots.PoolSubscriptions{E2E: "Hypershift Managed Azure"}, SlotCount: 2, ResourceType: "external-" + name,
		SlotAssets: slots.SlotAssets{MockIdentities: &slots.MockIdentitiesAsset{}, E2EIdentities: &slots.E2EIdentitiesAsset{Provisioning: slots.IdentityProvisioningUnmanaged, ResourceGroupPrefix: "msi-" + name, ResourceGroupCount: 3, ProvisioningRegion: "westus3"}}}
}

func TestExternalSubscriptionSelectsEveryLogicalDEVPool(t *testing.T) {
	catalog := &slots.Catalog{Environments: map[string]slots.Environment{
		"dev":    {Pools: []slots.Pool{externalTestPool("large")}},
		"dev-xs": {Pools: []slots.Pool{externalTestPool("small")}},
	}}
	selected, err := selectExternalSubscription(catalog, externalAccount{ID: externalTestSubscription, Name: "Hypershift Managed Azure", TenantID: externalSubscriptionTenant, State: "Enabled"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(selected.poolNames, []string{"dev/large", "dev-xs/small"}) || len(selected.slots) != 4 {
		t.Fatalf("incomplete selection: %+v", selected)
	}
	if len(selected.requests) != 2 || selected.requests[0].Environment != "dev" || selected.requests[1].Environment != "dev-xs" {
		t.Fatalf("logical environments lost: %+v", selected.requests)
	}
	for _, request := range selected.requests {
		for _, pool := range request.Pools {
			if pool.DeployEnv != "ci01" {
				t.Fatalf("deployment environment overloaded: %+v", pool)
			}
		}
	}
	if selected.slots[2].Environment != "dev-xs" || selected.slots[2].ResourceName != "external-small-00" || selected.slots[2].IdentityContainerPrefix != "msi-small-00" {
		t.Fatalf("incorrect dev-xs resource identity: %+v", selected.slots[2])
	}
	for _, slot := range selected.slots {
		if slot.Subscriptions.E2E.ID != externalTestSubscription || slot.DeployEnvironment != "ci01" {
			t.Fatalf("runtime subscription or deploy environment changed: %+v", slot)
		}
	}
	if catalog.Environments["dev"].Pools[0].DeployEnv != "ci01" {
		t.Fatal("selection mutated catalog")
	}
}

func TestExternalSubscriptionRejectsUnsupportedScope(t *testing.T) {
	for _, test := range []struct {
		name, environment, tenant, state string
		managed                          bool
	}{
		{name: "INT", environment: "int", tenant: externalSubscriptionTenant, state: "Enabled"},
		{name: "other tenant", environment: "dev", tenant: "other", state: "Enabled"},
		{name: "disabled", environment: "dev", tenant: externalSubscriptionTenant, state: "Disabled"},
		{name: "managed", environment: "dev", tenant: externalSubscriptionTenant, state: "Enabled", managed: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			pool := externalTestPool("target")
			if test.managed {
				pool.SlotAssets.E2EIdentities.Provisioning = slots.AssetProvisioningManaged
			}
			catalog := &slots.Catalog{Environments: map[string]slots.Environment{test.environment: {Pools: []slots.Pool{pool}}}}
			_, err := selectExternalSubscription(catalog, externalAccount{ID: externalTestSubscription, Name: pool.Subscriptions.E2E, TenantID: test.tenant, State: test.state})
			if err == nil {
				t.Fatal("unsupported scope accepted")
			}
		})
	}
}

func TestExternalRBACRejectsRevocationReplacementAndUnknownChanges(t *testing.T) {
	role := func(actions []any) map[string]any {
		return map[string]any{"properties": map[string]any{"roleName": "mock", "type": "CustomRole", "assignableScopes": []any{"/subscriptions/" + externalTestSubscription}, "permissions": []any{map[string]any{"actions": actions, "notActions": []any{}, "dataActions": []any{}, "notDataActions": []any{}}}}}
	}
	roleID := "/subscriptions/" + externalTestSubscription + "/providers/Microsoft.Authorization/roleDefinitions/role"
	for _, test := range []struct {
		name, kind, resource string
		before, after        map[string]any
		ready, wantError     bool
	}{
		{name: "add role", kind: "Create", resource: roleID, after: role([]any{"read"})},
		{name: "add permissions", kind: "Modify", resource: roleID, before: role([]any{"read"}), after: role([]any{"read", "write"})},
		{name: "remove permissions", kind: "Modify", resource: roleID, before: role([]any{"read", "write"}), after: role([]any{"read"}), wantError: true},
		{name: "delete role", kind: "Delete", resource: roleID, wantError: true},
		{name: "uninspectable", kind: "Unsupported", resource: roleID, wantError: true},
		{name: "missing payload", kind: "Create", resource: roleID, wantError: true},
		{name: "replacement or condition drift", kind: "Modify", resource: strings.Replace(roleID, "roleDefinitions", "roleAssignments", 1), before: role([]any{"read"}), after: role([]any{"read"}), wantError: true},
		{name: "infrastructure scope", kind: "Create", resource: "/subscriptions/other/providers/Microsoft.Authorization/roleAssignments/role", after: role([]any{"read"}), wantError: true},
		{name: "check reports missing grant", kind: "Create", resource: roleID, after: role([]any{"read"}), ready: true, wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := validateExternalRBACChanges(externalTestSubscription, []*armdeployments.WhatIfChange{{ResourceID: to.Ptr(test.resource), ChangeType: to.Ptr(armdeployments.ChangeType(test.kind)), Before: test.before, After: test.after}}, test.ready)
			if (err != nil) != test.wantError {
				t.Fatalf("got %v, wantError %v", err, test.wantError)
			}
		})
	}
}

type externalTestAssets struct {
	e2eReady, mockReady bool
	safetyError         error
	ownerDenied         bool
	mutations           []string
	credential          azcore.TokenCredential
}

type externalTestE2E struct{ state *externalTestAssets }

func (h externalTestE2E) PreflightExternalSubscription(_ context.Context, _ []assets.PoolRequest, _ string, credential azcore.TokenCredential) error {
	if credential != h.state.credential {
		return fmt.Errorf("E2E preflight received a different credential")
	}
	return h.state.safetyError
}
func (h externalTestE2E) ReconcileExternalSubscription(_ context.Context, _ []assets.PoolRequest, _ string, credential azcore.TokenCredential, apply bool) error {
	if credential != h.state.credential {
		return fmt.Errorf("E2E reconcile received a different credential")
	}
	if apply {
		h.state.mutations = append(h.state.mutations, "containers")
		h.state.e2eReady = true
	}
	if !h.state.e2eReady {
		return fmt.Errorf("containers missing")
	}
	return nil
}

type externalTestMock struct{ state *externalTestAssets }

func (h externalTestMock) PreflightExternalSubscription(_ context.Context, credential azcore.TokenCredential, _ string, _ []slots.ExpandedSlot, prepare bool) error {
	if credential != h.state.credential {
		return fmt.Errorf("mock preflight received a different credential")
	}
	if h.state.ownerDenied {
		return fmt.Errorf("Owner authorization denied")
	}
	if !h.state.mockReady && !prepare {
		return fmt.Errorf("identity missing: use --prepare-identities")
	}
	return nil
}
func (h externalTestMock) CheckExternalSubscription(_ context.Context, credential azcore.TokenCredential, _ string, _ []slots.ExpandedSlot) error {
	if credential != h.state.credential {
		return fmt.Errorf("mock check received a different credential")
	}
	if !h.state.mockReady {
		return fmt.Errorf("identity missing")
	}
	return nil
}
func (h externalTestMock) ApplyExternalSubscription(_ context.Context, credential azcore.TokenCredential, _ string, _ []slots.ExpandedSlot, prepare bool) error {
	if credential != h.state.credential {
		return fmt.Errorf("mock apply received a different credential")
	}
	if !h.state.mockReady && !prepare {
		return fmt.Errorf("preparation required")
	}
	if prepare && !h.state.mockReady {
		h.state.mutations = append(h.state.mutations, "prepare")
		h.state.mockReady = true
	}
	h.state.mutations = append(h.state.mutations, "grants")
	return nil
}

func TestExternalSubscriptionReconciliationSafetyBoundaries(t *testing.T) {
	for _, test := range []struct {
		name                                   string
		apply, prepare, missing, safetyFailure bool
		ownerDenied                            bool
		wantError                              bool
		wantMutations                          []string
	}{
		{name: "healthy check", wantMutations: nil},
		{name: "Reader checks readiness without Owner authorization", ownerDenied: true},
		{name: "Reader cannot apply", apply: true, ownerDenied: true, wantError: true},
		{name: "missing check", missing: true, wantError: true},
		{name: "routine apply", apply: true, wantMutations: []string{"containers", "grants"}},
		{name: "routine cannot prepare", apply: true, missing: true, wantError: true},
		{name: "explicit initial preparation", apply: true, prepare: true, missing: true, wantMutations: []string{"containers", "prepare", "grants"}},
		{name: "unsafe topology blocks all writes", apply: true, safetyFailure: true, wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			state := &externalTestAssets{e2eReady: !test.missing, mockReady: !test.missing, ownerDenied: test.ownerDenied}
			if test.safetyFailure {
				state.safetyError = fmt.Errorf("removed stack needs drain")
			}
			o := &ReconcileExternalSubscriptionOptions{completedReconcileExternalSubscriptionOptions: &completedReconcileExternalSubscriptionOptions{SubscriptionID: externalTestSubscription, RepoRoot: "/source", Apply: test.apply, PrepareIdentities: test.prepare, Out: io.Discard, e2e: externalTestE2E{state}, mock: externalTestMock{state}}}
			o.config.Clouds.Dev.Defaults.CI.Providers = "Microsoft.Compute"
			o.config.Clouds.Dev.Defaults.OwnerIDs = "11111111-1111-4111-8111-111111111111"
			o.config.Clouds.Dev.Defaults.OwnersGroupID = "22222222-2222-4222-8222-222222222222"
			if test.prepare {
				t.Setenv(mockidentities.OwnerIDsEnv, "")
				t.Setenv(mockidentities.OwnersGroupIDEnv, "")
			}
			dev := &o.config.Clouds.Dev.Defaults.CI.Dev
			dev.Bot.ApplicationName = "bot"
			dev.MockIdentities.FirstParty.ApplicationName = "fp"
			dev.MockIdentities.ARMHelper.ApplicationName = "arm"
			dev.MockIdentities.MSIMock.ApplicationName = "msi"
			endpoint := newExternalSDKEndpoint(t)
			endpoint.configure(o)
			credential := &externalSDKCredential{}
			endpoint.clients(t, o, credential)
			state.credential = credential
			err := o.reconcile(t.Context(), credential, externalSelection{})
			if (err != nil) != test.wantError {
				t.Fatalf("got %v,wantError %v", err, test.wantError)
			}
			if !reflect.DeepEqual(state.mutations, test.wantMutations) {
				t.Fatalf("asset writes %v,want %v", state.mutations, test.wantMutations)
			}
			if (!test.apply || test.wantError) && endpoint.writes != 0 {
				t.Fatalf("preflight/check mutated Azure %d times", endpoint.writes)
			}
			if test.apply && !test.wantError && endpoint.writes != 2 {
				t.Fatalf("expected two incremental deployments and no redundant healthy provider registration,got %d writes", endpoint.writes)
			}
		})
	}
}

func TestExternalRBACPreviewFailsClosed(t *testing.T) {
	for _, test := range []struct {
		name, response string
		wantError      bool
	}{
		{name: "complete no-change response", response: `{"status":"Succeeded","properties":{"changes":[]}}`},
		{name: "omitted changes", response: `{"status":"Succeeded","properties":{}}`, wantError: true},
		{name: "failed preview", response: `{"status":"Failed","properties":{"changes":[]}}`, wantError: true},
		{name: "embedded error", response: `{"status":"Succeeded","properties":{"changes":[]},"error":{"code":"Denied"}}`, wantError: true},
		{name: "null change", response: `{"status":"Succeeded","properties":{"changes":[null]}}`, wantError: true},
		{name: "unresolved potential changes", response: `{"status":"Succeeded","properties":{"changes":[],"potentialChanges":[{"changeType":"Create"}]}}`, wantError: true},
		{name: "unsafe create", response: `{"status":"Succeeded","properties":{"changes":[{"changeType":"Create","resourceId":"/subscriptions/other/providers/Microsoft.Authorization/roleAssignments/x","after":{"type":"Microsoft.Authorization/roleAssignments"}}]}}`, wantError: true},
		{name: "non-JSON response", response: "Resource changes: 0 to create, 0 to delete.\n", wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			endpoint := newExternalSDKEndpoint(t)
			endpoint.preview = test.response
			o := &ReconcileExternalSubscriptionOptions{completedReconcileExternalSubscriptionOptions: &completedReconcileExternalSubscriptionOptions{SubscriptionID: externalTestSubscription}}
			endpoint.clients(t, o, &externalSDKCredential{})
			err := o.previewDeployment(t.Context(), externalDeployment{name: "ci-bot-rbac-subscription"}, true)
			if (err != nil) != test.wantError {
				t.Fatalf("got %v,wantError %v", err, test.wantError)
			}
		})
	}
}

func TestExternalRoleChangesRejectExclusionsAndScopeRemoval(t *testing.T) {
	decode := func(data string) map[string]any {
		t.Helper()
		var value map[string]any
		if err := json.Unmarshal([]byte(data), &value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	before := decode(`{"properties":{"roleName":"mock","type":"CustomRole","assignableScopes":["a","b"],"permissions":[{"actions":["read","write"],"notActions":[],"dataActions":[],"notDataActions":[]}]}}`)
	for _, test := range []struct{ name, after string }{
		{name: "new denied action", after: `{"properties":{"roleName":"mock","type":"CustomRole","assignableScopes":["a","b"],"permissions":[{"actions":["read","write"],"notActions":["write"],"dataActions":[],"notDataActions":[]}]}}`},
		{name: "scope removed", after: `{"properties":{"roleName":"mock","type":"CustomRole","assignableScopes":["a"],"permissions":[{"actions":["read","write"],"notActions":[],"dataActions":[],"notDataActions":[]}]}}`},
		{name: "role renamed", after: `{"properties":{"roleName":"replacement","type":"CustomRole","assignableScopes":["a","b"],"permissions":[{"actions":["read","write"],"notActions":[],"dataActions":[],"notDataActions":[]}]}}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			if additiveExternalRoleDefinition(before, decode(test.after)) {
				t.Fatal("destructive role modification accepted")
			}
		})
	}
}
