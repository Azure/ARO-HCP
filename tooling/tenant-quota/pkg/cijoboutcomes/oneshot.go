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

package cijoboutcomes

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"time"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"

	"github.com/Azure/ARO-HCP/internal/utils"
)

type OneShotOptions struct {
	BuildIDs         []BuildID
	Releases         []string
	Since, Until     time.Time
	Limit, Workers   int
	Ingest           bool
	InspectArtifacts bool
}

func (o OneShotOptions) Validate() error {
	if o.Ingest && o.InspectArtifacts {
		return errors.New("--inspect-artifacts cannot be combined with --ingest")
	}
	if o.Workers < 0 {
		return errors.New("--workers must be positive")
	}
	if len(o.BuildIDs) > 0 {
		if len(o.Releases) > 0 || !o.Since.IsZero() || !o.Until.IsZero() || o.Limit != 0 {
			return errors.New("--build-id cannot be combined with release/time/limit selection")
		}
		for _, id := range o.BuildIDs {
			if strings.TrimSpace(string(id)) == "" {
				return errors.New("--build-id must not be empty")
			}
		}
	} else {
		if len(o.Releases) == 0 || o.Since.IsZero() || o.Until.IsZero() || !o.Since.Before(o.Until) || o.Limit <= 0 {
			return errors.New("select --build-id or --release with --since < --until and positive --limit")
		}
		for _, release := range o.Releases {
			if strings.TrimSpace(release) == "" {
				return errors.New("--release must not be empty")
			}
		}
	}
	return nil
}

type BatchReport struct {
	BuildID BuildID           `json:"buildID"`
	Tag     string            `json:"tag"`
	Table   string            `json:"table"`
	Status  string            `json:"status"`
	Rows    int               `json:"rows"`
	Payload []json.RawMessage `json:"payload,omitempty"`
	Error   string            `json:"error,omitempty"`
}

func (w *Writer) reportBatch(kind batchKind, id BuildID, status string, rows int, payload *bytes.Buffer, err error) {
	if w.report == nil {
		return
	}
	report := BatchReport{BuildID: id, Tag: kind.tag(id), Table: w.target(kind).Table, Status: status, Rows: rows}
	if err != nil {
		report.Error = err.Error()
	}
	if payload != nil {
		for _, row := range bytes.Split(bytes.TrimSpace(payload.Bytes()), []byte{'\n'}) {
			report.Payload = append(report.Payload, json.RawMessage(row))
		}
	}
	w.report(report)
}

