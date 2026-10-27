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

package root

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-logr/logr"

	"github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/cmd/workflow"
)

func TestRunPropagatesAttribution(t *testing.T) {
	t.Setenv("JOB_NAME", "test-prow-job")
	t.Setenv("BUILD_ID", "12345")
	t.Setenv("PROW_JOB_ID", "prow-uuid")
	var runIDs []string
	for range 2 {
		registry, err := newRegistry()
		if err != nil {
			t.Fatal(err)
		}
		if err := registry.ApplyOptions("shared-leftovers", []string{"shared-leftovers.subscription-id=test-subscription"}); err != nil {
			t.Fatal(err)
		}
		selected, err := registry.Get("shared-leftovers")
		if err != nil {
			t.Fatal(err)
		}

		var logs bytes.Buffer
		ctx := logr.NewContext(context.Background(), logr.FromSlogHandler(slog.NewJSONHandler(&logs, nil)))
		opts := Options{completedOptions: &completedOptions{
			Workflow: selected, Common: workflow.Common{DryRun: true, Parallelism: 1},
		}}
		if err := opts.Run(ctx); err == nil {
			t.Fatal("expected missing credential error")
		}
		decoder := json.NewDecoder(&logs)
		var runID string
		entries := 0
		for decoder.More() {
			var entry map[string]any
			if err := decoder.Decode(&entry); err != nil {
				t.Fatal(err)
			}
			id, ok := entry["runID"].(string)
			if !ok || id == "" {
				t.Fatalf("missing run ID: %v", entry)
			}
			if runID == "" {
				runID = id
			}
			if id != runID || entry["prowJob"] != "test-prow-job" || entry["prowBuildID"] != "12345" ||
				entry["prowJobID"] != "prow-uuid" || entry["subscriptionID"] != "test-subscription" ||
				entry["dryRun"] != true || entry["commitSHA"] == "" {
				t.Fatalf("missing propagated attribution: %v", entry)
			}
			entries++
		}
		if entries != 2 {
			t.Fatalf("expected root and workflow log entries, got %d", entries)
		}
		runIDs = append(runIDs, runID)
	}
	if runIDs[0] == runIDs[1] {
		t.Fatal("independent runs reused an ID")
	}
}

const testPolicyYAML = `
rgOrdered:
  discovery:
    rules:
      - action: delete
        match:
          any: true
        olderThan: "1h"
`

func writePolicyFile(t *testing.T) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "policy.yaml")
	if err := os.WriteFile(path, []byte(testPolicyYAML), 0o600); err != nil {
		t.Fatalf("failed to write policy file: %v", err)
	}
	return path
}

