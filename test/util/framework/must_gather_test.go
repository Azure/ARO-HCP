// Copyright 2025 Microsoft Corporation
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

package framework

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr/funcr"

	"github.com/Azure/ARO-Tools/config/types"
)

func TestMustGatherOnFailure(t *testing.T) {
	for _, testCase := range []struct {
		name              string
		passed            bool
		noResourceGroups  bool
		noArtifactDir     bool
		logDir            string
		usePath           bool
		binaryMissing     bool
		configUnavailable bool
		missingConfigKey  string
		noSubscription    bool
		subscriptionError bool
		runError          bool
		cancelDuringRun   bool
		noContent         bool
		archiveError      bool
		tempError         bool
		symlink           bool
		canceled          bool
		wantRuns          int
		wantLog           string
	}{
		{name: "failed test", wantRuns: 2, wantLog: "running hcpctl must-gather before resource deletion"},
		{name: "passed test", passed: true, wantLog: "test passed"},
		{name: "no resource groups", noResourceGroups: true, wantLog: "no resource group"},
		{name: "no output directory", noArtifactDir: true, wantLog: "ARTIFACT_DIR unset and test LogDirPath unavailable"},
		{name: "log directory fallback", noArtifactDir: true, logDir: "/test-logs", wantRuns: 2, wantLog: "ARTIFACT_DIR unset: using test LogDirPath"},
		{name: "binary from PATH", usePath: true, wantRuns: 2, wantLog: "running hcpctl must-gather"},
		{name: "binary unavailable", usePath: true, binaryMissing: true, wantLog: "HCPCTL_BINARY unset and hcpctl not in PATH"},
		{name: "service config unavailable", configUnavailable: true, wantLog: "kusto config missing (service config unavailable)"},
		{name: "kusto cluster missing", missingConfigKey: "kustoName", wantLog: "kusto config missing (kusto.kustoName)"},
		{name: "kusto region missing", missingConfigKey: "location", wantLog: "kusto config missing (kusto.location)"},
		{name: "subscription missing", noSubscription: true, wantLog: "subscription ID unavailable"},
		{name: "subscription lookup failed", subscriptionError: true, wantLog: "subscription ID unavailable"},
		{name: "command failures do not stop collection", runError: true, wantRuns: 2, wantLog: "hcpctl must-gather failed; continuing cleanup"},
		{name: "canceled subprocess preserves partial output", cancelDuringRun: true, wantRuns: 1, wantLog: "created must-gather archive"},
		{name: "no content", noContent: true, wantRuns: 2, wantLog: "no must-gather content produced; nothing to archive"},
		{name: "failed command without content", runError: true, noContent: true, wantRuns: 2, wantLog: "no must-gather content produced; nothing to archive"},
		{name: "archive failure", archiveError: true, wantRuns: 2, wantLog: "archiving must-gather output failed; continuing cleanup"},
		{name: "temporary directory failure", tempError: true, wantLog: "creating must-gather temporary directory failed; continuing cleanup"},
		{name: "partial archive removed on failure", symlink: true, wantRuns: 2, wantLog: "archiving must-gather output failed; continuing cleanup"},
		{name: "canceled collection", canceled: true, wantLog: "collection context expired"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var logs strings.Builder
			outputRoot := t.TempDir()
			logDir := ""
			if testCase.logDir != "" {
				logDir = outputRoot
			}
			if testCase.tempError {
				t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "missing"))
			}
			if testCase.archiveError {
				if err := os.WriteFile(filepath.Join(outputRoot, "must-gather"), nil, 0600); err != nil {
					t.Fatal(err)
				}
			}
			var tempDirs []string
			wantFiles := map[string]string{"summary.json": "summary", "nested/logs.txt": "partial diagnostic logs"}
			resourceGroups := []string{"first-rg", "second-rg"}
			if testCase.cancelDuringRun {
				resourceGroups = resourceGroups[:1]
			}
			if testCase.noResourceGroups {
				resourceGroups = nil
			}
			runs := 0
			collector := mustGatherCollector{
				getenv: func(name string) string {
					switch name {
					case "ARTIFACT_DIR":
						if !testCase.noArtifactDir {
							return outputRoot
						}
					case "HCPCTL_BINARY":
						if !testCase.usePath {
							return "/configured/hcpctl"
						}
					default:
						t.Fatalf("unexpected environment lookup: %s", name)
					}
					return ""
				},
				lookPath: func(name string) (string, error) {
					if !testCase.usePath || name != "hcpctl" {
						t.Fatalf("unexpected PATH lookup: %s", name)
					}
					if testCase.binaryMissing {
						return "", errors.New("not found")
					}
					return "/path/hcpctl", nil
				},
				serviceConfig: func() (types.Configuration, error) {
					if testCase.passed || testCase.noResourceGroups {
						t.Fatal("skipped test must not load service config")
					}
					if testCase.configUnavailable {
						return nil, errors.New("unavailable")
					}
					kusto := map[string]interface{}{"kustoName": "test-kusto", "location": "test-region"}
					delete(kusto, testCase.missingConfigKey)
					return types.Configuration{"kusto": kusto}, nil
				},
				subscriptionID: func(context.Context) (string, error) {
					if testCase.subscriptionError {
						return "", errors.New("unavailable")
					}
					if testCase.noSubscription {
						return "", nil
					}
					return "test-subscription", nil
				},
				run: func(ctx context.Context, binary string, args ...string) error {
					deadline, ok := ctx.Deadline()
					if !ok || time.Until(deadline) > 10*time.Minute || time.Until(deadline) <= 0 {
						t.Fatal("must-gather must have a positive timeout of at most ten minutes")
					}
					wantBinary := "/configured/hcpctl"
					if testCase.usePath {
						wantBinary = "/path/hcpctl"
					}
					if binary != wantBinary {
						t.Errorf("binary = %q, want %q", binary, wantBinary)
					}
					if len(args) != 12 || args[10] != "--output-path" {
						t.Fatalf("unexpected arguments: %q", args)
					}
					outputPath := args[11]
					if outputPath == outputRoot || strings.HasPrefix(outputPath, outputRoot+string(os.PathSeparator)) {
						t.Fatalf("temporary output %q must be outside artifacts %q", outputPath, outputRoot)
					}
					entries, err := os.ReadDir(outputPath)
					if err != nil || len(entries) != 0 {
						t.Fatalf("expected fresh empty temporary directory, entries=%v, err=%v", entries, err)
					}
					for _, previous := range tempDirs {
						if previous == outputPath {
							t.Fatalf("temporary directory reused: %s", outputPath)
						}
					}
					tempDirs = append(tempDirs, outputPath)
					wantArgs := []string{
						"must-gather", "query",
						"--subscription-id", "test-subscription",
						"--resource-group", resourceGroups[runs],
						"--kusto", "test-kusto",
						"--region", "test-region",
						"--output-path", outputPath,
					}
					if !reflect.DeepEqual(args, wantArgs) {
						t.Errorf("args = %q, want %q", args, wantArgs)
					}
					if !strings.Contains(logs.String(), resourceGroups[runs]) || !strings.Contains(logs.String(), outputPath) {
						t.Error("run must log resource group and output path before invoking hcpctl")
					}
					runs++
					if !testCase.noContent {
						for name, content := range wantFiles {
							path := filepath.Join(outputPath, name)
							if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
								t.Fatal(err)
							}
							if err := os.WriteFile(path, []byte(content), 0600); err != nil {
								t.Fatal(err)
							}
						}
					}
					if testCase.symlink {
						if err := os.Symlink(filepath.Join(outputPath, "summary.json"), filepath.Join(outputPath, "unsupported-link")); err != nil {
							t.Fatal(err)
						}
					}
					if testCase.cancelDuringRun {
						cancel()
						return ctx.Err()
					}
					if testCase.runError {
						return errors.New("command failed")
					}
					return nil
				},
				logger: funcr.New(func(prefix, args string) {
					logs.WriteString(prefix + args + "\n")
				}, funcr.Options{}),
			}
			if testCase.canceled {
				cancel()
			}
			collector.collect(ctx, !testCase.passed, resourceGroups, logDir)
			if runs != testCase.wantRuns {
				t.Errorf("ran must-gather %d times, want %d", runs, testCase.wantRuns)
			}
			if !strings.Contains(logs.String(), testCase.wantLog) {
				t.Errorf("logs = %q, want reason %q", logs.String(), testCase.wantLog)
			}
			for _, tempDir := range tempDirs {
				if _, err := os.Stat(tempDir); !os.IsNotExist(err) {
					t.Errorf("temporary directory remains: %s (err=%v)", tempDir, err)
				}
			}
			if testCase.archiveError {
				return
			}
			wantArchives := 0
			for _, resourceGroup := range resourceGroups {
				if _, err := os.Stat(filepath.Join(outputRoot, "must-gather", resourceGroup)); !os.IsNotExist(err) {
					t.Errorf("raw tree must not exist for %s: %v", resourceGroup, err)
				}
				archivePath := filepath.Join(outputRoot, "must-gather", resourceGroup+".zip")
				data, err := os.ReadFile(archivePath)
				if testCase.wantRuns == 0 || testCase.noContent || testCase.symlink {
					if !os.IsNotExist(err) {
						t.Errorf("unexpected archive %s (err=%v)", archivePath, err)
					}
					continue
				}
				if err != nil {
					t.Fatal(err)
				}
				wantArchives++
				reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
				if err != nil {
					t.Fatal(err)
				}
				gotFiles := map[string]string{}
				for _, entry := range reader.File {
					if entry.FileInfo().IsDir() {
						continue
					}
					file, err := entry.Open()
					if err != nil {
						t.Fatal(err)
					}
					content, readErr := io.ReadAll(file)
					if err := errors.Join(readErr, file.Close()); err != nil {
						t.Fatal(err)
					}
					gotFiles[entry.Name] = string(content)
				}
				if !reflect.DeepEqual(gotFiles, wantFiles) {
					t.Errorf("archive files = %v, want %v", gotFiles, wantFiles)
				}
				if !strings.Contains(logs.String(), "created must-gather archive") || !strings.Contains(logs.String(), archivePath) {
					t.Errorf("archive success and path not logged: %s", logs.String())
				}
			}
			entries, err := os.ReadDir(filepath.Join(outputRoot, "must-gather"))
			if err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			if len(entries) != wantArchives {
				t.Errorf("artifact directory has %d entries, want %d archives", len(entries), wantArchives)
			}
			for _, entry := range entries {
				if entry.IsDir() || filepath.Ext(entry.Name()) != ".zip" {
					t.Errorf("unexpected non-zip artifact: %s", entry.Name())
				}
			}
		})
	}
}

