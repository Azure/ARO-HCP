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

// Package cijoboutcomes records independent, immutable CI artifact batches in Kusto.
package cijoboutcomes

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/Azure/azure-kusto-go/azkustodata"
	"github.com/Azure/azure-kusto-go/azkustodata/kql"
	"github.com/Azure/azure-kusto-go/azkustoingest"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"

	"github.com/Azure/ARO-HCP/tooling/tenant-quota/pkg/config"
)

type batchKind string

const (
	runBatch                batchKind = "run"
	testsBatch              batchKind = "tests"
	namesBatch              batchKind = "names"
	observabilityTestsBatch batchKind = "observability-tests"
	observabilityNamesBatch batchKind = "observability-names"
)

var batchKinds = [...]batchKind{runBatch, testsBatch, namesBatch, observabilityTestsBatch, observabilityNamesBatch}

func (kind batchKind) tag(id BuildID) string { return string(kind) + "-" + string(id) }
func runTag(id string) string                { return runBatch.tag(BuildID(id)) }
func testsTag(id string) string              { return testsBatch.tag(BuildID(id)) }

func (w *Writer) target(kind batchKind) config.KustoTableConfig {
	switch kind {
	case runBatch:
		return w.config.CIJobOutcomes.Outcomes
	case namesBatch, observabilityNamesBatch:
		return w.config.CIJobOutcomes.TestNames
	default:
		return w.config.CIJobOutcomes.TestResults
	}
}

// Clients survive discovery passes and reconciles. Partial initialization is
// closed before retrying; successful clients are closed only after workers join.
func (w *Writer) initializeKusto(ctx context.Context) error {
	return w.initializeKustoClients(ctx, true)
}

// Read-only initialization never constructs ingestors or requires schema changes.
func (w *Writer) initializeKustoClients(ctx context.Context, ingest bool) error {
	credential, err := azidentity.NewDefaultAzureCredential(&azidentity.DefaultAzureCredentialOptions{RequireAzureTokenCredentials: true})
	if err != nil {
		return err
	}
	return w.initializeKustoWithCredential(ctx, ingest, credential)
}

func (w *Writer) initializeKustoWithCredential(ctx context.Context, ingest bool, credential azcore.TokenCredential) error {
	lifetime, cancel := context.WithCancel(ctx)
	ok := false
	defer func() {
		if !ok {
			cancel()
		}
	}()
	httpClient := *w.client
	transport := httpClient.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	httpClient.Transport = lifetimeTransport{lifetime: lifetime, base: transport}
	settings := w.config.CIJobOutcomes
	connection := azkustodata.NewConnectionStringBuilder(settings.ClusterURI)
	if credential != nil {
		connection = connection.WithTokenCredential(credential)
	}
	client, err := azkustodata.New(connection, azkustodata.WithHttpClient(&httpClient))
	if err != nil {
		return err
	}
	targets := map[string]*tableIngestor{}
	closeClients := func() {
		cancel()
		for _, target := range targets {
			if err := target.ingestor.Close(); err != nil {
				w.logger.Error(err, "Failed to close ingestor", "table", target.name)
			}
		}
		if err := client.Close(); err != nil {
			w.logger.Error(err, "Failed to close query client")
		}
	}
	defer func() {
		if !ok {
			closeClients()
		}
	}()
	if ingest {
		for _, guard := range []struct {
			target config.KustoTableConfig
			column string
		}{
			{settings.Outcomes, "adoBuildId"}, {settings.TestResults, "message"},
		} {
			if err := w.checkMapping(ctx, client, guard.target, guard.column); err != nil {
				return err
			}
		}
		for _, target := range []config.KustoTableConfig{settings.Outcomes, settings.TestNames, settings.TestResults} {
			ingestor, err := azkustoingest.New(
				azkustodata.NewConnectionStringBuilder(settings.IngestionURI).WithTokenCredential(credential),
				azkustoingest.WithHttpClient(&httpClient),
				azkustoingest.WithDefaultDatabase(settings.Database), azkustoingest.WithDefaultTable(target.Table))
			if err != nil {
				return err
			}
			targets[target.Table] = &tableIngestor{name: target.Table, mapping: target.IngestionMapping, ingestor: ingestor}
		}
	}
	w.cursor = func(ctx context.Context, release string) (time.Time, error) {
		dataset, err := client.Query(ctx, settings.Database, cursorQuery(settings, release))
		if err != nil {
			return time.Time{}, err
		}
		for _, table := range dataset.Tables() {
			// Query also returns metadata tables, including when max() is null.
			if !table.IsPrimaryResult() {
				continue
			}
			for _, row := range table.Rows() {
				value, err := row.DateTimeByName("cursor")
				if err != nil {
					return time.Time{}, err
				}
				if value != nil {
					return *value, nil
				}
			}
		}
		return time.Time{}, nil
	}
	w.tagExists = func(ctx context.Context, kind batchKind, id BuildID) (bool, error) {
		statement := extentTagQuery(w.target(kind).Table, kind.tag(id))
		dataset, err := client.Mgmt(ctx, settings.Database, statement)
		if err != nil {
			return false, err
		}
		for _, table := range dataset.Tables() {
			if len(table.Rows()) > 0 {
				return true, nil
			}
		}
		return false, nil
	}
	if ingest {
		w.submit = func(ctx context.Context, kind batchKind, id BuildID, payload *bytes.Buffer) error {
			return ingestPayload(ctx, targets[w.target(kind).Table], payload, kind.tag(id))
		}
	}
	w.closeClients = closeClients
	ok = true
	return nil
}

