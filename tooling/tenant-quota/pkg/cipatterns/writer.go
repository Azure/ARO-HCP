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

package cipatterns

import (
	"bytes"
	"context"
	"fmt"
	"net/http"

	"github.com/Azure/azure-kusto-go/azkustodata"
	"github.com/Azure/azure-kusto-go/azkustodata/kql"
	"github.com/Azure/azure-kusto-go/azkustoingest"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/kusto"
	tenantkusto "github.com/Azure/ARO-HCP/tooling/tenant-quota/pkg/kusto"
)

var resultColumns = map[string]string{
	"resultKey": "string", "rawFingerprint": "string", "extractorVersion": "string", "resultSchemaVersion": "long",
	"origin": "string", "buildId": "string", "source": "string", "testId": "string",
	"startedAt": "datetime", "finishedAt": "datetime", "setupFinishTime": "datetime", "testStartTime": "datetime", "cleanupStartTime": "datetime",
	"message": "string", "testName": "string", "findings": "dynamic", "computedAt": "datetime", "producerCommit": "string",
}

func (c *Controller) initializeKusto(ctx context.Context) error {
	credential, err := azidentity.NewDefaultAzureCredential(&azidentity.DefaultAzureCredentialOptions{RequireAzureTokenCredentials: true})
	if err != nil {
		return fmt.Errorf("create Kusto credential: %w", err)
	}
	return c.initializeKustoWithCredential(ctx, credential)
}

func (c *Controller) initializeKustoWithCredential(ctx context.Context, credential azcore.TokenCredential) error {
	lifetime, cancel := context.WithCancel(ctx)
	httpClient := *c.client
	base := httpClient.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	httpClient.Transport = kusto.CompleteTransport{Base: tenantkusto.OwnerLifetimeTransport{Lifetime: lifetime, Base: base}, MaxResponseBytes: maxReadBytes}
	settings := c.config.CIJobOutcomes
	connection := azkustodata.NewConnectionStringBuilder(settings.ClusterURI)
	if credential != nil {
		connection = connection.WithTokenCredential(credential)
	}
	client, err := azkustodata.New(connection, azkustodata.WithHttpClient(&httpClient))
	if err != nil {
		cancel()
		return fmt.Errorf("create query client: %w", err)
	}
	var ingestor *azkustoingest.Ingestion
	closeClients := func() {
		cancel()
		if ingestor != nil {
			if err := ingestor.Close(); err != nil {
				c.logger.Error("Close result ingestor", "error", err)
			}
		}
		if err := client.Close(); err != nil {
			c.logger.Error("Close query client", "error", err)
		}
	}
	ok := false
	defer func() {
		if !ok {
			closeClients()
		}
	}()
	for _, table := range []struct {
		name    string
		columns map[string]string
	}{
		{settings.TestResults.Table, map[string]string{
			"buildId": "string", "source": "string", "testId": "string", "failed": "bool", "message": "string",
			"startedAt": "datetime", "finishedAt": "datetime", "setupFinishTime": "datetime", "testStartTime": "datetime", "cleanupStartTime": "datetime",
		}},
		{settings.TestNames.Table, map[string]string{"testId": "string", "name": "string"}},
		{c.config.CIFailurePatterns.Results.Table, resultColumns},
	} {
		if err := c.checkSchema(ctx, client, table.name, table.columns); err != nil {
			return err
		}
	}
	if err := c.checkMapping(ctx, client); err != nil {
		return err
	}
	ingestionConnection := azkustodata.NewConnectionStringBuilder(settings.IngestionURI)
	if credential != nil {
		ingestionConnection = ingestionConnection.WithTokenCredential(credential)
	}
	ingestor, err = azkustoingest.New(ingestionConnection, azkustoingest.WithHttpClient(&httpClient),
		azkustoingest.WithDefaultDatabase(settings.Database), azkustoingest.WithDefaultTable(c.config.CIFailurePatterns.Results.Table))
	if err != nil {
		return fmt.Errorf("create result ingestor: %w", err)
	}
	c.readBuildIDs = func(ctx context.Context, after string) ([]string, error) { return c.loadBuildIDs(ctx, client, after) }
	c.readInputs = func(ctx context.Context, build string) ([]input, error) { return c.loadInputs(ctx, client, build) }
	c.readResults = func(ctx context.Context, inputs []input, t failurePatternVersion) ([]resultRow, error) {
		return c.loadResults(ctx, client, inputs, t)
	}
	c.submit = func(ctx context.Context, payload *bytes.Buffer) error {
		ctx, cancel := context.WithTimeout(ctx, c.operationTimeout())
		defer cancel()
		if err := tenantkusto.IngestJSONLines(ctx, ingestor, c.config.CIFailurePatterns.Results.IngestionMapping, payload); err != nil {
			return fmt.Errorf("queue failure results: %w", err)
		}
		return nil
	}
	c.closeClients = closeClients
	ok = true
	return nil
}

func (c *Controller) checkSchema(ctx context.Context, client *azkustodata.Client, name string, required map[string]string) error {
	rows, err := c.query(ctx, client, kql.New("").AddTable(name).AddLiteral(" | getschema | project ColumnName, ColumnType"), map[string]any{})
	if err != nil {
		return fmt.Errorf("read %s schema: %w", name, err)
	}
	columns := map[string]string{}
	for _, row := range rows {
		column, err := row.StringByName("ColumnName")
		if err != nil {
			return err
		}
		kind, err := row.StringByName("ColumnType")
		if err != nil {
			return err
		}
		if _, exists := columns[column]; exists {
			return fmt.Errorf("duplicate schema column %s.%s", name, column)
		}
		columns[column] = kind
	}
	for column, kind := range required {
		if columns[column] != kind {
			return fmt.Errorf("table %s requires %s:%s; deploy schema first", name, column, kind)
		}
	}
	return nil
}

func (c *Controller) checkMapping(ctx context.Context, client *azkustodata.Client) error {
	ctx, cancel := context.WithTimeout(ctx, c.operationTimeout())
	defer cancel()
	table := c.config.CIFailurePatterns.Results
	statement := kql.New(".show table ").AddTable(table.Table).AddLiteral(" ingestion json mapping ").AddString(table.IngestionMapping)
	dataset, err := client.Mgmt(ctx, c.config.CIJobOutcomes.Database, statement, azkustodata.ServerTimeout(c.operationTimeout()))
	if err != nil {
		return fmt.Errorf("read results mapping: %w", err)
	}
	rows, err := primaryRows(dataset)
	if err != nil {
		return err
	}
	if len(rows) != 1 {
		return fmt.Errorf("expected one mapping %q, got %d; deploy schema first", table.IngestionMapping, len(rows))
	}
	mapping, err := rows[0].StringByName("Mapping")
	if err != nil {
		return err
	}
	return validateMapping(mapping)
}

func validateMapping(mapping string) error {
	entries, err := tenantkusto.ParseMapping(mapping)
	if err != nil {
		return fmt.Errorf("decode results mapping: %w", err)
	}
	found := map[string]bool{}
	for _, entry := range entries {
		kind, required := resultColumns[entry.Column]
		if !required {
			continue
		}
		if found[entry.Column] {
			return fmt.Errorf("duplicate mapping for %s", entry.Column)
		}
		if !entry.Matches(entry.Column, kind) {
			return fmt.Errorf("invalid mapping for %s; deploy schema first", entry.Column)
		}
		found[entry.Column] = true
	}
	for column := range resultColumns {
		if !found[column] {
			return fmt.Errorf("missing mapping for %s; deploy schema first", column)
		}
	}
	return nil
}
