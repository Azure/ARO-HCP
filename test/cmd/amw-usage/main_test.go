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

package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Azure/ARO-HCP/test/util/amwusage"
)

const testEvidence = `{"schemaVersion":1,"subscription":"test","run":{"job":"manual","start":1789964745,"end":1789973020,"durationMs":8275000,"platformStart":1789964100,"platformEnd":1789973640},"workspaces":[],"manifests":[],"records":{}}`

const testContext = `{"schemaVersion":1,"run":{"start":"2026-09-21T04:25:45Z","end":"2026-09-21T06:43:40Z","prow":"https://prow.example/run"},"baseline":{"start":"2026-09-21T03:30:00Z","end":"2026-09-21T04:00:00Z","reason":"Explicit comparator","sources":["https://example.com/evidence"]},"clusters":[],"limitations":["Not independently verified"]}`

func TestCLIValidationAndHelp(t *testing.T) {
	for _, args := range [][]string{{}, {"unknown"}, {"render"}, {"collect", "--start", "not-a-date"}, {"render", "extra"}} {
		if err := run(context.Background(), args, io.Discard, io.Discard); err == nil {
			t.Fatalf("invalid invocation accepted: %v", args)
		}
	}
	for _, command := range []string{"collect", "render", "scan", "refresh", "recover-namespaces", "enrich", "status", "compact", "repair"} {
		var stderr bytes.Buffer
		if err := run(context.Background(), []string{command, "--help"}, io.Discard, &stderr); err != nil || stderr.Len() == 0 {
			t.Fatalf("help failed for %s: %v", command, err)
		}
	}
}

func TestCLIRefreshOfflineAndValidation(t *testing.T) {
	t.Setenv("AZURE_TOKEN_CREDENTIALS", "deliberately-invalid")
	path := filepath.Join(t.TempDir(), "scan.db")
	input := filepath.Join(t.TempDir(), "fresh.json")
	for _, args := range [][]string{{"refresh"}, {"refresh", "--db", path}, {"refresh", "--db", path, "--input", input}} {
		if err := run(context.Background(), args, io.Discard, io.Discard); err == nil {
			t.Fatalf("invalid refresh accepted: %v", args)
		}
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("refresh created DB: %v", err)
		}
	}
	seed := `{"schemaVersion":1,"run":{"start":100000,"end":100600},"workspaces":[{"id":"/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/test/providers/Microsoft.Monitor/accounts/test","name":"test","endpoint":"https://test.westus3.prometheus.monitor.azure.com","names":[],"discovery":"names"}],"records":{"names":{"ok":true,"status":200,"request":{"kind":"discovery","url":"https://test.westus3.prometheus.monitor.azure.com/api/v1/label/__name__/values?start=1970-01-02T03:46:40Z&end=1970-01-02T03:56:40Z"},"body":"{\"status\":\"success\",\"data\":[]}"}}}`
	store, err := amwusage.OpenScanStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Initialize(context.Background(), strings.NewReader(seed)); err != nil {
		t.Fatal(err)
	}
	store.Close()
	if err := os.WriteFile(input, []byte(seed), 0600); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	if err := run(context.Background(), []string{"refresh", "--db", path, "--input", input, "--retry-empty"}, &stdout, io.Discard); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "offline") || !strings.Contains(stdout.String(), `"coverageComplete":false`) {
		t.Fatalf("missing empty semantics: %s", stdout.String())
	}
	content, err := os.ReadFile(input)
	if err != nil || string(content) != seed {
		t.Fatalf("seed changed: %v", err)
	}
	if err := run(context.Background(), []string{"refresh", "--db", path, "--input", path}, io.Discard, io.Discard); err == nil {
		t.Fatal("input alias accepted")
	}
}

func TestCLINamespaceRecoveryValidation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent.db")
	for _, args := range [][]string{
		{"recover-namespaces"},
		{"recover-namespaces", "--db", path},
		{"recover-namespaces", "--db", path, "--workers", "3"},
		{"recover-namespaces", "--db", path, "--workers", "0"},
	} {
		if err := run(context.Background(), args, io.Discard, io.Discard); err == nil {
			t.Fatalf("unsafe recovery accepted: %v", args)
		}
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("recovery created absent database: %v", err)
		}
	}
}

