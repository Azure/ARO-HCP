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
	"testing"
	"time"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/agent"
	"github.com/go-logr/logr"
)

type fakeAnalysisSession struct {
	usage               agent.UsageReport
	savedPath           string
	disconnectWasCalled bool
	deleteWasCalled     bool
}

func (s *fakeAnalysisSession) SendAndWait(context.Context, string) (string, error) {
	return "", nil
}

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
	if gotMode := info.Mode().Perm(); gotMode != 0o644 {
		t.Errorf("usage report mode = %o, want 644", gotMode)
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
