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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const expiryWarning = 90 * 24 * time.Hour

func applicationWithCredentials(credentials ...PasswordCredential) *Application {
	application := newApplication()
	application.PasswordCredentials = credentials
	return application
}

func storedSecret() *fakeStore {
	return &fakeStore{values: map[string]string{
		"bot-client-secret": "existing",
		"bot-client-id":     "app-client",
		"bot-tenant-id":     "tenant",
	}}
}

func TestEnsureFailsWhenStoredSecretIsExpired(t *testing.T) {
	for _, dryRun := range []bool{false, true} {
		graph := &fakeGraph{application: applicationWithCredentials(PasswordCredential{Hint: "exi", End: fixedNow.Add(-time.Hour)})}
		store := storedSecret()

		err := ensure(t, graph, store, Options{DryRun: dryRun, ExpiryWarning: expiryWarning})

		require.ErrorContains(t, err, `client secret "bot-client-secret" expired at`)
		require.ErrorContains(t, err, "ensure-secret --rotate --mapping 'app;bot-client-secret'")
		assert.Empty(t, graph.passwords, "an expired secret is reported, never silently replaced")
		assert.Empty(t, store.sets)
	}
}

func TestEnsureStillReconcilesTheRestWhenStoredSecretIsExpired(t *testing.T) {
	graph := &fakeGraph{application: applicationWithCredentials(PasswordCredential{Hint: "exi", End: fixedNow.Add(-time.Hour)})}
	store := storedSecret()
	delete(store.values, "bot-tenant-id")

	err := ensure(t, graph, store, Options{GrantConsent: true})

	require.Error(t, err)
	assert.Equal(t, []string{"bot-tenant-id"}, store.sets)
	assert.NotEmpty(t, graph.assigned, "consent is still granted")
}

func TestEnsureOnlyWarnsWhenStoredSecretIsExpiring(t *testing.T) {
	graph := &fakeGraph{application: applicationWithCredentials(PasswordCredential{Hint: "exi", End: fixedNow.Add(30 * 24 * time.Hour)})}

	require.NoError(t, ensure(t, graph, storedSecret(), Options{ExpiryWarning: expiryWarning}))
	assert.Empty(t, graph.passwords)
}

func TestEnsureFailsWhenStoredSecretMatchesNoCredential(t *testing.T) {
	graph := &fakeGraph{application: applicationWithCredentials(PasswordCredential{Hint: "zzz", End: fixedNow.AddDate(1, 0, 0)})}

	err := ensure(t, graph, storedSecret(), Options{})

	require.ErrorContains(t, err, "matches none of the application's 1 client secrets")
}

func TestEnsureUsesEarliestEndingCredentialOnHintCollision(t *testing.T) {
	graph := &fakeGraph{application: applicationWithCredentials(
		PasswordCredential{Hint: "exi", End: fixedNow.AddDate(1, 0, 0)},
		PasswordCredential{Hint: "exi", End: fixedNow.Add(-time.Hour)},
	)}

	err := ensure(t, graph, storedSecret(), Options{})

	require.ErrorContains(t, err, "expired")
}

func TestEnsureRotateReplacesStoredSecretOnly(t *testing.T) {
	graph := &fakeGraph{application: applicationWithCredentials(PasswordCredential{Hint: "exi", End: fixedNow.AddDate(1, 0, 0)})}
	store := storedSecret()

	require.NoError(t, ensure(t, graph, store, Options{Rotate: true}))

	assert.Equal(t, []string{"bot-client-secret-managed"}, graph.passwords)
	assert.Equal(t, fixedNow.Add(secretLifetime), graph.expiry)
	assert.Equal(t, "minted", store.values["bot-client-secret"])
	assert.Equal(t, []string{"bot-client-secret"}, store.sets, "client ID and tenant ID are not rewritten")
}

func TestEnsureRotateRecoversFromExpiredOrUnmatchedSecret(t *testing.T) {
	for name, credentials := range map[string][]PasswordCredential{
		"expired":   {{Hint: "exi", End: fixedNow.Add(-time.Hour)}},
		"unmatched": {{Hint: "zzz", End: fixedNow.AddDate(1, 0, 0)}},
		"none":      nil,
	} {
		t.Run(name, func(t *testing.T) {
			graph := &fakeGraph{application: applicationWithCredentials(credentials...)}
			store := storedSecret()

			require.NoError(t, ensure(t, graph, store, Options{Rotate: true}))

			assert.Equal(t, "minted", store.values["bot-client-secret"])
		})
	}
}

func TestEnsureRotateDryRunChangesNothingAndDoesNotFail(t *testing.T) {
	graph := &fakeGraph{application: applicationWithCredentials(PasswordCredential{Hint: "exi", End: fixedNow.Add(-time.Hour)})}
	store := storedSecret()

	require.NoError(t, ensure(t, graph, store, Options{Rotate: true, DryRun: true, ExpiryWarning: expiryWarning}))

	assert.Empty(t, graph.passwords)
	assert.Empty(t, store.sets)
}

func TestEnsureRotateMintsWhenNoSecretIsStored(t *testing.T) {
	graph := &fakeGraph{application: newApplication()}
	store := &fakeStore{values: map[string]string{}}

	require.NoError(t, ensure(t, graph, store, Options{Rotate: true}))

	assert.Equal(t, "minted", store.values["bot-client-secret"])
}