func TestCLIEnrichRequiresExistingDatabaseAndPlan(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent.db")
	for _, args := range [][]string{{"enrich"}, {"enrich", "--db", path}, {"enrich", "--db", path, "--workers", "5"}} {
		if err := run(context.Background(), args, io.Discard, io.Discard); err == nil {
			t.Fatalf("invalid enrich accepted: %v", args)
		}
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("enrich created absent database: %v", err)
		}
	}
	store, err := amwusage.OpenScanStore(path)
	if err != nil {
		t.Fatal(err)
	}
	store.Close()
	if err := run(context.Background(), []string{"enrich", "--db", path}, io.Discard, io.Discard); err == nil || !strings.Contains(err.Error(), "first enrichment requires --plan") {
		t.Fatalf("missing durable plan not rejected before credentials: %v", err)
	}
}

func TestCLIRepairRequiresExistingDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent.db")
	for _, args := range [][]string{
		{"repair"},
		{"repair", "--db", path, "--query", "parent"},
		{"repair", "--db", path},
	} {
		if err := run(context.Background(), args, io.Discard, io.Discard); err == nil {
			t.Fatalf("unsafe repair accepted: %v", args)
		}
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("repair created a database: %v", err)
		}
	}
}

func TestCLIStatusOffline(t *testing.T) {
	t.Setenv("AZURE_TOKEN_CREDENTIALS", "deliberately-invalid")
	path := filepath.Join(t.TempDir(), "scan.db")
	if err := run(context.Background(), []string{"status", "--db", path}, io.Discard, io.Discard); err == nil {
		t.Fatal("status created absent database")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("status touched absent database: %v", err)
	}
	store, err := amwusage.OpenScanStore(path)
	if err != nil {
		t.Fatal(err)
	}
	seed := `{"schemaVersion":1,"run":{"start":100000,"end":100600},"workspaces":[{"id":"/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/test/providers/Microsoft.Monitor/accounts/test","name":"test","endpoint":"https://test.westus3.prometheus.monitor.azure.com","names":["up"]}],"records":{}}`
	if err := store.Initialize(context.Background(), strings.NewReader(seed)); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := run(context.Background(), []string{"status", "--db", path}, &output, io.Discard); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), `"rankingTotal":3`) {
		t.Fatalf("status missing durable plan: %s", output.String())
	}
	// Header detection, not the extension, selects SQLite rendering.
	report := filepath.Join(filepath.Dir(path), "report.html")
	if err := run(context.Background(), []string{"render", "--input", path, "--output", report}, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(report)
	if err != nil || !strings.Contains(string(body), "<html") {
		t.Fatalf("database report: %v", err)
	}
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		if err := renderReport(path, path+suffix); err == nil {
			t.Fatalf("database/sidecar overwrite allowed: %s", suffix)
		}
	}
	if err := finishScanReport(context.Background(), path, "", nil, &output); err != nil {
		t.Fatalf("normal completion did not render: %v", err)
	}
	if _, err := os.Stat(scanReportPath(path)); err != nil {
		t.Fatalf("partial report absent: %v", err)
	}
	if err := finishScanReport(context.Background(), path, scanReportPath(path), nil, io.Discard); err == nil || !strings.Contains(err.Error(), "HTML generation failed") {
		t.Fatalf("protected seed lost: %v", err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	output.Reset()
	if err := run(canceled, []string{"scan", "--db", path, "--azure-cli"}, &output, io.Discard); !errors.Is(err, context.Canceled) {
		t.Fatalf("CLI scan lost cancellation: %v", err)
	}
	if !strings.Contains(output.String(), "Automatic HTML skipped") || !strings.Contains(output.String(), "amw-usage render --input") {
		t.Fatalf("canceled CLI scan did not offer offline rendering: %s", output.String())
	}
	alias := filepath.Join(filepath.Dir(path), "alias.json")
	if err := os.Symlink(path, alias); err != nil {
		t.Fatal(err)
	}
	if err := renderReport(alias, path+"-wal"); err == nil {
		t.Fatal("symlink database allowed real WAL overwrite")
	}
	if err := renderReport(alias, report); err != nil {
		t.Fatalf("SQLite header autodetection for .json alias: %v", err)
	}
}

func TestCanceledScanSkipsRender(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, scanErr := range []error{nil, context.Canceled, errors.New("scan failed")} {
		var out bytes.Buffer
		path := filepath.Join(t.TempDir(), "nonexistent.db")
		err := finishScanReport(ctx, path, "", scanErr, &out)
		if !errors.Is(err, context.Canceled) || (scanErr != nil && !errors.Is(err, scanErr)) {
			t.Fatalf("lost original error: %v", err)
		}
		if strings.Contains(err.Error(), "HTML generation failed") || !strings.Contains(out.String(), "amw-usage render") {
			t.Fatalf("cancellation attempted render: %v %s", err, out.String())
		}
		if _, err := os.Stat(scanReportPath(path)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("cancellation created HTML: %v", err)
		}
	}
}

func TestCanceledCollectSkipsRender(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	path := filepath.Join(t.TempDir(), "evidence.json")
	var out bytes.Buffer
	err := runWithCollector(ctx, collectArgs(path), &out, io.Discard, func(context.Context, amwusage.CollectOptions) (amwusage.CollectSummary, error) {
		cancel()
		return amwusage.CollectSummary{}, context.Canceled
	})
	if !errors.Is(err, context.Canceled) || strings.Contains(err.Error(), "HTML generation failed") || !strings.Contains(out.String(), "amw-usage render") {
		t.Fatalf("cancellation attempted render: %v %s", err, out.String())
	}
}

func TestCLICompactMissingDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent.db")
	if err := run(context.Background(), []string{"compact", "--db", path}, io.Discard, io.Discard); err == nil {
		t.Fatal("compact accepted absent database")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("compact created database: %v", err)
	}
}

