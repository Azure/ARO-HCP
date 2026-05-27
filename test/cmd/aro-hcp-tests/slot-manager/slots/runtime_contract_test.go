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

func TestVerifyCustomerSubscriptionName(t *testing.T) {
	t.Parallel()

	clusterProfileDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(clusterProfileDir, "customer-dev-subscription-name"), []byte("customer-dev\n"), 0o644); err != nil {
		t.Fatalf("expected write to succeed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(clusterProfileDir, "customer-other-subscription-name"), []byte("customer-other\n"), 0o644); err != nil {
		t.Fatalf("expected write to succeed: %v", err)
	}

	resolved, err := VerifyCustomerSubscriptionName(clusterProfileDir, "customer-dev")
	if err != nil {
		t.Fatalf("expected subscription verification to succeed: %v", err)
	}
	if resolved != "customer-dev" {
		t.Fatalf("expected verified subscription %q, got %q", "customer-dev", resolved)
	}
}

func TestVerifyCustomerSubscriptionNameRejectsDuplicateMatches(t *testing.T) {
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

	_, err := VerifyCustomerSubscriptionName(clusterProfileDir, "customer-dev")
	if err == nil {
		t.Fatal("expected duplicate match verification to fail")
	}
	if !strings.Contains(err.Error(), "multiple customer subscription name files matched") {
		t.Fatalf("expected duplicate match error, got %v", err)
	}
}