func TestArchiveMustGatherCanceled(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		timeout bool
		wantErr error
	}{
		{name: "already canceled", wantErr: context.Canceled},
		{name: "deadline exceeded", timeout: true, wantErr: context.DeadlineExceeded},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			sourceDir := t.TempDir()
			if err := os.WriteFile(filepath.Join(sourceDir, "partial.log"), []byte("partial diagnostics"), 0600); err != nil {
				t.Fatal(err)
			}
			archivePath := filepath.Join(t.TempDir(), "must-gather", "test-rg.zip")
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if testCase.timeout {
				ctx, cancel = context.WithTimeout(context.Background(), 0)
				defer cancel()
			}
			if err := archiveMustGather(ctx, sourceDir, archivePath); !errors.Is(err, testCase.wantErr) {
				t.Fatalf("archive error = %v, want %v", err, testCase.wantErr)
			}
			if _, err := os.Stat(archivePath); !os.IsNotExist(err) {
				t.Errorf("canceled archive must be removed: %v", err)
			}
		})
	}
}

func TestMustGatherCopyCanceledBetweenChunks(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reads := 0
	source := mustGatherReaderFunc(func(buffer []byte) (int, error) {
		reads++
		buffer[0] = 'x'
		cancel()
		return 1, nil
	})
	_, err := io.Copy(io.Discard, mustGatherContextReader{ctx: ctx, reader: source})
	if !errors.Is(err, context.Canceled) || reads != 1 {
		t.Errorf("copy must stop between chunks: err=%v, reads=%d", err, reads)
	}
}

func TestMustGatherWriteCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var destination bytes.Buffer
	written, err := (mustGatherContextWriter{ctx: ctx, writer: &destination}).Write([]byte("partial archive"))
	if !errors.Is(err, context.Canceled) || written != 0 || destination.Len() != 0 {
		t.Errorf("canceled writer must not write: written=%d, err=%v, content=%q", written, err, destination.String())
	}
}

type mustGatherReaderFunc func([]byte) (int, error)

func (reader mustGatherReaderFunc) Read(buffer []byte) (int, error) {
	return reader(buffer)
}
