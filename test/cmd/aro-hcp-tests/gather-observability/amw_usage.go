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
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/go-logr/logr"

	"github.com/Azure/ARO-HCP/test/util/amwusage"
)

const amwUsageReport = "amw-usage-summary.html"

func amwUsageStatus(err error) observabilityTab {
	return observabilityTab{Title: "AMW Metric Usage", HTML: string(incompleteHTML([]byte(`<h1>AMW Metric Usage</h1><p>Best-effort diagnostic, not an e2e gate. Missing or unfinished queries are unknown, not zero.</p><p>Artifacts: <a href="amw-usage-summary.html">standalone report</a>, <a href="amw-usage.db">scan database</a>, <a href="amw-usage-seed.json">discovery evidence</a>.</p>`), err))}
}

func (o Options) collectAMWUsage(ctx, reportCtx context.Context) (observabilityTab, error) {
	return o.runAMWUsage(ctx, reportCtx, amwusage.Collect)
}

func (o Options) runAMWUsage(ctx, reportCtx context.Context, collect func(context.Context, amwusage.CollectOptions) (amwusage.CollectSummary, error)) (tab observabilityTab, resultErr error) {
	reportPath := filepath.Join(o.OutputDir, amwUsageReport)
	tab = amwUsageStatus(errors.New("collection in progress; metric usage is unknown"))
	if err := writeObservabilityHTML(reportPath, []byte(tab.HTML)); err != nil {
		return amwUsageStatus(err), err
	}
	// Preserve a useful standalone status even when discovery or rendering fails.
	defer func() {
		if reportCtx.Err() != nil {
			tab = amwUsageStatus(errors.New("report budget exhausted; database and seed retained for offline rendering"))
			resultErr = errors.Join(resultErr, reportCtx.Err())
		}
		if strings.Contains(tab.HTML, "collection in progress; metric usage is unknown") {
			tab = amwUsageStatus(errors.New("analysis unavailable; metric usage is unknown"))
		}
		if resultErr != nil {
			warning := string(incompleteHTML(nil, resultErr))
			if strings.Contains(tab.HTML, "<body>") {
				tab.HTML = strings.Replace(tab.HTML, "<body>", "<body>"+warning, 1)
			} else {
				tab.HTML = warning + tab.HTML
			}
		}
		resultErr = errors.Join(resultErr, writeObservabilityHTML(reportPath, []byte(tab.HTML)))
	}()
	start, end := o.TimeWindow.Start.UTC().Truncate(time.Second), o.TimeWindow.End.UTC().Truncate(time.Second)
	now := time.Now().UTC().Truncate(time.Second)
	if end.After(now) {
		end = now
	}
	seedPath := filepath.Join(o.OutputDir, "amw-usage-seed.json")
	options := amwusage.CollectOptions{Start: start, End: end, Output: seedPath, Credential: o.cred, RunName: "gather-observability"}
	for _, key := range slices.Sorted(maps.Keys(o.Workspaces)) {
		workspace := o.Workspaces[key]
		options.Workspaces = append(options.Workspaces, workspace.String())
	}
	if err := options.Validate(); err != nil {
		return tab, fmt.Errorf("AMW collection window %s to %s: %w", start.Format(time.RFC3339), end.Format(time.RFC3339), err)
	}
	logger := logr.FromContextOrDiscard(ctx)
	logger.Info("collecting AMW usage", "start", start, "end", end, "workers", 2)
	summary, collectErr := collect(ctx, options)
	logger.Info("collected AMW discovery seed", "requests", summary.Requests, "failed", summary.Failed)
	// Collect checkpoints after every response. Even a failed seed can contain
	// useful workspace/platform evidence; never replace it with synthetic zeros.
	seed, err := os.Open(seedPath)
	if err != nil {
		return tab, errors.Join(collectErr, err)
	}
	database := filepath.Join(o.OutputDir, "amw-usage.db")
	store, err := amwusage.OpenScanStore(database)
	if err != nil {
		return tab, errors.Join(collectErr, err, seed.Close())
	}
	initializeErr := errors.Join(store.Initialize(ctx, seed), seed.Close())
	var scanErr error
	if initializeErr == nil {
		scanErr = store.Scan(ctx, amwusage.ScanOptions{Workers: 2, Credential: o.cred})
	}
	// Scan joins its workers and releases leases before Close checkpoints WAL.
	closeErr := store.Close()
	resultErr = errors.Join(collectErr, initializeErr, scanErr, closeErr)
	if initializeErr == nil {
		scanSummary, err := amwusage.ReadScanSummary(reportCtx, database)
		resultErr = errors.Join(resultErr, err)
		if err == nil {
			logger.Info("AMW scan coverage", "metrics", scanSummary.Metrics, "succeeded", scanSummary.RankingSucceeded, "total", scanSummary.RankingTotal, "complete", scanSummary.CoverageComplete)
			if !scanSummary.CoverageComplete {
				resultErr = errors.Join(resultErr, fmt.Errorf("AMW coverage incomplete: %d/%d ranking queries succeeded; %d empty catalogs", scanSummary.RankingSucceeded, scanSummary.RankingTotal, scanSummary.EmptyCatalogs))
			}
		}
	}
	file, err := os.CreateTemp(o.OutputDir, ".amw-report-*")
	if err != nil {
		return tab, errors.Join(resultErr, err)
	}
	defer os.Remove(file.Name())
	renderErr := amwusage.RenderDatabaseContext(reportCtx, file, database)
	if err := errors.Join(renderErr, file.Close()); err != nil {
		return tab, errors.Join(resultErr, fmt.Errorf("render AMW report: %w", err))
	}
	if err := reportCtx.Err(); err != nil {
		return tab, errors.Join(resultErr, err)
	}
	rendered, err := os.Open(file.Name())
	if err != nil {
		return tab, errors.Join(resultErr, err)
	}
	content, readErr := io.ReadAll(io.LimitReader(rendered, amwusage.DatabaseReportMaxBytes+1))
	if err := errors.Join(readErr, rendered.Close()); err != nil {
		return tab, errors.Join(resultErr, err)
	}
	if len(content) > amwusage.DatabaseReportMaxBytes {
		return tab, errors.Join(resultErr, errors.New("AMW report exceeds embedding size limit; database preserved"))
	}
	if err := reportCtx.Err(); err != nil {
		return tab, errors.Join(resultErr, err)
	}
	tab.HTML = string(content)
	return tab, resultErr
}
