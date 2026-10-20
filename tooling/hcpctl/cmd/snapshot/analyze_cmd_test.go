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

package snapshot

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/agent"
)

func TestValidateClaudeCodeProvider(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	dataDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dataDir, "manifest.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	opts := defaultAnalyzeOptions()
	opts.DataDir = dataDir
	opts.AROHCPPath = dataDir
	opts.HypershiftPath = dataDir
	opts.MaestroPath = dataDir
	opts.ClustersServicePath = dataDir
	cmd := &cobra.Command{}
	if err := bindAnalyzeOptions(opts, cmd); err != nil {
		t.Fatal(err)
	}
	if err := cmd.ParseFlags([]string{"--provider=claude-code", "--model=custom-model"}); err != nil {
		t.Fatal(err)
	}
	validated, err := opts.validate()
	if err != nil {
		t.Fatal(err)
	}
	if validated.claudeCodeConfig == nil || validated.claudeCodeConfig.Model != "custom-model" || validated.copilotConfig != nil || validated.claudeConfig != nil {
		t.Fatal("claude-code did not select its own configuration")
	}
	opts.AnthropicAPIKeyFile = filepath.Join(dataDir, "missing-key")
	if _, err := opts.validate(); err == nil || !strings.Contains(err.Error(), "--anthropic-api-key-file") {
		t.Fatalf("missing API key file not rejected: %v", err)
	}
	if err := os.WriteFile(opts.AnthropicAPIKeyFile, []byte("test-key"), 0o600); err != nil {
		t.Fatal(err)
	}
	validated, err = opts.validate()
	if err != nil || validated.claudeCodeConfig.APIKeyFile != opts.AnthropicAPIKeyFile {
		t.Fatalf("API key file not forwarded: %v", err)
	}
	if validated.claudeCodeConfig.Backend != agent.ClaudeBackendAPI {
		t.Fatal("default API backend not forwarded")
	}
}

