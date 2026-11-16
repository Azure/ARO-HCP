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

package safeoutput

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Azure/ARO-HCP/tooling/depctl/internal/forge"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/policy"
)

func proposal(number int, mutate func(*forge.PullRequest)) forge.PullRequest {
	pull := forge.PullRequest{
		Number: number, Author: policy.ToolOwner, State: "open", BaseRef: policy.ToolBase,
		Title: policy.ToolTitlePrefix + "bump yq to 4.54.2", Body: policy.ToolBodyMarker + " mikefarah/yq -->",
		Labels: []string{policy.ToolLabel},
	}
	if mutate != nil {
		mutate(&pull)
	}
	return pull
}

func artifact(t *testing.T, items string) *Artifact {
	t.Helper()
	parsed, err := Parse([]byte(`{"items":` + items + `}`))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	return parsed
}

func TestParseRejectsIncompleteEnvelopes(t *testing.T) {
	cases := map[string]string{
		"not an object":     `[]`,
		"errors reported":   `{"errors":[{"type":"x"}],"items":[]}`,
		"missing items":     `{"errors":[]}`,
		"items not a list":  `{"items":{}}`,
		"item without type": `{"items":[{}]}`,
		"item type numeric": `{"items":[{"type":7}]}`,
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse([]byte(content)); !errors.Is(err, ErrInvalidArtifact) {
				t.Fatalf("Parse() error = %v, want ErrInvalidArtifact", err)
			}
		})
	}
}

func TestValidateRequiresCompleteProposalFields(t *testing.T) {
	cases := map[string]string{
		"missing branch": `[{"type":"create_pull_request","title":"t","body":"b"}]`,
		"blank title":    `[{"type":"create_pull_request","branch":"b","title":" ","body":"b"}]`,
		"missing body":   `[{"type":"create_pull_request","branch":"b","title":"t"}]`,
		"other repo":     `[{"type":"create_pull_request","branch":"b","title":"t","body":"b","repo":"other/repo"}]`,
		"null repo":      `[{"type":"create_pull_request","branch":"b","title":"t","body":"b","repo":null}]`,
		"other base":     `[{"type":"create_pull_request","branch":"b","title":"t","body":"b","base":"release"}]`,
		"null base":      `[{"type":"create_pull_request","branch":"b","title":"t","body":"b","base":null}]`,
	}
	for name, items := range cases {
		t.Run(name, func(t *testing.T) {
			if err := artifact(t, items).Validate("Azure/ARO-HCP"); !errors.Is(err, ErrInvalidArtifact) {
				t.Fatalf("Validate() error = %v, want ErrInvalidArtifact", err)
			}
		})
	}
	allowed := []string{
		`[{"type":"create_pull_request","branch":"b","title":"t","body":"b"}]`,
		`[{"type":"create_pull_request","branch":"b","title":"t","body":"b","repo":"azure/aro-hcp","base":""}]`,
		`[{"type":"noop","message":"nothing to do"}]`,
	}
	for _, items := range allowed {
		if err := artifact(t, items).Validate("Azure/ARO-HCP"); err != nil {
			t.Fatalf("Validate(%s) error = %v", items, err)
		}
	}
}

