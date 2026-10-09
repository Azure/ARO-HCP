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
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Azure/azure-kusto-go/azkustodata"
	"github.com/Azure/azure-kusto-go/azkustodata/kql"
	v1 "github.com/Azure/azure-kusto-go/azkustodata/query/v1"
)

// Run against a disposable local emulator, never an Azure cluster:
// KUSTO_TEST_ENDPOINT=http://localhost:18089 go test ./pkg/cijoboutcomes -run TestWriterKustoIntegration -v
// The test creates and drops its own database; queued ingestion is not exercised.
func TestWriterKustoIntegration(t *testing.T) {
	endpoint := os.Getenv("KUSTO_TEST_ENDPOINT")
	if endpoint == "" {
		t.Skip("set KUSTO_TEST_ENDPOINT to a local Kusto emulator to run")
	}
	u, err := url.Parse(endpoint)
	require.NoError(t, err)
	require.Equal(t, "http", u.Scheme, "only a local, unauthenticated emulator is supported")
	require.Contains(t, []string{"localhost", "127.0.0.1", "::1"}, u.Hostname(), "refusing a non-local endpoint")
	require.Nil(t, u.User)
	require.Empty(t, u.RawQuery)
	require.Empty(t, u.Fragment)
	require.Contains(t, []string{"", "/"}, u.Path)

	// Disable proxies and reject SDK metadata requests or redirects off the emulator.
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	t.Cleanup(transport.CloseIdleConnections)
	httpClient := &http.Client{Timeout: 30 * time.Second, Transport: ingestionTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Scheme != "http" || r.URL.Host != u.Host {
			return nil, fmt.Errorf("refusing request outside local emulator: %s", r.URL)
		}
		return transport.RoundTrip(r)
	})}
	client, err := azkustodata.New(azkustodata.NewConnectionStringBuilder(endpoint), azkustodata.WithHttpClient(httpClient))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	database := fmt.Sprintf("writer_integration_%d", time.Now().UnixNano())
	_, err = client.Mgmt(ctx, "", (&kql.Builder{}).AddUnsafe(fmt.Sprintf(
		`.create database %s persist (@"/kustodata/dbs/%s/md", @"/kustodata/dbs/%s/data")`, database, database, database)))
	require.NoError(t, err)
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, err := client.Mgmt(cleanupCtx, "", (&kql.Builder{}).AddUnsafe(".drop database "+database))
		require.NoError(t, err, "drop temporary emulator database")
	})
	exec := func(t *testing.T, statement *kql.Builder) v1.Dataset {
		t.Helper()
		dataset, err := client.Mgmt(ctx, database, statement)
		require.NoError(t, err, "%s", statement.String())
		return dataset
	}
	w := newTestWriter(t)
	w.client = httpClient
	w.config.CIJobOutcomes.ClusterURI = endpoint
	w.config.CIJobOutcomes.Database = database
	for _, kind := range []batchKind{discoveredBatch, processedBatch, runBatch, testsBatch, namesBatch} {
		target := w.target(kind)
		content, err := os.ReadFile(filepath.Join("../../../../dev-infrastructure/modules/logs/kusto/tables", target.Table+".kql"))
		require.NoError(t, err)
		dataset := exec(t, (&kql.Builder{}).AddUnsafe(".execute database script <|\n"+string(content)))
		for _, table := range dataset.Tables() {
			for _, row := range table.Rows() {
				result, err := row.StringByName("Result")
				require.NoError(t, err)
				require.Equal(t, "Completed", result, "%s: %s", target.Table, row.String())
			}
		}
	}
	require.NoError(t, w.initializeKustoWithCredential(ctx, false, nil))
	t.Cleanup(w.closeClients)
	job := GCSJob{Name: "periodic-ci-Azure-ARO-HCP-test", Prefix: "logs/periodic-ci-Azure-ARO-HCP-test/"}

	t.Run("empty tables", func(t *testing.T) {
		cursor, err := w.jobCursor(ctx, job)
		require.NoError(t, err)
		require.Equal(t, "", cursor, "an empty max(long) must become an empty string, not null or zero")
		pending, err := w.pendingJobs(ctx)
		require.NoError(t, err)
		require.Empty(t, pending)
		exists, err := w.discoveredExists(ctx, testJobURI)
		require.NoError(t, err)
		require.False(t, exists)
		exists, err = w.processedExists(ctx, testBuildID)
		require.NoError(t, err)
		require.False(t, exists)
		exists, err = w.tagExists(ctx, runBatch, testBuildID)
		require.NoError(t, err)
		require.False(t, exists, "management metadata must not count as an extent")
	})

	const pullJob = "pull-ci-Azure-ARO-HCP-test"
	const aliasID = "1976270000000000124"
	const batchID = "1976270000000000125"
	const otherID = "1976270000000000126"
	prefix := "gs://" + w.config.CIJobOutcomes.GCSBucket + "/"
	fixtures := []DiscoveredJob{
		{JobURI: testJobURI, BuildID: string(testBuildID), JobName: job.Name},
		{JobURI: testJobURI, BuildID: string(testBuildID), JobName: job.Name},
		{JobURI: JobURI(prefix + job.Prefix + "9"), BuildID: "9", JobName: job.Name},
		{JobURI: JobURI(prefix + "pr-logs/pull/Azure_ARO-HCP/1/" + pullJob + "/" + aliasID), BuildID: aliasID, JobName: pullJob},
		{JobURI: JobURI(prefix + "pr-logs/pull/Azure_ARO-HCP/2/" + pullJob + "/" + aliasID), BuildID: aliasID, JobName: pullJob},
		{JobURI: JobURI(prefix + "pr-logs/pull/batch/" + pullJob + "/" + batchID), BuildID: batchID, JobName: pullJob},
		{JobURI: JobURI(prefix + "pr-logs/directory/" + pullJob + "/" + otherID), BuildID: otherID, JobName: pullJob},
		{JobURI: JobURI(prefix + "pr-logs/pull/Azure_ARO-HCP/3/other/" + otherID), BuildID: otherID, JobName: "other"},
		{JobURI: JobURI("gs://other-bucket/" + job.Prefix + otherID), BuildID: otherID, JobName: job.Name},
		{JobURI: JobURI(prefix + "Logs/" + job.Name + "/" + otherID), BuildID: otherID, JobName: job.Name},
	}
	statement := kql.New(".set-or-append ").AddTable(w.target(discoveredBatch).Table).
		AddLiteral(" <| datatable(jobUri:string, buildId:string, jobName:string)[")
	for i, row := range fixtures {
		if i > 0 {
			statement.AddLiteral(",")
		}
		statement.AddString(string(row.JobURI)).AddLiteral(",").AddString(row.BuildID).AddLiteral(",").AddString(row.JobName)
	}
	exec(t, statement.AddLiteral("]"))
	exec(t, kql.New(".set-or-append ").AddTable(w.target(processedBatch).Table).
		AddLiteral(" <| datatable(buildId:string)[").AddString(aliasID).AddLiteral(",").AddString(aliasID).AddLiteral("]"))

	t.Run("exact scoped cursors", func(t *testing.T) {
		for _, tc := range []struct {
			job  GCSJob
			want string
		}{
			{job, string(testBuildID)},
			{GCSJob{Name: pullJob, Prefix: "pr-logs/directory/" + pullJob + "/", Aliases: true}, aliasID},
			{GCSJob{Name: pullJob, Prefix: "pr-logs/pull/batch/" + pullJob + "/"}, batchID},
			{GCSJob{Name: "missing", Prefix: job.Prefix}, ""},
		} {
			cursor, err := w.jobCursor(ctx, tc.job)
			require.NoError(t, err)
			require.Equal(t, tc.want, cursor, "%+v", tc.job)
			t.Logf("cursor for %+v = %q", tc.job, cursor)
		}
	})
	t.Run("pending distinct leftanti", func(t *testing.T) {
		pending, err := w.pendingJobs(ctx)
		require.NoError(t, err)
		var want []JobURI
		for i, row := range fixtures {
			if i != 1 && row.BuildID != aliasID {
				want = append(want, row.JobURI)
			}
		}
		require.ElementsMatch(t, want, pending, "deduplicate discovery and exclude every alias of a processed build")
		t.Logf("pending unique URIs: %d", len(pending))
	})
	t.Run("row existence", func(t *testing.T) {
		for _, tc := range []struct {
			uri  JobURI
			want bool
		}{{testJobURI, true}, {testJobURI + "-missing", false}, {testJobURI + `" or true //`, false}} {
			exists, err := w.discoveredExists(ctx, tc.uri)
			require.NoError(t, err)
			require.Equal(t, tc.want, exists)
		}
		for _, tc := range []struct {
			id   BuildID
			want bool
		}{{BuildID(aliasID), true}, {testBuildID, false}} {
			exists, err := w.processedExists(ctx, tc.id)
			require.NoError(t, err)
			require.Equal(t, tc.want, exists)
		}
	})
	t.Run("management mappings", func(t *testing.T) {
		for _, guard := range []struct {
			kind   batchKind
			column string
		}{{runBatch, "adoBuildId"}, {testsBatch, "message"}, {discoveredBatch, "jobUri"},
			{discoveredBatch, "buildId"}, {discoveredBatch, "jobName"}, {processedBatch, "buildId"}} {
			require.NoError(t, w.checkMapping(ctx, client, w.target(guard.kind), guard.column))
		}
		require.ErrorContains(t, w.checkMapping(ctx, client, w.target(discoveredBatch), "missing"), "missing missing")
	})
	t.Run("management extent tags", func(t *testing.T) {
		exec(t, (&kql.Builder{}).AddUnsafe(fmt.Sprintf(
			`.ingest inline into table ciJobOutcomes with (format="multijson", ingestionMappingReference="ciJobOutcomesMapping", tags='["ingest-by:%s"]') <| {"buildId":"%s"}`,
			runBatch.tag(testBuildID), testBuildID)))
		dataset := exec(t, extentTagQuery(w.target(runBatch).Table, runBatch.tag(testBuildID)))
		primaryRows := 0
		for _, table := range dataset.Tables() {
			t.Logf("management table %q: primary=%t rows=%d", table.Name(), table.IsPrimaryResult(), len(table.Rows()))
			if table.IsPrimaryResult() {
				require.Equal(t, "ExtentId", table.ColumnByName("ExtentId").Name())
				primaryRows += len(table.Rows())
			}
		}
		require.Equal(t, 1, primaryRows)
		for _, tc := range []struct {
			kind batchKind
			id   BuildID
			want bool
		}{{runBatch, testBuildID, true}, {runBatch, BuildID(aliasID), false}, {testsBatch, testBuildID, false}} {
			exists, err := w.tagExists(ctx, tc.kind, tc.id)
			require.NoError(t, err)
			require.Equal(t, tc.want, exists)
		}
	})
}
