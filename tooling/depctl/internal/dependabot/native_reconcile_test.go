// Copyright 2026 Microsoft Corporation
// Licensed under the Apache License, Version 2.0.

package dependabot

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/google/go-github/v69/github"

	"github.com/Azure/ARO-HCP/tooling/depctl/internal/policy"
)

const nativeReconcileFailure = "https://prow.ci.openshift.org/view/failed"
const nativeReconcileEvidence = "Resolved transient infrastructure issue shown by " + nativeReconcileFailure + "; verified in https://prow.ci.openshift.org/view/comparison."

type nativeReconcileFixture struct {
	*nativeInventoryTestFixture
	writes         []string
	ciReads        int
	beforeCI       func(*nativeReconcileFixture)
	invalidReceipt bool
}

// Publisher exposes separate real SDK transports and allows only the fixed issue-comment write.
func (f *nativeReconcileFixture) publisher() *NativePublisher {
	f.t.Helper()
	client := func(handler http.HandlerFunc) *github.Client {
		server := httptest.NewServer(handler)
		f.t.Cleanup(server.Close)
		api := github.NewClient(server.Client())
		api.BaseURL, _ = url.Parse(server.URL + "/")
		return api
	}
	reader := client(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Has("recursive") {
			f.t.Errorf("unexpected recursive tree query: %s", r.URL)
		}
		f.serve(w, r, false)
	})
	ci := client(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/statuses") {
			f.ciReads++
			if f.beforeCI != nil {
				f.beforeCI(f)
			}
		}
		f.serve(w, r, true)
	})
	writer := client(func(w http.ResponseWriter, r *http.Request) {
		const prefix = "/repos/" + nativeInventoryTestRepository + "/issues/"
		rawNumber, prefixOK := strings.CutPrefix(r.URL.Path, prefix)
		rawNumber, suffixOK := strings.CutSuffix(rawNumber, "/comments")
		number, err := strconv.Atoi(rawNumber)
		if r.Method != http.MethodPost || !prefixOK || !suffixOK || err != nil {
			f.t.Errorf("unauthorized writer request: %s %s", r.Method, r.URL)
			http.NotFound(w, r)
			return
		}
		var input map[string]string
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil || len(input) != 1 || input["body"] == "" {
			f.t.Errorf("unexpected comment payload: %v (%v)", input, err)
			http.Error(w, "malformed comment", http.StatusBadRequest)
			return
		}
		f.writes = append(f.writes, input["body"])
		f.addComment(number, nativePublicationRobot, input["body"])
		w.Header().Set("Content-Type", "application/json")
		if f.invalidReceipt {
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 0, "body": input["body"]})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 100, "body": input["body"]})
	})
	p, err := NewNativePublisher(nativeInventoryTestRepository, WithNativePublicationClients(reader, ci, writer))
	if err != nil {
		f.t.Fatal(err)
	}
	return p
}

// Tree models only the requested direct directory entries; unrelated subtrees remain unreachable.
func (f *nativeReconcileFixture) tree(ref string, entries ...map[string]any) {
	if entries == nil {
		entries = []map[string]any{}
	}
	f.set("git/trees/"+ref, map[string]any{"truncated": false, "tree": entries})
}

// NativeReconcileTreeEntry gives each fixture directory or file an immutable object ID.
func nativeReconcileTreeEntry(path, kind, mode, sha string) map[string]any {
	return map[string]any{"path": path, "mode": mode, "type": kind, "sha": sha}
}

// ActionSource supplies an immutable official update to a handwritten workflow.
func (f *nativeReconcileFixture) actionSource() {
	file := ".github/workflows/unit.yml"
	f.addPull(1, nativePublicationBot, "dependabot/actions", "native Actions pin", "", file, false)
	current, previous := nativeInventoryTestSHA(107), nativeInventoryTestSHA(106)
	f.set("contents/"+file+"?ref="+nativeInventoryTestSHA(0), nativePublicationFixtureBlob("    - uses: actions/checkout@"+previous+" # v4.1.0\n"))
	f.set("contents/"+file+"?ref="+nativeInventoryTestSHA(1), nativePublicationFixtureBlob("    - uses: actions/checkout@"+current+" # v4.2.0\n"))
	f.set("/repos/actions/checkout/tags", []any{map[string]any{"name": "v4.2.0", "commit": map[string]any{"sha": current}}})
	githubTree, workflowsTree := nativeInventoryTestSHA(200), nativeInventoryTestSHA(201)
	for _, ref := range []string{nativeInventoryTestSHA(0), nativeInventoryTestSHA(1)} {
		f.tree(ref, nativeReconcileTreeEntry(".github", "tree", "040000", githubTree), nativeReconcileTreeEntry("vendor", "tree", "040000", nativeInventoryTestSHA(999)))
	}
	f.tree(githubTree, nativeReconcileTreeEntry("workflows", "tree", "040000", workflowsTree))
	f.tree(workflowsTree, nativeReconcileTreeEntry("unit.yml", "blob", "100644", nativeInventoryTestSHA(202)))
}