func TestValidateClaudeBackends(t *testing.T) {
	dataDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dataDir, "manifest.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, provider := range []string{providerClaude, providerClaudeCode} {
		for _, test := range []struct {
			name                    string
			flags                   []string
			envProject, envRegion   string
			wantProject, wantRegion string
			wantErr                 string
		}{
			{name: "missing project", flags: []string{"--claude-backend=vertex", "--vertex-region=us-east5"}, wantErr: "--vertex-project"},
			{name: "missing region", flags: []string{"--claude-backend=vertex", "--vertex-project=test-project"}, wantErr: "--vertex-region"},
			{
				name:       "explicit vertex flags",
				flags:      []string{"--claude-backend=vertex", "--vertex-project=test-project", "--vertex-region=us-east5", "--anthropic-api-key-file=" + filepath.Join(dataDir, "unused-key")},
				envProject: "ignored-project", envRegion: "ignored-region", wantProject: "test-project", wantRegion: "us-east5",
			},
			{
				name: "vertex environment fallback", flags: []string{"--claude-backend=vertex"},
				envProject: "env-project", envRegion: "us-east5", wantProject: "env-project", wantRegion: "us-east5",
			},
			{name: "unknown backend", flags: []string{"--claude-backend=unsupported"}, wantErr: "unknown --claude-backend"},
		} {
			t.Run(provider+"/"+test.name, func(t *testing.T) {
				t.Setenv("GOOGLE_VERTEX_PROJECT_ID", test.envProject)
				t.Setenv("GOOGLE_VERTEX_REGION", test.envRegion)
				opts := defaultAnalyzeOptions()
				opts.DataDir = dataDir
				opts.AROHCPPath = dataDir
				opts.HypershiftPath = dataDir
				opts.MaestroPath = dataDir
				opts.ClustersServicePath = dataDir
				cmd := &cobra.Command{}
				if err := bindAnalyzeOptions(opts, cmd); err != nil {
					t.Fatal(err)
				}
				flags := append([]string{"--provider=" + provider, "--model=custom-model", "--anthropic-model=custom-anthropic-model"}, test.flags...)
				if err := cmd.ParseFlags(flags); err != nil {
					t.Fatal(err)
				}
				validated, err := opts.validate()
				if test.wantErr != "" {
					if err == nil || !strings.Contains(err.Error(), test.wantErr) {
						t.Fatalf("expected error containing %q, got %v", test.wantErr, err)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				if provider == providerClaudeCode {
					want := &agent.ClaudeCodeConfig{
						Backend: agent.ClaudeBackendVertex, VertexProject: test.wantProject, VertexRegion: test.wantRegion,
						Model: "custom-model", APIKeyFile: opts.AnthropicAPIKeyFile,
					}
					if !reflect.DeepEqual(validated.claudeCodeConfig, want) || validated.claudeConfig != nil {
						t.Fatalf("claude-code config = %+v, want %+v", validated.claudeCodeConfig, want)
					}
				} else {
					want := &agent.ClaudeConfig{
						Backend: agent.ClaudeBackendVertex, VertexProject: test.wantProject, VertexRegion: test.wantRegion,
						Model: "custom-anthropic-model", APIKeyFile: opts.AnthropicAPIKeyFile,
					}
					if !reflect.DeepEqual(validated.claudeConfig, want) || validated.claudeCodeConfig != nil {
						t.Fatalf("claude config = %+v, want %+v", validated.claudeConfig, want)
					}
				}
			})
		}
	}
}

type fakeAnalysisSession struct {
	usage               agent.UsageReport
	savedPath           string
	disconnectWasCalled bool
	deleteWasCalled     bool
}

func (s *fakeAnalysisSession) SendAndWait(context.Context, string) (string, error) {
	return "", nil
}

func (s *fakeAnalysisSession) ResetHistory() {}

func (s *fakeAnalysisSession) Usage() agent.UsageReport {
	return s.usage.Clone()
}

func (s *fakeAnalysisSession) SaveConversation(path string) {
	s.savedPath = path
}

func (s *fakeAnalysisSession) SessionID() string {
	return "test-session"
}

func (s *fakeAnalysisSession) Disconnect() error {
	s.disconnectWasCalled = true
	return nil
}

func (s *fakeAnalysisSession) Delete(context.Context) error {
	s.deleteWasCalled = true
	return nil
}

func TestWriteUsageReport(t *testing.T) {
	startedAt := time.Date(2026, time.August, 3, 17, 0, 0, 0, time.UTC)
	endedAt := startedAt.Add(5 * time.Minute)
	want := agent.UsageReport{
		StartedAt: startedAt,
		EndedAt:   endedAt,
		Requests:  1,
		Tokens: agent.TokenUsage{
			UncachedInputTokens:   100,
			CacheReadInputTokens:  200,
			CacheWriteInputTokens: 300,
			OutputTokens:          25,
			TotalTokens:           625,
		},
		Breakdown: []agent.UsageBreakdown{{
			Provider:   "anthropic",
			Model:      "claude-test",
			Dimensions: map[string]string{"backend": "api"},
			Requests:   1,
		}},
	}

	path := filepath.Join(t.TempDir(), "nested", "usage.json")
	if err := writeUsageReport(path, want); err != nil {
		t.Fatalf("writeUsageReport() error = %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading usage report: %v", err)
	}
	var got agent.UsageReport
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshaling usage report: %v", err)
	}

	if !got.StartedAt.Equal(startedAt) || !got.EndedAt.Equal(endedAt) {
		t.Errorf("usage interval = %s..%s, want %s..%s", got.StartedAt, got.EndedAt, startedAt, endedAt)
	}
	if got.Requests != want.Requests || !reflect.DeepEqual(got.Tokens, want.Tokens) {
		t.Errorf("usage totals = %+v, want %+v", got, want)
	}
	if len(got.Breakdown) != 1 || got.Breakdown[0].Dimensions["backend"] != "api" {
		t.Errorf("usage breakdown = %+v, want Anthropic API entry", got.Breakdown)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat usage report: %v", err)
	}
	gotMode := info.Mode().Perm()
	if gotMode&0o600 != 0o600 {
		t.Errorf("usage report mode = %o, want owner read/write permissions", gotMode)
	}
	if unexpected := gotMode &^ 0o644; unexpected != 0 {
		t.Errorf("usage report mode = %o, includes unexpected permissions %o", gotMode, unexpected)
	}
}