func TestCLIRenderOffline(t *testing.T) {
	t.Setenv("AZURE_TOKEN_CREDENTIALS", "deliberately-invalid")
	dir := t.TempDir()
	input, output := filepath.Join(dir, "data.json"), filepath.Join(dir, "report.html")
	if err := os.WriteFile(input, []byte(testEvidence), 0600); err != nil {
		t.Fatal(err)
	}
	args := []string{"render", "--input", input, "--output", output}
	if err := run(context.Background(), args, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	report, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.ToLower(string(report)), "<html") {
		t.Fatal("render is not HTML")
	}
	if err := os.WriteFile(output, []byte("old report"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := run(context.Background(), args, io.Discard, io.Discard); err != nil {
		t.Fatalf("repeat render failed: %v", err)
	}
	replaced, err := os.ReadFile(output)
	if err != nil || !bytes.Equal(report, replaced) {
		t.Fatalf("repeat render did not replace report: %v", err)
	}
	evidence, err := os.ReadFile(input)
	if err != nil || string(evidence) != testEvidence {
		t.Fatalf("render changed evidence: %v", err)
	}
}

func collectArgs(output string) []string {
	return []string{"collect", "--azure-cli", "--workspace", "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/test/providers/Microsoft.Monitor/accounts/test", "--start", "2026-09-21T04:25:45Z", "--end", "2026-09-21T06:43:40Z", "--output", output}
}

func TestCollectGeneratesHTML(t *testing.T) {
	for _, outcome := range []string{"success", "partial", "invalid-evidence", "no-evidence"} {
		t.Run(outcome, func(t *testing.T) {
			dir := t.TempDir()
			output, report := filepath.Join(dir, "evidence.json"), filepath.Join(dir, "evidence.html")
			failure := errors.New("original collection failure")
			var stdout bytes.Buffer
			err := runWithCollector(context.Background(), collectArgs(output), &stdout, io.Discard, func(_ context.Context, o amwusage.CollectOptions) (amwusage.CollectSummary, error) {
				if o.Output != output || o.Credential == nil {
					t.Fatal("CLI did not configure collector")
				}
				data := testEvidence
				if outcome == "invalid-evidence" {
					data = "invalid JSON"
				}
				if outcome != "no-evidence" {
					if err := os.WriteFile(o.Output, []byte(data), 0600); err != nil {
						t.Fatal(err)
					}
				}
				if outcome != "success" {
					return amwusage.CollectSummary{Requests: 2, Failed: 1}, failure
				}
				return amwusage.CollectSummary{Requests: 2}, nil
			})
			if outcome == "success" && err != nil {
				t.Fatal(err)
			}
			if outcome != "success" && !errors.Is(err, failure) {
				t.Fatalf("original collection error lost: %v", err)
			}
			if !strings.Contains(stdout.String(), output) || !strings.Contains(stdout.String(), report) {
				t.Fatalf("output paths missing: %s", stdout.String())
			}
			if outcome == "success" || outcome == "partial" {
				html, err := os.ReadFile(report)
				if err != nil || !strings.Contains(string(html), "<html") {
					t.Fatalf("HTML not generated: %v", err)
				}
			} else {
				if !strings.Contains(err.Error(), "HTML generation failed") {
					t.Fatalf("render failure lost: %v", err)
				}
				if _, err := os.Stat(report); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("invalid evidence produced HTML: %v", err)
				}
			}
		})
	}
}

func TestCollectPreflightProtectsInputs(t *testing.T) {
	for _, kind := range []string{"existing-raw", "same-path", "dangling-raw-alias", "selection-path", "selection-symlink", "selection-hardlink", "selection-parent-symlink"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			output, report := filepath.Join(dir, "evidence.json"), filepath.Join(dir, "evidence.html")
			selection := ""
			switch kind {
			case "existing-raw":
				if err := os.WriteFile(output, []byte(testEvidence), 0600); err != nil {
					t.Fatal(err)
				}
			case "same-path":
				output = report
			case "dangling-raw-alias":
				if err := os.Symlink(output, report); err != nil {
					t.Fatal(err)
				}
			case "selection-path":
				selection = report
			default:
				selection = filepath.Join(dir, "selection.json")
			}
			if selection != "" {
				if err := os.WriteFile(selection, []byte(`{}`), 0600); err != nil {
					t.Fatal(err)
				}
				switch kind {
				case "selection-symlink":
					if err := os.Symlink(selection, report); err != nil {
						t.Fatal(err)
					}
				case "selection-hardlink":
					if err := os.Link(selection, report); err != nil {
						t.Fatal(err)
					}
				case "selection-parent-symlink":
					if err := os.Rename(selection, report); err != nil {
						t.Fatal(err)
					}
					alias := filepath.Join(t.TempDir(), "alias")
					if err := os.Symlink(dir, alias); err != nil {
						t.Fatal(err)
					}
					selection = filepath.Join(alias, "evidence.html")
				}
			}
			args := collectArgs(output)
			if selection != "" {
				args = append(args, "--selection", selection)
			}
			err := runWithCollector(context.Background(), args, io.Discard, io.Discard, func(context.Context, amwusage.CollectOptions) (amwusage.CollectSummary, error) {
				t.Fatal("collector invoked before unsafe output was rejected")
				return amwusage.CollectSummary{}, nil
			})
			if err == nil {
				t.Fatal("unsafe output accepted")
			}
			if selection != "" {
				data, err := os.ReadFile(selection)
				if err != nil || string(data) != `{}` {
					t.Fatalf("selection changed: %v", err)
				}
			}
			if kind == "existing-raw" {
				data, err := os.ReadFile(output)
				if err != nil || string(data) != testEvidence {
					t.Fatalf("raw evidence changed: %v", err)
				}
			}
		})
	}
}

