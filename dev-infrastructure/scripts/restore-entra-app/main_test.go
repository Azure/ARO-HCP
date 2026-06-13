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

package main

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// parseEnvConfig
// ---------------------------------------------------------------------------

func envFromMap(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestParseEnvConfig_RequiredField(t *testing.T) {
	_, err := parseEnvConfig(envFromMap(map[string]string{}))
	if err == nil || !strings.Contains(err.Error(), "APPLICATION_NAME") {
		t.Errorf("expected APPLICATION_NAME error, got %v", err)
	}
}

func TestParseEnvConfig_Defaults(t *testing.T) {
	c, err := parseEnvConfig(envFromMap(map[string]string{
		"APPLICATION_NAME": "arohcp-ga-stg",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.applicationName != "arohcp-ga-stg" {
		t.Errorf("applicationName=%q", c.applicationName)
	}
	if c.dryRun {
		t.Errorf("dryRun=true, want false by default")
	}
}

func TestParseEnvConfig_DryRunVariants(t *testing.T) {
	for _, v := range []string{"true", "True", "TRUE", "1", "yes", "YES", " true "} {
		c, err := parseEnvConfig(envFromMap(map[string]string{
			"APPLICATION_NAME": "app",
			"DRY_RUN":          v,
		}))
		if err != nil {
			t.Fatalf("DRY_RUN=%q: unexpected error: %v", v, err)
		}
		if !c.dryRun {
			t.Errorf("DRY_RUN=%q: dryRun=false, want true", v)
		}
	}
}

func TestParseEnvConfig_DryRunFalseVariants(t *testing.T) {
	for _, v := range []string{"", "false", "0", "no"} {
		c, err := parseEnvConfig(envFromMap(map[string]string{
			"APPLICATION_NAME": "app",
			"DRY_RUN":          v,
		}))
		if err != nil {
			t.Fatalf("DRY_RUN=%q: unexpected error: %v", v, err)
		}
		if c.dryRun {
			t.Errorf("DRY_RUN=%q: dryRun=true, want false", v)
		}
	}
}

func TestParseEnvConfig_RejectsQuotes(t *testing.T) {
	for _, name := range []string{"app'name", `app"name`} {
		_, err := parseEnvConfig(envFromMap(map[string]string{
			"APPLICATION_NAME": name,
		}))
		if err == nil || !strings.Contains(err.Error(), "quotes") {
			t.Errorf("APPLICATION_NAME=%q: expected quotes error, got %v", name, err)
		}
	}
}

// ---------------------------------------------------------------------------
// fakeRestorer
// ---------------------------------------------------------------------------

type restoreCall struct {
	objectType string
	objectID   string
}

type fakeRestorer struct {
	activeAppID   string
	activeAppErr  error
	deletedAppID  string
	deletedAppErr error
	deletedSPID   string
	deletedSPErr  error
	restoreErr    error
	restoreCalls  []restoreCall
}

func (f *fakeRestorer) getActiveApp(_ context.Context, _ string) (string, error) {
	return f.activeAppID, f.activeAppErr
}

func (f *fakeRestorer) getDeletedApp(_ context.Context, _ string) (string, error) {
	return f.deletedAppID, f.deletedAppErr
}

func (f *fakeRestorer) getDeletedSP(_ context.Context, _ string) (string, error) {
	return f.deletedSPID, f.deletedSPErr
}

func (f *fakeRestorer) restore(_ context.Context, objectType, objectID string) error {
	f.restoreCalls = append(f.restoreCalls, restoreCall{objectType, objectID})
	return f.restoreErr
}

// ---------------------------------------------------------------------------
// runWith
// ---------------------------------------------------------------------------

func TestRunWith_ActiveAppExists(t *testing.T) {
	f := &fakeRestorer{activeAppID: "aaa-bbb"}
	err := runWith(context.Background(), &config{applicationName: "myapp"}, f)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(f.restoreCalls) != 0 {
		t.Errorf("expected no restore calls, got %d", len(f.restoreCalls))
	}
}

func TestRunWith_NoActiveNoDeleted(t *testing.T) {
	f := &fakeRestorer{}
	err := runWith(context.Background(), &config{applicationName: "myapp"}, f)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(f.restoreCalls) != 0 {
		t.Errorf("expected no restore calls, got %d", len(f.restoreCalls))
	}
}

func TestRunWith_DeletedAppOnly(t *testing.T) {
	f := &fakeRestorer{deletedAppID: "del-app-id"}
	err := runWith(context.Background(), &config{applicationName: "myapp"}, f)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(f.restoreCalls) != 1 {
		t.Fatalf("expected 1 restore call, got %d", len(f.restoreCalls))
	}
	if f.restoreCalls[0].objectType != "application" || f.restoreCalls[0].objectID != "del-app-id" {
		t.Errorf("restore call = %+v", f.restoreCalls[0])
	}
}

func TestRunWith_DeletedAppAndSP(t *testing.T) {
	f := &fakeRestorer{deletedAppID: "del-app-id", deletedSPID: "del-sp-id"}
	err := runWith(context.Background(), &config{applicationName: "myapp"}, f)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(f.restoreCalls) != 2 {
		t.Fatalf("expected 2 restore calls, got %d", len(f.restoreCalls))
	}
	if f.restoreCalls[0].objectType != "application" {
		t.Errorf("first restore should be application, got %s", f.restoreCalls[0].objectType)
	}
	if f.restoreCalls[1].objectType != "servicePrincipal" {
		t.Errorf("second restore should be servicePrincipal, got %s", f.restoreCalls[1].objectType)
	}
}

func TestRunWith_DryRun(t *testing.T) {
	f := &fakeRestorer{deletedAppID: "del-app-id", deletedSPID: "del-sp-id"}
	err := runWith(context.Background(), &config{applicationName: "myapp", dryRun: true}, f)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(f.restoreCalls) != 0 {
		t.Errorf("expected no restore calls in dry-run, got %d", len(f.restoreCalls))
	}
}

func TestRunWith_ActiveAppError(t *testing.T) {
	f := &fakeRestorer{activeAppErr: errors.New("graph auth failed")}
	err := runWith(context.Background(), &config{applicationName: "myapp"}, f)
	if err == nil || !strings.Contains(err.Error(), "graph auth failed") {
		t.Errorf("expected auth error, got %v", err)
	}
}

func TestRunWith_DeletedAppError(t *testing.T) {
	f := &fakeRestorer{deletedAppErr: errors.New("permission denied")}
	err := runWith(context.Background(), &config{applicationName: "myapp"}, f)
	if err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Errorf("expected permission error, got %v", err)
	}
}

func TestRunWith_RestoreAppError(t *testing.T) {
	f := &fakeRestorer{deletedAppID: "del-app-id", restoreErr: errors.New("conflict")}
	err := runWith(context.Background(), &config{applicationName: "myapp"}, f)
	if err == nil || !strings.Contains(err.Error(), "conflict") {
		t.Errorf("expected conflict error, got %v", err)
	}
}

func TestRunWith_DeletedSPError(t *testing.T) {
	f := &fakeRestorer{deletedAppID: "del-app-id", deletedSPErr: errors.New("timeout")}
	err := runWith(context.Background(), &config{applicationName: "myapp"}, f)
	if err == nil || !strings.Contains(err.Error(), "timeout") {
		t.Errorf("expected timeout error, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// odataFilterURL
// ---------------------------------------------------------------------------

func TestOdataFilterURL(t *testing.T) {
	got := odataFilterURL("https://graph.microsoft.com/v1.0/applications", "my-app")
	if !strings.Contains(got, "%24filter=displayName+eq+%27my-app%27") &&
		!strings.Contains(got, "%24filter=displayName+eq+'my-app'") &&
		!strings.Contains(got, "$filter=displayName+eq+'my-app'") {
		t.Errorf("unexpected URL encoding: %s", got)
	}
	if !strings.Contains(got, "%24select=id") && !strings.Contains(got, "$select=id") {
		t.Errorf("missing $select=id: %s", got)
	}
}
