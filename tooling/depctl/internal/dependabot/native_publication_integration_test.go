// Copyright 2026 Microsoft Corporation
// Licensed under the Apache License, Version 2.0.

package dependabot

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	automation "github.com/Azure/ARO-HCP/tooling/github"
)

// TestNativePublicationIntegrationCreatesAndReuses proves real content, readiness, attribution and idempotence.
func TestNativePublicationIntegrationCreatesAndReuses(t *testing.T) {
	f := newNativeIntegrationFixture(t)
	f.labelExists = false
	request := f.request("v1.1.0", "2", 1)
	first := f.published(request)
	second := f.published(request)
	if first["number"] != second["number"] || first["head_sha"] != second["head_sha"] || second["unchanged"] != true || second["reused"] != true {
		t.Fatalf("non-idempotent receipts: %v %v", first, second)
	}
	head := first["head_sha"].(string)
	if f.remoteGit("show", "-s", "--format=%P", head) != f.base {
		t.Fatal("first publication has unexpected ancestry")
	}
	if f.remoteGit("show", "-s", "--format=%ae", head) != "42+"+nativePublicationRobot+"@users.noreply.github.com" {
		t.Fatal("commit lacks verified App identity")
	}
	pull := f.pulls[first["number"].(int)]
	if pull["draft"] != false || pull["user"].(map[string]any)["login"] != nativePublicationRobot {
		t.Fatalf("not a ready robot PR: %v", pull)
	}
	if !reflect.DeepEqual(pull["labels"], []any{map[string]any{"id": 42, "name": nativePublicationLabel}}) {
		t.Fatalf("labels = %v", pull["labels"])
	}
	for _, field := range []string{"title", "body"} {
		if strings.Contains(pull[field].(string), "@") || !strings.Contains(pull[field].(string), "＠library") {
			t.Fatalf("unsafe or altered %s: %s", field, pull[field])
		}
	}
	if !strings.Contains(pull["body"].(string), "café") {
		t.Fatal("Unicode text lost")
	}
	for _, path := range []string{"backend/go.mod", "internal/go.mod", "image-sync/oc-mirror/acrauth/go.mod", "vendor/modules.txt", "image-sync/oc-mirror/acrauth/vendor/modules.txt"} {
		if !strings.Contains(f.remoteGit("show", head+":"+path), "v1.1.0") {
			t.Fatalf("consumer not published: %s", path)
		}
	}
	if len(f.comments[1]) != 0 {
		t.Fatal("publication notified native source")
	}
	if !reflect.DeepEqual(f.writes, []string{"POST labels", "POST pulls", "POST issues/100/labels"}) {
		t.Fatalf("unexpected publication effects: %v", f.writes)
	}
}

// TestNativePublicationIntegrationUpdatesOwnership preserves branch history while revising the exact source marker.
func TestNativePublicationIntegrationUpdatesOwnership(t *testing.T) {
	f := newNativeIntegrationFixture(t)
	first := f.published(f.request("v1.1.0", "2", 1))
	oldHead := first["head_sha"].(string)
	source := f.pulls[1]
	f.git("checkout", "--quiet", "dependabot/1")
	f.write("backend/go.mod", strings.ReplaceAll(f.git("show", "HEAD:backend/go.mod")+"\n", "v1.1.0", "v1.2.0"))
	f.commit("new upstream intent")
	sha := f.git("rev-parse", "HEAD")
	f.git("push", f.remote, "HEAD:refs/heads/dependabot/1")
	f.git("checkout", "--quiet", "main")
	source["head"].(map[string]any)["sha"] = sha
	request := f.request("v1.2.0", "3", 1)
	second := f.published(request)
	if first["number"] != second["number"] || oldHead == second["head_sha"] {
		t.Fatalf("replacement was not updated: %v %v", first, second)
	}
	if got := f.remoteGit("show", "-s", "--format=%P", second["head_sha"].(string)); got != oldHead+" "+f.base {
		t.Fatalf("history rewritten: %s", got)
	}
	metadata, err := nativePublicationMetadata(f.pulls[100]["body"].(string))
	if err != nil {
		t.Fatal(err)
	}
	sources := metadata["sources"].([]any)
	if sources[0].(map[string]any)["head_sha"] != sha {
		t.Fatalf("stale ownership marker: %v", metadata)
	}
	if !reflect.DeepEqual(f.writes, []string{"POST pulls", "POST issues/100/labels", "PATCH pulls/100"}) {
		t.Fatalf("unexpected ownership effects: %v", f.writes)
	}
}