func TestCLIContext(t *testing.T) {
	for _, mode := range []string{"valid", "invalid", "empty", "missing", "same-path", "symlink", "hardlink", "parent-symlink"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			output, report := filepath.Join(dir, "evidence.json"), filepath.Join(dir, "evidence.html")
			contextFile := filepath.Join(dir, "context.json")
			body := testContext
			if mode == "invalid" {
				body = `{"schemaVersion":2}`
			}
			if mode == "empty" {
				body = ""
			}
			if mode == "same-path" {
				contextFile = report
			}
			if mode != "missing" {
				if err := os.WriteFile(contextFile, []byte(body), 0600); err != nil {
					t.Fatal(err)
				}
			}
			switch mode {
			case "symlink", "hardlink":
				link := os.Link
				if mode == "symlink" {
					link = os.Symlink
				}
				if err := link(contextFile, report); err != nil {
					t.Fatal(err)
				}
			case "parent-symlink":
				if err := os.Rename(contextFile, report); err != nil {
					t.Fatal(err)
				}
				alias := filepath.Join(t.TempDir(), "alias")
				if err := os.Symlink(dir, alias); err != nil {
					t.Fatal(err)
				}
				contextFile = filepath.Join(alias, "evidence.html")
			}
			args := append(collectArgs(output), "--context", contextFile)
			called := false
			err := runWithCollector(context.Background(), args, io.Discard, io.Discard, func(_ context.Context, o amwusage.CollectOptions) (amwusage.CollectSummary, error) {
				called = true
				if mode != "valid" {
					t.Fatal("invalid context or alias reached collector")
				}
				if string(o.Context) != testContext {
					t.Fatal("context was not forwarded intact")
				}
				return amwusage.CollectSummary{}, os.WriteFile(o.Output, []byte(testEvidence), 0600)
			})
			if mode == "valid" {
				if err != nil || !called {
					t.Fatalf("valid context rejected: %v", err)
				}
			} else if err == nil || called {
				t.Fatalf("unsafe context accepted: %v", err)
			}
			if mode != "missing" {
				got, err := os.ReadFile(contextFile)
				if err != nil || string(got) != body {
					t.Fatalf("context changed: %v", err)
				}
			}
		})
	}
}

