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
	"fmt"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v3"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/msi/armmsi"

	"github.com/Azure/ARO-HCP/test/pkg/leaseenv"
	"github.com/Azure/ARO-HCP/test/util/mipool"
)

func TestRelease(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name               string
		initialState       leaseState
		initialLeasedBy    string
		cleanups           []func() error
		wantErr            bool
		wantState          leaseState
		wantLeasedBy       string
		wantHistoryLen     int
		wantCleanupsCalled int
	}{
		{
			name:               "free entry is a no-op",
			initialState:       leaseStateFree,
			wantErr:            false,
			wantState:          leaseStateFree,
			wantLeasedBy:       "",
			wantHistoryLen:     0,
			wantCleanupsCalled: 0,
		},
		{
			name:               "assigned entry skips cleanups",
			initialState:       leaseStateAssigned,
			initialLeasedBy:    "test-spec-1",
			wantErr:            false,
			wantState:          leaseStateFree,
			wantLeasedBy:       "",
			wantHistoryLen:     1,
			wantCleanupsCalled: 0,
		},
		{
			name:               "busy entry runs cleanups",
			initialState:       leaseStateBusy,
			initialLeasedBy:    "test-spec-1",
			wantErr:            false,
			wantState:          leaseStateFree,
			wantLeasedBy:       "",
			wantHistoryLen:     1,
			wantCleanupsCalled: 2,
		},
		{
			name:            "busy entry with failing cleanup returns error",
			initialState:    leaseStateBusy,
			initialLeasedBy: "test-spec-1",
			cleanups: []func() error{
				func() error { return nil },
				func() error { return fmt.Errorf("cleanup failed") },
			},
			wantErr:            true,
			wantState:          leaseStateBusy,
			wantLeasedBy:       "test-spec-1",
			wantHistoryLen:     0,
			wantCleanupsCalled: 2,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cleanupsCalled := 0
			entry := &leasedIdentityPoolEntry{
				ResourceGroup: "rg-test",
				Current: leaseEntry{
					State:    tc.initialState,
					LeasedBy: tc.initialLeasedBy,
				},
			}

			cleanups := tc.cleanups
			if cleanups == nil && tc.wantCleanupsCalled > 0 {
				for range tc.wantCleanupsCalled {
					cleanups = append(cleanups, func() error {
						cleanupsCalled++
						return nil
					})
				}
			} else if cleanups != nil {
				origCleanups := cleanups
				cleanups = make([]func() error, len(origCleanups))
				for i, fn := range origCleanups {
					cleanups[i] = func() error {
						cleanupsCalled++
						return fn()
					}
				}
			}

			err := entry.release(cleanups...)

			if (err != nil) != tc.wantErr {
				t.Errorf("release() error = %v, wantErr %v", err, tc.wantErr)
			}
			if entry.Current.State != tc.wantState {
				t.Errorf("release() state = %q, want %q", entry.Current.State, tc.wantState)
			}
			if entry.Current.LeasedBy != tc.wantLeasedBy {
				t.Errorf("release() leasedBy = %q, want %q", entry.Current.LeasedBy, tc.wantLeasedBy)
			}
			if len(entry.History) != tc.wantHistoryLen {
				t.Errorf("release() history length = %d, want %d", len(entry.History), tc.wantHistoryLen)
			}
			if cleanupsCalled != tc.wantCleanupsCalled {
				t.Errorf("release() cleanups called = %d, want %d", cleanupsCalled, tc.wantCleanupsCalled)
			}
		})
	}
}

func TestAssignFromEnv(t *testing.T) {

	tests := []struct {
		name       string
		containers []string
		count      int
		wantErr    bool
		wantLen    int
	}{
		{
			name:       "exact match",
			containers: []string{"rg-00", "rg-01"},
			count:      2,
			wantLen:    2,
		},
		{
			name:       "more available than requested",
			containers: []string{"rg-00", "rg-01", "rg-02"},
			count:      2,
			wantLen:    2,
		},
		{
			name:       "single container",
			containers: []string{"rg-00"},
			count:      1,
			wantLen:    1,
		},
		{
			name:       "not enough containers",
			containers: []string{"rg-00"},
			count:      2,
			wantErr:    true,
		},
		{
			name:       "zero count",
			containers: []string{"rg-00", "rg-01"},
			count:      0,
			wantLen:    0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(leaseenv.MSIContainers, strings.Join(tt.containers, " "))

			tc := &perItOrDescribeTestContext{}
			err := tc.assignFromEnv(tt.containers, tt.count)

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tc.miEnvAssigned {
					t.Fatal("miEnvAssigned should be false after assignment error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !tc.miEnvAssigned {
				t.Fatal("miEnvAssigned should be true after successful env assignment")
			}
			if len(tc.miEnvAssignedContainers) != tt.wantLen {
				t.Fatalf("miEnvAssignedContainers length = %d, want %d", len(tc.miEnvAssignedContainers), tt.wantLen)
			}
			if tc.miEnvAssignedIdx != 0 {
				t.Fatalf("miEnvAssignedIdx = %d, want 0", tc.miEnvAssignedIdx)
			}
		})
	}
}