// TestNativePublicationIntegrationRejectsUntrustedSources fails before any public branch or GitHub mutation.
func TestNativePublicationIntegrationRejectsUntrustedSources(t *testing.T) {
	tests := []struct {
		name, reason string
		change       func(*nativeIntegrationFixture, *NativePublicationRequest)
	}{
		{name: "stale source", reason: "stale head", change: func(f *nativeIntegrationFixture, r *NativePublicationRequest) {
			r.request.SourcePRs[0].HeadSHA = strings.Repeat("f", 40)
		}},
		{name: "mixed selected targets", reason: "one actual upgrade family/target", change: func(f *nativeIntegrationFixture, r *NativePublicationRequest) {
			sha := f.addSource(2, "internal/go.mod", strings.ReplaceAll(f.git("show", f.base+":internal/go.mod")+"\n", "v1.0.0", "v1.2.0"))
			r.request.SourcePRs = append(r.request.SourcePRs, nativePublicationSource{Number: 2, HeadSHA: sha})
		}},
		{name: "patch below source intent", reason: "older family", change: func(f *nativeIntegrationFixture, r *NativePublicationRequest) { *r = *f.request("v1.0.1", "2", 1) }},
		{name: "human source author", reason: "non-bot", change: func(f *nativeIntegrationFixture, r *NativePublicationRequest) {
			f.pulls[1]["user"] = map[string]any{"login": "human", "type": "User"}
		}},
		{name: "fork source", reason: "foreign repository", change: func(f *nativeIntegrationFixture, r *NativePublicationRequest) {
			f.pulls[1]["head"].(map[string]any)["repo"] = map[string]any{"full_name": "foreign/fork"}
		}},
		{name: "human commit on bot branch", reason: "non-Dependabot", change: func(f *nativeIntegrationFixture, r *NativePublicationRequest) {
			f.overrides["pulls/1/commits"] = []any{map[string]any{"sha": r.request.SourcePRs[0].HeadSHA, "author": map[string]any{"login": "human", "type": "User"}}}
		}},
		{name: "incomplete source files", reason: "incomplete changed-file", change: func(f *nativeIntegrationFixture, r *NativePublicationRequest) { f.pulls[1]["changed_files"] = 2 }},
		{name: "incomplete source comments", reason: "incomplete commit or comment", change: func(f *nativeIntegrationFixture, r *NativePublicationRequest) { f.pulls[1]["comments"] = 1 }},
		{name: "removed current source", reason: "inventory changed", change: func(f *nativeIntegrationFixture, r *NativePublicationRequest) {
			f.overrides["pulls"] = []any{}
		}},
		{name: "source rename", reason: "additions/removals/renames", change: func(f *nativeIntegrationFixture, r *NativePublicationRequest) {
			f.files[1] = []any{map[string]any{"filename": "backend/go.mod", "status": "renamed", "previous_filename": "old/go.mod"}}
		}},
		{name: "missing merge base", reason: "missing immutable merge base", change: func(f *nativeIntegrationFixture, r *NativePublicationRequest) {
			f.overrides["compare/"+f.base+"..."+r.request.SourcePRs[0].HeadSHA] = map[string]any{}
		}},
		{name: "omitted same target source", reason: "omits current same-family/target", change: func(f *nativeIntegrationFixture, r *NativePublicationRequest) {
			f.addSource(2, "internal/go.mod", strings.ReplaceAll(f.git("show", f.base+":internal/go.mod")+"\n", "v1.0.0", "v1.1.0"))
		}},
		{name: "title cannot prove disjoint sibling", reason: "unsupported/protected", change: func(f *nativeIntegrationFixture, r *NativePublicationRequest) {
			f.addSource(2, "api/Makefile", "models:\n\t@echo changed\n")
			f.pulls[2]["title"] = "chore(deps): bump Docker base"
		}},
		{name: "mixed Docker Go sibling", reason: "unsupported/protected", change: func(f *nativeIntegrationFixture, r *NativePublicationRequest) {
			f.addSource(2, "backend/Dockerfile", "FROM example.invalid/base:1.1\n")
			f.files[2] = append(f.files[2], map[string]any{"filename": "internal/go.mod", "status": "modified"})
			f.pulls[2]["changed_files"] = 2
		}},
		{name: "malicious immutable source intent", reason: "non-version Go", change: func(f *nativeIntegrationFixture, r *NativePublicationRequest) {
			sha := r.request.SourcePRs[0].HeadSHA
			text := f.remoteGit("show", sha+":backend/go.mod") + "\nreplace example.com/library => ../evil\n"
			f.overrides["contents/backend/go.mod?ref="+sha] = nativePublicationFixtureBlob(text)
		}},
		{name: "protected patch", reason: "protected", change: func(f *nativeIntegrationFixture, r *NativePublicationRequest) {
			f.write("backend/Dockerfile", "FROM example.invalid/unapproved:1\n")
			r.request.Patch += f.git("diff") + "\n"
			f.git("restore", ".")
		}},
		{name: "consumer omitted from patch", reason: "older family", change: func(f *nativeIntegrationFixture, r *NativePublicationRequest) {
			f.write("backend/go.mod", strings.ReplaceAll(f.git("show", f.base+":backend/go.mod")+"\n", "v1.0.0", "v1.1.0"))
			r.request.Patch = f.git("diff") + "\n"
			f.git("restore", ".")
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			f := newNativeIntegrationFixture(t)
			request := f.request("v1.1.0", "2", 1)
			test.change(f, request)
			_, err := f.publish(request)
			if err == nil || !strings.Contains(err.Error(), test.reason) {
				t.Fatalf("error = %v, want %q", err, test.reason)
			}
			if refs := f.remoteGit("for-each-ref", "--format=%(refname)", "refs/heads/native-dependabot-"); refs != "" || len(f.writes) != 0 {
				t.Fatalf("rejected input had effects: refs=%s writes=%v", refs, f.writes)
			}
		})
	}
}

