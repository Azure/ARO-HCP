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

package framework

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gofrs/flock"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	azfake "github.com/Azure/azure-sdk-for-go/sdk/azcore/fake"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

var immutableDNSRejection = ClusterPatchRejection{
	StatusCode: http.StatusBadRequest, Code: "InvalidRequestContent",
	Target: "properties.dns.baseDomainPrefix", Message: "field is immutable",
}

const immutableDNSResponse = `{"error":{"code":"InvalidRequestContent","target":"properties.dns.baseDomainPrefix","message":"Forbidden: field is immutable"}}`

func testGuardedClusterClient(t *testing.T, manifest SharedClusterManifest, transport policy.Transporter) *GuardedNegativeClusterClient {
	t.Helper()
	client, err := azcorearm.NewClient("test", "v0.0.0", &azfake.TokenCredential{}, &azcorearm.ClientOptions{
		ClientOptions: azcore.ClientOptions{Transport: transport, Retry: policy.RetryOptions{MaxRetries: -1}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return &GuardedNegativeClusterClient{manifest: manifest, manifestPath: os.Getenv(SharedFixtureManifestEnv), client: client}
}

func TestGuardedClusterPatchContract(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		status     int
		body       string
		patchError error
		change     bool
		afterError bool
		wantError  bool
	}{
		{name: "rejected and unchanged", status: 400, body: immutableDNSResponse},
		{name: "accepted", status: 202, body: `{}`, wantError: true},
		{name: "wrong status", status: 409, body: immutableDNSResponse, wantError: true},
		{name: "wrong code", status: 400, body: strings.ReplaceAll(immutableDNSResponse, "InvalidRequestContent", "Other"), wantError: true},
		{name: "wrong target", status: 400, body: strings.ReplaceAll(immutableDNSResponse, "baseDomainPrefix", "other"), wantError: true},
		{name: "wrong reason", status: 400, body: strings.ReplaceAll(immutableDNSResponse, "field is immutable", "invalid syntax"), wantError: true},
		{name: "transport error", patchError: errors.New("connection lost"), wantError: true},
		{name: "changed despite rejection", status: 400, body: immutableDNSResponse, change: true, wantError: true},
		{name: "cannot verify after", status: 400, body: immutableDNSResponse, afterError: true, wantError: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			manifest := publishedTestManifest(t)
			var methods []string
			patch := map[string]any{"properties": map[string]any{"dns": map[string]any{"baseDomainPrefix": "changed"}}}
			client := testGuardedClusterClient(t, manifest, &fakeTransport{do: func(request *http.Request) (*http.Response, error) {
				methods = append(methods, request.Method)
				if request.URL.Path != manifest.ClusterResourceID || request.URL.Query().Get("api-version") != "2024-06-10-preview" {
					t.Errorf("wrong request target: %s", request.URL)
				}
				if request.Method == http.MethodPatch {
					var actual map[string]any
					if err := json.NewDecoder(request.Body).Decode(&actual); err != nil || !reflect.DeepEqual(actual, patch) {
						t.Errorf("PATCH body: %v, %v", actual, err)
					}
					if testCase.patchError != nil {
						return nil, testCase.patchError
					}
					response, err := jobIDResponse(testCase.body)
					response.StatusCode = testCase.status
					return response, err
				}
				if len(methods) == 3 && testCase.afterError {
					return nil, errors.New("GET failed")
				}
				state := "Succeeded"
				if len(methods) == 3 && testCase.change {
					state = "Updating"
				}
				return jobIDResponse(fmt.Sprintf(`{"id":%q,"properties":{"dns":{"baseDomainPrefix":"original"},"provisioningState":%q}}`, manifest.ClusterResourceID, state))
			}})
			if err := client.RejectPatch20240610(t.Context(), patch, immutableDNSRejection); (err != nil) != testCase.wantError {
				t.Fatalf("guarded PATCH error: %v", err)
			}
			if !reflect.DeepEqual(methods, []string{"GET", "PATCH", "GET"}) {
				t.Fatalf("must capture before and after even on unexpected rejection: %v", methods)
			}
			if _, err := ReadSharedClusterManifest(); (err != nil) != testCase.wantError {
				t.Fatalf("fixture quarantine: %v", err)
			}
			if testCase.wantError {
				if err := client.RejectPatch20240610(t.Context(), patch, immutableDNSRejection); err == nil || len(methods) != 3 {
					t.Fatalf("quarantined fixture permitted more requests: %v, %v", methods, err)
				}
			}
		})
	}
}

func TestGuardedClusterWritesSerialize(t *testing.T) {
	manifest := publishedTestManifest(t)
	var requests atomic.Int32
	var patches atomic.Int32
	patchEntered := make(chan struct{})
	releasePatch := make(chan struct{})
	defer close(releasePatch)
	transport := &fakeTransport{do: func(request *http.Request) (*http.Response, error) {
		requests.Add(1)
		if request.Method == http.MethodPatch {
			if patches.Add(1) == 1 {
				close(patchEntered)
				select {
				case <-releasePatch:
				case <-request.Context().Done():
					return nil, request.Context().Err()
				}
			}
			response, err := jobIDResponse(immutableDNSResponse)
			response.StatusCode = http.StatusBadRequest
			return response, err
		}
		return jobIDResponse(fmt.Sprintf(`{"id":%q}`, manifest.ClusterResourceID))
	}}
	first := testGuardedClusterClient(t, manifest, transport)
	second := testGuardedClusterClient(t, manifest, transport)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	finished := make(chan error, 1)
	go func() { finished <- first.RejectPatch20240610(ctx, map[string]any{}, immutableDNSRejection) }()
	select {
	case <-patchEntered:
	case <-ctx.Done():
		t.Fatal("first borrower did not enter PATCH")
	}
	waitCtx, stopWaiting := context.WithTimeout(ctx, 100*time.Millisecond)
	defer stopWaiting()
	if err := second.RejectPatch20240610(waitCtx, map[string]any{}, immutableDNSRejection); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("second borrower must time out waiting for lock: %v", err)
	}
	if requests.Load() != 2 {
		t.Fatalf("second borrower escaped lock: %d requests", requests.Load())
	}
	releasePatch <- struct{}{}
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	if err := second.RejectPatch20240610(ctx, map[string]any{}, immutableDNSRejection); err != nil {
		t.Fatalf("second borrower could not proceed after first: %v", err)
	}
	if requests.Load() != 6 {
		t.Fatalf("two borrowers must each perform GET/PATCH/GET: %d", requests.Load())
	}
	lock := flock.New(first.manifestPath + ".guarded-write.lock")
	locked, err := lock.TryLock()
	if err != nil || !locked {
		t.Fatalf("lock not released: %v", err)
	}
	if err := lock.Unlock(); err != nil {
		t.Fatal(err)
	}
}

