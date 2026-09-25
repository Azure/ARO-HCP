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

package certificates_test

import (
	"io"
	"strings"
	"testing"
	"time"

	"github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/cmd/certificates"
	"github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/cmd/root"
)

func TestDefaultsAndRegistration(t *testing.T) {
	cmd, err := root.NewCommand()
	if err != nil {
		t.Fatal(err)
	}
	child, _, err := cmd.Find([]string{"ci-certificates"})
	if err != nil || child.Name() != "ci-certificates" {
		t.Fatalf("subcommand not registered: %v", err)
	}
	for flag, expected := range map[string]string{"dry-run": "true", "delete-active": "true", "purge-deleted": "true", "min-age": "168h0m0s", "max-deletions": "1000", "max-purges": "1000", "workers": "2", "timeout": "30m0s"} {
		if actual := child.Flags().Lookup(flag).DefValue; actual != expected {
			t.Errorf("--%s default = %s, want %s", flag, actual, expected)
		}
	}
	for _, flag := range []string{"vault", "subscription-id", "infrastructure-subscriptions", "policy", "workflow"} {
		if child.Flags().Lookup(flag) != nil {
			t.Errorf("unexpected scope override --%s", flag)
		}
	}
	if err := child.ParseFlags([]string{"--dry-run=false", "--min-age=24h", "--max-deletions=1", "--max-purges=1", "--workers=1", "--timeout=1h"}); err != nil {
		t.Fatal(err)
	}
	if dryRun, _ := child.Flags().GetBool("dry-run"); dryRun {
		t.Fatal("explicit apply flag did not disable dry-run")
	}
	if minAge, _ := child.Flags().GetDuration("min-age"); minAge != 24*time.Hour {
		t.Fatalf("min-age = %s", minAge)
	}

	inventory, _, err := child.Find([]string{"inventory"})
	if err != nil || inventory.Name() != "inventory" {
		t.Fatalf("inventory subcommand not registered: %v", err)
	}
	for flag, expected := range map[string]string{"include-pending": "true", "max-items": "0", "timeout": "2h0m0s"} {
		if actual := inventory.Flags().Lookup(flag).DefValue; actual != expected {
			t.Errorf("inventory --%s default = %s, want %s", flag, actual, expected)
		}
	}
	for _, flag := range []string{"dry-run", "delete-active", "purge-deleted", "min-age", "max-deletions", "max-purges", "workers", "vault"} {
		if inventory.Flags().Lookup(flag) != nil {
			t.Errorf("unexpected inventory mutation/scope flag --%s", flag)
		}
	}
}

func TestInvalidFlagsFailBeforeCredentials(t *testing.T) {
	t.Setenv("AZURE_TOKEN_CREDENTIALS", "")
	for _, test := range []struct {
		args    []string
		message string
	}{
		{[]string{"--min-age=23h"}, "--min-age must be at least 24h"},
		{[]string{"--max-deletions=0"}, "--max-deletions must be positive"},
		{[]string{"--max-deletions=-1"}, "--max-deletions must be positive"},
		{[]string{"--max-purges=0"}, "--max-purges must be positive"},
		{[]string{"--max-purges=-1"}, "--max-purges must be positive"},
		{[]string{"--workers=0"}, "--workers must be positive"},
		{[]string{"--workers=-1"}, "--workers must be positive"},
		{[]string{"--delete-active=false", "--purge-deleted=false"}, "at least one of --delete-active or --purge-deleted must be enabled"},
		{[]string{"--timeout=0s"}, "--timeout must be positive"},
		{[]string{"--timeout=-1s"}, "--timeout must be positive"},
		{[]string{"--vault=other.vault.azure.net"}, "unknown flag"},
		{[]string{"--subscription-id=one-subscription"}, "unknown flag"},
		{[]string{"unexpected"}, "unknown command"},
	} {
		t.Run(strings.Join(test.args, " "), func(t *testing.T) {
			cmd := certificates.NewCommand()
			cmd.SetOut(io.Discard)
			cmd.SetErr(io.Discard)
			cmd.SetArgs(test.args)
			err := cmd.ExecuteContext(t.Context())
			if err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("error = %v, want %q", err, test.message)
			}
		})
	}
}