// GeneratedActionSource records a compiled pin change whose editable source must independently cover the target.
func (f *nativeReconcileFixture) generatedActionSource(sourcePin string) {
	f.actionSource()
	main, head := nativeInventoryTestSHA(0), nativeInventoryTestSHA(1)
	workflowsTree, headGitHubTree, headWorkflowsTree := nativeInventoryTestSHA(201), nativeInventoryTestSHA(203), nativeInventoryTestSHA(204)
	blob := nativeInventoryTestSHA(202)
	f.set("pulls/1/files", []any{map[string]any{"filename": ".github/workflows/unit.lock.yml", "status": "modified"}})
	for _, ref := range []string{main, head} {
		f.set("contents/.github/workflows/unit.lock.yml?ref="+ref, f.api["contents/.github/workflows/unit.yml?ref="+ref].body)
	}
	lockEntry := nativeReconcileTreeEntry("unit.lock.yml", "blob", "100644", blob)
	sourceEntry := nativeReconcileTreeEntry("unit.md", "blob", "100644", blob)
	f.tree(workflowsTree, lockEntry, sourceEntry)
	f.tree(head, nativeReconcileTreeEntry(".github", "tree", "040000", headGitHubTree))
	f.tree(headGitHubTree, nativeReconcileTreeEntry("workflows", "tree", "040000", headWorkflowsTree))
	if sourcePin == "" {
		f.tree(headWorkflowsTree, lockEntry)
		return
	}
	f.tree(headWorkflowsTree, lockEntry, sourceEntry)
	f.set("contents/.github/workflows/unit.md?ref="+head, nativePublicationFixtureBlob("    - uses: actions/checkout@"+sourcePin+"\n"))
}

// NPMSource records a native manifest bump and optionally an attributed lock consumer.
func (f *nativeReconcileFixture) npmSource(withLock bool) {
	f.addPull(1, nativePublicationBot, "dependabot/npm", "native npm", "", "package.json", false)
	head := nativeInventoryTestSHA(1)
	entry := nativeReconcileTreeEntry("package.json", "blob", "100644", nativeInventoryTestSHA(200))
	for _, version := range []struct {
		number  int
		version string
	}{{0, "1.0.0"}, {1, "1.1.0"}} {
		ref := nativeInventoryTestSHA(version.number)
		f.set("contents/package.json?ref="+ref, nativePublicationFixtureBlob(fmt.Sprintf(`{"dependencies":{"lodash":%q}}`, version.version)))
		if withLock {
			f.set("contents/package-lock.json?ref="+ref, nativePublicationFixtureBlob(fmt.Sprintf(`{"lockfileVersion":3,"packages":{"node_modules/lodash":{"version":%q}}}`, version.version)))
		}
	}
	if !withLock {
		f.tree(head, entry)
		return
	}
	f.api["pulls/1"].body.(map[string]any)["changed_files"] = 2
	f.set("pulls/1/files", []any{
		map[string]any{"filename": "package.json", "status": "modified"},
		map[string]any{"filename": "package-lock.json", "status": "modified"},
	})
	f.tree(head, entry, nativeReconcileTreeEntry("package-lock.json", "blob", "100644", nativeInventoryTestSHA(201)))
}

// RetryHold supplies the exact unedited merge-bot evidence and trusted label provenance.
func (f *nativeReconcileFixture) retryHold(number int) {
	f.addComment(number, policy.MergeBot, fmt.Sprintf("/hold\n\nRevision %s was retested 3 times: holding", nativeInventoryTestSHA(number)))
	pull := f.api[fmt.Sprintf("pulls/%d", number)].body.(map[string]any)
	pull["labels"] = append(pull["labels"].([]any), map[string]any{"name": policy.HoldLabel})
	f.set(fmt.Sprintf("issues/%d/events", number), []any{map[string]any{
		"id": 1, "event": "labeled", "actor": map[string]any{"login": policy.ProwBot},
		"label": map[string]any{"name": policy.HoldLabel}, "created_at": nativeInventoryTestTime,
	}})
}

