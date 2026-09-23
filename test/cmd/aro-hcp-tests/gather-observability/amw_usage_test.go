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

package gatherobservability

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr"

	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"

	"github.com/Azure/ARO-HCP/test/util/amwusage"
	"github.com/Azure/ARO-HCP/test/util/timing"
)

func TestGatherTimeoutEnvironment(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  time.Duration
	}{
		{"", 4 * time.Minute}, {"65m", 65 * time.Minute},
		{"bad", 0}, {"0", 0}, {"-1m", 0}, {"1m", 0},
	} {
		t.Run(tc.value, func(t *testing.T) {
			t.Setenv("GATHER_OBSERVABILITY_TIMEOUT", tc.value)
			o := &RawOptions{OutputDir: "output", RenderedConfig: "config", SubscriptionID: "sub"}
			validated, err := o.Validate()
			if tc.want == 0 {
				if err == nil || !strings.Contains(err.Error(), "GATHER_OBSERVABILITY_TIMEOUT") {
					t.Fatalf("invalid timeout not rejected: %v", err)
				}
			} else if err != nil || validated.timeout != tc.want {
				t.Fatalf("timeout %q: options=%+v, error=%v", tc.value, validated, err)
			}
		})
	}
}

func TestGatherAMWConcurrentAndNonGating(t *testing.T) {
	t.Parallel()
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "diagnostic only", true: "preserve independent error"}[fail], func(t *testing.T) {
			o := Options{completedOptions: &completedOptions{OutputDir: t.TempDir(), timeout: gatherReportReserve + 100*time.Millisecond}}
			injected := errors.New("independent setup failure")
			if fail {
				o.queriesError = injected
			}
			deps := o.dependencies()
			started := make(chan struct{})
			deps.collectAMWUsage = func(ctx, reportCtx context.Context) (observabilityTab, error) {
				data, err := os.ReadFile(filepath.Join(o.OutputDir, "observability-summary.html"))
				if err != nil || !strings.Contains(string(data), "collection in progress") {
					t.Error("initial report was not published before AMW started")
				}
				acquisitionDeadline, _ := ctx.Deadline()
				reportDeadline, _ := reportCtx.Deadline()
				if reportDeadline.Sub(acquisitionDeadline) != gatherReportReserve {
					t.Error("report reserve was not subtracted from shared deadline")
				}
				close(started)
				<-ctx.Done()
				if reportCtx.Err() != nil {
					t.Error("acquisition exhausted reporting budget")
				}
				return amwUsageStatus(ctx.Err()), ctx.Err()
			}
			deps.collectUtilization = func(ctx context.Context, _ map[string]*workspaceData) utilizationReport {
				select {
				case <-started:
				case <-time.After(time.Second):
					t.Error("AMW was blocked behind standard collection")
				}
				<-ctx.Done()
				return utilizationReport{SchemaVersion: utilizationSchemaVersion, GeneratedAt: time.Now(), Start: time.Unix(1000, 0), End: time.Unix(2000, 0), Warnings: []string{ctx.Err().Error()}}
			}
			err := o.run(logr.NewContext(t.Context(), logr.Discard()), deps)
			if fail && !errors.Is(err, injected) || !fail && err != nil {
				t.Fatalf("AMW changed independent error semantics: %v", err)
			}
			for _, name := range []string{"observability-summary.html", "junit_alerts.xml", "utilization.json"} {
				data, err := os.ReadFile(filepath.Join(o.OutputDir, name))
				if err != nil {
					t.Fatalf("artifact missing after collection deadline: %s: %v", name, err)
				}
				if name == "observability-summary.html" && (!strings.Contains(string(data), "AMW Metric Usage") || !strings.Contains(string(data), "context deadline exceeded")) {
					t.Error("final report omitted AMW partial diagnostic")
				}
			}
		})
	}
}

type amwCancelCredential struct{ cancel context.CancelFunc }

func (c amwCancelCredential) GetToken(ctx context.Context, _ policy.TokenRequestOptions) (azcore.AccessToken, error) {
	c.cancel()
	return azcore.AccessToken{}, ctx.Err()
}