func TestRootFlagIsolation(t *testing.T) {
	// Invalid age is a sentinel proving accepted flag placement reaches child
	// validation, without ever creating credentials or contacting Azure.
	t.Setenv("AZURE_TOKEN_CREDENTIALS", "")
	for _, test := range []struct {
		args    []string
		message string
	}{
		{[]string{"--subscription-id=OTHER", "ci-certificates", "--dry-run=false"}, "rejects parent command flags --subscription-id"},
		{[]string{"--policy=unused.yaml", "ci-certificates"}, "rejects parent command flags --policy"},
		{[]string{"--workflow=shared-leftovers", "ci-certificates"}, "rejects parent command flags --workflow"},
		{[]string{"--resource-group=only-this-rg", "ci-certificates"}, "rejects parent command flags --resource-group"},
		{[]string{"--parallelism=1", "ci-certificates"}, "rejects parent command flags --parallelism"},
		{[]string{"--wait=true", "ci-certificates"}, "rejects parent command flags --wait"},
		{[]string{"--dry-run=true", "ci-certificates", "--dry-run=false"}, "rejects parent command flags --dry-run"},
		{[]string{"--dry-run=false", "ci-certificates", "--dry-run=true"}, "rejects parent command flags --dry-run"},
		{[]string{"--dry-run=true", "ci-certificates"}, "rejects parent command flags --dry-run"},
		{[]string{"ci-certificates", "--subscription-id=OTHER", "--dry-run=false"}, "unknown flag: --subscription-id"},
		{[]string{"ci-certificates", "--policy=unused.yaml"}, "unknown flag: --policy"},
		{[]string{"ci-certificates", "--workflow=shared-leftovers"}, "unknown flag: --workflow"},
		{[]string{"ci-certificates", "--resource-group=only-this-rg"}, "unknown flag: --resource-group"},
		{[]string{"ci-certificates", "--parallelism=1"}, "unknown flag: --parallelism"},
		{[]string{"ci-certificates", "--wait=true"}, "unknown flag: --wait"},
		{[]string{"ci-certificates", "--dry-run=false"}, "--min-age must be at least 24h"},
		{[]string{"ci-certificates", "--dry-run=true"}, "--min-age must be at least 24h"},
		{[]string{"--verbosity=1", "ci-certificates"}, "--min-age must be at least 24h"},
		{[]string{"ci-certificates", "--verbosity=1"}, "--min-age must be at least 24h"},
	} {
		t.Run(strings.Join(test.args, " "), func(t *testing.T) {
			cmd, err := root.NewCommand()
			if err != nil {
				t.Fatal(err)
			}
			// Match production main's traversal and persistent logging flag.
			cmd.TraverseChildren = true
			cmd.PersistentFlags().IntP("verbosity", "v", 0, "set the verbosity level")
			cmd.SetOut(io.Discard)
			cmd.SetErr(io.Discard)
			cmd.SetArgs(append(test.args, "--min-age=23h"))
			err = cmd.ExecuteContext(t.Context())
			if err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("error = %v, want %q", err, test.message)
			}
			if strings.Contains(test.message, "rejects parent") && !strings.Contains(err.Error(), "scope is fixed to https://aro-hcp-dev-svc-kv.vault.azure.net and both DEV infrastructure guard subscriptions") {
				t.Fatalf("error does not explain the fixed scope: %v", err)
			}
		})
	}
}

func TestInventoryInvalidFlagsFailBeforeCredentials(t *testing.T) {
	t.Setenv("AZURE_TOKEN_CREDENTIALS", "")
	for _, test := range []struct {
		args    []string
		message string
	}{
		{[]string{"inventory", "--max-items=-1"}, "--max-items must not be negative"},
		{[]string{"inventory", "--timeout=0s"}, "--timeout must be positive"},
		{[]string{"inventory", "--timeout=-1s"}, "--timeout must be positive"},
		{[]string{"inventory", "--dry-run"}, "unknown flag"},
		{[]string{"--dry-run", "inventory"}, "unknown flag"},
	} {
		t.Run(strings.Join(test.args, " "), func(t *testing.T) {
			cmd := certificates.NewCommand()
			cmd.SetOut(io.Discard)
			cmd.SetErr(io.Discard)
			cmd.SetArgs(test.args)
			err := cmd.ExecuteContext(t.Context())
			if err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("error = %v, want %q", err, test.message)
			}
		})
	}
}