func TestGuardedClusterFailsBeforeWriting(t *testing.T) {
	for _, scenario := range []string{"missing manifest", "stale manifest", "changed manifest", "invalid expectation", "failed pre-GET", "wrong cluster"} {
		t.Run(scenario, func(t *testing.T) {
			manifest := publishedTestManifest(t)
			writes := 0
			client := testGuardedClusterClient(t, manifest, &fakeTransport{do: func(request *http.Request) (*http.Response, error) {
				if request.Method != http.MethodGet {
					writes++
				}
				if scenario == "failed pre-GET" {
					return nil, errors.New("GET failed")
				}
				return jobIDResponse(`{"id":"wrong"}`)
			}})
			expected := immutableDNSRejection
			switch scenario {
			case "missing manifest":
				if err := os.Remove(client.manifestPath); err != nil {
					t.Fatal(err)
				}
			case "stale manifest", "changed manifest":
				manifest.ExpiresAt = manifest.ExpiresAt.Add(time.Minute)
				if scenario == "stale manifest" {
					manifest.Ready = false
				}
				if err := WriteSharedClusterManifest(client.manifestPath, manifest); err != nil {
					t.Fatal(err)
				}
			case "invalid expectation":
				expected.Code = ""
			}
			if err := client.RejectPatch20240610(t.Context(), map[string]any{}, expected); err == nil || writes != 0 {
				t.Fatalf("unsafe precondition allowed write: writes=%d, error=%v", writes, err)
			}
		})
	}
}