// Statuses supplies current-head required status evidence to the separate CI reader.
func (f *nativeReconcileFixture) statuses(number int, pipeline, e2e string) {
	f.ci["commits/"+nativeInventoryTestSHA(number)+"/statuses"] = nativeInventoryTestResponse{body: []any{
		map[string]any{"id": 1, "context": "ci/pipeline-gate", "state": pipeline, "created_at": nativeInventoryTestTime},
		map[string]any{"id": 2, "context": "ci/prow/e2e-parallel", "state": e2e, "target_url": nativeReconcileFailure, "created_at": nativeInventoryTestTime},
	}}
}

// MergedReplacement proves ancestry and version coverage at the replacement, merge, and main.
func (f *nativeReconcileFixture) mergedReplacement() {
	pull := f.addReplacement(100, true, nativePublicationSource{Number: 1, HeadSHA: nativeInventoryTestSHA(1)})
	merge, main, directory := nativeInventoryTestSHA(101), nativeInventoryTestSHA(0), nativeInventoryTestSHA(200)
	before := nativeInventoryTestSHA(102)
	f.set("contents/backend/go.mod?ref="+before, f.api["contents/backend/go.mod?ref="+main].body)
	f.set("compare/"+main+"..."+nativeInventoryTestSHA(1), map[string]any{"merge_base_commit": map[string]any{"sha": before}})
	pull["merge_commit_sha"] = merge
	f.set("compare/"+merge+"..."+main, map[string]any{"status": "ahead", "merge_base_commit": map[string]any{"sha": merge}})
	covered := nativePublicationFixtureBlob("module example.test/component\n\ngo 1.26.0\n\nrequire example.com/library v1.2.0\n")
	for _, ref := range []string{nativeInventoryTestSHA(100), merge, main} {
		f.tree(ref, nativeReconcileTreeEntry("backend", "tree", "040000", directory))
		f.set("contents/backend/go.mod?ref="+ref, covered)
	}
	f.tree(directory, nativeReconcileTreeEntry("go.mod", "blob", "100644", nativeInventoryTestSHA(201)))
}

// NativeReconcileOutput writes the machine envelope and reads it through the production validator.
func nativeReconcileOutput(t *testing.T, raw string, nested bool) (*NativeActionRequest, error) {
	t.Helper()
	item := map[string]any{"type": "native_action", "request": raw, "output_id": "opaque"}
	if nested {
		delete(item, "request")
		item["data"] = map[string]string{"request": raw}
	}
	payload, err := json.Marshal(map[string]any{"items": []any{item}})
	if err != nil {
		t.Fatal(err)
	}
	return nativeReconcileRead(t, string(payload))
}

// NativeReconcileRead uses a real bounded regular file for untrusted output.
func nativeReconcileRead(t *testing.T, payload string) (*NativeActionRequest, error) {
	t.Helper()
	filename := filepath.Join(t.TempDir(), "output.json")
	if err := os.WriteFile(filename, []byte(payload), 0600); err != nil {
		t.Fatal(err)
	}
	return ReadNativeActionRequest(filename)
}

