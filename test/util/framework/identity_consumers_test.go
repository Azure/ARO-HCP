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
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/go-logr/logr/funcr"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"

	hcpsdk "github.com/Azure/ARO-HCP/test/sdk/v20261001/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
)

type identityGuardCredential struct{}

func (identityGuardCredential) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "unit-test", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

type identityGuardTransport func(*http.Request) (*http.Response, error)

func (f identityGuardTransport) Do(req *http.Request) (*http.Response, error) {
	return f(req)
}

func identityGuardResponse(t *testing.T, req *http.Request, status int, body any) (*http.Response, error) {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(string(data))), Request: req}, nil
}

func identityGuardFactory(t *testing.T, transport identityGuardTransport) *hcpsdk.ClientFactory {
	t.Helper()
	factory, err := hcpsdk.NewClientFactory("sub", identityGuardCredential{}, &azcorearm.ClientOptions{
		ClientOptions: azcore.ClientOptions{Retry: policy.RetryOptions{MaxRetries: -1}, Transport: transport},
	})
	if err != nil {
		t.Fatal(err)
	}
	return factory
}

func identityGuardCluster(name, group string) *hcpsdk.HcpOpenShiftCluster {
	identityID := func(group string) *string {
		return to.Ptr("/subscriptions/sub/resourceGroups/" + group + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/service")
	}
	return &hcpsdk.HcpOpenShiftCluster{
		ID: to.Ptr("/subscriptions/sub/resourceGroups/old/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + name),
		Properties: &hcpsdk.HcpOpenShiftClusterProperties{
			ProvisioningState: to.Ptr(hcpsdk.ProvisioningStateDeleting),
			Platform: &hcpsdk.PlatformProfile{OperatorsAuthentication: &hcpsdk.OperatorsAuthenticationProfile{
				UserAssignedIdentities: &hcpsdk.UserAssignedIdentitiesProfile{
					ServiceManagedIdentity: identityID(group),
					ControlPlaneOperators:  map[string]*string{"operator": identityID("other")},
					DataPlaneOperators:     map[string]*string{"operator": identityID("other")},
				},
			}},
		},
	}
}

func TestIdentityConsumerGuardMode(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"", "disabled", "audit", "enforce", "off", "ENFORCE"} {
		t.Run(mode, func(t *testing.T) {
			got, err := IdentityConsumerGuardMode(mode)
			want := mode
			if want == "" {
				want = "disabled"
			}
			valid := want == "disabled" || want == "audit" || want == "enforce"
			if (err == nil) != valid || valid && got != want {
				t.Fatalf("mode=%q result=%q error=%v", mode, got, err)
			}
		})
	}
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
		wantFree       int
	}{
		{name: "surviving consumer retains only its container", mode: "enforce", consumer: true, wantErr: true, wantTracked: 1, wantContainer: 1, wantFree: 1},
		{name: "incomplete inventory blocks all mutations", mode: "enforce", incomplete: true, wantErr: true},
		{name: "cancelled cleanup", mode: "enforce", cancelled: true, wantErr: true},
		{name: "audit permits surviving consumer", mode: "audit", consumer: true, wantTracked: 2, wantContainer: 2, wantFree: 2},
		{name: "audit permits unavailable inventory", mode: "audit", incomplete: true, wantTracked: 2, wantContainer: 2, wantFree: 2},
		{name: "audit cannot ignore cancellation", mode: "audit", cancelled: true, wantErr: true},
		{name: "tracked assignment failure retains only affected container", mode: "enforce", trackedError: true, wantErr: true, wantTracked: 2, wantContainer: 1, wantFree: 1},
		{name: "container cleanup failure retains only affected container", mode: "enforce", containerError: true, wantErr: true, wantTracked: 2, wantContainer: 2, wantFree: 1},
		{name: "clean containers become reusable", mode: "enforce", wantTracked: 2, wantContainer: 2, wantFree: 2},
		{name: "disabled makes no consumer requests", mode: "disabled", consumer: true, wantTracked: 2, wantContainer: 2, wantFree: 2},
		{name: "empty policy defaults disabled", consumer: true, wantTracked: 2, wantContainer: 2, wantFree: 2},
		{name: "disabled cannot ignore cancellation", mode: "disabled", cancelled: true, wantErr: true},
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
			consumerRequests := 0
			factory := identityGuardFactory(t, func(req *http.Request) (*http.Response, error) {
				consumerRequests++
				if req.Method != http.MethodGet {
					t.Fatalf("consumer scan attempted mutation: %s", req.Method)
				}
				body := map[string]any{"value": []any{}}
				if test.incomplete {
					body = map[string]any{}
				} else if test.consumer && !strings.HasSuffix(req.URL.Path, "/nodePools") {
					body["value"] = []*hcpsdk.HcpOpenShiftCluster{identityGuardCluster("old", "identity-rg-01")}
				}
				return identityGuardResponse(t, req, http.StatusOK, body)
			})
			tracked, container := 0, 0
			available, err := FilterIdentityConsumers20261001(ctx, factory, "sub", containers, "teardown", test.mode)
			if err == nil {
				err = releaseIdentityContainers(containers, available,
					func(group string) error {
						tracked++
						if test.trackedError && group == containers[0] {
							return errors.New("tracked assignment deletion failed")
						}
						return nil
					},
					func(group string) error {
						return state.releaseByContainerName(group, func() error {
							container++
							if test.containerError && group == containers[0] {
								return errors.New("container FIC/RBAC deletion failed")
							}
							return nil
						})
					},
				)
			}
			if (err != nil) != test.wantErr || tracked != test.wantTracked || container != test.wantContainer {
				t.Fatalf("release error=%v tracked=%d container=%d; expected error=%t tracked=%d container=%d", err, tracked, container, test.wantErr, test.wantTracked, test.wantContainer)
			}
			if test.cancelled && !errors.Is(err, context.Canceled) {
				t.Fatalf("lost cancellation: %v", err)
			}
			if test.mode == "disabled" || test.mode == "" {
				if consumerRequests != 0 {
					t.Fatalf("disabled guard made %d consumer requests", consumerRequests)
				}
			} else if test.consumer && (!strings.Contains(logs.String(), "identity-rg-01") || !strings.Contains(logs.String(), `"consumer"=`)) {
				t.Fatalf("missing consumer evidence: %s", logs.String())
			}
			state.entries = nil
			if err := state.readUnlocked(); err != nil {
				t.Fatal(err)
			}
			free := 0
			for _, entry := range state.entries {
				if entry.Current.State == leaseStateFree {
					free++
				}
			}
			if free != test.wantFree {
				t.Fatalf("persisted free containers=%d, want %d", free, test.wantFree)
			}
		})
	}
}

