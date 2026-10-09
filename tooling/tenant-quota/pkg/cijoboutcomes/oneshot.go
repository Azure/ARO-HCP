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
	"crypto/sha256"
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
	JobURIs          []JobURI
	JobNames         []string
	Discovery        bool
	AllHistory, Bulk bool
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
	if !o.Discovery {
		if len(o.JobURIs) == 0 || len(o.JobNames) > 0 || !o.Since.IsZero() || !o.Until.IsZero() || o.Limit != 0 || o.AllHistory || o.Bulk {
			return errors.New("ci-outcomes requires --job-uri and does not support discovery selection")
		}
		for _, uri := range o.JobURIs {
			if _, err := CanonicalJobURI(string(uri)); err != nil {
				return err
			}
		}
	} else {
		if len(o.JobURIs) > 0 || o.InspectArtifacts {
			return errors.New("ci-discovery does not support --job-uri or --inspect-artifacts; discovery dry-run already bypasses Kusto")
		}
		if o.Until.IsZero() || (o.AllHistory && !o.Since.IsZero()) || (!o.AllHistory && (o.Since.IsZero() || !o.Since.Before(o.Until))) {
			return errors.New("ci-discovery requires --since < --until or --all-history with --until")
		}
		if (o.Bulk && (!o.AllHistory || o.Limit != 0)) || (!o.Bulk && o.Limit <= 0) {
			return errors.New("ci-discovery requires positive --limit or --all-history --bulk without --limit")
		}
		for _, name := range o.JobNames {
			if strings.TrimSpace(name) == "" {
				return errors.New("--job-name must not be empty")
			}
		}
	}
	return nil
}