func TestWriteUsageReportReturnsDirectoryError(t *testing.T) {
	parentFile := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(parentFile, []byte("content"), 0o600); err != nil {
		t.Fatalf("creating parent file: %v", err)
	}

	err := writeUsageReport(filepath.Join(parentFile, "usage.json"), agent.UsageReport{})
	if err == nil {
		t.Fatal("writeUsageReport() error = nil, want directory creation error")
	}
}

func TestFinalizeAnalysisSessionPreservesUsageOnError(t *testing.T) {
	startedAt := time.Date(2026, time.August, 3, 17, 0, 0, 0, time.UTC)
	endedAt := startedAt.Add(5 * time.Minute)
	session := &fakeAnalysisSession{usage: agent.UsageReport{
		Requests: 2,
		Tokens: agent.TokenUsage{
			UncachedInputTokens: 100,
			OutputTokens:        25,
			TotalTokens:         125,
		},
	}}
	originalErr := errors.New("analysis failed")
	runErr := originalErr
	outputDir := t.TempDir()

	finalizeAnalysisSession(context.Background(), logr.Discard(), session, outputDir, startedAt, endedAt, &runErr)

	if !errors.Is(runErr, originalErr) {
		t.Fatalf("finalizeAnalysisSession() error = %v, want original analysis error", runErr)
	}
	if !session.disconnectWasCalled || session.deleteWasCalled {
		t.Errorf("session cleanup = disconnect:%t delete:%t, want disconnect only", session.disconnectWasCalled, session.deleteWasCalled)
	}
	if wantPath := filepath.Join(outputDir, "conversation.json"); session.savedPath != wantPath {
		t.Errorf("conversation path = %q, want %q", session.savedPath, wantPath)
	}

	data, err := os.ReadFile(filepath.Join(outputDir, "usage.json"))
	if err != nil {
		t.Fatalf("reading preserved usage report: %v", err)
	}
	var got agent.UsageReport
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshaling preserved usage report: %v", err)
	}
	if got.Requests != 2 || got.Tokens.TotalTokens != 125 {
		t.Errorf("preserved usage = %+v, want requests=2 totalTokens=125", got)
	}
	if !got.StartedAt.Equal(startedAt) || !got.EndedAt.Equal(endedAt) {
		t.Errorf("preserved interval = %s..%s, want %s..%s", got.StartedAt, got.EndedAt, startedAt, endedAt)
	}
}

func TestFinalizeAnalysisSessionReturnsUsageWriteError(t *testing.T) {
	outputDir := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(outputDir, []byte("content"), 0o600); err != nil {
		t.Fatalf("creating output path: %v", err)
	}

	session := &fakeAnalysisSession{}
	var runErr error
	finalizeAnalysisSession(context.Background(), logr.Discard(), session, outputDir, time.Now(), time.Now(), &runErr)

	if runErr == nil {
		t.Fatal("finalizeAnalysisSession() error = nil, want usage write error")
	}
	if !session.disconnectWasCalled || session.deleteWasCalled {
		t.Errorf("session cleanup = disconnect:%t delete:%t, want disconnect only", session.disconnectWasCalled, session.deleteWasCalled)
	}
}

// newDynamicTestOptions returns analyze options bound to a command with
// worktree paths pointing at an existing directory, ready for flag parsing.
func newDynamicTestOptions(t *testing.T) (*RawAnalyzeOptions, *cobra.Command) {
	t.Helper()
	repo := t.TempDir()
	opts := defaultAnalyzeOptions()
	opts.AROHCPPath = repo
	opts.HypershiftPath = repo
	opts.MaestroPath = repo
	opts.ClustersServicePath = repo
	cmd := &cobra.Command{}
	if err := bindAnalyzeOptions(opts, cmd); err != nil {
		t.Fatal(err)
	}
	return opts, cmd
}