func TestIdentityTeardownPreservesBusyPrincipals(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name               string
		failedTracked      bool
		failedFIC          bool
		missingIdentity    bool
		incompleteRoles    bool
		failedTrackedGet   bool
		allSafe            bool
		failedUnassociated bool
	}{
		{name: "safe B and C release while A and its RBAC remain"},
		{name: "tracked B deletion failure does not retain C", failedTracked: true},
		{name: "FIC B deletion failure does not retain C", failedFIC: true},
		{name: "missing identity blocks all mutations", missingIdentity: true},
		{name: "later role inventory failure blocks all mutations", incompleteRoles: true},
		{name: "tracked principal lookup failure blocks all mutations", failedTrackedGet: true},
		{name: "all safe containers clean unassociated tracked roles", allSafe: true},
		{name: "unassociated cleanup failure retains all containers", allSafe: true, failedUnassociated: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			groups := []string{"identity-rg-00", "identity-rg-01", "identity-rg-02"}
			principal := func(group string) string { return group + "-service-principal" }
			trackedID := func(group string) string {
				return "/subscriptions/sub/providers/Microsoft.Authorization/roleAssignments/tracked-" + group
			}
			scope := func(group string) string { return "/subscriptions/sub/resourceGroups/" + group }
			unassociatedID := scope(groups[1]) + "/providers/Microsoft.Authorization/roleAssignments/unassociated"
			assignment := func(id, owner, assignmentScope string) *armauthorization.RoleAssignment {
				return &armauthorization.RoleAssignment{
					ID: to.Ptr(id), Name: to.Ptr(id[strings.LastIndex(id, "/")+1:]),
					Properties: &armauthorization.RoleAssignmentProperties{PrincipalID: to.Ptr(owner), Scope: to.Ptr(assignmentScope)},
				}
			}
			var deleteLock sync.Mutex
			var deletes []string
			transport := identityGuardTransport(func(req *http.Request) (*http.Response, error) {
				if req.Method == http.MethodDelete {
					deleteLock.Lock()
					deletes = append(deletes, req.URL.Path)
					deleteLock.Unlock()
					if test.failedTracked && req.URL.Path == trackedID(groups[1]) || test.failedUnassociated && req.URL.Path == unassociatedID {
						return identityGuardResponse(t, req, 403, map[string]any{"error": map[string]string{"code": "AuthorizationFailed"}})
					}
					return identityGuardResponse(t, req, 200, map[string]any{})
				}
				for _, group := range groups {
					if strings.HasSuffix(req.URL.Path, "/resourceGroups/"+group+"/providers/Microsoft.ManagedIdentity/userAssignedIdentities") {
						if test.allSafe {
							t.Fatal("unguarded cleanup must not inventory identity principals")
						}
						var identities []*armmsi.Identity
						for _, name := range NewDefaultIdentities().ToSlice() {
							if test.missingIdentity && group == groups[2] && name == ServiceManagedIdentityName {
								continue
							}
							identities = append(identities, &armmsi.Identity{
								ID:         to.Ptr(scope(group) + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/" + name),
								Properties: &armmsi.UserAssignedIdentityProperties{PrincipalID: to.Ptr(principal(group))},
							})
						}
						return identityGuardResponse(t, req, 200, map[string]any{"value": identities})
					}
					if req.URL.Path == trackedID(group) {
						if test.allSafe {
							t.Fatal("unguarded cleanup must not read tracked role assignments")
						}
						if test.failedTrackedGet && group == groups[2] {
							return identityGuardResponse(t, req, 403, map[string]any{"error": map[string]string{"code": "AuthorizationFailed"}})
						}
						return identityGuardResponse(t, req, 200, assignment(trackedID(group), strings.ToUpper(principal(group)), "/subscriptions/sub"))
					}
					if req.URL.Path == scope(group)+"/providers/Microsoft.Authorization/roleAssignments" {
						if test.incompleteRoles && group == groups[2] {
							return identityGuardResponse(t, req, 200, map[string]any{})
						}
						local := scope(group) + "/providers/Microsoft.Authorization/roleAssignments/local"
						protected := scope(group) + "/providers/Microsoft.Authorization/roleAssignments/busy-principal"
						roles := []*armauthorization.RoleAssignment{
							assignment(local, principal(group), scope(group)),
							assignment(protected, principal(groups[0]), scope(group)),
						}
						if group == groups[1] {
							roles = append(roles, assignment(unassociatedID, "unrelated-principal", scope(group)))
						}
						return identityGuardResponse(t, req, 200, map[string]any{"value": roles})
					}
				}
				if req.URL.Path == unassociatedID {
					return identityGuardResponse(t, req, 200, assignment(unassociatedID, "unrelated-principal", scope(groups[1])))
				}
				t.Fatalf("unexpected teardown request: %s %s", req.Method, req.URL)
				return nil, nil
			})
			options := &azcorearm.ClientOptions{ClientOptions: azcore.ClientOptions{Retry: policy.RetryOptions{MaxRetries: -1}, Transport: transport}}
			msi, err := armmsi.NewClientFactory("sub", identityGuardCredential{}, options)
			if err != nil {
				t.Fatal(err)
			}
			roles, err := armauthorization.NewRoleAssignmentsClient("sub", identityGuardCredential{}, options)
			if err != nil {
				t.Fatal(err)
			}
			state := identityGuardTestState(t)
			state.entries = append(state.entries, leasedIdentityPoolEntry{ResourceGroup: groups[2], Current: leaseEntry{State: leaseStateBusy, LeasedBy: "old-spec"}})
			if err := state.writeUnlocked(); err != nil {
				t.Fatal(err)
			}
			tracked := []string{trackedID(groups[0]), trackedID(groups[1]), trackedID(groups[2]), unassociatedID}
			tc := &perItOrDescribeTestContext{createdRoleAssignmentIDs: slices.Clone(tracked), subscriptionID: "sub"}
			available := groups[1:]
			if test.allSafe {
				available = groups
			}
			plan, err := planIdentityRoleAssignmentCleanup(t.Context(), msi.NewUserAssignedIdentitiesClient(), roles, "sub", groups, available, tracked)
			fics := map[string]int{}
			if err == nil {
				err = tc.cleanupRoleAssignmentIDs(t.Context(), roles, plan.unassociated)
			}
			if err == nil {
				err = releaseIdentityContainers(groups, available,
					func(group string) error {
						return tc.cleanupRoleAssignmentIDs(t.Context(), roles, plan.byContainer[group])
					},
					func(group string) error {
						return state.releaseByContainerName(group,
							func() error {
								fics[group]++
								if test.failedFIC && group == groups[1] {
									return fmt.Errorf("FIC deletion failed")
								}
								return nil
							},
							func() error { return tc.cleanupLeasedIdentityContainerRoleAssignments(t.Context(), roles, group, plan) },
						)
					},
				)
			}
			if (err == nil) != (test.allSafe && !test.failedUnassociated) {
				t.Fatalf("unexpected teardown result: %v", err)
			}
			preflightFailure := test.missingIdentity || test.incompleteRoles || test.failedTrackedGet
			if preflightFailure && (len(deletes) != 0 || len(fics) != 0) {
				t.Fatalf("incomplete inventory mutated resources: deletes=%v FICs=%v", deletes, fics)
			}
			if !test.allSafe {
				for _, deleted := range deletes {
					if deleted == trackedID(groups[0]) || deleted == unassociatedID || strings.HasSuffix(deleted, "/busy-principal") || strings.Contains(deleted, "/resourceGroups/"+groups[0]+"/") {
						t.Fatalf("deleted busy or conservatively retained RBAC: %s", deleted)
					}
				}
				if fics[groups[0]] != 0 || !slices.Contains(tc.createdRoleAssignmentIDs, trackedID(groups[0])) || !slices.Contains(tc.createdRoleAssignmentIDs, unassociatedID) {
					t.Fatalf("lost busy cleanup state: FICs=%v tracked=%v", fics, tc.createdRoleAssignmentIDs)
				}
			}
			if err := state.readUnlocked(); err != nil {
				t.Fatal(err)
			}
			for _, entry := range state.entries {
				wantFree := !preflightFailure && !test.failedUnassociated && (test.allSafe || entry.ResourceGroup != groups[0])
				if entry.ResourceGroup == groups[1] && (test.failedTracked || test.failedFIC) {
					wantFree = false
				}
				if (entry.Current.State == leaseStateFree) != wantFree {
					t.Fatalf("container=%s state=%s, want free=%t", entry.ResourceGroup, entry.Current.State, wantFree)
				}
				if wantFree && (!slices.Contains(deletes, trackedID(entry.ResourceGroup)) || fics[entry.ResourceGroup] != 1) {
					t.Fatalf("container freed before cleanup: group=%s deletes=%v FICs=%v", entry.ResourceGroup, deletes, fics)
				}
			}
			if test.failedTracked && !slices.Contains(tc.createdRoleAssignmentIDs, trackedID(groups[1])) {
				t.Fatal("failed tracked assignment was forgotten")
			}
		})
	}
}