type BatchReport struct {
	JobURI  JobURI            `json:"jobUri,omitempty"`
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

// RunOnce consumes this Writer. It never starts polling, reads a cursor, or
// retries a key. A fresh invocation checks live tags rather than cached batches.
// Reports are JSONL; workers may interleave reports but never individual lines.
func (w *Writer) RunOnce(ctx context.Context, options OneShotOptions, output io.Writer) (err error) {
	ctx = utils.ContextWithControllerName(ctx, w.name)
	ctx = utils.ContextWithLogger(ctx, w.logger)
	if options.Discovery {
		ctx = utils.ContextWithLogger(utils.ContextWithControllerName(ctx, w.discoveryName), w.discoveryLogger)
	}
	defer utilruntime.HandleCrashWithContext(ctx, func(_ context.Context, value any) { err = fmt.Errorf("one-shot panic: %v", value) })
	defer w.queue.ShutDown()
	defer w.discoveryQueue.ShutDown()
	if err := options.Validate(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	w.dryRun = !options.Ingest
	w.batches = newTTLCache[string, struct{}](w.config.CIJobOutcomes.GetCacheSize(), w.config.CIJobOutcomes.GetCacheTTL())
	if options.InspectArtifacts {
		w.tagExists = func(context.Context, batchKind, BuildID) (bool, error) { return false, nil }
		w.processedExists = func(context.Context, BuildID) (bool, error) { return false, nil }
	} else if options.Ingest || !options.Discovery {
		initialize := w.initializeReadOnly
		if options.Ingest {
			initialize = w.initialize
		}
		if err := initialize(ctx); err != nil {
			return err
		}
		if w.closeClients != nil {
			defer w.closeClients()
		}
	}
	ids, selectionErr := w.selectOnce(ctx, options)
	// Do not submit a misleading partial bootstrap after a failed page or scope.
	if options.Bulk && selectionErr != nil {
		return selectionErr
	}
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
	if options.Discovery {
		submit := w.submitDiscovery
		w.submitDiscovery = func(ctx context.Context, rows []DiscoveredJob, tag string) error {
			report := BatchReport{Tag: tag, Table: w.target(discoveredBatch).Table, Rows: len(rows), Status: "would-submit"}
			if len(rows) == 1 {
				report.JobURI, report.BuildID = rows[0].JobURI, BuildID(rows[0].BuildID)
			}
			var err error
			if options.Ingest {
				err = submit(ctx, rows, tag)
				report.Status = "submitted"
			} else {
				for _, row := range rows {
					payload, marshalErr := json.Marshal(row)
					if marshalErr != nil {
						err = marshalErr
						break
					}
					report.Payload = append(report.Payload, payload)
				}
			}
			if err != nil {
				report.Status, report.Error = "error", err.Error()
			}
			w.report(report)
			return err
		}
		if !options.Ingest {
			w.discoveredExists = func(context.Context, JobURI) (bool, error) { return false, nil }
		} else {
			exists := w.discoveredExists
			w.discoveredExists = func(ctx context.Context, uri JobURI) (bool, error) {
				found, err := exists(ctx, uri)
				if found && err == nil {
					w.report(BatchReport{JobURI: uri, Tag: discoveryTag(uri), Table: w.target(discoveredBatch).Table, Status: "existing"})
				}
				return found, err
			}
		}
	}
	if options.Bulk {
		rows := make([]DiscoveredJob, 0, len(ids))
		for _, uri := range ids {
			row, err := JobReference(uri)
			if err != nil {
				return err
			}
			rows = append(rows, row)
		}
		if len(rows) > 0 {
			payload, err := encodeRows(rows)
			if err != nil {
				return err
			}
			tag := fmt.Sprintf("discovered-bulk-%x", sha256.Sum256(payload.Bytes()))
			submitErr := w.submitDiscovery(ctx, rows, tag)
			failures = append(failures, submitErr)
		} else {
			w.report(BatchReport{Table: w.target(discoveredBatch).Table, Status: "empty"})
		}
		return errors.Join(append(failures, ctx.Err())...)
	}
	workers := options.Workers
	if workers == 0 {
		workers = w.config.CIJobOutcomes.GetWorkers()
		if options.Discovery {
			workers = w.config.CIJobOutcomes.GetDiscoveryWorkers()
		}
	}
	if workers <= 0 {
		return errors.New("workers must be positive")
	}
	jobs := make(chan JobURI)
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
					if options.Discovery {
						return w.reconcileDiscovery(keyCtx, id)
					}
					local := *w
					row, err := JobReference(id)
					if err != nil {
						return err
					}
					local.report = func(report BatchReport) {
						report.JobURI = id
						report.BuildID = BuildID(row.BuildID)
						w.report(report)
					}
					local.processedExists = func(ctx context.Context, buildID BuildID) (bool, error) {
						found, err := w.processedExists(ctx, buildID)
						if found && err == nil {
							local.reportBatch(processedBatch, buildID, "existing", 0, nil, nil)
						}
						return found, err
					}
					delay, err := local.reconcile(keyCtx, id)
					if err == nil && delay > 0 {
						local.report(BatchReport{Status: "waiting"})
					}
					return err
				}()
				if err != nil {
					w.report(BatchReport{JobURI: id, Status: "error", Error: err.Error()})
					mu.Lock()
					failures = append(failures, fmt.Errorf("job %s: %w", id, err))
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

func (w *Writer) selectOnce(ctx context.Context, options OneShotOptions) ([]JobURI, error) {
	if !options.Discovery {
		var ids []JobURI
		for _, raw := range options.JobURIs {
			uri, err := CanonicalJobURI(string(raw))
			if err != nil {
				return nil, err
			}
			ids = append(ids, uri)
		}
		slices.Sort(ids)
		return slices.Compact(ids), nil
	}
	jobs, err := w.gcs.ListJobs(ctx)
	if err != nil {
		return nil, err
	}
	var refs []DiscoveredJob
	var failures []error
	matched := map[string]bool{}
	for _, job := range jobs {
		if len(options.JobNames) > 0 && !slices.Contains(options.JobNames, job.Name) {
			continue
		}
		matched[job.Name] = true
		err := w.gcs.ListRuns(ctx, job, options.Since, options.Until, func(raw JobURI) error {
			ref, err := JobReference(raw)
			if err == nil {
				refs = append(refs, ref)
			}
			return err
		})
		if err != nil {
			failures = append(failures, fmt.Errorf("list %s: %w", job.Prefix, err))
		}
	}
	for _, name := range options.JobNames {
		if !matched[name] {
			failures = append(failures, fmt.Errorf("job name %q did not match configured discovery scopes", name))
		}
	}
	slices.SortFunc(refs, func(a, b DiscoveredJob) int {
		// Validated 19-digit snowflakes have the same numeric and lexical order.
		if order := strings.Compare(a.BuildID, b.BuildID); order != 0 {
			return order
		}
		return strings.Compare(string(a.JobURI), string(b.JobURI))
	})
	refs = slices.Compact(refs)
	if options.Limit > 0 && len(refs) > options.Limit {
		refs = refs[:options.Limit]
	}
	ids := make([]JobURI, 0, len(refs))
	for _, ref := range refs {
		ids = append(ids, ref.JobURI)
	}
	return ids, errors.Join(failures...)
}