func TestGuardDefersWhileAnOwnedProposalIsOpen(t *testing.T) {
	fake := forge.NewFake()
	fake.Seed(proposal(42, nil), forge.Snapshot{})
	parsed := artifact(t, `[{"type":"create_pull_request","branch":"b","title":"t","body":"b"}]`)
	result, err := Guard(context.Background(), fake, "Azure/ARO-HCP", parsed)
	if err != nil {
		t.Fatalf("Guard() error = %v", err)
	}
	if !result.Deferred || len(result.Active) != 1 || result.Active[0] != 42 {
		t.Fatalf("result = %+v", result)
	}
	content, err := parsed.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	var written struct {
		Items []struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"items"`
	}
	if err := json.Unmarshal(content, &written); err != nil {
		t.Fatal(err)
	}
	if len(written.Items) != 1 || written.Items[0].Type != Noop || written.Items[0].Message != "Waiting for existing tooling PR(s): #42" {
		t.Fatalf("written items = %+v", written.Items)
	}
}

func TestGuardRecognizesTheFamilyMarkerBeforeLabeling(t *testing.T) {
	fake := forge.NewFake()
	fake.Seed(proposal(43, func(pull *forge.PullRequest) { pull.Labels = nil }), forge.Snapshot{})
	parsed := artifact(t, `[{"type":"create_pull_request","branch":"b","title":"t","body":"b"}]`)
	result, err := Guard(context.Background(), fake, "Azure/ARO-HCP", parsed)
	if err != nil || !result.Deferred {
		t.Fatalf("Guard() = %+v, %v", result, err)
	}
}

func TestGuardIgnoresUnrelatedProposalsAndBases(t *testing.T) {
	fake := forge.NewFake()
	fake.Seed(proposal(44, func(pull *forge.PullRequest) { pull.Author = "someone[bot]" }), forge.Snapshot{})
	fake.Seed(proposal(45, func(pull *forge.PullRequest) { pull.BaseRef = "release" }), forge.Snapshot{})
	fake.Seed(proposal(46, func(pull *forge.PullRequest) {
		pull.Labels = nil
		pull.Title = "chore(tools): ordinary maintenance"
		pull.Body = ""
	}), forge.Snapshot{})
	parsed := artifact(t, `[{"type":"create_pull_request","branch":"b","title":"t","body":"b"}]`)
	result, err := Guard(context.Background(), fake, "Azure/ARO-HCP", parsed)
	if err != nil {
		t.Fatalf("Guard() error = %v", err)
	}
	if result.Deferred || len(result.Active) != 0 {
		t.Fatalf("result = %+v", result)
	}
	if !parsed.HasCreate() {
		t.Fatal("an unblocked artifact must keep its create request")
	}
}

func TestDeferKeepsOtherItemsAndSkipsTheNoop(t *testing.T) {
	parsed := artifact(t, `[{"type":"create_pull_request","branch":"b","title":"t","body":"b"},{"type":"add_labels","labels":["x"]}]`)
	parsed.Defer([]int{9})
	content, err := parsed.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	var written struct {
		Items []struct {
			Type string `json:"type"`
		} `json:"items"`
	}
	if err := json.Unmarshal(content, &written); err != nil {
		t.Fatal(err)
	}
	if len(written.Items) != 1 || written.Items[0].Type != "add_labels" {
		t.Fatalf("items = %+v", written.Items)
	}
}

func TestWriteArtifactKeepsTheOriginalAndCleansUpOnFailure(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "output.json")
	original := []byte(`{"items":[]}`)
	if err := os.WriteFile(path, original, 0o644); err != nil {
		t.Fatal(err)
	}
	// An invalid raw item makes serialization fail before a temporary file exists.
	broken := &Artifact{
		fields: map[string]json.RawMessage{"items": json.RawMessage(`[]`)},
		items:  []json.RawMessage{json.RawMessage(`{`)},
	}
	if err := WriteArtifact(path, broken); err == nil {
		t.Fatal("WriteArtifact() must fail on a malformed item")
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != string(original) {
		t.Fatalf("original changed on failure: %s", content)
	}
	assertNoTemporaryFiles(t, directory)

	// Replacing a directory fails after the temporary file was written.
	target := filepath.Join(directory, "target-directory")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	valid, err := Parse(original)
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteArtifact(target, valid); err == nil {
		t.Fatal("WriteArtifact() must fail when the destination is a directory")
	}
	if info, err := os.Stat(target); err != nil || !info.IsDir() {
		t.Fatalf("destination changed on failure: %v", err)
	}
	assertNoTemporaryFiles(t, directory)
}

// Fails when a rewrite left a temporary file behind.
func assertNoTemporaryFiles(t *testing.T, directory string) {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "depctl-guard-") {
			t.Fatalf("temporary file left behind: %s", entry.Name())
		}
	}
}

func TestWriteArtifactPreservesUnknownFields(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "output.json")
	original := `{"errors":[],"items":[{"type":"create_pull_request","branch":"b","title":"t","body":"b"}],"unknown":{"kept":true}}`
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	parsed, err := Parse([]byte(original))
	if err != nil {
		t.Fatal(err)
	}
	parsed.Defer([]int{7})
	if err := WriteArtifact(path, parsed); err != nil {
		t.Fatalf("WriteArtifact() error = %v", err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var written map[string]json.RawMessage
	if err := json.Unmarshal(content, &written); err != nil {
		t.Fatal(err)
	}
	var unknown struct {
		Kept bool `json:"kept"`
	}
	if err := json.Unmarshal(written["unknown"], &unknown); err != nil || !unknown.Kept {
		t.Fatalf("unknown field = %s", written["unknown"])
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o, want 600", info.Mode().Perm())
	}
}