// RunOnce consumes this Writer. It never starts discovery, reads a cursor, or
// retries a key. A fresh invocation checks live tags rather than cached batches.
// Reports are JSONL; workers may interleave reports but never individual lines.
func (w *Writer) RunOnce(ctx context.Context, options OneShotOptions, output io.Writer) (err error) {
	ctx = utils.ContextWithControllerName(ctx, w.name)
	ctx = utils.ContextWithLogger(ctx, w.logger)
	defer utilruntime.HandleCrashWithContext(ctx, func(_ context.Context, value any) { err = fmt.Errorf("one-shot panic: %v", value) })
	defer w.queue.ShutDown()
	if err := options.Validate(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	w.dryRun = !options.Ingest
	if len(options.Releases) > 0 {
		cfg := *w.config
		cfg.CIJobOutcomes.Releases = slices.Clone(options.Releases)
		slices.Sort(cfg.CIJobOutcomes.Releases)
		cfg.CIJobOutcomes.Releases = slices.Compact(cfg.CIJobOutcomes.Releases)
		w.config = &cfg
	}
	w.batches = newTTLCache[string, struct{}](w.config.CIJobOutcomes.GetCacheSize(), w.config.CIJobOutcomes.GetCacheTTL())
	if options.InspectArtifacts {
		w.tagExists = func(context.Context, batchKind, BuildID) (bool, error) { return false, nil }
	} else {
		initialize := w.initializeReadOnly
		if options.Ingest {
			initialize = w.initialize
		}
		if err := initialize(ctx); err != nil {
			return err
		}
		defer w.closeClients()
	}
	ids, selectionErr := w.selectOnce(ctx, options)
	var mu sync.Mutex
	var failures []error
	failures = append(failures, selectionErr)
	encoder := json.NewEncoder(output)
	w.report = func(report BatchReport) {
		mu.Lock()
		defer mu.Unlock()
		if err := encoder.Encode(report); err != nil {
			failures = append(failures, fmt.Errorf("write report: %w", err))
		}
	}
	defer func() { w.report = nil }()
	workers := options.Workers
	if workers == 0 {
		workers = w.config.CIJobOutcomes.GetWorkers()
	}
	if workers <= 0 {
		return errors.New("workers must be positive")
	}
	jobs := make(chan BuildID)
	var group sync.WaitGroup
	for range min(workers, len(ids)) {
		group.Add(1)
		go func() {
			defer utilruntime.HandleCrashWithContext(ctx)
			defer group.Done()
			for id := range jobs {
				keyCtx := utils.ContextWithLogger(ctx, utils.AddLoggerValues(utils.LoggerFromContext(ctx), id))
				err := func() (err error) {
					defer utilruntime.HandleCrashWithContext(keyCtx, func(_ context.Context, value any) { err = fmt.Errorf("reconcile panic: %v", value) })
					if err := keyCtx.Err(); err != nil {
						return err
					}
					return w.reconcile(keyCtx, id)
				}()
				if err != nil {
					w.report(BatchReport{BuildID: id, Status: "error", Error: err.Error()})
					mu.Lock()
					failures = append(failures, fmt.Errorf("build %s: %w", id, err))
					mu.Unlock()
				}
			}
		}()
	}
send:
	for _, id := range ids {
		select {
		case <-ctx.Done():
			break send
		case jobs <- id:
		}
	}
	close(jobs)
	group.Wait()
	return errors.Join(append(failures, ctx.Err())...)
}

func (w *Writer) selectOnce(ctx context.Context, options OneShotOptions) ([]BuildID, error) {
	if len(options.BuildIDs) > 0 {
		ids := slices.Clone(options.BuildIDs)
		slices.Sort(ids)
		return slices.Compact(ids), nil
	}
	var candidates []runMetadata
	var failures []error
	releases := slices.Clone(options.Releases)
	jobFilter := strings.ToLower(w.config.CIJobOutcomes.JobFilter)
	slices.Sort(releases)
	for _, release := range slices.Compact(releases) {
		runs, err := queryRuns(ctx, w.client, w.config.CIJobOutcomes.SippyURI, release, []map[string]string{
			{"columnField": "name", "operatorValue": "contains", "value": w.config.CIJobOutcomes.JobFilter},
			{"columnField": "timestamp", "operatorValue": ">=", "value": options.Since.UTC().Format(time.RFC3339Nano)},
			{"columnField": "timestamp", "operatorValue": "<", "value": options.Until.UTC().Format(time.RFC3339Nano)},
		})
		if err != nil {
			failures = append(failures, err)
			continue
		}
		for _, run := range runs {
			if run.Timestamp.IsZero() || run.Timestamp.Before(options.Since) || !run.Timestamp.Before(options.Until) || !strings.Contains(strings.ToLower(run.Job), jobFilter) {
				continue
			}
			if !validRun(ctx, run, BuildID(run.ProwID)) {
				failures = append(failures, fmt.Errorf("malformed or mismatched Sippy metadata for %s", run.ProwID))
				continue
			}
			candidates = append(candidates, runMetadata{run: run, release: release})
		}
	}
	slices.SortFunc(candidates, func(a, b runMetadata) int {
		if order := a.run.Timestamp.Compare(b.run.Timestamp.Time); order != 0 {
			return order
		}
		if order := strings.Compare(a.run.ProwID, b.run.ProwID); order != 0 {
			return order
		}
		return strings.Compare(a.release, b.release)
	})
	seen := map[BuildID]bool{}
	var ids []BuildID
	for _, candidate := range candidates {
		id := BuildID(candidate.run.ProwID)
		if seen[id] {
			continue
		}
		seen[id] = true
		if candidate.run.hasTerminalOutcome() {
			w.metadata.put(id, candidate)
		}
		ids = append(ids, id)
		if len(ids) == options.Limit {
			break
		}
	}
	return ids, errors.Join(failures...)
}