// TestNativePublicationIntegrationSiblingMembership accepts complete same-target membership and proven Docker disjointness.
func TestNativePublicationIntegrationSiblingMembership(t *testing.T) {
	for _, mode := range []string{"complete same target", "different target", "unrelated Docker"} {
		t.Run(mode, func(t *testing.T) {
			f := newNativeIntegrationFixture(t)
			numbers := []int{1}
			switch mode {
			case "complete same target":
				f.addSource(2, "internal/go.mod", strings.ReplaceAll(f.git("show", f.base+":internal/go.mod")+"\n", "v1.0.0", "v1.1.0"))
				numbers = append(numbers, 2)
			case "different target":
				f.addSource(2, "internal/go.mod", strings.ReplaceAll(f.git("show", f.base+":internal/go.mod")+"\n", "v1.0.0", "v1.2.0"))
			case "unrelated Docker":
				f.addSource(2, "backend/Dockerfile", "FROM example.invalid/base:1.1\n")
			}
			request := f.request("v1.1.0", "2", numbers...)
			result := f.published(request)
			if f.remoteGit("show", result["head_sha"].(string)+":backend/Dockerfile") != "FROM example.invalid/base:1.0" {
				t.Fatal("unrelated source was applied")
			}
			meta, err := nativePublicationMetadata(f.pulls[100]["body"].(string))
			if err != nil || len(meta["sources"].([]any)) != len(numbers) {
				t.Fatalf("ownership = %v: %v", meta, err)
			}
		})
	}
}

// SetHold installs complete current-head comment and label provenance, without granting native CI write authority.
func (f *nativeIntegrationFixture) setHold(number int, mode string) {
	head := f.detail(number)["head"].(map[string]any)["sha"].(string)
	actor, body := "human", "/hold awaiting manual review"
	if mode == "robot retry" {
		actor, body = "openshift-merge-bot[bot]", "/hold\n\nRevision "+head+" was retested 3 times: holding"
	}
	if mode == "unexplained robot" {
		actor = nativePublicationRobot
	}
	f.comments[number] = []any{map[string]any{"id": 1, "user": map[string]any{"login": actor}, "body": body, "created_at": "2026-01-01T00:00:00Z", "updated_at": "2026-01-01T00:00:00Z"}}
	f.pulls[number]["comments"] = 1
	f.pulls[number]["labels"] = append(f.pulls[number]["labels"].([]any), map[string]any{"name": "do-not-merge/hold"})
	f.events[number] = []any{map[string]any{"id": 1, "event": "labeled", "actor": map[string]any{"login": "openshift-ci[bot]"}, "label": map[string]any{"name": "do-not-merge/hold"}, "created_at": "2026-01-01T00:00:00Z"}}
}

