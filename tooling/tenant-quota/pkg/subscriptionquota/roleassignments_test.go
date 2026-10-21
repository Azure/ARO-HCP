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

package subscriptionquota

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
)

type fakeRoleAssignmentMetricsGetter struct {
	metrics roleAssignmentMetrics
	err     error
}

func (f *fakeRoleAssignmentMetricsGetter) Get(context.Context) (roleAssignmentMetrics, error) {
	return f.metrics, f.err
}

func TestRoleAssignmentSource(t *testing.T) {
	source := NewRoleAssignmentSource()
	if got := source.Name(); got != "rbac" {
		t.Fatalf("Name() = %q, want %q", got, "rbac")
	}
	if source.IsRegional() {
		t.Fatal("IsRegional() = true, want false")
	}

	var gotSubscriptionID string
	source.newClient = func(subscriptionID string, _ azcore.TokenCredential) (roleAssignmentMetricsGetter, error) {
		gotSubscriptionID = subscriptionID
		return &fakeRoleAssignmentMetricsGetter{
			metrics: roleAssignmentMetrics{
				currentCount: 123,
				limit:        8000,
			},
		}, nil
	}

	results, errs := source.Collect(context.Background(), nil, "subscription-id", "")
	if len(errs) != 0 {
		t.Fatalf("Collect() errors = %v, want none", errs)
	}
	if gotSubscriptionID != "subscription-id" {
		t.Fatalf("client subscription ID = %q, want %q", gotSubscriptionID, "subscription-id")
	}
	if len(results) != 1 {
		t.Fatalf("Collect() returned %d results, want 1", len(results))
	}

	got := results[0]
	if got.QuotaName != "roleAssignments" {
		t.Fatalf("QuotaName = %q, want %q", got.QuotaName, "roleAssignments")
	}
	if got.LocalizedName != "Role Assignments" {
		t.Fatalf("LocalizedName = %q, want %q", got.LocalizedName, "Role Assignments")
	}
	if got.CurrentValue != 123 {
		t.Fatalf("CurrentValue = %v, want 123", got.CurrentValue)
	}
	if got.Limit != 8000 {
		t.Fatalf("Limit = %v, want 8000", got.Limit)
	}
	if got.SubscriptionID != "subscription-id" {
		t.Fatalf("SubscriptionID = %q, want %q", got.SubscriptionID, "subscription-id")
	}
	if got.Region != "" {
		t.Fatalf("Region = %q, want empty", got.Region)
	}
}

func TestRoleAssignmentSourceErrors(t *testing.T) {
	testCases := []struct {
		name       string
		newClient  roleAssignmentMetricsClientFactory
		wantErrSub string
	}{
		{
			name: "client creation failure",
			newClient: func(string, azcore.TokenCredential) (roleAssignmentMetricsGetter, error) {
				return nil, fmt.Errorf("create failed")
			},
			wantErrSub: "create role assignment metrics client: create failed",
		},
		{
			name: "metrics request failure",
			newClient: func(string, azcore.TokenCredential) (roleAssignmentMetricsGetter, error) {
				return &fakeRoleAssignmentMetricsGetter{err: fmt.Errorf("request failed")}, nil
			},
			wantErrSub: "get role assignment metrics: request failed",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			source := &RoleAssignmentSource{newClient: tc.newClient}
			results, errs := source.Collect(context.Background(), nil, "subscription-id", "")
			if len(results) != 0 {
				t.Fatalf("Collect() returned %d results, want none", len(results))
			}
			if len(errs) != 1 {
				t.Fatalf("Collect() returned %d errors, want 1", len(errs))
			}
			if !strings.Contains(errs[0].Error(), tc.wantErrSub) {
				t.Fatalf("Collect() error = %v, want substring %q", errs[0], tc.wantErrSub)
			}
		})
	}
}
