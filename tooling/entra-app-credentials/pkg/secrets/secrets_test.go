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

package secrets

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	graphAppID = "00000003-0000-0000-c000-000000000000"
	roleID     = "498476ce-e0fe-48b0-b801-37ba7e2685c6"
	scopeID    = "e1fe6dd8-ba31-4d61-89e7-88639da4683d"
)

var fixedNow = time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)

type fakeGraph struct {
	application *Application
	assignments []AppRoleAssignment
	grant       *PermissionGrant

	passwords   []string
	assigned    []AppRoleAssignment
	created     []string
	updated     []string
	passwordErr error
	assignErr   error
	expiry      time.Time
}

func (f *fakeGraph) FindApplication(context.Context, string) (*Application, error) {
	return f.application, nil
}

func (f *fakeGraph) FindServicePrincipal(_ context.Context, appID string) (*ServicePrincipal, error) {
	if appID == graphAppID {
		return &ServicePrincipal{ID: "graph-sp", ScopeValues: map[string]string{scopeID: "User.Read"}}, nil
	}
	return &ServicePrincipal{ID: "app-sp"}, nil
}

func (f *fakeGraph) TenantID(context.Context) (string, error) { return "tenant", nil }

func (f *fakeGraph) AddPassword(_ context.Context, _, displayName string, end time.Time) (string, error) {
	if f.passwordErr != nil {
		return "", f.passwordErr
	}
	f.passwords = append(f.passwords, displayName)
	f.expiry = end
	return "minted", nil
}

func (f *fakeGraph) ListAppRoleAssignments(context.Context, string) ([]AppRoleAssignment, error) {
	return f.assignments, nil
}

func (f *fakeGraph) CreateAppRoleAssignment(_ context.Context, _, resourceID, appRoleID string) error {
	if f.assignErr != nil {
		return f.assignErr
	}
	f.assigned = append(f.assigned, AppRoleAssignment{ResourceID: resourceID, AppRoleID: appRoleID})
	return nil
}

func TestConsentFailureIsOnlyAWarning(t *testing.T) {
	graph := &fakeGraph{application: newApplication(), assignErr: fmt.Errorf("Authorization_RequestDenied")}
	store := &fakeStore{values: map[string]string{}}

	require.NoError(t, ensure(t, graph, store, Options{GrantConsent: true}))

	assert.Equal(t, []string{"bot-client-secret-managed"}, graph.passwords)
	assert.Equal(t, "minted", store.values["bot-client-secret"])
}

func (f *fakeGraph) FindPermissionGrant(context.Context, string, string) (*PermissionGrant, error) {
	return f.grant, nil
}

func (f *fakeGraph) CreatePermissionGrant(_ context.Context, _, _, scope string) error {
	f.created = append(f.created, scope)
	return nil
}

func (f *fakeGraph) UpdatePermissionGrant(_ context.Context, _, scope string) error {
	f.updated = append(f.updated, scope)
	return nil
}

type fakeStore struct {
	values map[string]string
	sets   []string
}

func (f *fakeStore) Get(_ context.Context, name string) (string, bool, error) {
	value, found := f.values[name]
	return value, found, nil
}

func (f *fakeStore) Set(_ context.Context, name, value string) error {
	f.values[name] = value
	f.sets = append(f.sets, name)
	return nil
}

func newApplication() *Application {
	return &Application{
		ID:    "app-object",
		AppID: "app-client",
		RequiredResourceAccess: []ResourceAccess{{
			ResourceAppID: graphAppID,
			Access:        []Access{{ID: roleID, Type: ResourceAccessRole}, {ID: scopeID, Type: ResourceAccessScope}},
		}},
	}
}

func ensure(t *testing.T, graph *fakeGraph, store *fakeStore, options Options) error {
	t.Helper()
	options.Timeout = time.Minute
	return NewEnsurer(graph, store, WithClock(func() time.Time { return fixedNow })).Ensure(context.Background(), []Binding{{ApplicationDisplayName: "app", ClientSecretName: "bot-client-secret"}}, options)
}