// TestNativeActionRequest checks both runtime envelope shapes and local rejection before any credentials exist.
func TestNativeActionRequest(t *testing.T) {
	valid := fmt.Sprintf(`{"number":1,"expected_head_sha":%q,"action":"admit"}`, nativeInventoryTestSHA(1))
	for _, nested := range []bool{false, true} {
		request, err := nativeReconcileOutput(t, valid, nested)
		if err != nil || request == nil {
			t.Fatalf("nested=%v: %v, %v", nested, request, err)
		}
	}
	for _, raw := range []string{
		`{}`, `null`, `[]`, `{"action":"admit"}`, strings.Replace(valid, `"number":1`, `"number":true`, 1),
		strings.Replace(valid, `"number":1`, `"number":1.0`, 1), strings.Replace(valid, `"number":1`, `"number":1,"number":2`, 1),
		strings.Replace(valid, nativeInventoryTestSHA(1), "aaaa", 1), strings.Replace(valid, "admit", "merge", 1),
		strings.Replace(valid, "admit", "close-superseded", 1), strings.TrimSuffix(valid, "}") + `,"replacement_pr":2}`,
		strings.TrimSuffix(valid, "}") + `,"unknown":1}`, strings.TrimSuffix(valid, "}") + `,"evidence":"\ud800"}`,
	} {
		if _, err := nativeReconcileOutput(t, raw, false); err == nil {
			t.Errorf("accepted invalid request %s", raw)
		}
	}
	for _, evidence := range []string{
		"try again",
		nativeReconcileEvidence + "\n\t/retest",
		nativeReconcileEvidence + "\n\u00a0/approve",
		nativeReconcileEvidence + " @reviewer",
		nativeReconcileEvidence + " <!-- " + nativePublicationMarker + " -->",
		nativeReconcileEvidence + " <!-- " + policy.RecoveryMarker + " -->",
	} {
		payload, err := json.Marshal(map[string]any{"number": 1, "expected_head_sha": nativeInventoryTestSHA(1), "action": "recover-retry", "evidence": evidence})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := nativeReconcileOutput(t, string(payload), false); err == nil {
			t.Errorf("accepted unsafe recovery justification %q", evidence)
		}
	}
	for _, output := range []string{
		`{}`, `{"items":[{"request":"{}"}]}`, `{"items":[],"errors":["agent error"]}`, `{"items":[],"errors":{}}`,
		`{"items":[{"type":"native_action","request":"{}"},{"type":"native_action","request":"{}"}]}`,
		`{"items":[{"type":"native_action","request":"{}"},{"type":"native_replacement","request_sha256":"hash"}]}`,
		`{"items":[{"type":"native_action","request":"{}","data":{"request":"{}"}}]}`,
		`{"items":[{"type":"native_action","request":{}}]}`,
	} {
		if _, err := nativeReconcileRead(t, output); err == nil {
			t.Errorf("accepted invalid envelope %s", output)
		}
	}
	if request, err := nativeReconcileRead(t, `{"items":[{"type":"native_replacement"}]}`); request != nil || err != nil {
		t.Fatalf("no action = %v, %v", request, err)
	}
}