func TestIdentityGuardVanishedParent(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name         string
		childStatus  int
		parentStatus int
		parentCode   string
		parentError  bool
		laterPage    bool
		wantErr      bool
		wantGets     int
	}{
		{name: "confirmed missing", childStatus: 404, parentStatus: 404, parentCode: "ResourceNotFound", wantGets: 1},
		{name: "confirmed missing after node pool page", childStatus: 404, parentStatus: 404, parentCode: "ResourceNotFound", laterPage: true, wantGets: 1},
		{name: "confirmed missing resource group", childStatus: 404, parentStatus: 404, parentCode: "ResourceGroupNotFound", wantGets: 1},
		{name: "parent still exists", childStatus: 404, parentStatus: 200, wantErr: true, wantGets: 1},
		{name: "parent get forbidden", childStatus: 404, parentStatus: 403, parentCode: "AuthorizationFailed", wantErr: true, wantGets: 1},
		{name: "parent get unrelated 404", childStatus: 404, parentStatus: 404, parentCode: "NoRegisteredProviderFound", wantErr: true, wantGets: 1},
		{name: "parent get transport failure", childStatus: 404, parentError: true, wantErr: true, wantGets: 1},
		{name: "non 404 child failure", childStatus: 403, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			groups := []string{"identity-rg-00", "identity-rg-01"}
			parentGets := 0
			continued := false
			factory := identityGuardFactory(t, func(req *http.Request) (*http.Response, error) {
				switch {
				case strings.HasSuffix(req.URL.Path, "/hcpOpenShiftClusters"):
					return identityGuardResponse(t, req, 200, map[string]any{"value": []*hcpsdk.HcpOpenShiftCluster{identityGuardCluster("gone", groups[0]), identityGuardCluster("surviving", groups[1])}})
				case strings.HasSuffix(req.URL.Path, "/gone"):
					parentGets++
					if test.parentError {
						return nil, errors.New("parent transport failed")
					}
					return identityGuardResponse(t, req, test.parentStatus, map[string]any{"error": map[string]string{"code": test.parentCode, "message": "parent lookup"}})
				case strings.Contains(req.URL.Path, "/gone/nodePools"):
					if test.laterPage && req.URL.Query().Get("page") == "" {
						next := *req.URL
						query := next.Query()
						query.Set("page", "2")
						next.RawQuery = query.Encode()
						return identityGuardResponse(t, req, 200, map[string]any{"value": []any{}, "nextLink": next.String()})
					}
					return identityGuardResponse(t, req, test.childStatus, map[string]any{"error": map[string]string{"code": "ResourceNotFound", "message": "node pool list"}})
				default:
					continued = true
					return identityGuardResponse(t, req, 200, map[string]any{"value": []any{}})
				}
			})
			available, err := FilterIdentityConsumers20261001(t.Context(), factory, "sub", groups, "teardown", "enforce")
			if (err != nil) != test.wantErr || parentGets != test.wantGets {
				t.Fatalf("available=%v error=%v parent gets=%d, want error=%t gets=%d", available, err, parentGets, test.wantErr, test.wantGets)
			}
			if !test.wantErr && (!continued || !slices.Equal(available, groups[:1])) {
				t.Fatalf("stale parent reference retained or live parent skipped: available=%v continued=%t", available, continued)
			}
			if test.wantErr && len(available) != 0 {
				t.Fatalf("incomplete inventory admitted %v", available)
			}
		})
	}
}