func TestEnsureMintsAndStoresAllSecretsWhenMissing(t *testing.T) {
	graph := &fakeGraph{application: newApplication()}
	store := &fakeStore{values: map[string]string{}}

	require.NoError(t, ensure(t, graph, store, Options{}))

	assert.Equal(t, map[string]string{
		"bot-client-secret": "minted",
		"bot-client-id":     "app-client",
		"bot-tenant-id":     "tenant",
	}, store.values)
	assert.Equal(t, []string{"bot-client-secret-managed"}, graph.passwords)
	assert.Equal(t, fixedNow.Add(2*365*24*time.Hour), graph.expiry)
}

func TestEnsureNeverReplacesExistingSecretsAndBackfillsTheRest(t *testing.T) {
	graph := &fakeGraph{application: newApplication()}
	store := &fakeStore{values: map[string]string{"bot-client-secret": "existing", "bot-client-id": "kept"}}

	require.NoError(t, ensure(t, graph, store, Options{}))

	assert.Empty(t, graph.passwords)
	assert.Equal(t, "existing", store.values["bot-client-secret"])
	assert.Equal(t, "kept", store.values["bot-client-id"])
	assert.Equal(t, []string{"bot-tenant-id"}, store.sets)
}

func TestEnsureStoresNothingWhenMintingFails(t *testing.T) {
	graph := &fakeGraph{application: newApplication(), passwordErr: fmt.Errorf("denied")}
	store := &fakeStore{values: map[string]string{}}

	err := ensure(t, graph, store, Options{})

	require.ErrorContains(t, err, "denied")
	assert.Empty(t, store.sets)
}

func TestEnsureDryRunChangesNothing(t *testing.T) {
	graph := &fakeGraph{application: newApplication()}
	store := &fakeStore{values: map[string]string{}}

	require.NoError(t, ensure(t, graph, store, Options{DryRun: true, GrantConsent: true}))

	assert.Empty(t, store.sets)
	assert.Empty(t, graph.passwords)
	assert.Empty(t, graph.assigned)
	assert.Empty(t, graph.created)
}

func TestConsentGrantsMissingPermissionsOnly(t *testing.T) {
	graph := &fakeGraph{application: newApplication()}
	store := &fakeStore{values: map[string]string{}}

	require.NoError(t, ensure(t, graph, store, Options{GrantConsent: true}))

	assert.Equal(t, []AppRoleAssignment{{ResourceID: "graph-sp", AppRoleID: roleID}}, graph.assigned)
	assert.Equal(t, []string{"User.Read"}, graph.created)
}

func TestConsentIsIdempotent(t *testing.T) {
	graph := &fakeGraph{
		application: newApplication(),
		assignments: []AppRoleAssignment{{ResourceID: "graph-sp", AppRoleID: roleID}},
		grant:       &PermissionGrant{ID: "grant", Scope: "User.Read Mail.Read"},
	}
	store := &fakeStore{values: map[string]string{}}

	require.NoError(t, ensure(t, graph, store, Options{GrantConsent: true}))

	assert.Empty(t, graph.assigned)
	assert.Empty(t, graph.created)
	assert.Empty(t, graph.updated)
}

func TestConsentAddsMissingScopeToExistingGrantWithoutDroppingOthers(t *testing.T) {
	graph := &fakeGraph{
		application: newApplication(),
		assignments: []AppRoleAssignment{{ResourceID: "graph-sp", AppRoleID: roleID}},
		grant:       &PermissionGrant{ID: "grant", Scope: "Mail.Read"},
	}
	store := &fakeStore{values: map[string]string{}}

	require.NoError(t, ensure(t, graph, store, Options{GrantConsent: true}))

	assert.Equal(t, []string{"Mail.Read User.Read"}, graph.updated)
	assert.Empty(t, graph.created)
}