// TestNativeReconcile exercises fixed writes, idempotency, and fail-closed evidence against real SDK HTTP clients.
func TestNativeReconcile(t *testing.T) {
	head := nativeInventoryTestSHA(1)
	admit := "<!-- " + nativePublicationMarker + "admit:" + head + " -->\n/ok-to-test"
	closeBody := "<!-- " + nativePublicationMarker + "close:" + head + ":100 -->\nCovered by merged replacement https://github.com/" + nativeInventoryTestRepository + "/pull/100.\n\n/close"
	recovery := "<!-- " + nativePublicationMarker + "recovery:" + head + " -->\n" + nativeReconcileEvidence + "\n\n"
	tests := []struct {
		name, action, evidence, body, reject, repeated string
		number, replacement                            int
		setup                                          func(*nativeReconcileFixture)
	}{
		{name: "fixed native admission and idempotency", action: "admit", body: admit, repeated: "already-admitted", setup: (*nativeReconcileFixture).actionSource},
		{name: "foreign author", action: "admit", reject: "foreign", setup: func(f *nativeReconcileFixture) {
			f.api["pulls/1"].body.(map[string]any)["user"] = map[string]any{"login": "human", "type": "User"}
		}},
		{name: "foreign fork", action: "admit", reject: "foreign", setup: func(f *nativeReconcileFixture) {
			f.api["pulls/1"].body.(map[string]any)["head"].(map[string]any)["repo"] = map[string]any{"full_name": "other/fork"}
		}},
		{name: "human hold", action: "admit", reject: "human/unknown", setup: func(f *nativeReconcileFixture) { f.actionSource(); f.hold(1) }},
		{name: "manifest-only Go", action: "admit", reject: "workspace/vendor", setup: func(f *nativeReconcileFixture) { f.tree(head) }},
		{name: "manifest-only npm", action: "admit", reject: "updated lock", setup: func(f *nativeReconcileFixture) { f.npmSource(false) }},
		{name: "complete npm consumers", action: "admit", body: admit, setup: func(f *nativeReconcileFixture) { f.npmSource(true) }},
		{name: "compiled lock and editable source coverage", action: "admit", body: admit, setup: func(f *nativeReconcileFixture) { f.generatedActionSource(nativeInventoryTestSHA(107)) }},
		{name: "compiled lock with stale editable source", action: "admit", reject: "source tag lacks intended", setup: func(f *nativeReconcileFixture) { f.generatedActionSource(nativeInventoryTestSHA(106)) }},
		{name: "compiled lock with lost live source", action: "admit", reject: "lost its existing source", setup: func(f *nativeReconcileFixture) { f.generatedActionSource("") }},
		{name: "partial commits", action: "admit", reject: "remote evidence is incomplete", setup: func(f *nativeReconcileFixture) { f.set("pulls/1/commits", []any{}) }},
		{name: "stale requested head", action: "admit", reject: "stale head", setup: func(f *nativeReconcileFixture) {
			f.api["pulls/1"].body.(map[string]any)["head"].(map[string]any)["sha"] = nativeInventoryTestSHA(2)
		}},
		{name: "unofficial action pin", action: "admit", reject: "official", setup: func(f *nativeReconcileFixture) { f.actionSource(); f.set("/repos/actions/checkout/tags", []any{}) }},
		{name: "truncated targeted tree", action: "admit", reject: "incomplete targeted", setup: func(f *nativeReconcileFixture) {
			f.actionSource()
			f.set("git/trees/"+head, map[string]any{"truncated": true, "tree": []any{}})
		}},
		{name: "recovery requires both causal links", action: "recover-retry", evidence: "Resolved by comparing this unrelated https://github.com/some/comparison.", reject: "current failure", setup: func(f *nativeReconcileFixture) { f.retryHold(1); f.statuses(1, "success", "failure") }},
		{name: "recovery held and bounded", action: "recover-retry", evidence: nativeReconcileEvidence, body: recovery + "/hold cancel\n/retest-required", repeated: "error:hold cancellation pending", setup: func(f *nativeReconcileFixture) { f.retryHold(1); f.statuses(1, "success", "failure") }},
		{name: "recovery unheld and bounded", action: "recover-retry", evidence: nativeReconcileEvidence, body: recovery + "/retest-required", repeated: "error:budget", setup: func(f *nativeReconcileFixture) { f.statuses(1, "success", "failure") }},
		{name: "pending CI preserves budget", action: "recover-retry", evidence: nativeReconcileEvidence, reject: "pending", setup: func(f *nativeReconcileFixture) { f.retryHold(1); f.statuses(1, "pending", "failure") }},
		{name: "pending discovered in fresh sample", action: "recover-retry", evidence: nativeReconcileEvidence, reject: "pending", setup: func(f *nativeReconcileFixture) {
			f.retryHold(1)
			f.statuses(1, "success", "failure")
			f.beforeCI = func(f *nativeReconcileFixture) {
				if f.ciReads == 2 {
					f.statuses(1, "pending", "failure")
				}
			}
		}},
		{name: "foreign head check fails closed", action: "release-hold", reject: "check", setup: func(f *nativeReconcileFixture) {
			f.retryHold(1)
			f.statuses(1, "success", "success")
			f.ci["commits/"+head+"/check-runs"] = nativeInventoryTestResponse{body: map[string]any{"total_count": 1, "check_runs": []any{map[string]any{"id": 1, "name": "foreign", "head_sha": nativeInventoryTestSHA(2), "status": "completed", "conclusion": "success"}}}}
		}},
		{name: "unexplained hold cannot release green head", action: "release-hold", reject: "human/unknown", setup: func(f *nativeReconcileFixture) {
			f.retryHold(1)
			f.api["issues/1/comments"].body.([]any)[0].(map[string]any)["body"] = "/hold unexplained"
			f.statuses(1, "success", "success")
		}},
		{name: "red required CI", action: "release-hold", reject: "required current-head CI", setup: func(f *nativeReconcileFixture) { f.retryHold(1); f.statuses(1, "success", "failure") }},
		{name: "verified robot release", number: 100, action: "release-hold", body: "/hold cancel", repeated: "not-held-or-cancel-pending", setup: func(f *nativeReconcileFixture) {
			pull := f.addReplacement(100, false, nativePublicationSource{Number: 1, HeadSHA: head})
			pull["labels"] = []any{map[string]any{"name": nativePublicationLabel}}
			f.retryHold(100)
			f.statuses(100, "success", "success")
		}},
		{name: "robot cannot admit", number: 100, action: "admit", reject: "only recovery", setup: (*nativeReconcileFixture).mergedReplacement},
		{name: "robot release requires exact ownership", number: 100, action: "release-hold", reject: "unverified replacement", setup: func(f *nativeReconcileFixture) {
			f.addReplacement(100, false, nativePublicationSource{Number: 1, HeadSHA: head})
			f.retryHold(100)
			f.statuses(100, "success", "success")
		}},
		{name: "unmerged cannot close source", action: "close-superseded", replacement: 100, reject: "state", setup: func(f *nativeReconcileFixture) {
			f.addReplacement(100, false, nativePublicationSource{Number: 1, HeadSHA: head})
		}},
		{name: "merged closure only via Prow and idempotent", action: "close-superseded", replacement: 100, body: closeBody, repeated: "close-already-requested", setup: (*nativeReconcileFixture).mergedReplacement},
		{name: "human-copied closure marker does not consume request", action: "close-superseded", replacement: 100, body: closeBody, setup: func(f *nativeReconcileFixture) {
			f.mergedReplacement()
			f.addComment(1, "human", closeBody)
		}},
		{name: "old-head robot closure marker does not consume request", action: "close-superseded", replacement: 100, body: closeBody, setup: func(f *nativeReconcileFixture) {
			f.mergedReplacement()
			f.addComment(1, nativePublicationRobot, strings.Replace(closeBody, head, nativeInventoryTestSHA(88), 1))
		}},
		{name: "robot quoted marker and command does not consume request", action: "close-superseded", replacement: 100, body: closeBody, setup: func(f *nativeReconcileFixture) {
			f.mergedReplacement()
			f.addComment(1, nativePublicationRobot, "Example:\n"+closeBody)
			f.addComment(1, nativePublicationRobot, closeBody+"\n```")
			f.addComment(1, nativePublicationRobot, strings.TrimSuffix(closeBody, "/close"))
		}},
		{name: "wrong immutable marker source", action: "close-superseded", replacement: 100, reject: "exact native source", setup: func(f *nativeReconcileFixture) {
			f.addReplacement(100, true, nativePublicationSource{Number: 1, HeadSHA: nativeInventoryTestSHA(7)})
		}},
		{name: "coverage reverted on main", action: "close-superseded", replacement: 100, reject: "not covered", setup: func(f *nativeReconcileFixture) {
			f.mergedReplacement()
			f.set("contents/backend/go.mod?ref="+nativeInventoryTestSHA(0), nativePublicationFixtureBlob("module example.test/component\n\ngo 1.26.0\n\nrequire example.com/library v1.0.0\n"))
		}},
		{name: "main moved during coverage", action: "close-superseded", replacement: 100, reject: "coverage evidence changed", setup: func(f *nativeReconcileFixture) { f.mergedReplacement(); f.moveMain = true }},
		{name: "malformed write receipt is not retried", action: "admit", body: admit, reject: "comment response incomplete", setup: func(f *nativeReconcileFixture) { f.actionSource(); f.invalidReceipt = true }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			f := &nativeReconcileFixture{nativeInventoryTestFixture: newNativeInventoryTestFixture(t)}
			test.setup(f)
			number := test.number
			if number == 0 {
				number = 1
			}
			payload := map[string]any{"number": number, "expected_head_sha": nativeInventoryTestSHA(number), "action": test.action}
			if test.evidence != "" {
				payload["evidence"] = test.evidence
			}
			if test.replacement != 0 {
				payload["replacement_pr"] = test.replacement
			}
			raw, err := json.Marshal(payload)
			if err != nil {
				t.Fatal(err)
			}
			request, err := nativeReconcileOutput(t, string(raw), true)
			if err != nil {
				t.Fatal(err)
			}
			p := f.publisher()
			result, err := p.Reconcile(context.Background(), request)
			if test.reject != "" {
				if err == nil || !strings.Contains(err.Error(), test.reject) {
					t.Fatalf("expected %q error, got %v (%v)", test.reject, result, err)
				}
			} else if err != nil || !reflect.DeepEqual(result, map[string]any{"status": "requested", "number": number, "action": test.action, "comment_id": int64(100)}) {
				t.Fatalf("unexpected result %v, error %v", result, err)
			}
			if test.repeated != "" {
				result, err := p.Reconcile(context.Background(), request)
				if reject, ok := strings.CutPrefix(test.repeated, "error:"); ok {
					if err == nil || !strings.Contains(err.Error(), reject) {
						t.Fatalf("repeat error = %v, want %q", err, reject)
					}
				} else if err != nil || result["status"] != test.repeated {
					t.Fatalf("repeat = %v (%v), want %q", result, err, test.repeated)
				}
			}
			if test.body == "" && len(f.writes) != 0 || test.body != "" && !reflect.DeepEqual(f.writes, []string{test.body}) {
				t.Fatalf("writes = %q, want at most the exact comment %q", f.writes, test.body)
			}
		})
	}
}