func TestRenderRejectsEvidenceAliases(t *testing.T) {
	for _, kind := range []string{"same-path", "clean-path", "symlink", "hardlink", "parent-symlink"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			input := filepath.Join(dir, "data.json")
			output := input
			original := []byte(testEvidence)
			if err := os.WriteFile(input, original, 0600); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "clean-path":
				output = dir + string(os.PathSeparator) + "." + string(os.PathSeparator) + "data.json"
			case "symlink", "hardlink":
				output = filepath.Join(dir, "alias.html")
				link := os.Link
				if kind == "symlink" {
					link = os.Symlink
				}
				if err := link(input, output); err != nil {
					t.Fatal(err)
				}
			case "parent-symlink":
				alias := filepath.Join(t.TempDir(), "alias")
				if err := os.Symlink(dir, alias); err != nil {
					t.Fatal(err)
				}
				output = filepath.Join(alias, "data.json")
			}
			err := run(context.Background(), []string{"render", "--input", input, "--output", output}, io.Discard, io.Discard)
			if err == nil || !strings.Contains(err.Error(), "input evidence") {
				t.Fatalf("alias not rejected: %v", err)
			}
			got, err := os.ReadFile(input)
			if err != nil || !bytes.Equal(got, original) {
				t.Fatalf("evidence changed: %q %v", got, err)
			}
		})
	}
}

func TestReportWriteFailureCleanup(t *testing.T) {
	for _, failure := range []string{"write", "rename"} {
		t.Run(failure, func(t *testing.T) {
			dir := t.TempDir()
			input, output := filepath.Join(dir, "data.json"), filepath.Join(dir, "report.html")
			if err := os.WriteFile(input, []byte("evidence"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(output, []byte("previous report"), 0600); err != nil {
				t.Fatal(err)
			}
			writeFailure := errors.New("injected write failure")
			err := replaceReport(input, output, func(w io.Writer) error {
				if _, err := io.WriteString(w, "partial report"); err != nil {
					return err
				}
				if failure == "write" {
					return writeFailure
				}
				// Force publication failure after writing, independently of permissions.
				if err := os.Remove(output); err != nil {
					return err
				}
				return os.Mkdir(output, 0700)
			})
			if err == nil {
				t.Fatal("expected replacement failure")
			}
			if failure == "write" {
				if !errors.Is(err, writeFailure) {
					t.Fatalf("write error lost: %v", err)
				}
				old, err := os.ReadFile(output)
				if err != nil || string(old) != "previous report" {
					t.Fatalf("failed write changed old report: %q %v", old, err)
				}
			} else if info, err := os.Stat(output); err != nil || !info.IsDir() {
				t.Fatalf("failed publication changed target: %v", err)
			}
			files, err := filepath.Glob(filepath.Join(dir, ".amw-report-*"))
			if err != nil || len(files) != 0 {
				t.Fatalf("temporary files leaked: %v %v", files, err)
			}
		})
	}
}
