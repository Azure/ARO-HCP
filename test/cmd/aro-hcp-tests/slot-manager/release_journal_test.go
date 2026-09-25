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
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/slots"
)

func TestReleasePartialStateWithoutCatalogOrHandlerAndRetry(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	state := &slots.AcquiredSlotState{
		Version: 2,
		Leases: slots.LeaseSet{
			Primary: slots.Lease{ResourceType: "primary", ResourceName: "primary-00"},
			Assets: map[slots.AssetKind][]slots.Lease{
				"unknown-kind": {{ResourceType: "secondary", ResourceName: "secondary-04"}, {ResourceType: "secondary", ResourceName: "secondary-07"}},
			},
		},
	}
	if err := state.Validate(); err == nil {
		t.Fatal("partial release journal must not pass runtime validation")
	}
	if err := state.ValidateForRelease(); err != nil {
		t.Fatalf("partial journal must remain releasable: %v", err)
	}
	if err := slots.WriteAcquiredSlotState(dir, state); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var calls []string
	fail := true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Names []string `json:"names"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Names) != 1 {
			t.Errorf("invalid release request: %+v, %v", body, err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, body.Names[0])
		if body.Names[0] == "secondary-04" && fail {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	options := &RawReleaseOptions{SharedDir: dir, LeaseProxyServerURL: server.URL, LeaseProxyTimeout: time.Second}
	if err := Release(ctx, options); err == nil {
		t.Fatal("failed return was not reported")
	}
	mu.Lock()
	got := append([]string(nil), calls...)
	calls = nil
	fail = false
	mu.Unlock()
	if !reflect.DeepEqual(got, []string{"secondary-04", "secondary-07", "primary-00"}) {
		t.Fatalf("did not attempt every unknown/partial lease despite cancellation: %v", got)
	}
	if err := Release(ctx, options); err != nil {
		t.Fatalf("retry failed: %v", err)
	}
	mu.Lock()
	got = append([]string(nil), calls...)
	mu.Unlock()
	if !reflect.DeepEqual(got, []string{"secondary-04"}) {
		t.Fatalf("retry unsafely returned completed names: %v", got)
	}
	if _, err := slots.LoadAcquiredSlotState(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("successful retry did not remove journal: %v", err)
	}
}