func TestAssignFromEnvIsIndependent(t *testing.T) {
	t.Setenv(leaseenv.MSIContainers, "rg-00 rg-01")

	containers := []string{"rg-00", "rg-01"}
	tc := &perItOrDescribeTestContext{}
	if err := tc.assignFromEnv(containers, 2); err != nil {
		t.Fatalf("assignFromEnv: %v", err)
	}
	containers[0] = "mutated"
	if tc.miEnvAssignedContainers[0] != "rg-00" {
		t.Fatalf("internal assignment mutated via caller slice: %v", tc.miEnvAssignedContainers)
	}
}

func TestLeaseFromEnv(t *testing.T) {
	t.Parallel()

	tc := &perItOrDescribeTestContext{}
	tc.miEnvAssignedContainers = []string{"rg-00", "rg-01", "rg-02"}
	tc.miEnvAssignedIdx = 0

	for i, wantRG := range []string{"rg-00", "rg-01", "rg-02"} {
		pool, err := tc.leaseFromEnv()
		if err != nil {
			t.Fatalf("lease %d: unexpected error: %v", i, err)
		}
		if pool.ResourceGroupName != wantRG {
			t.Fatalf("lease %d: ResourceGroupName = %q, want %q", i, pool.ResourceGroupName, wantRG)
		}
	}

	_, err := tc.leaseFromEnv()
	if err == nil {
		t.Fatal("expected error after exhausting all containers, got nil")
	}
}

