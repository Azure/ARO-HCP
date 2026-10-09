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
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"

	"github.com/Azure/ARO-HCP/tooling/tenant-quota/pkg/cijoboutcomes"
)

func TestMainDispatch(t *testing.T) {
	if os.Getenv("TENANT_QUOTA_DISPATCH_TEST") == "1" {
		os.Args = append(os.Args[:1], strings.Fields(os.Getenv("TENANT_QUOTA_DISPATCH_ARGS"))...)
		main()
		os.Exit(0)
	}
	for _, tc := range []struct {
		args, want string
		fail       bool
	}{
		{"", "read config file", true},
		{"--help", "Usage: tenant-quota-collector", false},
		{"-h", "Usage: tenant-quota-collector", false},
		{"ci-outcomes --help", "job-uri", false},
		{"ci-discovery --help", "all-history", false},
		{"ci-outcome", "unknown command or arguments", true},
		{"--unknown", "unknown command or arguments", true},
		{"--help unexpected", "unknown command or arguments", true},
		{"ci-outcomes --unknown", "flag provided but not defined", true},
		{"ci-discovery --unknown", "flag provided but not defined", true},
		{"ci-outcomes --build-id 123", "flag provided but not defined", true},
		{"ci-discovery --release Presubmits", "flag provided but not defined", true},
	} {
		t.Run(tc.args, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestMainDispatch$")
			command.Env = append(os.Environ(), "TENANT_QUOTA_DISPATCH_TEST=1", "TENANT_QUOTA_DISPATCH_ARGS="+tc.args, "CONFIG_PATH="+filepath.Join(t.TempDir(), "missing.yaml"))
			var stdout, stderr bytes.Buffer
			command.Stdout, command.Stderr = &stdout, &stderr
			err := command.Run()
			if tc.fail {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			require.NoError(t, ctx.Err(), "dispatch must finish rather than start a daemon")
			require.Contains(t, stdout.String()+stderr.String(), tc.want)
			if tc.args != "" {
				require.Empty(t, stdout.String(), "command logging/help must not pollute JSONL stdout")
				require.NotContains(t, stderr.String(), "read config file", "only no arguments may load daemon configuration")
			}
		})
	}
}

func TestCIOutcomesFlags(t *testing.T) {
	uri := "gs://test-platform-results-public/logs/periodic-ci-Azure-ARO-HCP-test/1976270000000000123"
	selection := "--job-uri " + uri
	for _, args := range []string{
		"", "--build-id 123", "--release Presubmits", "--since yesterday", "--unknown",
		selection + " --limit 0", selection + " --workers 0", selection + " --workers -1",
		selection + " --dry-run=false", selection + " --dry-run --ingest", selection + " --inspect-artifacts --ingest",
		selection + " positional", "--job-uri https://example.com", selection + "/artifacts", selection + "?query",
	} {
		t.Run(args, func(t *testing.T) {
			_, _, err := parseCIOutcomes(append([]string{"--config", "runtime.yaml"}, strings.Fields(args)...), io.Discard)
			require.Error(t, err)
		})
	}
	_, _, err := parseCIOutcomes([]string{"--job-uri", uri}, io.Discard)
	require.ErrorContains(t, err, "--config")
	_, _, err = parseCIOutcomes([]string{"--config", "runtime.yaml", "--job-uri", ""}, io.Discard)
	require.Error(t, err)
	for _, args := range []string{selection, selection + " --dry-run", selection + " --inspect-artifacts"} {
		path, options, err := parseCIOutcomes(append([]string{"--config", "runtime.yaml"}, strings.Fields(args)...), io.Discard)
		require.NoError(t, err)
		require.Equal(t, "runtime.yaml", path)
		require.False(t, options.Ingest, "only explicit --ingest may write")
	}
	_, options, err := parseCIOutcomes([]string{"--config", "runtime.yaml", "--job-uri", uri, "--job-uri", uri + "/", "--ingest", "--workers", "2"}, io.Discard)
	require.NoError(t, err)
	require.True(t, options.Ingest)
	require.Equal(t, []cijoboutcomes.JobURI{cijoboutcomes.JobURI(uri), cijoboutcomes.JobURI(uri)}, options.JobURIs)
	require.Equal(t, 2, options.Workers)
}