func TestAMWArtifactsAndWindow(t *testing.T) {
	t.Parallel()
	for _, empty := range []bool{false, true} {
		t.Run(map[bool]string{false: "canceled scan", true: "empty catalog"}[empty], func(t *testing.T) {
			dir := t.TempDir()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			now := time.Now()
			o := Options{completedOptions: &completedOptions{
				OutputDir: dir, cred: &amwCancelCredential{cancel: cancel},
				TimeWindow: timing.TimeWindow{Start: now.Add(-5 * time.Hour), End: now.Add(45 * time.Minute)},
				Workspaces: map[string]azcorearm.ResourceID{workspaceSvc: *mustParseResourceID("00000000-0000-0000-0000-000000000001", "region", "svc")},
			}}
			collect := func(_ context.Context, opts amwusage.CollectOptions) (amwusage.CollectSummary, error) {
				if opts.End.After(time.Now()) || opts.Start.Nanosecond() != 0 || opts.End.Nanosecond() != 0 || opts.End.Sub(opts.Start) < 4*time.Hour {
					t.Errorf("AMW window was truncated, padded, or left in the future: %s to %s", opts.Start, opts.End)
				}
				if opts.Credential != o.cred || len(opts.Workspaces) != 1 {
					t.Error("existing credential/workspace was not passed to collection")
				}
				names := []string{"up"}
				if empty {
					names = []string{}
				}
				body, _ := json.Marshal(map[string]any{"status": "success", "data": names})
				params := url.Values{"start": {opts.Start.Format(time.RFC3339)}, "end": {opts.End.Format(time.RFC3339)}}
				seed := map[string]any{
					"schemaVersion": 1,
					"run":           map[string]any{"start": opts.Start.Unix(), "end": opts.End.Unix()},
					"workspaces":    []any{map[string]any{"id": opts.Workspaces[0], "name": "svc", "endpoint": "https://svc.westus3.prometheus.monitor.azure.com", "names": names, "discovery": "discovery"}},
					"records":       map[string]any{"discovery": map[string]any{"ok": true, "body": string(body), "request": map[string]string{"url": "https://svc.westus3.prometheus.monitor.azure.com/api/v1/label/__name__/values?" + params.Encode()}}},
				}
				data, err := json.Marshal(seed)
				if err != nil {
					return amwusage.CollectSummary{}, err
				}
				return amwusage.CollectSummary{}, os.WriteFile(opts.Output, data, 0600)
			}
			tab, err := o.runAMWUsage(ctx, t.Context(), collect)
			if err == nil || !strings.Contains(err.Error(), "AMW coverage incomplete") {
				t.Fatalf("incomplete coverage not reported: %v", err)
			}
			if !empty && !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation was lost: %v", err)
			}
			for _, name := range []string{amwUsageReport, "amw-usage-seed.json", "amw-usage.db"} {
				if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
					t.Fatalf("missing artifact %s: %v", name, err)
				}
			}
			data, err := os.ReadFile(filepath.Join(dir, amwUsageReport))
			if err != nil || string(data) != tab.HTML || !strings.Contains(strings.ToLower(tab.HTML), "<!doctype html>") || strings.Contains(tab.HTML, "collection in progress") {
				t.Fatalf("standalone report and embedded tab differ or partial database was not rendered: %v", err)
			}
			summary, err := amwusage.ReadScanSummary(t.Context(), filepath.Join(dir, "amw-usage.db"))
			if err != nil || summary.CoverageComplete || summary.Queries["running"] != 0 {
				t.Fatalf("leases not released or false complete coverage: %+v, %v", summary, err)
			}
			canceled, stop := context.WithCancel(t.Context())
			stop()
			if err := amwusage.RenderDatabaseContext(canceled, io.Discard, filepath.Join(dir, "amw-usage.db")); !errors.Is(err, context.Canceled) {
				t.Fatalf("renderer ignored canceled report budget: %v", err)
			}
		})
	}
}

