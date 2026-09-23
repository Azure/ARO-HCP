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

package slots

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveCustomerSubscription(t *testing.T) {
	t.Parallel()

	clusterProfileDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(clusterProfileDir, "customer-dev-subscription-name"), []byte("customer-dev\n"), 0o644); err != nil {
		t.Fatalf("expected write to succeed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(clusterProfileDir, "customer-dev-subscription-id"), []byte("customer-dev-id\n"), 0o644); err != nil {
		t.Fatalf("expected write to succeed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(clusterProfileDir, "customer-other-subscription-name"), []byte("customer-other\n"), 0o644); err != nil {
		t.Fatalf("expected write to succeed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(clusterProfileDir, "customer-other-subscription-id"), []byte("customer-other-id\n"), 0o644); err != nil {
		t.Fatalf("expected write to succeed: %v", err)
	}

	resolved, err := ResolveCustomerSubscription([]string{clusterProfileDir}, "customer-dev")
	if err != nil {
		t.Fatalf("expected subscription verification to succeed: %v", err)
	}
	if resolved.Name != "customer-dev" {
		t.Fatalf("expected verified subscription %q, got %q", "customer-dev", resolved.Name)
	}
	if resolved.ID != "customer-dev-id" {
		t.Fatalf("expected subscription ID %q, got %q", "customer-dev-id", resolved.ID)
	}
	if resolved.ClusterProfileDir != clusterProfileDir {
		t.Fatalf("expected matched dir %q, got %q", clusterProfileDir, resolved.ClusterProfileDir)
	}
}

func TestResolveCustomerSubscriptionAcrossDirs(t *testing.T) {
	t.Parallel()

	rhDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(rhDir, "customer-shard0-subscription-name"), []byte("rh-sub\n"), 0o644); err != nil {
		t.Fatalf("expected write to succeed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(rhDir, "customer-shard0-subscription-id"), []byte("rh-sub-id\n"), 0o644); err != nil {
		t.Fatalf("expected write to succeed: %v", err)
	}
	testTenantDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(testTenantDir, "customer-shard0-subscription-name"), []byte("test-tenant-sub\n"), 0o644); err != nil {
		t.Fatalf("expected write to succeed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(testTenantDir, "customer-shard0-subscription-id"), []byte("test-tenant-sub-id\n"), 0o644); err != nil {
		t.Fatalf("expected write to succeed: %v", err)
	}

	resolved, err := ResolveCustomerSubscription([]string{rhDir, testTenantDir}, "test-tenant-sub")
	if err != nil {
		t.Fatalf("expected subscription verification to succeed: %v", err)
	}
	if resolved.Name != "test-tenant-sub" {
		t.Fatalf("expected verified subscription %q, got %q", "test-tenant-sub", resolved.Name)
	}
	if resolved.ID != "test-tenant-sub-id" {
		t.Fatalf("expected subscription ID %q, got %q", "test-tenant-sub-id", resolved.ID)
	}
	if resolved.ClusterProfileDir != testTenantDir {
		t.Fatalf("expected matched dir %q, got %q", testTenantDir, resolved.ClusterProfileDir)
	}
}

func TestResolveCustomerSubscriptionRejectsMatchInMultipleDirs(t *testing.T) {
	t.Parallel()

	dirA := t.TempDir()
	dirB := t.TempDir()
	for _, dir := range []string{dirA, dirB} {
		if err := os.WriteFile(filepath.Join(dir, "customer-shard0-subscription-name"), []byte("dup-sub\n"), 0o644); err != nil {
			t.Fatalf("expected write to succeed: %v", err)
		}
	}

	_, err := ResolveCustomerSubscription([]string{dirA, dirB}, "dup-sub")
	if err == nil {
		t.Fatal("expected cross-dir duplicate match verification to fail")
	}
	if !strings.Contains(err.Error(), "multiple customer subscription name files matched") {
		t.Fatalf("expected duplicate match error, got %v", err)
	}
}

func TestResolveCustomerSubscriptionRejectsDuplicateMatches(t *testing.T) {
	t.Parallel()

	clusterProfileDir := t.TempDir()
	for _, fileName := range []string{
		"customer-dev-1-subscription-name",
		"customer-dev-2-subscription-name",
	} {
		if err := os.WriteFile(filepath.Join(clusterProfileDir, fileName), []byte("customer-dev\n"), 0o644); err != nil {
			t.Fatalf("expected write to succeed: %v", err)
		}
	}

	_, err := ResolveCustomerSubscription([]string{clusterProfileDir}, "customer-dev")
	if err == nil {
		t.Fatal("expected duplicate match verification to fail")
	}
	if !strings.Contains(err.Error(), "multiple customer subscription name files matched") {
		t.Fatalf("expected duplicate match error, got %v", err)
	}
}

func TestResolveCustomerSubscriptionRequiresMatchingID(t *testing.T) {
	t.Parallel()

	clusterProfileDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(clusterProfileDir, "customer-shard0-subscription-name"), []byte("customer-dev\n"), 0o644); err != nil {
		t.Fatalf("expected write to succeed: %v", err)
	}

	_, err := ResolveCustomerSubscription([]string{clusterProfileDir}, "customer-dev")
	if err == nil {
		t.Fatal("expected missing subscription ID to fail")
	}
	if !strings.Contains(err.Error(), "failed to read customer subscription ID") {
		t.Fatalf("expected missing subscription ID error, got %v", err)
	}
}

func TestResolveCustomerSubscriptionRejectsEmptyID(t *testing.T) {
	t.Parallel()

	clusterProfileDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(clusterProfileDir, "customer-shard0-subscription-name"), []byte("customer-dev\n"), 0o644); err != nil {
		t.Fatalf("expected write to succeed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(clusterProfileDir, "customer-shard0-subscription-id"), []byte("\n"), 0o644); err != nil {
		t.Fatalf("expected write to succeed: %v", err)
	}

	_, err := ResolveCustomerSubscription([]string{clusterProfileDir}, "customer-dev")
	if err == nil {
		t.Fatal("expected empty subscription ID to fail")
	}
	if !strings.Contains(err.Error(), "is empty") {
		t.Fatalf("expected empty subscription ID error, got %v", err)
	}
}