func TestCIDiscoveryFlags(t *testing.T) {
	interval := "--since 2026-10-08T00:00:00Z --until 2026-10-09T00:00:00Z --limit 10"
	bulk := "--all-history --bulk --until 2026-10-09T00:00:00Z"
	for _, args := range []string{
		"", "--all-history --bulk", interval + " --all-history", interval + " --bulk",
		interval + " --until 2026-10-08T00:00:00Z", interval + " --limit 0", interval + " --limit -1",
		interval + " --workers 0", interval + " --workers -1", interval + " --inspect-artifacts",
		interval + " --dry-run=false", interval + " --dry-run --ingest", interval + " positional",
		bulk + " --since 2026-10-08T00:00:00Z", bulk + " --limit 0", bulk + " --limit 1",
		"--all-history --until 2026-10-09T00:00:00Z", "--since yesterday", "--release Presubmits", "--build-id 123",
	} {
		t.Run(args, func(t *testing.T) {
			_, _, err := parseCICommand("ci-discovery", append([]string{"--config", "runtime.yaml"}, strings.Fields(args)...), io.Discard)
			require.Error(t, err)
		})
	}
	for _, args := range []string{interval, bulk, "--all-history --until 2026-10-09T00:00:00Z --limit 10"} {
		_, options, err := parseCICommand("ci-discovery", append([]string{"--config", "runtime.yaml"}, strings.Fields(args)...), io.Discard)
		require.NoError(t, err)
		require.True(t, options.Discovery)
		require.False(t, options.Ingest)
	}
	_, options, err := parseCICommand("ci-discovery", append([]string{"--config", "runtime.yaml"}, strings.Fields(interval+" --job-name first --job-name second --ingest --workers 3")...), io.Discard)
	require.NoError(t, err)
	require.Equal(t, []string{"first", "second"}, options.JobNames)
	require.True(t, options.Ingest)
	require.Equal(t, 3, options.Workers)
	require.Equal(t, 24*time.Hour, options.Until.Sub(options.Since))
}

func TestCIHelpNeedsNoConfig(t *testing.T) {
	for _, command := range []string{"ci-outcomes", "ci-discovery"} {
		var stdout, stderr bytes.Buffer
		err := runCommand(t.Context(), []string{command, "--help"}, &stdout, &stderr, slog.New(slog.NewTextHandler(io.Discard, nil)))
		require.NoError(t, err)
		require.Empty(t, stdout.String())
		require.Contains(t, stderr.String(), "config")
		if command == "ci-discovery" {
			require.Contains(t, stderr.String(), "inclusive fixed upper bound")
			require.Contains(t, stderr.String(), "ascending build ID sort, URI tie-break")
		}
	}
}

func TestCIOutcomesRunsWithoutTenantSecretsOrAzureCredentials(t *testing.T) {
	originalCrash, originalHandlers := utilruntime.ReallyCrash, utilruntime.PanicHandlers
	t.Cleanup(func() {
		utilruntime.ReallyCrash, utilruntime.PanicHandlers = originalCrash, originalHandlers
		panicMetricsOnce = sync.Once{}
	})
	t.Setenv("AZURE_TOKEN_CREDENTIALS", "invalid-credential")
	t.Setenv("SECRETS_STORE_PATH", "/does-not-exist")
	originalTransport := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = originalTransport })
	http.DefaultTransport = cliTransport(func(r *http.Request) (*http.Response, error) {
		require.Equal(t, "storage.googleapis.com", r.URL.Host, "no Sippy, Kusto, or Azure calls")
		if strings.Contains(r.URL.Path, "/storage/v1/") {
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
		}
		return nil, errors.New("artifact request failed")
	})
	var output bytes.Buffer
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	args := []string{"ci-discovery", "--config", "ci-outcomes.example.yaml", "--all-history", "--bulk", "--until", "2026-10-09T00:00:00Z"}
	require.NoError(t, runCommand(t.Context(), args, &output, io.Discard, logger))
	args = []string{"--config", "ci-outcomes.example.yaml", "--inspect-artifacts", "--job-uri", "gs://test-platform-results-public/logs/periodic-ci-Azure-ARO-HCP-test/1976270000000000123"}
	require.ErrorContains(t, runCIOutcomes(t.Context(), args, &output, io.Discard, logger), "artifact request failed", "partial work errors propagate to main's nonzero exit")
	require.Contains(t, output.String(), `"status":"error"`)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	require.ErrorIs(t, runCIOutcomes(ctx, args, io.Discard, io.Discard, logger), context.Canceled)
}

type cliTransport func(*http.Request) (*http.Response, error)

func (f cliTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