func TestAMWInvalidWindowWritesDiagnostic(t *testing.T) {
	t.Parallel()
	o := Options{completedOptions: &completedOptions{OutputDir: t.TempDir(), TimeWindow: timing.TimeWindow{Start: time.Now(), End: time.Now().Add(time.Minute)}}}
	tab, err := o.runAMWUsage(t.Context(), t.Context(), func(context.Context, amwusage.CollectOptions) (amwusage.CollectSummary, error) {
		t.Fatal("invalid collection should not make cloud requests")
		return amwusage.CollectSummary{}, nil
	})
	if err == nil || !strings.Contains(tab.HTML, "AMW collection window") {
		t.Fatalf("invalid window did not produce an explicit diagnostic: %v", err)
	}
	if _, err := os.Stat(filepath.Join(o.OutputDir, amwUsageReport)); err != nil {
		t.Fatalf("invalid window lost standalone diagnostic: %v", err)
	}
}

func TestGatherDeadlineDoesNotPassUnknownAlerts(t *testing.T) {
	t.Parallel()
	o := Options{completedOptions: &completedOptions{
		OutputDir: t.TempDir(), timeout: gatherReportReserve + 20*time.Millisecond,
		resourceGroups: sets.New("/subscriptions/sub/resourceGroups/region"),
		TimeWindow:     timing.TimeWindow{Start: time.Unix(1000, 0), End: time.Unix(2000, 0)},
	}}
	deps := o.dependencies()
	deps.collectAMWUsage = nil
	deps.fetchAlerts = func(ctx context.Context, _ azcore.TokenCredential, _ string, _, _ time.Time) ([]alert, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	deps.fetchMetricAlertRules = func(ctx context.Context, _ azcore.TokenCredential, _, _ string) ([]string, error) {
		return []string{"quiet"}, ctx.Err()
	}
	err := o.run(logr.NewContext(t.Context(), logr.Discard()), deps)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("alert collection deadline was not fatal: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(o.OutputDir, "junit_alerts.xml"))
	if err != nil || !strings.Contains(string(data), "context deadline exceeded") || !strings.Contains(string(data), "<failure") {
		t.Fatalf("JUnit lost incomplete alert failure: %s, %v", data, err)
	}
	data, err = os.ReadFile(filepath.Join(o.OutputDir, "observability-summary.html"))
	if err != nil || !strings.Contains(string(data), "Alert collection incomplete") || strings.Contains(string(data), "No alerts fired during the test window.") {
		t.Fatalf("canceled alerts rendered as a healthy zero: %v", err)
	}
}

func TestGatherExpiredReportPreservesCheckpointAndJoinsAMW(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(logr.NewContext(t.Context(), logr.Discard()))
	defer cancel()
	o := Options{completedOptions: &completedOptions{OutputDir: t.TempDir(), TimeWindow: timing.TimeWindow{Start: time.Unix(1000, 0), End: time.Unix(2000, 0)}}}
	deps := o.dependencies()
	checkpoint := make(chan struct{})
	joined := make(chan struct{})
	deps.collectAMWUsage = func(context.Context, context.Context) (observabilityTab, error) {
		defer close(joined)
		<-checkpoint
		cancel()
		return amwUsageStatus(context.Canceled), context.Canceled
	}
	pages := 0
	deps.renderPage = func(path string, tabs []observabilityTab) error {
		pages++
		if pages > 2 {
			t.Error("started expensive final rendering after report deadline")
		}
		err := renderObservabilityPage(path, tabs)
		if pages == 2 {
			close(checkpoint)
		}
		return err
	}
	if err := o.run(ctx, deps); err != nil {
		t.Fatalf("AMW cancellation became a new gate: %v", err)
	}
	select {
	case <-joined:
	default:
		t.Fatal("gather returned with an unjoined AMW writer")
	}
	data, err := os.ReadFile(filepath.Join(o.OutputDir, "observability-summary.html"))
	if err != nil || pages != 2 || !strings.Contains(string(data), `"title":"Utilization"`) {
		t.Fatalf("completed standard checkpoint was lost: pages=%d, %v", pages, err)
	}
}