// TestNativePublicationIntegrationHolds preserves human and unknown holds while allowing evidenced robot retry holds.
func TestNativePublicationIntegrationHolds(t *testing.T) {
	for _, target := range []string{"source", "replacement"} {
		for _, mode := range []string{"human", "unexplained robot", "robot retry"} {
			t.Run(target+"/"+mode, func(t *testing.T) {
				f := newNativeIntegrationFixture(t)
				request := f.request("v1.1.0", "2", 1)
				number := 1
				old := ""
				if target == "replacement" {
					first := f.published(request)
					number = first["number"].(int)
					old = first["head_sha"].(string)
				}
				f.setHold(number, mode)
				f.writes = nil
				result, err := f.publish(request)
				if mode == "robot retry" {
					if err != nil {
						t.Fatal(err)
					}
					if result["number"] != 100 {
						t.Fatalf("receipt = %v", result)
					}
					return
				}
				if err == nil || !strings.Contains(err.Error(), "human/unknown") {
					t.Fatalf("hold bypassed: %v", err)
				}
				current := f.remoteGit("for-each-ref", "--format=%(objectname)", "refs/heads/"+f.branch)
				if current != old || len(f.writes) != 0 {
					t.Fatalf("held publication changed: %s %v", current, f.writes)
				}
			})
		}
	}
}