func TestRawOptionsValidate_DeprecatedAliasesTargetTheSelectedWorkflow(t *testing.T) {
	t.Parallel()

	policyPath := writePolicyFile(t)
	testCases := []struct {
		name        string
		opts        RawOptions
		errContains string
	}{
		{
			name: "rg-ordered ignores whitespace-only resource group and relies on policy discovery",
			opts: RawOptions{
				SubscriptionID: "sub-id", Workflow: "rg-ordered", PolicyFile: policyPath, DryRun: true,
				Parallelism: 1, ResourceGroups: []string{"   "},
			},
		},
		{
			name: "rg-ordered allows trimmed non-empty resource group",
			opts: RawOptions{
				SubscriptionID: "sub-id", Workflow: "rg-ordered", PolicyFile: policyPath, DryRun: true,
				Parallelism: 1, ResourceGroups: []string{"   rg-one   "},
			},
		},
		{
			name: "shared-leftovers ignores whitespace selectors",
			opts: RawOptions{
				SubscriptionID: "sub-id", Workflow: "shared-leftovers", DryRun: true,
				Parallelism: 1, ResourceGroups: []string{"   "},
			},
		},
		{
			name: "shared-leftovers rejects explicit rg selectors",
			opts: RawOptions{
				SubscriptionID: "sub-id", Workflow: "shared-leftovers", DryRun: true,
				Parallelism: 1, ResourceGroups: []string{"rg-one"},
			},
			errContains: `workflow "shared-leftovers" has no option "resource-group"`,
		},
		{
			name: "subscription is required",
			opts: RawOptions{
				Workflow: "shared-leftovers", DryRun: true, Parallelism: 1,
			},
			errContains: "shared-leftovers.subscription-id is required",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := tc.opts.Validate(context.Background())
			if tc.errContains == "" {
				if err != nil {
					t.Fatalf("expected validation success, got %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.errContains) {
				t.Fatalf("error = %v, want %q", err, tc.errContains)
			}
		})
	}
}

func TestRawOptionsValidate_ExplicitOptionsOverrideDeprecatedAliases(t *testing.T) {
	t.Parallel()

	registry, err := newRegistry()
	if err != nil {
		t.Fatal(err)
	}
	opts := RawOptions{
		registry: registry, Workflow: "shared-leftovers", Parallelism: 1,
		SubscriptionID: "from-alias",
		Options:        []string{"shared-leftovers.subscription-id=from-options"},
	}
	validated, err := opts.Validate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := validated.workflow.LogValues(); len(got) != 2 || got[1] != "from-options" {
		t.Fatalf("log values = %v, want the explicit option to win", got)
	}
}

const certificatePolicyHeader = "certificates:\n  vaultURL: https://example.vault.azure.net\n  ownerInventory:\n    subscriptions: [Example Subscription]\n"

func writeCertificatePolicy(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "certificates.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestWorkflowOptionsThroughTheCommand drives the real command. Without
// credentials configured, a run that passes validation fails while creating the
// credential, which proves the arguments were accepted without ever contacting
// Azure.
func TestWorkflowOptionsThroughTheCommand(t *testing.T) {
	t.Setenv("AZURE_TOKEN_CREDENTIALS", "")
	const accepted = "failed to create Azure credential"

	certificatePolicy := "../../certificates.policy.yaml"
	unguarded := writeCertificatePolicy(t, certificatePolicyHeader+"  discovery:\n    protectedTags: {persist: \"true\"}\n    rules:\n    - {name: r, action: delete, match: {nameRegex: \"^x$\"}, olderThan: 168h}\n")
	misspelled := writeCertificatePolicy(t, certificatePolicyHeader+"  discovery:\n    protectedTags: {persist: \"true\"}\n    rules:\n    - {name: r, action: skip, match: {nameRegex: \"^x$\"}, olderThen: 1h}\n")
	rgPolicy := writePolicyFile(t)

	for _, test := range []struct {
		name    string
		args    []string
		message string
	}{
		// rg-ordered and shared-leftovers.
		{"rg-ordered options", []string{"--policy=" + rgPolicy, "--options=rg-ordered.subscription-id=x", "--options=rg-ordered.resource-group=a", "--options=rg-ordered.wait=false"}, accepted},
		{"rg-ordered deprecated aliases", []string{"--policy=" + rgPolicy, "--subscription-id=x", "--resource-group=a", "--wait=false"}, accepted},
		{"rg-ordered requires a subscription", []string{"--policy=" + rgPolicy}, "rg-ordered.subscription-id is required"},
		{"rg-ordered requires a policy", []string{"--options=rg-ordered.subscription-id=x"}, "--policy is required for rg-ordered workflow"},
		{"shared-leftovers options", []string{"--workflow=shared-leftovers", "--options=shared-leftovers.subscription-id=x", "--options=shared-leftovers.wait=false"}, accepted},
		{"shared-leftovers rejects rg options", []string{"--workflow=shared-leftovers", "--options=shared-leftovers.subscription-id=x", "--options=shared-leftovers.resource-group=a"}, `no option "resource-group"`},

		// certificates.
		{"certificates", []string{"--workflow=certificates", "--dry-run=true", "--policy=" + certificatePolicy}, accepted},
		{"certificates without --dry-run", []string{"--workflow=certificates", "--policy=" + certificatePolicy}, accepted},
		{"certificates apply with options", []string{"--workflow=certificates", "--dry-run=false", "--policy=" + certificatePolicy, "--options=certificates.max-deletions=5", "--options=certificates.max-purges=5", "--parallelism=2", "--timeout=10m"}, accepted},
		{"certificates purge only", []string{"--workflow=certificates", "--policy=" + certificatePolicy, "--options=certificates.delete-active=false"}, accepted},
		{"certificates policy required", []string{"--workflow=certificates"}, "--policy is required for certificates workflow"},
		{"certificates policy missing file", []string{"--workflow=certificates", "--policy=/does/not/exist"}, "failed to read policy file"},
		{"certificates policy unknown key", []string{"--workflow=certificates", "--policy=" + misspelled}, "unknown field"},
		{"certificates policy unguarded delete rule", []string{"--workflow=certificates", "--policy=" + unguarded}, "delete rules must set conditions.ownerResourceGroupAbsent"},
		{"rg policy is not a certificate policy", []string{"--workflow=certificates", "--policy=" + rgPolicy}, "unknown field"},
		{"certificates rejects deprecated subscription alias", []string{"--workflow=certificates", "--policy=" + certificatePolicy, "--subscription-id=x"}, `workflow "certificates" has no option "subscription-id"`},
		{"certificates rejects deprecated resource-group alias", []string{"--workflow=certificates", "--policy=" + certificatePolicy, "--resource-group=rg"}, `no option "resource-group"`},
		{"certificates rejects deprecated wait alias", []string{"--workflow=certificates", "--policy=" + certificatePolicy, "--wait=false"}, `no option "wait"`},
		{"certificates no actions", []string{"--workflow=certificates", "--options=certificates.delete-active=false", "--options=certificates.purge-deleted=false"}, "at least one of certificates.delete-active or certificates.purge-deleted must be enabled"},
		{"certificates zero deletions", []string{"--workflow=certificates", "--options=certificates.max-deletions=0"}, "certificates.max-deletions must be positive"},
		{"certificates negative purges", []string{"--workflow=certificates", "--options=certificates.max-purges=-1"}, "certificates.max-purges must be positive"},
		{"certificates invalid value", []string{"--workflow=certificates", "--options=certificates.max-purges=many"}, "certificates.max-purges:"},

		// Common options and --options handling.
		{"unknown workflow", []string{"--workflow=nope"}, `--workflow: unknown workflow "nope" (available: rg-ordered, shared-leftovers, certificates)`},
		{"zero parallelism", []string{"--workflow=certificates", "--parallelism=0"}, "--parallelism must be >= 1"},
		{"negative timeout", []string{"--workflow=certificates", "--timeout=-1s"}, "--timeout must not be negative"},
		{"malformed option", []string{"--workflow=certificates", "--options=max-deletions=5"}, "must have the form <workflow>.<key>=<value>"},
		{"unknown option key lists valid keys", []string{"--workflow=certificates", "--options=certificates.nope=1"}, "(valid: delete-active, max-deletions, max-purges, purge-deleted)"},
		{"option of another workflow", []string{"--workflow=certificates", "--options=rg-ordered.wait=true"}, `options for workflow "rg-ordered" cannot be used with --workflow=certificates`},
		{"unknown option workflow", []string{"--workflow=certificates", "--options=nope.key=1"}, `unknown workflow "nope"`},
		{"removed subcommand", []string{"ci-certificates"}, "unknown command"},
		{"removed min-age flag", []string{"--workflow=certificates", "--min-age=24h"}, "unknown flag"},
		{"certificate settings are no longer flags", []string{"--workflow=certificates", "--max-deletions=5"}, "unknown flag"},
	} {
		t.Run(test.name, func(t *testing.T) {
			cmd, err := NewCommand()
			if err != nil {
				t.Fatal(err)
			}
			cmd.SetOut(io.Discard)
			cmd.SetErr(io.Discard)
			cmd.SetArgs(test.args)
			err = cmd.ExecuteContext(logr.NewContext(t.Context(), logr.Discard()))
			if err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("error = %v, want %q", err, test.message)
			}
		})
	}
}

func TestHelpDocumentsWorkflowOptionsAndHidesDeprecatedAliases(t *testing.T) {
	cmd, err := NewCommand()
	if err != nil {
		t.Fatal(err)
	}
	var help bytes.Buffer
	cmd.SetOut(&help)
	cmd.SetErr(&help)
	cmd.SetArgs([]string{"--help"})
	if err := cmd.ExecuteContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"--options", "rg-ordered.subscription-id=<string>", "certificates.max-deletions=<int> (default 1000)", "shared-leftovers.wait=<bool>"} {
		if !strings.Contains(help.String(), want) {
			t.Errorf("help missing %q:\n%s", want, help.String())
		}
	}
	for _, hidden := range []string{"--subscription-id", "--resource-group", "--wait "} {
		if strings.Contains(help.String(), hidden) {
			t.Errorf("deprecated flag %q must be hidden from help:\n%s", hidden, help.String())
		}
	}
}
