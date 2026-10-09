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
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
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
		{"ci-outcomes --help", "job START", false},
		{"ci-outcome", "unknown command or arguments", true},
		{"--unknown", "unknown command or arguments", true},
		{"--help unexpected", "unknown command or arguments", true},
		{"ci-outcomes --unknown", "flag provided but not defined", true},
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
	interval := "--release Presubmits --since 2026-10-08T00:00:00Z --until 2026-10-09T00:00:00Z --limit 10"
	for _, args := range []string{
		"", "--build-id 123 --release Presubmits", "--build-id 123 --limit 0", "--build-id 123 --workers 0",
		"--build-id 123 --workers -1", "--build-id 123 --dry-run=false", "--build-id 123 --dry-run --ingest",
		"--build-id 123 --inspect-artifacts --ingest", "--release Presubmits", "--since yesterday", "--unknown",
		interval + " --limit 0", interval + " --limit -1", interval + " --until 2026-10-08T00:00:00Z",
		interval + " --until 2026-10-07T00:00:00Z", interval + " positional",
	} {
		t.Run(args, func(t *testing.T) {
			_, _, err := parseCIOutcomes(append([]string{"--config", "runtime.yaml"}, strings.Fields(args)...), io.Discard)
			require.Error(t, err)
		})
	}
	_, _, err := parseCIOutcomes([]string{"--build-id", "123"}, io.Discard)
	require.ErrorContains(t, err, "--config")
	_, _, err = parseCIOutcomes([]string{"--config", "runtime.yaml", "--build-id", ""}, io.Discard)
	require.Error(t, err)
	for _, args := range []string{"--build-id 123", "--build-id 123 --dry-run", "--build-id 123 --inspect-artifacts", interval} {
		path, options, err := parseCIOutcomes(append([]string{"--config", "runtime.yaml"}, strings.Fields(args)...), io.Discard)
		require.NoError(t, err)
		require.Equal(t, "runtime.yaml", path)
		require.False(t, options.Ingest, "only explicit --ingest may write")
	}
	_, options, err := parseCIOutcomes([]string{"--config", "runtime.yaml", "--build-id", "123", "--build-id", "456", "--ingest", "--workers", "2"}, io.Discard)
	require.NoError(t, err)
	require.True(t, options.Ingest)
	require.Equal(t, []cijoboutcomes.BuildID{"123", "456"}, options.BuildIDs)
	require.Equal(t, 2, options.Workers)
	_, options, err = parseCIOutcomes(append([]string{"--config", "runtime.yaml"}, strings.Fields(interval+" --release aro-stage")...), io.Discard)
	require.NoError(t, err)
	require.Equal(t, []string{"Presubmits", "aro-stage"}, options.Releases)
	require.Equal(t, 24*time.Hour, options.Until.Sub(options.Since))
}

func TestCIOutcomesHelpNeedsNoConfig(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := runCIOutcomes(t.Context(), []string{"--help"}, &stdout, &stderr, slog.New(slog.NewTextHandler(io.Discard, nil)))
	require.NoError(t, err)
	require.Empty(t, stdout.String())
	require.Contains(t, stderr.String(), "job START")
}

func TestCIOutcomesRunsWithoutTenantSecretsOrAzureCredentials(t *testing.T) {
	originalCrash, originalHandlers := utilruntime.ReallyCrash, utilruntime.PanicHandlers
	t.Cleanup(func() {
		utilruntime.ReallyCrash, utilruntime.PanicHandlers = originalCrash, originalHandlers
		panicMetricsOnce = sync.Once{}
	})
	t.Setenv("AZURE_TOKEN_CREDENTIALS", "invalid-credential")
	t.Setenv("SECRETS_STORE_PATH", "/does-not-exist")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/jobs/runs", r.URL.Path)
		_, _ = w.Write([]byte(`{"rows":[]}`))
	}))
	defer server.Close()
	data, err := os.ReadFile("ci-outcomes.example.yaml")
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "runtime.yaml")
	require.NoError(t, os.WriteFile(path, bytes.ReplaceAll(data, []byte("https://sippy.dptools.openshift.org"), []byte(server.URL)), 0600))
	var output bytes.Buffer
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	args := []string{"--config", path, "--inspect-artifacts", "--release", "Presubmits", "--since", "2026-10-08T00:00:00Z", "--until", "2026-10-09T00:00:00Z", "--limit", "1"}
	require.NoError(t, runCIOutcomes(t.Context(), args, &output, io.Discard, logger))
	args = []string{"--config", path, "--inspect-artifacts", "--build-id", "123"}
	require.ErrorContains(t, runCIOutcomes(t.Context(), args, &output, io.Discard, logger), "metadata not found", "partial work errors propagate to main's nonzero exit")
	require.Contains(t, output.String(), `"status":"error"`)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	require.ErrorIs(t, runCIOutcomes(ctx, args, io.Discard, io.Discard, logger), context.Canceled)
}