// TestNativePublicationIntegrationRaces rechecks mutable authorization and advertised refs at the write boundary.
func TestNativePublicationIntegrationRaces(t *testing.T) {
	for _, mode := range []string{"source moved", "main moved", "remote main missing", "human hold added", "concurrent normal push"} {
		t.Run(mode, func(t *testing.T) {
			f := newNativeIntegrationFixture(t)
			request := f.request("v1.1.0", "2", 1)
			competing := ""
			if mode == "main moved" || mode == "concurrent normal push" {
				f.git("checkout", "--quiet", "-b", "competing", f.base)
				f.write("main-only.txt", "concurrent change\n")
				f.commit("concurrent writer")
				competing = f.git("rev-parse", "HEAD")
				f.git("push", f.remote, "HEAD:refs/heads/competing")
				f.git("checkout", "--quiet", "main")
			}
			if mode == "concurrent normal push" {
				if err := os.WriteFile(f.raceFile, []byte("refs/heads/"+f.branch+" "+competing+"\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			f.boundary = func(route string) {
				if route != "GET labels/"+nativePublicationLabel {
					return
				}
				f.boundary = nil
				switch mode {
				case "source moved":
					f.pulls[1]["head"].(map[string]any)["sha"] = strings.Repeat("f", 40)
				case "main moved":
					f.remoteGit("update-ref", "refs/heads/main", competing)
				case "remote main missing":
					f.remoteGit("update-ref", "-d", "refs/heads/main")
				case "human hold added":
					f.setHold(1, "human")
				}
			}
			result, err := f.publish(request)
			switch mode {
			case "main moved":
				if err != nil || result["status"] != "deferred" {
					t.Fatalf("stale candidate = %v, %v; want deferral", result, err)
				}
			case "remote main missing":
				if err == nil || !strings.Contains(err.Error(), "remote main is missing") {
					t.Fatalf("missing remote main = %v, %v; want an error", result, err)
				}
			default:
				if err == nil {
					t.Fatal("race accepted")
				}
			}
			if mode == "concurrent normal push" && !errors.Is(err, automation.ErrIndeterminate) {
				t.Fatalf("push failure lost uncertainty: %v", err)
			}
			got := f.remoteGit("for-each-ref", "--format=%(objectname)", "refs/heads/"+f.branch)
			want := ""
			if mode == "concurrent normal push" {
				want = competing
			}
			if got != want || len(f.writes) != 0 {
				t.Fatalf("concurrent state overwritten: head=%s want=%s writes=%v err=%v", got, want, f.writes, err)
			}
		})
	}
}

// TestNativePublicationIntegrationReplacementConflicts never takes over a foreign, ambiguous or drifted proposal.
func TestNativePublicationIntegrationReplacementConflicts(t *testing.T) {
	for _, mode := range []string{"foreign author", "fork", "conflicting marker", "duplicate replacement", "missing remote", "drifted remote", "closed unmerged"} {
		t.Run(mode, func(t *testing.T) {
			f := newNativeIntegrationFixture(t)
			request := f.request("v1.1.0", "2", 1)
			first := f.published(request)
			old := first["head_sha"].(string)
			switch mode {
			case "foreign author":
				f.pulls[100]["user"] = map[string]any{"login": "human", "type": "User"}
			case "fork":
				f.pulls[100]["head"].(map[string]any)["repo"] = map[string]any{"full_name": "foreign/fork"}
			case "conflicting marker":
				f.pulls[100]["body"] = "foreign metadata"
			case "duplicate replacement":
				duplicate := f.pull(101, nativePublicationRobot, f.branch, old, f.pulls[100]["title"].(string), f.pulls[100]["body"].(string))
				duplicate["labels"] = []any{map[string]any{"name": nativePublicationLabel}}
				f.pulls[101] = duplicate
			case "missing remote":
				f.remoteGit("update-ref", "-d", "refs/heads/"+f.branch)
			case "drifted remote":
				frozen := f.pull(100, nativePublicationRobot, f.branch, old, f.pulls[100]["title"].(string), f.pulls[100]["body"].(string))
				frozen["labels"], frozen["changed_files"] = f.pulls[100]["labels"], f.pulls[100]["changed_files"]
				f.overrides["pulls/100"] = frozen
				f.remoteGit("update-ref", "refs/heads/"+f.branch, f.base)
			case "closed unmerged":
				f.pulls[100]["state"] = "closed"
			}
			f.writes = nil
			expected := f.remoteGit("for-each-ref", "--format=%(objectname)", "refs/heads/"+f.branch)
			_, err := f.publish(request)
			if err == nil {
				t.Fatal("unowned replacement accepted")
			}
			if got := f.remoteGit("for-each-ref", "--format=%(objectname)", "refs/heads/"+f.branch); got != expected || len(f.writes) != 0 {
				t.Fatalf("conflict mutated publication: head=%s writes=%v err=%v", got, f.writes, err)
			}
		})
	}
}

// TestNativePublicationIntegrationUncertainMutations preserves exact interrupted publication state without automatic retry.
func TestNativePublicationIntegrationUncertainMutations(t *testing.T) {
	for _, mutation := range []string{"POST pulls", "POST issues/100/labels", "PATCH pulls/100"} {
		t.Run(mutation, func(t *testing.T) {
			f := newNativeIntegrationFixture(t)
			request := f.request("v1.1.0", "2", 1)
			if strings.HasPrefix(mutation, "PATCH") {
				f.published(request)
				request.request.Body = "Revised publication evidence."
			}
			f.writes = nil
			f.failWrite = mutation
			_, err := f.publish(request)
			if !errors.Is(err, automation.ErrIndeterminate) {
				t.Fatalf("uncertainty lost: %v", err)
			}
			attempts := 0
			for _, write := range f.writes {
				if write == mutation {
					attempts++
				}
			}
			if attempts != 1 || f.writes[len(f.writes)-1] != mutation {
				t.Fatalf("uncertain mutation retried or followed by another write: %v", f.writes)
			}
			orphan := f.remoteGit("rev-parse", "refs/heads/"+f.branch)
			f.failWrite = ""
			result := f.published(request)
			if result["head_sha"] != orphan || result["unchanged"] != true || result["number"] != 100 {
				t.Fatalf("recovery churned exact publication: %v", result)
			}
		})
	}
}

// MergePublication retains the published branch and proves an actual main ancestry relation for subsequent releases.
func (f *nativeIntegrationFixture) mergePublication(number int) {
	f.t.Helper()
	pull := f.detail(number)
	head := pull["head"].(map[string]any)["sha"].(string)
	f.git("fetch", f.remote, "refs/heads/"+f.branch)
	f.git("merge", "--quiet", "--no-ff", "-m", "merge replacement", head)
	f.base = f.git("rev-parse", "HEAD")
	f.git("push", f.remote, "HEAD:refs/heads/main")
	pull["state"], pull["merged"], pull["merge_commit_sha"] = "closed", true, f.base
}

// TestNativePublicationIntegrationMergedSuccession proves retained-branch ancestry and exact two-parent orphan recovery.
func TestNativePublicationIntegrationMergedSuccession(t *testing.T) {
	for _, mode := range []string{"next release", "interrupted next release", "unproven merge", "already covered"} {
		t.Run(mode, func(t *testing.T) {
			f := newNativeIntegrationFixture(t)
			first := f.published(f.request("v1.1.0", "2", 1))
			f.mergePublication(100)
			f.write("main-only.txt", "trusted intervening main change\n")
			f.commit("advance main")
			f.base = f.git("rev-parse", "HEAD")
			f.git("push", f.remote, "HEAD:refs/heads/main")
			source := 2
			if mode == "already covered" {
				source = 1
			} else {
				f.addSource(2, "backend/go.mod", strings.ReplaceAll(f.git("show", f.base+":backend/go.mod")+"\n", "v1.1.0", "v1.2.0"))
			}
			request := f.request("v1.2.0", "3", source)
			if mode == "unproven merge" {
				f.pulls[100]["merge_commit_sha"] = f.pulls[1]["head"].(map[string]any)["sha"]
			}
			f.writes = nil
			if mode == "unproven merge" || mode == "already covered" {
				_, err := f.publish(request)
				if err == nil {
					t.Fatal("unsafe merged succession accepted")
				}
				if mode == "already covered" && !strings.Contains(err.Error(), "already covers") {
					t.Fatalf("wrong refusal: %v", err)
				}
				if f.remoteGit("rev-parse", "refs/heads/"+f.branch) != first["head_sha"] || len(f.writes) != 0 {
					t.Fatal("refused succession changed state")
				}
				return
			}
			orphan := ""
			if mode == "interrupted next release" {
				f.failWrite = "POST pulls"
				if _, err := f.publish(request); !errors.Is(err, automation.ErrIndeterminate) {
					t.Fatalf("expected uncertain create: %v", err)
				}
				orphan = f.remoteGit("rev-parse", "refs/heads/"+f.branch)
				f.failWrite = ""
			}
			result := f.published(request)
			if result["number"] != 101 || result["reused"] != false {
				t.Fatalf("successor not distinct: %v", result)
			}
			if got := f.remoteGit("show", "-s", "--format=%P", result["head_sha"].(string)); got != first["head_sha"].(string)+" "+f.base {
				t.Fatalf("unsafe successor ancestry: %s", got)
			}
			if orphan != "" && (result["head_sha"] != orphan || result["unchanged"] != true) {
				t.Fatalf("two-parent orphan churn: %v", result)
			}
			if f.pulls[100]["state"] != "closed" || f.pulls[100]["merged"] != true {
				t.Fatal("predecessor changed")
			}
		})
	}
}

// TestNativePublicationIntegrationRejectsInexactOrphans recovers no branch based merely on a familiar name or robot attribution.
func TestNativePublicationIntegrationRejectsInexactOrphans(t *testing.T) {
	for _, mode := range []string{"foreign attribution", "changed source marker", "different tree", "wrong base trailer", "wrong parent"} {
		t.Run(mode, func(t *testing.T) {
			f := newNativeIntegrationFixture(t)
			request := f.request("v1.1.0", "2", 1)
			f.failWrite = "POST pulls"
			if _, err := f.publish(request); !errors.Is(err, automation.ErrIndeterminate) {
				t.Fatalf("expected interrupted publication: %v", err)
			}
			orphan := f.remoteGit("rev-parse", "refs/heads/"+f.branch)
			f.failWrite, f.writes = "", nil
			switch mode {
			case "foreign attribution":
				f.overrides["commits/"+orphan] = map[string]any{"sha": orphan, "author": map[string]any{"login": "human", "type": "User"}}
			case "changed source marker":
				sha := f.addSource(2, "internal/go.mod", strings.ReplaceAll(f.git("show", f.base+":internal/go.mod")+"\n", "v1.0.0", "v1.1.0"))
				request.request.SourcePRs = append(request.request.SourcePRs, nativePublicationSource{Number: 2, HeadSHA: sha})
			case "different tree":
				request = f.request("v1.1.0", "3", 1)
			case "wrong base trailer", "wrong parent":
				f.git("fetch", f.remote, "refs/heads/"+f.branch)
				tree := f.git("rev-parse", orphan+"^{tree}")
				marker, err := nativeEncodePublicationMarker(strings.TrimPrefix(f.branch, "native-dependabot-"), request.request.SourcePRs)
				if err != nil {
					t.Fatal(err)
				}
				base := f.base
				parents := []string{f.base}
				if mode == "wrong base trailer" {
					base = strings.Repeat("f", 40)
				}
				if mode == "wrong parent" {
					parents = []string{orphan}
				}
				// Commit-tree is test plumbing: the production path still authenticates every supplied object.
				args := []string{"commit-tree", tree}
				for _, parent := range parents {
					args = append(args, "-p", parent)
				}
				args = append(args, "-m", "interrupted publication\n\n"+marker+"\nnative-dependabot-base: "+base+"\n")
				replacement := f.git(args...)
				f.git("push", f.remote, replacement+":refs/heads/fixture-corruption")
				f.remoteGit("update-ref", "refs/heads/"+f.branch, replacement)
				orphan = replacement
			}
			_, err := f.publish(request)
			if err == nil {
				t.Fatal("inexact orphan recovered")
			}
			if got := f.remoteGit("rev-parse", "refs/heads/"+f.branch); got != orphan || len(f.writes) != 0 {
				t.Fatalf("orphan was overwritten: %s %v", got, f.writes)
			}
		})
	}
}

// TestNativePublicationIntegrationRejectsDowngrade preserves higher current-main family floors, including prereleases.
func TestNativePublicationIntegrationRejectsDowngrade(t *testing.T) {
	for _, test := range []struct{ target, current string }{{"v1.18.4", "v1.19.0"}, {"v3.0.0-beta.3", "v3.0.0-beta.4"}} {
		t.Run(fmt.Sprint(test.target), func(t *testing.T) {
			f := newNativeIntegrationFixture(t)
			source := f.addSource(2, "backend/go.mod", strings.ReplaceAll(f.git("show", f.base+":backend/go.mod")+"\n", "v1.0.0", test.target))
			f.writeGo(test.current, "3")
			f.commit("higher trusted main floor")
			f.base = f.git("rev-parse", "HEAD")
			f.git("push", f.remote, "HEAD:refs/heads/main")
			request := f.request(test.target, "2", 2)
			request.request.SourcePRs[0].HeadSHA = source
			_, err := f.publish(request)
			if err == nil || !strings.Contains(err.Error(), "current-base family") {
				t.Fatalf("downgrade refusal = %v", err)
			}
			if len(f.writes) != 0 || f.remoteGit("for-each-ref", "--format=%(refname)", "refs/heads/native-dependabot-") != "" {
				t.Fatal("downgrade changed publication")
			}
		})
	}
}

// TestNativePublicationIntegrationVendorAdditions publishes regenerated bytes without running dependency code.
func TestNativePublicationIntegrationVendorAdditions(t *testing.T) {
	f := newNativeIntegrationFixture(t)
	request := f.request("v1.1.0", "2", 1)
	f.writeGo("v1.1.0", "2")
	marker := f.root + "/executed"
	for _, prefix := range []string{"vendor/", "image-sync/oc-mirror/acrauth/vendor/"} {
		f.write(prefix+"example.com/library/new.go", "package library\n//go:generate touch "+marker+"\nfunc init() { panic(\"dependency code must not run during publication\") }\n")
	}
	f.git("add", ".")
	request.request.Patch = f.git("diff", "--cached") + "\n"
	f.git("restore", "--staged", ".")
	f.git("restore", ".")
	for _, prefix := range []string{"vendor/", "image-sync/oc-mirror/acrauth/vendor/"} {
		if err := os.Remove(f.root + "/" + prefix + "example.com/library/new.go"); err != nil {
			t.Fatal(err)
		}
	}
	result := f.published(request)
	for _, prefix := range []string{"vendor/", "image-sync/oc-mirror/acrauth/vendor/"} {
		if !strings.Contains(f.remoteGit("show", result["head_sha"].(string)+":"+prefix+"example.com/library/new.go"), "dependency code must not run") {
			t.Fatal("generated addition missing")
		}
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("publication ran dependency code: %v", err)
	}
}

// TestNativePublicationIntegrationNativeCIIsEvidence does not mistake native CI state for replacement authorization.
func TestNativePublicationIntegrationNativeCIIsEvidence(t *testing.T) {
	for _, state := range []string{"awaiting-admission", "pending", "queued", "in_progress", "failed"} {
		t.Run(state, func(t *testing.T) {
			f := newNativeIntegrationFixture(t)
			sha := f.pulls[1]["head"].(map[string]any)["sha"].(string)
			switch state {
			case "awaiting-admission":
				f.pulls[1]["labels"] = []any{map[string]any{"name": "needs-ok-to-test"}}
			case "pending", "failed":
				status := "pending"
				if state == "failed" {
					status = "failure"
				}
				f.overrides["commits/"+sha+"/statuses"] = []any{map[string]any{"id": 1, "context": "ci/prow/e2e", "state": status, "created_at": "2026-01-01T00:00:00Z"}}
			case "queued", "in_progress":
				f.overrides["commits/"+sha+"/check-runs"] = map[string]any{"total_count": 1, "check_runs": []any{map[string]any{"id": 1, "name": "ci/prow/e2e", "head_sha": sha, "status": state, "conclusion": nil}}}
			}
			f.published(f.request("v1.1.0", "2", 1))
		})
	}
}

// TestNativePublicationIntegrationSameTargetUpdates distinguishes metadata-only source changes from generated-byte fast forwards.
func TestNativePublicationIntegrationSameTargetUpdates(t *testing.T) {
	for _, mode := range []string{"ownership marker only", "regenerated vendor bytes"} {
		t.Run(mode, func(t *testing.T) {
			f := newNativeIntegrationFixture(t)
			first := f.published(f.request("v1.1.0", "2", 1))
			old := first["head_sha"].(string)
			numbers, revision := []int{1}, "2"
			switch mode {
			case "ownership marker only":
				f.addSource(2, "internal/go.mod", strings.ReplaceAll(f.git("show", f.base+":internal/go.mod")+"\n", "v1.0.0", "v1.1.0"))
				numbers = append(numbers, 2)
			case "regenerated vendor bytes":
				revision = "3"
			}
			second := f.published(f.request("v1.1.0", revision, numbers...))
			if second["number"] != first["number"] {
				t.Fatalf("created another proposal: %v", second)
			}
			head := second["head_sha"].(string)
			if mode == "ownership marker only" {
				if head != old || second["unchanged"] != true {
					t.Fatalf("metadata-only change churned Git history: %v", second)
				}
				metadata, err := nativePublicationMetadata(f.pulls[100]["body"].(string))
				if err != nil || len(metadata["sources"].([]any)) != 2 {
					t.Fatalf("marker not advanced: %v %v", metadata, err)
				}
				return
			}
			if head == old || f.remoteGit("rev-parse", head+"^1") != old {
				t.Fatalf("regenerated update rewrote history: %v", second)
			}
		})
	}
}

// TestNativePublicationIntegrationClosedAfterRecheck never recreates a replacement closed before shared inventory.
func TestNativePublicationIntegrationClosedAfterRecheck(t *testing.T) {
	f := newNativeIntegrationFixture(t)
	request := f.request("v1.1.0", "2", 1)
	first := f.published(request)
	number := first["number"].(int)
	pull := f.pulls[number]
	title, body, draft := pull["title"], pull["body"], pull["draft"]
	head := first["head_sha"].(string)
	pullCount := len(f.pulls)
	f.writes = nil
	closed := false
	f.boundary = func(route string) {
		if route != "GET branches/"+f.branch {
			return
		}
		f.boundary = nil
		closed = true
		pull["state"] = "closed"
	}
	_, err := f.publish(request)
	if !closed || !errors.Is(err, automation.ErrConflict) {
		t.Fatalf("closure did not fail with shared identity conflict: closed=%t err=%v", closed, err)
	}
	if len(f.writes) != 0 || len(f.pulls) != pullCount || pull["state"] != "closed" || pull["title"] != title || pull["body"] != body || pull["draft"] != draft {
		t.Fatalf("closed replacement mutated or recreated: writes=%v pulls=%v", f.writes, f.pulls)
	}
	if got := f.remoteGit("rev-parse", "refs/heads/"+f.branch); got != head {
		t.Fatalf("unchanged replacement branch moved: got=%s want=%s", got, head)
	}
}
