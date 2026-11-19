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

package maintenance

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

const nativeHead = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func nativeSnapshotInput() map[string]any {
	return map[string]any{
		"head": nativeHead, "commits": []string{nativeHead},
		"comments":    []any{map[string]any{"id": 1, "body": "/hold\n\nRevision " + nativeHead + " was retested 3 times: holding\n", "user": map[string]any{"login": "openshift-merge-bot[bot]"}, "created_at": "2026-11-05T15:00:00Z", "updated_at": "2026-11-05T15:00:00Z"}},
		"hold_events": []any{map[string]any{"id": 1, "event": "labeled", "actor": map[string]any{"login": "openshift-ci[bot]"}, "created_at": "2026-11-05T15:00:00Z"}},
		"statuses":    []any{map[string]any{"id": 1, "context": "ci/job", "state": "success", "created_at": "2026-11-05T15:00:00Z"}},
		"checks":      []any{},
	}
}

func runNativeSnapshot(t *testing.T, input any) (map[string]any, error) {
	t.Helper()
	data, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	command := newSnapshotEvidenceCommand()
	var output bytes.Buffer
	command.SetIn(bytes.NewReader(data))
	command.SetOut(&output)
	command.SetErr(&bytes.Buffer{})
	err = command.Execute()
	if err != nil {
		if output.Len() != 0 {
			t.Fatalf("failure emitted successful JSON: %q", output.String())
		}
		return nil, err
	}
	var result map[string]any
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result, nil
}

func TestNativeSnapshotPolicyContract(t *testing.T) {
	for _, actor := range []string{"openshift-ci[bot]", "openshift-merge-bot[bot]", "reviewer", "unknown[bot]"} {
		t.Run(actor, func(t *testing.T) {
			input := nativeSnapshotInput()
			input["hold_events"].([]any)[0].(map[string]any)["actor"] = map[string]any{"login": actor}
			result, err := runNativeSnapshot(t, input)
			if err != nil {
				t.Fatal(err)
			}
			trusted := actor == "openshift-ci[bot]" || actor == "openshift-merge-bot[bot]"
			if result["trusted_hold_label"] != trusted {
				t.Fatalf("result=%v", result)
			}
			holds := result["active_holds"].([]any)
			if len(holds) != 1 || holds[0].(map[string]any)["kind"] != "retry-exhaustion" {
				t.Fatalf("holds=%v", holds)
			}
			if result["cancellation_pending"] != false {
				t.Fatalf("result=%v", result)
			}
			ci := result["ci"].(map[string]any)
			if len(ci["positive"].([]any)) != 1 || len(ci["blocked"].([]any)) != 0 {
				t.Fatalf("ci=%v", ci)
			}
		})
	}
}

func TestNativeSnapshotMalformedEvidenceFailsClosed(t *testing.T) {
	for _, field := range []string{"comments", "commits", "hold_events", "statuses", "checks", "head"} {
		for _, mode := range []string{"missing", "null", "object"} {
			t.Run(field+"/"+mode, func(t *testing.T) {
				input := nativeSnapshotInput()
				switch mode {
				case "missing":
					delete(input, field)
				case "null":
					input[field] = nil
				case "object":
					input[field] = map[string]any{}
				}
				if _, err := runNativeSnapshot(t, input); err == nil {
					t.Fatal("accepted missing/malformed evidence")
				}
			})
		}
	}
	for _, change := range []string{"comment body", "comment id bool", "comment time", "hold actor", "hold time", "status context", "status id", "check head", "check null", "conflicting comment", "conflicting label"} {
		t.Run(change, func(t *testing.T) {
			input := nativeSnapshotInput()
			comment := input["comments"].([]any)[0].(map[string]any)
			event := input["hold_events"].([]any)[0].(map[string]any)
			status := input["statuses"].([]any)[0].(map[string]any)
			switch change {
			case "comment body":
				delete(comment, "body")
			case "comment id bool":
				comment["id"] = true
			case "comment time":
				comment["created_at"] = "2026-01-01"
			case "hold actor":
				delete(event, "actor")
			case "hold time":
				event["created_at"] = "missing"
			case "status context":
				delete(status, "context")
			case "status id":
				status["id"] = 0
			case "check head":
				input["checks"] = []any{map[string]any{"name": "job", "head_sha": strings.Repeat("b", 40), "status": "completed", "conclusion": "success"}}
			case "check null":
				input["checks"] = []any{nil}
			case "conflicting comment":
				input["comments"] = append(input["comments"].([]any), map[string]any{"id": 1, "body": "/hold cancel", "user": map[string]any{"login": "human"}, "created_at": "2026-11-05T15:00:00Z"})
			case "conflicting label":
				input["hold_events"] = append(input["hold_events"].([]any), map[string]any{"id": 1, "event": "unlabeled", "actor": map[string]any{"login": "human"}, "created_at": "2026-11-05T15:00:00Z"})
			}
			if _, err := runNativeSnapshot(t, input); err == nil {
				t.Fatal("accepted malformed evidence")
			}
		})
	}
}

func TestNativeSnapshotEmptyArraysRemainArrays(t *testing.T) {
	input := nativeSnapshotInput()
	for _, field := range []string{"comments", "hold_events", "statuses", "checks"} {
		input[field] = []any{}
	}
	result, err := runNativeSnapshot(t, input)
	if err != nil {
		t.Fatal(err)
	}
	if result["active_holds"] == nil || result["ci"].(map[string]any)["positive"] == nil || result["ci"].(map[string]any)["blocked"] == nil {
		t.Fatalf("null arrays: %v", result)
	}
}

func TestMaintenanceCommandsExposeSharedContract(t *testing.T) {
	command, err := NewCommand("")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"assess", "release-hold", "verify-upgrade", "recover-retry", "release-regenerated", "compiler-versions", "snapshot-evidence", "inventory"} {
		found, _, err := command.Find([]string{name})
		if err != nil || found.Name() != name {
			t.Fatalf("missing %s", name)
		}
	}
	for _, mode := range []string{"assess", "release-hold", "verify-upgrade", "recover-retry", "release-regenerated"} {
		action, _, _ := command.Find([]string{mode})
		for _, flag := range []string{"repository", "number", "head", "kind", "evidence", "previous-head"} {
			if action.Flags().Lookup(flag) == nil {
				t.Fatalf("missing %s --%s", mode, flag)
			}
		}
	}
}