// Kusto auth metadata and resource discovery can use background contexts.
// Bind their HTTP work to the owner without changing the artifact client's lifetime.
// SDK retry sleeps outside HTTP may still delay shutdown.
type lifetimeTransport struct {
	lifetime context.Context
	base     http.RoundTripper
}

func (t lifetimeTransport) CloseIdleConnections() {
	if closer, ok := t.base.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}

func (t lifetimeTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	ctx, cancel := context.WithCancel(request.Context())
	stop := context.AfterFunc(t.lifetime, cancel)
	cleanup := func() { stop(); cancel() }
	keep := false
	defer func() {
		if !keep {
			cleanup()
		}
	}()
	if t.lifetime.Err() != nil {
		cancel()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	response, err := t.base.RoundTrip(request.Clone(ctx))
	if err != nil {
		return response, err
	}
	// Cancellation must remain attached until the body is consumed, not just
	// until headers arrive. Cleanup also detaches the lifetime callback.
	if response.Body != nil {
		response.Body = &lifetimeBody{ReadCloser: response.Body, cleanup: cleanup}
		keep = true
	}
	return response, nil
}

type lifetimeBody struct {
	io.ReadCloser
	cleanup func()
}

func (b *lifetimeBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err != nil {
		b.cleanup()
	}
	return n, err
}

func (b *lifetimeBody) Close() error {
	defer b.cleanup()
	return b.ReadCloser.Close()
}

func extentTagQuery(table, tag string) *kql.Builder {
	return kql.New(".show table [").AddString(table).
		AddLiteral("] extents where tags has ").AddString("ingest-by:" + tag).AddLiteral(" | project ExtentId")
}

func cursorQuery(settings config.CIJobOutcomesConfig, release string) *kql.Builder {
	return kql.New("").AddTable(settings.Outcomes.Table).
		AddLiteral(" | where sippyRelease == ").AddString(release).
		AddLiteral(" and jobName contains ").AddString(settings.JobFilter).
		AddLiteral(" and finishedAt > ").AddDateTime(time.Time{}).
		AddLiteral(" | summarize cursor = max(startedAt)")
}

func (w *Writer) checkMapping(ctx context.Context, client *azkustodata.Client, target config.KustoTableConfig, column string) error {
	statement := kql.New(".show table ").AddTable(target.Table).
		AddLiteral(" ingestion json mapping ").AddString(target.IngestionMapping)
	dataset, err := client.Mgmt(ctx, w.config.CIJobOutcomes.Database, statement)
	if err != nil {
		return fmt.Errorf("read %s mapping: %w", target.Table, err)
	}
	for _, table := range dataset.Tables() {
		if rows := table.Rows(); len(rows) > 0 {
			mapping, err := rows[0].StringByName("Mapping")
			if err != nil {
				return err
			}
			if err := validateMapping(mapping, column); err != nil {
				return fmt.Errorf("mapping %q: %w", target.IngestionMapping, err)
			}
			return nil
		}
	}
	return fmt.Errorf("mapping %q is missing; deploy the Kusto schema migration first", target.IngestionMapping)
}

func validateMapping(mapping, required string) error {
	var columns []struct {
		Column, DataType, Path string
		Properties             struct{ Path string }
	}
	if err := json.Unmarshal([]byte(mapping), &columns); err != nil {
		return fmt.Errorf("failed to decode mapping: %w", err)
	}
	for _, column := range columns {
		path := "$['" + required + "']"
		if column.Column == required && column.DataType == "string" && (column.Path == path || column.Properties.Path == path) {
			return nil
		}
	}
	return fmt.Errorf("missing %s; deploy the Kusto schema migration first", required)
}

func encodeRows[T any](rows []T) (*bytes.Buffer, error) {
	payload := &bytes.Buffer{}
	encoder := json.NewEncoder(payload)
	for i, row := range rows {
		if err := encoder.Encode(row); err != nil {
			return nil, fmt.Errorf("failed to encode row %d: %w", i, err)
		}
	}
	return payload, nil
}

type tableIngestor struct {
	name, mapping string
	ingestor      *azkustoingest.Ingestion
}

func ingestRows[T any](ctx context.Context, target *tableIngestor, rows []T, tag string) error {
	if len(rows) == 0 {
		return nil
	}
	payload, err := encodeRows(rows)
	if err != nil {
		return err
	}
	return ingestPayload(ctx, target, payload, tag)
}

func ingestPayload(ctx context.Context, target *tableIngestor, payload *bytes.Buffer, tag string) error {
	options := []azkustoingest.FileOption{
		azkustoingest.IngestionMappingRef(target.mapping, azkustoingest.MultiJSON),
		azkustoingest.FileFormat(azkustoingest.MultiJSON),
	}
	if tag != "" {
		ifNotExists, err := json.Marshal([]string{tag})
		if err != nil {
			return err
		}
		options = append(options, azkustoingest.Tags([]string{"ingest-by:" + tag}), azkustoingest.IfNotExists(string(ifNotExists)))
	}
	if _, err := target.ingestor.FromReader(ctx, payload, options...); err != nil {
		return fmt.Errorf("queue rows for %s: %w", target.name, err)
	}
	return nil
}
