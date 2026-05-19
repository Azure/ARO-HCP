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
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/slots"
)

func writeReleaseTestState(t *testing.T, sharedDir string) {
	t.Helper()

	state := &slots.AcquiredSlotState{
		Version:           1,
		DeployEnvironment: "ci01",
		RuntimeRegion:     "westus3",
		Slot: slots.ExpandedSlot{
			Environment:             "dev",
			SubscriptionName:        "dev-sub",
			Region:                  "westus3",
			ResourceType:            "aro-hcp-dev-westus3-slot",
			ResourceName:            "aro-hcp-dev-westus3-slot-00",
			SlotIndex:               0,
			IdentityContainerPrefix: "aro-hcp-msi-container-dev-00",
			IdentityContainerCount:  2,
		},
		LeasedResourceName: "aro-hcp-dev-westus3-slot-00",
	}

	if err := slots.WriteAcquiredSlotState(sharedDir, state); err != nil {
		t.Fatalf("expected state write to succeed: %v", err)
	}
	if err := slots.WriteEnvFile(sharedDir, state, "dev-sub"); err != nil {
		t.Fatalf("expected env file write to succeed: %v", err)
	}
}

func newReleaseTestServer(t *testing.T) (*httptest.Server, *[]string) {
	t.Helper()

	var mu sync.Mutex
	releasedNames := []string{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/lease/release" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}

		var body struct {
			Names []string `json:"names"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("expected release request body to decode: %v", err)
		}

		mu.Lock()
		releasedNames = append(releasedNames, body.Names...)
		mu.Unlock()

		w.WriteHeader(http.StatusNoContent)
	}))

	return server, &releasedNames
}

func TestReleaseRunHappyPath(t *testing.T) {
	t.Parallel()

	sharedDir := t.TempDir()
	writeReleaseTestState(t, sharedDir)

	server, releasedNames := newReleaseTestServer(t)
	defer server.Close()

	err := Release(context.Background(), &RawReleaseOptions{
		SharedDir:           sharedDir,
		LeaseProxyServerURL: server.URL,
		LeaseProxyTimeout:   5 * time.Second,
	})
	if err != nil {
		t.Fatalf("expected release to succeed: %v", err)
	}

	if got, want := *releasedNames, []string{"aro-hcp-dev-westus3-slot-00"}; !equalStrings(got, want) {
		t.Fatalf("unexpected released names: got %v want %v", got, want)
	}

	stateFile, err := slots.SlotStateFile(sharedDir)
	if err != nil {
		t.Fatalf("expected state file path to resolve: %v", err)
	}
	if _, err := os.Stat(stateFile); err == nil {
		t.Fatal("expected state file to be removed after release")
	}

	envFile, err := slots.EnvFile(sharedDir)
	if err != nil {
		t.Fatalf("expected env file path to resolve: %v", err)
	}
	if _, err := os.Stat(envFile); err == nil {
		t.Fatal("expected env file to be removed after release")
	}
}

func TestReleaseRunNoStateFileReturnsNil(t *testing.T) {
	t.Parallel()

	sharedDir := t.TempDir()

	server, releasedNames := newReleaseTestServer(t)
	defer server.Close()

	err := Release(context.Background(), &RawReleaseOptions{
		SharedDir:           sharedDir,
		LeaseProxyServerURL: server.URL,
		LeaseProxyTimeout:   5 * time.Second,
	})
	if err != nil {
		t.Fatalf("expected release to succeed when no state file exists: %v", err)
	}

	if got := *releasedNames; len(got) != 0 {
		t.Fatalf("expected no release calls when no state file exists, got %v", got)
	}
}

func TestReleaseRunProxyFailurePropagatesError(t *testing.T) {
	t.Parallel()

	sharedDir := t.TempDir()
	writeReleaseTestState(t, sharedDir)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("access denied"))
	}))
	defer server.Close()

	err := Release(context.Background(), &RawReleaseOptions{
		SharedDir:           sharedDir,
		LeaseProxyServerURL: server.URL,
		LeaseProxyTimeout:   5 * time.Second,
	})
	if err == nil {
		t.Fatal("expected release to fail when proxy returns an error")
	}
	if !strings.Contains(err.Error(), "access denied") {
		t.Fatalf("expected proxy error message in error, got %v", err)
	}

	if _, loadErr := slots.LoadAcquiredSlotState(sharedDir); loadErr != nil {
		t.Fatalf("expected state file to remain after failed release: %v", loadErr)
	}
}

func TestReleaseValidationRejectsEmptyFields(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		opts RawReleaseOptions
	}{
		{
			name: "empty shared dir",
			opts: RawReleaseOptions{
				SharedDir:           "",
				LeaseProxyServerURL: "http://proxy",
				LeaseProxyTimeout:   1 * time.Second,
			},
		},
		{
			name: "empty proxy URL",
			opts: RawReleaseOptions{
				SharedDir:           "/tmp/shared",
				LeaseProxyServerURL: "",
				LeaseProxyTimeout:   1 * time.Second,
			},
		},
		{
			name: "non-positive timeout",
			opts: RawReleaseOptions{
				SharedDir:           "/tmp/shared",
				LeaseProxyServerURL: "http://proxy",
				LeaseProxyTimeout:   0,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := tc.opts.Validate(); err == nil {
				t.Fatalf("expected validation to fail for %s", tc.name)
			}
		})
	}
}