func TestAssignFromEnvThenLeaseFromEnv(t *testing.T) {
	t.Setenv(leaseenv.MSIContainers, "rg-00 rg-01 rg-02")

	tc := &perItOrDescribeTestContext{}
	if err := tc.assignFromEnv([]string{"rg-00", "rg-01", "rg-02"}, 2); err != nil {
		t.Fatalf("assignFromEnv: %v", err)
	}

	pool1, err := tc.leaseFromEnv()
	if err != nil {
		t.Fatalf("first lease: %v", err)
	}
	if pool1.ResourceGroupName != "rg-00" {
		t.Fatalf("first lease RG = %q, want rg-00", pool1.ResourceGroupName)
	}

	pool2, err := tc.leaseFromEnv()
	if err != nil {
		t.Fatalf("second lease: %v", err)
	}
	if pool2.ResourceGroupName != "rg-01" {
		t.Fatalf("second lease RG = %q, want rg-01", pool2.ResourceGroupName)
	}

	_, err = tc.leaseFromEnv()
	if err == nil {
		t.Fatal("expected error after exhausting assigned containers, got nil")
	}
}

func TestGetLeasedIdentitiesUsesEnvPathWhenAssignedEmpty(t *testing.T) {
	t.Parallel()

	tc := &perItOrDescribeTestContext{}
	if err := tc.assignFromEnv([]string{"rg-00", "rg-01"}, 0); err != nil {
		t.Fatalf("assignFromEnv(0): %v", err)
	}
	if !tc.miEnvAssigned {
		t.Fatal("miEnvAssigned should be true after zero-count env assignment")
	}

	_, err := tc.getLeasedIdentities()
	if err == nil {
		t.Fatal("expected error leasing from empty env assignment, got nil")
	}
	if !strings.Contains(err.Error(), "parent-assigned") {
		t.Fatalf("error = %q, want parent-assigned env path (not file-pool fallback)", err)
	}
}

func TestAssignFromEnvRejectsNameOutsideLease(t *testing.T) {
	t.Setenv(leaseenv.MSIContainers, "rg-00")

	tc := &perItOrDescribeTestContext{}
	err := tc.assignFromEnv([]string{"not-in-the-lease"}, 1)
	if err == nil {
		t.Fatal("expected error for a container outside LEASED_MSI_CONTAINERS, got nil")
	}
	if tc.miEnvAssigned {
		t.Fatal("miEnvAssigned should stay false when the name is rejected")
	}

	allowed := &perItOrDescribeTestContext{}
	if err := allowed.assignFromEnv([]string{"RG-00"}, 1); err != nil {
		t.Fatalf("case-insensitive allowlist: %v", err)
	}
	pool, err := allowed.leaseFromEnv()
	if err != nil {
		t.Fatalf("leaseFromEnv: %v", err)
	}
	if pool.ResourceGroupName != "RG-00" {
		t.Fatalf("ResourceGroupName = %q, want RG-00", pool.ResourceGroupName)
	}
}