func TestValidateDynamicMode(t *testing.T) {
	objectiveFile := filepath.Join(t.TempDir(), "objective.txt")
	if err := os.WriteFile(objectiveFile, []byte("\n  nodepool np-1 stuck scaling  \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	emptyFile := filepath.Join(t.TempDir(), "empty.txt")
	if err := os.WriteFile(emptyFile, []byte(" \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshotDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(snapshotDir, "manifest.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	dynamicFlags := []string{"--kusto=my-adx", "--region=eastus", "--output=" + t.TempDir()}

	tests := []struct {
		name          string
		dataDir       string
		flags         []string
		wantErr       string
		wantObjective string
	}{
		{
			name:          "inline objective without snapshot",
			flags:         append([]string{"--dynamic=nodepool np-1 stuck scaling"}, dynamicFlags...),
			wantObjective: "nodepool np-1 stuck scaling",
		},
		{
			name:          "objective file is trimmed",
			flags:         append([]string{"--dynamic-file=" + objectiveFile}, dynamicFlags...),
			wantObjective: "nodepool np-1 stuck scaling",
		},
		{
			name:    "empty objective file",
			flags:   append([]string{"--dynamic-file=" + emptyFile}, dynamicFlags...),
			wantErr: "is empty",
		},
		{
			name:    "missing objective file",
			flags:   append([]string{"--dynamic-file=" + filepath.Join(t.TempDir(), "missing")}, dynamicFlags...),
			wantErr: "failed to read --dynamic-file",
		},
		{
			name:    "dynamic rejects a data dir",
			dataDir: snapshotDir,
			flags:   append([]string{"--dynamic=x"}, dynamicFlags...),
			wantErr: "do not take a <data-dir>",
		},
		{
			name:    "dynamic requires output",
			flags:   []string{"--dynamic=x", "--kusto=my-adx", "--region=eastus"},
			wantErr: "--output is required",
		},
		{
			name:    "dynamic requires kusto",
			flags:   []string{"--dynamic=x", "--region=eastus", "--output=out"},
			wantErr: "--kusto and --region are required",
		},
		{
			name:    "dynamic requires region",
			flags:   []string{"--dynamic=x", "--kusto=my-adx", "--output=out"},
			wantErr: "--kusto and --region are required",
		},
		{
			name:    "empty database name",
			flags:   append([]string{"--dynamic=x", "--hcp-database="}, dynamicFlags...),
			wantErr: "--hcp-database must not be empty",
		},
		{
			name:    "kusto flags require dynamic mode",
			dataDir: snapshotDir,
			flags:   []string{"--kusto=my-adx", "--region=eastus"},
			wantErr: "require --dynamic or --dynamic-file",
		},
		{
			name:    "database flags require dynamic mode",
			dataDir: snapshotDir,
			flags:   []string{"--service-database=Other"},
			wantErr: "require --dynamic or --dynamic-file",
		},
		{
			name:    "snapshot modes still require a data dir",
			flags:   []string{"--intent=x"},
			wantErr: "data-dir argument is required",
		},
		{
			name:    "via-kusto still requires via-region",
			flags:   append([]string{"--dynamic=x", "--via-kusto=hub"}, dynamicFlags...),
			wantErr: "--via-region is required",
		},
	}
	for _, mutex := range [][]string{
		{"--intent=a", "--intent-file=" + objectiveFile},
		{"--intent=a", "--dynamic=b"},
		{"--intent=a", "--dynamic-file=" + objectiveFile},
		{"--intent-file=" + objectiveFile, "--dynamic=b"},
		{"--intent-file=" + objectiveFile, "--dynamic-file=" + objectiveFile},
		{"--dynamic=a", "--dynamic-file=" + objectiveFile},
	} {
		tests = append(tests, struct {
			name          string
			dataDir       string
			flags         []string
			wantErr       string
			wantObjective string
		}{
			name:    "mutually exclusive " + strings.Join(mutex, " "),
			flags:   append(mutex, dynamicFlags...),
			wantErr: "mutually exclusive",
		})
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts, cmd := newDynamicTestOptions(t)
			opts.DataDir = tt.dataDir
			if err := cmd.ParseFlags(tt.flags); err != nil {
				t.Fatal(err)
			}
			validated, err := opts.validate()
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !validated.dynamic || validated.dataDir != "" {
				t.Fatalf("expected dynamic mode without a data dir, got dynamic=%v dataDir=%q", validated.dynamic, validated.dataDir)
			}
			if validated.intent != tt.wantObjective {
				t.Errorf("objective = %q, want %q", validated.intent, tt.wantObjective)
			}
			if got := validated.kustoEndpoint.String(); got != "https://my-adx.eastus.kusto.windows.net" {
				t.Errorf("kusto endpoint = %q", got)
			}
			if validated.serviceDatabase != defaultServiceDatabase ||
				validated.hcpDatabase != defaultHCPDatabase ||
				validated.monitoringEventsDatabase != defaultMonitoringEventsDatabase {
				t.Errorf("unexpected default databases: %q %q %q", validated.serviceDatabase, validated.hcpDatabase, validated.monitoringEventsDatabase)
			}
		})
	}
}

func TestValidateDynamicModeCustomDatabases(t *testing.T) {
	opts, cmd := newDynamicTestOptions(t)
	if err := cmd.ParseFlags([]string{
		"--dynamic=x", "--kusto=my-adx", "--region=eastus", "--output=out",
		"--service-database=Svc", "--hcp-database=Hcp", "--monitoring-events-database=Events",
	}); err != nil {
		t.Fatal(err)
	}
	validated, err := opts.validate()
	if err != nil {
		t.Fatal(err)
	}
	if validated.serviceDatabase != "Svc" || validated.hcpDatabase != "Hcp" || validated.monitoringEventsDatabase != "Events" {
		t.Errorf("custom databases not forwarded: %q %q %q", validated.serviceDatabase, validated.hcpDatabase, validated.monitoringEventsDatabase)
	}
	if validated.outputDir != "out" {
		t.Errorf("outputDir = %q, want out", validated.outputDir)
	}
}

func TestAnalyzeCommandArgs(t *testing.T) {
	cmd, err := newAnalyzeCommand()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Args(cmd, nil); err != nil {
		t.Errorf("dynamic mode must accept zero positional arguments: %v", err)
	}
	if err := cmd.Args(cmd, []string{"./data"}); err != nil {
		t.Errorf("snapshot modes must accept a data dir: %v", err)
	}
	if err := cmd.Args(cmd, []string{"a", "b"}); err == nil {
		t.Errorf("expected more than one positional argument to be rejected")
	}
}

func TestSetupAnalysisWorkspaceDynamic(t *testing.T) {
	repo := t.TempDir()
	workspace, cleanup, err := setupAnalysisWorkspace("", map[string]string{"ARO-HCP": repo}, true)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	if _, err := os.Lstat(filepath.Join(workspace, "data")); !os.IsNotExist(err) {
		t.Errorf("dynamic workspace must not contain a data directory: %v", err)
	}
	if target, err := os.Readlink(filepath.Join(workspace, "src", "ARO-HCP")); err != nil || target != repo {
		t.Errorf("source worktree not linked: %q %v", target, err)
	}
	libraryDir := filepath.Join(workspace, queryLibraryWorkspaceDir)
	for _, want := range []string{"INDEX.md", "partials/bundleMap.kql"} {
		if _, err := os.Stat(filepath.Join(libraryDir, want)); err != nil {
			t.Errorf("query library missing %s: %v", want, err)
		}
	}
	var kql, readmes int
	if err := filepath.WalkDir(libraryDir, func(_ string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		switch {
		case strings.HasSuffix(d.Name(), ".kql"):
			kql++
		case d.Name() == "README.md":
			readmes++
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if kql == 0 || readmes == 0 {
		t.Errorf("query library incomplete: %d KQL templates, %d READMEs", kql, readmes)
	}

	cleanup()
	if _, err := os.Stat(workspace); !os.IsNotExist(err) {
		t.Errorf("cleanup did not remove the workspace: %v", err)
	}
}

func TestSetupAnalysisWorkspaceSnapshot(t *testing.T) {
	dataDir := t.TempDir()
	workspace, cleanup, err := setupAnalysisWorkspace(dataDir, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if target, err := os.Readlink(filepath.Join(workspace, "data")); err != nil || target != dataDir {
		t.Errorf("data directory not linked: %q %v", target, err)
	}
	if _, err := os.Stat(filepath.Join(workspace, queryLibraryWorkspaceDir)); !os.IsNotExist(err) {
		t.Errorf("snapshot workspace must not contain the query library: %v", err)
	}
}