func TestIdentityGuardExplicitTeardownPolicy(t *testing.T) {
	for _, development := range []bool{false, true} {
		for _, mode := range []string{"", "disabled", "enforce"} {
			t.Setenv(IdentityConsumerGuardEnvvar, mode)
			calls := 0
			tc := &perItOrDescribeTestContext{
				perBinaryInvocationTestContext: &perBinaryInvocationTestContext{isDevelopmentEnvironment: development},
				clientFactory20261001: identityGuardFactory(t, func(req *http.Request) (*http.Response, error) {
					calls++
					return identityGuardResponse(t, req, 200, map[string]any{})
				}),
			}
			groups := []string{"identity-rg-00"}
			available, err := tc.filterLeasedIdentityConsumers(t.Context(), "sub", groups)
			if mode == "enforce" {
				if err == nil || calls != 1 || len(available) != 0 {
					t.Fatalf("explicit enforcement bypassed: development=%t available=%v calls=%d error=%v", development, available, calls, err)
				}
			} else if err != nil || !slices.Equal(available, groups) || calls != 0 {
				t.Fatalf("disabled teardown made consumer requests: development=%t mode=%q available=%v calls=%d error=%v", development, mode, available, calls, err)
			}
		}
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
		state.entries = append(state.entries, leasedIdentityPoolEntry{ResourceGroup: group, Current: leaseEntry{State: leaseStateBusy, LeasedBy: "old-spec"}})
	}
	if err := state.writeUnlocked(); err != nil {
		t.Fatal(err)
	}
	return state
}

func TestIdentityPoolCrashAndCleanupRetry(t *testing.T) {
	t.Parallel()
	state := identityGuardTestState(t)
	state.entries = nil
	if err := state.assignNTo("new-process", 1); !errors.Is(err, ErrNotEnoughFreeIdentityContainers) {
		t.Fatalf("process restart reused busy identity containers: %v", err)
	}
	if err := state.releaseByContainerName("identity-rg-00", func() error { return errors.New("partial cleanup") }); err == nil {
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