func TestAssignIdentityContainersRejectsUnleasedEnvName(t *testing.T) {
	t.Setenv(mipool.AssignedMIContainersEnvvar, "not-in-the-lease")
	t.Setenv(leaseenv.MSIContainers, "rg-00")

	tc := &perItOrDescribeTestContext{}
	err := tc.AssignIdentityContainers(context.Background(), 1, time.Second)
	if err == nil {
		t.Fatal("expected AssignIdentityContainers to reject a name outside the lease")
	}
	if tc.miEnvAssigned {
		t.Fatal("miEnvAssigned should stay false")
	}
}

func TestLeaseFromEnvConcurrent(t *testing.T) {
	const goroutines = 32
	tc := &perItOrDescribeTestContext{
		miEnvAssigned:           true,
		miEnvAssignedContainers: []string{"rg-00", "rg-01"},
	}

	var start sync.WaitGroup
	start.Add(1)
	var done sync.WaitGroup
	leased := make([]string, goroutines)
	errs := make([]error, goroutines)
	for i := 0; i < goroutines; i++ {
		done.Add(1)
		go func(i int) {
			defer done.Done()
			start.Wait()
			pool, err := tc.leaseFromEnv()
			if err != nil {
				errs[i] = err
				return
			}
			leased[i] = pool.ResourceGroupName
		}(i)
	}
	start.Done()
	done.Wait()

	counts := map[string]int{}
	var successes int
	for _, rg := range leased {
		if rg == "" {
			continue
		}
		successes++
		counts[rg]++
	}
	if successes != 2 || counts["rg-00"] != 1 || counts["rg-01"] != 1 {
		t.Fatalf("leased counts = %v successes = %d idx = %d errs = %v", counts, successes, tc.miEnvAssignedIdx, errs)
	}
}

type failTokenCredential struct{}

func (failTokenCredential) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{}, fmt.Errorf("repro token denied")
}

func TestEnvAssignedContainersCleanedWithoutFilePool(t *testing.T) {
	t.Setenv("ARTIFACT_DIR", t.TempDir())
	t.Setenv(leaseenv.MSIContainers, "rg-a")

	inv := &perBinaryInvocationTestContext{
		pooledIdentities: true,
		azureCredentials: failTokenCredential{},
	}
	tc := &perItOrDescribeTestContext{
		perBinaryInvocationTestContext: inv,
		subscriptionID:                 "00000000-0000-0000-0000-000000000000",
	}
	if err := tc.assignFromEnv([]string{"rg-a"}, 1); err != nil {
		t.Fatalf("assignFromEnv: %v", err)
	}
	if _, err := tc.leaseFromEnv(); err != nil {
		t.Fatalf("leaseFromEnv: %v", err)
	}

	state, err := inv.getLeasedIdentityPoolState()
	if err != nil {
		t.Fatalf("getLeasedIdentityPoolState: %v", err)
	}
	before, err := os.ReadFile(state.statePath)
	if err != nil {
		t.Fatalf("read pool file: %v", err)
	}

	visible, err := tc.leasedIdentityContainers()
	if err != nil {
		t.Fatalf("leasedIdentityContainers: %v", err)
	}
	if len(visible) != 1 || visible[0] != "rg-a" {
		t.Fatalf("leasedIdentityContainers = %v, want [rg-a]", visible)
	}

	releaseErr := tc.releaseLeasedIdentities(context.Background())
	if releaseErr == nil {
		t.Fatal("releaseLeasedIdentities returned nil; env-assigned containers were not cleaned")
	}
	if !strings.Contains(releaseErr.Error(), "repro token denied") {
		t.Fatalf("release error = %v, want the cleanup credential error", releaseErr)
	}

	after, err := os.ReadFile(state.statePath)
	if err != nil {
		t.Fatalf("re-read pool file: %v", err)
	}
	if string(before) != string(after) {
		t.Fatalf("file pool changed across env release:\nbefore:\n%s\nafter:\n%s", before, after)
	}
	fileLeases, err := state.getLeasedIdentityContainers(specID())
	if err != nil {
		t.Fatalf("getLeasedIdentityContainers: %v", err)
	}
	if len(fileLeases) != 0 {
		t.Fatalf("file-pool leases = %v, want empty", fileLeases)
	}
}
