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
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/go-logr/logr/funcr"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"

	hcpsdk "github.com/Azure/ARO-HCP/test/sdk/v20261001preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
)

type identityGuardCredential struct{}

func (identityGuardCredential) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "unit-test", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

type identityGuardTransport func(*http.Request) (*http.Response, error)

func (f identityGuardTransport) Do(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestIdentityGuardTeardown(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name           string
		mode           string
		consumer       bool
		incomplete     bool
		cancelled      bool
		trackedError   bool
		containerError bool
		wantErr        bool
		wantTracked    int
		wantContainer  int
		wantReusable   bool
	}{
		{name: "surviving consumer after failed or skipped resource cleanup", consumer: true, wantErr: true},
		{name: "incomplete inventory", incomplete: true, wantErr: true},
		{name: "cancelled cleanup", cancelled: true, wantErr: true},
		{name: "audit still reports surviving consumer", mode: "audit", consumer: true, wantTracked: 1, wantContainer: 2, wantReusable: true},
		{name: "audit reports unavailable inventory", mode: "audit", incomplete: true, wantTracked: 1, wantContainer: 2, wantReusable: true},
		{name: "audit cannot ignore cancelled cleanup", mode: "audit", cancelled: true, wantErr: true},
		{name: "tracked assignment failure retains both containers", trackedError: true, wantErr: true, wantTracked: 1},
		{name: "container cleanup failure retains containers", containerError: true, wantErr: true, wantTracked: 1, wantContainer: 2},
		{name: "clean containers become reusable", wantTracked: 1, wantContainer: 2, wantReusable: true},
		{name: "invalid policy is not an opt out", mode: "off", wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			state := identityGuardTestState(t)
			containers := []string{"identity-rg-00", "identity-rg-01"}
			var logs strings.Builder
			logger := funcr.New(func(_, message string) { logs.WriteString(message) }, funcr.Options{})
			ctx, cancel := context.WithCancel(logr.NewContext(t.Context(), logger))
			defer cancel()
			if test.cancelled {
				cancel()
			}
			factory, err := hcpsdk.NewClientFactory("sub", identityGuardCredential{}, &azcorearm.ClientOptions{
				ClientOptions: azcore.ClientOptions{
					Retry: policy.RetryOptions{MaxRetries: -1},
					Transport: identityGuardTransport(func(req *http.Request) (*http.Response, error) {
						if req.Method != http.MethodGet {
							t.Fatalf("consumer scan attempted mutation: %s", req.Method)
						}
						body := `{"value":[]}`
						if test.incomplete {
							body = `{}`
						} else if test.consumer && !strings.HasSuffix(req.URL.Path, "/nodePools") {
							body = `{"value":[{"id":"/subscriptions/sub/resourceGroups/old/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/old","properties":{"provisioningState":"Deleting","platform":{"operatorsAuthentication":{"userAssignedIdentities":{"serviceManagedIdentity":"/subscriptions/sub/resourceGroups/identity-rg-01/providers/Microsoft.ManagedIdentity/userAssignedIdentities/service","controlPlaneOperators":{"operator":"/subscriptions/sub/resourceGroups/other/providers/Microsoft.ManagedIdentity/userAssignedIdentities/control-plane"},"dataPlaneOperators":{"operator":"/subscriptions/sub/resourceGroups/other/providers/Microsoft.ManagedIdentity/userAssignedIdentities/ingress"}}}}}}]}`
						}
						return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
					}),
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			tracked, container := 0, 0
			err = releaseIdentityContainers(containers,
				func() error {
					return CheckIdentityConsumers20261001(ctx, factory, "sub", containers, "teardown", test.mode)
				},
				func() error {
					tracked++
					if test.trackedError {
						return errors.New("tracked assignment deletion failed")
					}
					return nil
				},
				func(group string) error {
					return state.releaseByContainerName(group, func() error {
						container++
						if test.containerError {
							return errors.New("container FIC/RBAC deletion failed")
						}
						return nil
					})
				},
			)
			if (err != nil) != test.wantErr || tracked != test.wantTracked || container != test.wantContainer {
				t.Fatalf("release error=%v tracked=%d container=%d; expected error=%t tracked=%d container=%d", err, tracked, container, test.wantErr, test.wantTracked, test.wantContainer)
			}
			if test.cancelled && !errors.Is(err, context.Canceled) {
				t.Fatalf("lost cancellation: %v", err)
			}
			if test.mode != "off" && !strings.Contains(logs.String(), `"phase"="teardown"`) {
				t.Fatalf("missing teardown audit: %s", logs.String())
			}
			if test.consumer && (!strings.Contains(logs.String(), "identity-rg-01") || !strings.Contains(logs.String(), `"consumer"=`)) {
				t.Fatalf("missing consumer evidence: %s", logs.String())
			}
			// Discard in-memory entries to prove the persisted state governs reuse.
			state.entries = nil
			err = state.assignNTo("next-spec", 2)
			if test.wantReusable {
				if err != nil {
					t.Fatalf("clean identities could not be reserved: %v", err)
				}
			} else if !errors.Is(err, ErrNotEnoughFreeIdentityContainers) {
				t.Fatalf("unsafe identities became reusable: %v", err)
			}
		})
	}
}

func identityGuardTestState(t *testing.T) *leasedIdentityPoolState {
	t.Helper()
	dir := t.TempDir()
	lock, err := os.Create(filepath.Join(dir, "lock"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { lock.Close() })
	state := &leasedIdentityPoolState{statePath: filepath.Join(dir, "state.yaml"), lockFile: lock}
	for _, group := range []string{"identity-rg-00", "identity-rg-01"} {
		state.entries = append(state.entries, leasedIdentityPoolEntry{
			ResourceGroup: group,
			Current:       leaseEntry{State: leaseStateBusy, LeasedBy: "old-spec"},
		})
	}
	if err := state.writeUnlocked(); err != nil {
		t.Fatal(err)
	}
	return state
}

func TestIdentityGuardDevTeardown(t *testing.T) {
	t.Parallel()
	tc := &perItOrDescribeTestContext{
		perBinaryInvocationTestContext: &perBinaryInvocationTestContext{isDevelopmentEnvironment: true},
	}
	// No credentials or transport exist: constructing an ARM client would fail.
	for _, cancelled := range []bool{false, true} {
		ctx, cancel := context.WithCancel(t.Context())
		if cancelled {
			cancel()
		}
		var cleanup int
		err := releaseIdentityContainers([]string{"identity-rg-00"},
			func() error { return tc.checkLeasedIdentityConsumers(ctx, "sub", []string{"identity-rg-00"}) },
			func() error { cleanup++; return nil },
			func(string) error { cleanup++; return nil },
		)
		cancel()
		if cancelled {
			if !errors.Is(err, context.Canceled) || cleanup != 0 {
				t.Fatalf("cancelled DEV teardown: error=%v cleanup=%d", err, cleanup)
			}
		} else if err != nil || cleanup != 2 {
			t.Fatalf("DEV must skip the guard, not cleanup: error=%v cleanup=%d", err, cleanup)
		}
	}
}
func TestIdentityPoolCrashAndCleanupRetry(t *testing.T) {
	t.Parallel()
	state := identityGuardTestState(t)
	state.entries = nil
	if err := state.assignNTo("new-process", 1); !errors.Is(err, ErrNotEnoughFreeIdentityContainers) {
		t.Fatalf("process restart reused busy identity containers: %v", err)
	}
	err := state.releaseByContainerName("identity-rg-00", func() error { return errors.New("partial cleanup") })
	if err == nil {
		t.Fatal("cleanup failure was suppressed")
	}
	if err := state.assignNTo("next-spec", 1); !errors.Is(err, ErrNotEnoughFreeIdentityContainers) {
		t.Fatalf("failed cleanup freed the container: %v", err)
	}
	if err := state.releaseByContainerName("identity-rg-00", func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := state.assignNTo("next-spec", 1); err != nil {
		t.Fatalf("successful cleanup retry did not free the container: %v", err)
	}
}
