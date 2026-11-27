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

package dependabot

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/google/go-github/v69/github"

	"github.com/Azure/ARO-HCP/tooling/depctl/internal/policy"
)

const nativeInventoryTestRepository = "owner/repo"
const nativeInventoryTestTime = "2026-01-05T12:00:00Z"

// NativeInventoryTestSHA makes deterministic, distinct immutable fixture identities.
func nativeInventoryTestSHA(number int) string { return fmt.Sprintf("%040x", number) }

type nativeInventoryTestResponse struct {
	body   any
	status int
	next   string
}

type nativeInventoryTestFixture struct {
	t         *testing.T
	api       map[string]nativeInventoryTestResponse
	ci        map[string]nativeInventoryTestResponse
	sources   []any
	robots    []any
	mainReads int
	moveMain  bool
}

// NewNativeInventoryTestFixture supplies default complete evidence for one native Go source.
func newNativeInventoryTestFixture(t *testing.T) *nativeInventoryTestFixture {
	t.Helper()
	f := &nativeInventoryTestFixture{t: t, api: map[string]nativeInventoryTestResponse{}, ci: map[string]nativeInventoryTestResponse{}, sources: []any{}, robots: []any{}}
	f.addGoSource(1, "backend/go.mod", "v1.1.0")
	return f
}

// Set binds a fixture response to one exact App API resource.
func (f *nativeInventoryTestFixture) set(path string, body any) {
	f.api[path] = nativeInventoryTestResponse{body: body}
}

// AddPull provides complete SDK evidence and the corresponding creator inventory row.
func (f *nativeInventoryTestFixture) addPull(number int, actor, branch, title, body, file string, merged bool) map[string]any {
	state := "open"
	if merged {
		state = "closed"
	}
	head := nativeInventoryTestSHA(number)
	user := map[string]any{"login": actor, "type": "Bot"}
	labels := []any{}
	repository := map[string]any{"full_name": nativeInventoryTestRepository}
	pull := map[string]any{
		"number": number, "state": state, "merged": merged, "draft": false, "title": title, "body": body,
		"html_url": fmt.Sprintf("https://github.com/%s/pull/%d", nativeInventoryTestRepository, number),
		"labels":   labels, "user": user, "commits": 1, "changed_files": 1, "comments": 0,
		"base": map[string]any{"ref": "main", "sha": nativeInventoryTestSHA(0), "repo": repository},
		"head": map[string]any{"ref": branch, "sha": head, "repo": repository},
	}
	issue := map[string]any{"number": number, "user": user, "state": state, "body": body, "labels": labels, "pull_request": map[string]any{"url": pull["html_url"]}}
	if actor == nativePublicationRobot {
		f.robots = append(f.robots, issue)
	}
	if actor == nativePublicationBot {
		f.sources = append(f.sources, issue)
	}
	f.set(fmt.Sprintf("pulls/%d", number), pull)
	f.set(fmt.Sprintf("pulls/%d/commits", number), []any{map[string]any{"sha": head, "author": user}})
	f.set(fmt.Sprintf("pulls/%d/files", number), []any{map[string]any{"filename": file, "status": "modified"}})
	f.set(fmt.Sprintf("issues/%d/comments", number), []any{})
	f.set(fmt.Sprintf("issues/%d/events", number), []any{})
	f.set("compare/"+nativeInventoryTestSHA(0)+"..."+head, map[string]any{"merge_base_commit": map[string]any{"sha": nativeInventoryTestSHA(0)}})
	f.ci["commits/"+head+"/statuses"] = nativeInventoryTestResponse{body: []any{}}
	f.ci["commits/"+head+"/check-runs"] = nativeInventoryTestResponse{body: map[string]any{"total_count": 0, "check_runs": []any{}}}
	return pull
}

// AddGoSource records a source with an independently verifiable immutable manifest bump.
func (f *nativeInventoryTestFixture) addGoSource(number int, file, target string) {
	f.addPull(number, nativePublicationBot, fmt.Sprintf("dependabot/test-%d", number), "Go native source", "", file, false)
	manifest := "module example.test/component\n\ngo 1.26.0\n\nrequire example.com/library %s\n"
	f.set("contents/"+file+"?ref="+nativeInventoryTestSHA(0), nativePublicationFixtureBlob(fmt.Sprintf(manifest, "v1.0.0")))
	f.set("contents/"+file+"?ref="+nativeInventoryTestSHA(number), nativePublicationFixtureBlob(fmt.Sprintf(manifest, target)))
}

// AddReplacement constructs the verified group branch and ownership marker without publishing it.
func (f *nativeInventoryTestFixture) addReplacement(number int, merged bool, identities ...nativePublicationSource) map[string]any {
	group := fmt.Sprintf("%x", sha256.Sum256([]byte("go:example.com/library")))[:24]
	marker, err := nativeEncodePublicationMarker(group, identities)
	if err != nil {
		f.t.Fatal(err)
	}
	pull := f.addPull(number, nativePublicationRobot, "native-dependabot-"+group, "chore(deps): upgrade library", marker, "backend/go.mod", merged)
	if merged {
		pull["labels"] = []any{map[string]any{"name": nativePublicationLabel}}
	}
	return pull
}

// AddComment exposes trusted and untrusted comments with complete history metadata.
func (f *nativeInventoryTestFixture) addComment(number int, author, body string) {
	path := fmt.Sprintf("issues/%d/comments", number)
	comments := f.api[path].body.([]any)
	comments = append(comments, map[string]any{
		"id": len(comments) + 1, "user": map[string]any{"login": author}, "body": body,
		"created_at": nativeInventoryTestTime, "updated_at": nativeInventoryTestTime,
	})
	f.set(path, comments)
	f.api[fmt.Sprintf("pulls/%d", number)].body.(map[string]any)["comments"] = len(comments)
}

// Hold supplies both the human command and the visible untrusted hold label.
func (f *nativeInventoryTestFixture) hold(number int) {
	f.addComment(number, "human", "/hold\nInvestigating")
	pull := f.api[fmt.Sprintf("pulls/%d", number)].body.(map[string]any)
	pull["labels"] = []any{map[string]any{"name": policy.HoldLabel}}
	f.set(fmt.Sprintf("issues/%d/events", number), []any{map[string]any{
		"id": 1, "event": "labeled", "actor": map[string]any{"login": "human"},
		"label": map[string]any{"name": policy.HoldLabel}, "created_at": nativeInventoryTestTime,
	}})
}

// Serve rejects any mutation, any CI read on the App client and any metadata read on the CI client.
func (f *nativeInventoryTestFixture) serve(w http.ResponseWriter, r *http.Request, ci bool) {
	if r.Method != http.MethodGet {
		f.t.Errorf("inventory made a write: %s %s", r.Method, r.URL)
		http.Error(w, "inventory is read only", http.StatusMethodNotAllowed)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/repos/"+nativeInventoryTestRepository+"/")
	query := r.URL.Query()
	var response nativeInventoryTestResponse
	var ok bool
	switch {
	case ci:
		response, ok = f.ci[path]
	case path == "git/ref/heads/main":
		f.mainReads++
		sha := nativeInventoryTestSHA(0)
		if f.moveMain && f.mainReads > 1 {
			sha = nativeInventoryTestSHA(900)
		}
		response.body, ok = map[string]any{"ref": "refs/heads/main", "object": map[string]any{"sha": sha, "type": "commit"}}, true
	case path == "issues":
		creator, state, page := query.Get("creator"), query.Get("state"), query.Get("page")
		response, ok = f.api[path+"/"+creator+"/"+page]
		if !ok && page == "1" && query.Get("per_page") == "100" {
			switch {
			case creator == nativePublicationBot && state == "open":
				response.body, ok = f.sources, true
			case creator == nativePublicationRobot && state == "all":
				response.body, ok = f.robots, true
			}
		}
	case query.Has("ref"):
		response, ok = f.api[path+"?ref="+query.Get("ref")]
	default:
		response, ok = f.api[path]
	}
	if !ok {
		f.t.Errorf("unexpected request (ci=%v): %s", ci, r.URL)
		http.NotFound(w, r)
		return
	}
	if response.next != "" {
		w.Header().Set("Link", fmt.Sprintf("<http://%s%s?%s>; rel=\"next\"", r.Host, r.URL.Path, response.next))
	}
	w.Header().Set("Content-Type", "application/json")
	if response.status != 0 {
		w.WriteHeader(response.status)
	}
	if err := json.NewEncoder(w).Encode(response.body); err != nil {
		f.t.Error(err)
	}
}

// Publisher exposes only the two expected read credentials to the inventory entry point.
func (f *nativeInventoryTestFixture) publisher() *NativePublisher {
	f.t.Helper()
	reader := func(ci bool) *github.Client {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { f.serve(w, r, ci) }))
		f.t.Cleanup(server.Close)
		client := github.NewClient(server.Client())
		var err error
		client.BaseURL, err = url.Parse(server.URL + "/")
		if err != nil {
			f.t.Fatal(err)
		}
		return client
	}
	p, err := NewNativePublisher(nativeInventoryTestRepository, WithNativeInventoryClients(reader(false), reader(true)))
	if err != nil {
		f.t.Fatal(err)
	}
	return p
}

// TestNativeInventorySelectsCompleteTargets verifies paginated grouping and the real offline selection contract.
func TestNativeInventorySelectsCompleteTargets(t *testing.T) {
	f := newNativeInventoryTestFixture(t)
	f.addGoSource(2, "internal/go.mod", "v1.1.0")
	f.addGoSource(3, "other/go.mod", "v1.2.0")
	f.addReplacement(100, true, nativePublicationSource{Number: 3, HeadSHA: nativeInventoryTestSHA(3)})
	query := url.Values{"creator": {nativePublicationBot}, "state": {"open"}, "per_page": {"100"}, "page": {"2"}}
	f.api["issues/"+nativePublicationBot+"/1"] = nativeInventoryTestResponse{body: []any{f.sources[1]}, next: query.Encode()}
	f.set("issues/"+nativePublicationBot+"/2", []any{f.sources[0], f.sources[2]})
	inventory, err := f.publisher().Inventory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	groups := inventory["groups"].([]*nativeInventoryGroupRecord)
	if len(groups) != 2 || groups[0].Group != groups[1].Group || groups[0].Target != "v1.1.0" || groups[1].Target != "v1.2.0" ||
		groups[0].NextAction.Action != "prepare_replacement" || groups[1].NextAction.Action != "close_superseded" {
		t.Fatalf("incorrect cross-target classification: %+v", groups)
	}
	if len(groups[0].Sources) != 2 || groups[0].Sources[0]["number"] != 1 || groups[0].Sources[1]["number"] != 2 {
		t.Fatalf("expected both sorted same-target sources, got %#v", groups[0].Sources)
	}
	payload, err := json.Marshal(inventory)
	if err != nil {
		t.Fatal(err)
	}
	selection, err := SelectNativeInventoryGroup(bytes.NewReader(payload), nativeInventoryTestRepository, groups[0].Group.(string), groups[0].Target)
	if err != nil || selection.BaseSHA != nativeInventoryTestSHA(0) || !slices.Equal(selection.Manifests, []string{"backend/go.mod", "internal/go.mod"}) {
		t.Fatalf("offline selection = %+v, %v; inventory = %s", selection, err, payload)
	}
}

// TestNativeInventoryClassifiesLifecycle covers source CI, sibling completeness and verified ownership gates.
func TestNativeInventoryClassifiesLifecycle(t *testing.T) {
	identity := nativePublicationSource{Number: 1, HeadSHA: nativeInventoryTestSHA(1)}
	tests := []struct {
		name, action, reason string
		setup                func(*nativeInventoryTestFixture)
	}{
		{name: "unadmitted native CI is only evidence", action: "prepare_replacement", setup: func(f *nativeInventoryTestFixture) {
			f.api["pulls/1"].body.(map[string]any)["labels"] = []any{map[string]any{"name": "needs-ok-to-test"}}
		}},
		{name: "human native hold", action: "blocked", reason: "PR #1: human/unknown hold", setup: func(f *nativeInventoryTestFixture) { f.hold(1) }},
		{name: "existing pending open in publication window", action: "maintain_replacement", setup: func(f *nativeInventoryTestFixture) {
			f.addReplacement(100, false, identity)
			f.ci["commits/"+nativeInventoryTestSHA(100)+"/statuses"] = nativeInventoryTestResponse{body: []any{map[string]any{
				"id": 1, "context": "ci/prow/e2e-parallel", "state": "pending", "created_at": nativeInventoryTestTime,
			}}}
		}},
		{name: "existing held replacement", action: "blocked", reason: "replacement has human/unknown hold", setup: func(f *nativeInventoryTestFixture) {
			f.addReplacement(100, false, identity)
			f.hold(100)
		}},
		{name: "disjoint Dockerfile", action: "prepare_replacement", setup: func(f *nativeInventoryTestFixture) {
			f.addPull(2, nativePublicationBot, "dependabot/docker", "native docker", "", "backend/Dockerfile", false)
		}},
		{name: "title is not disjointness evidence", action: "blocked", reason: "PR #2: unsupported/protected native source path: api/Makefile", setup: func(f *nativeInventoryTestFixture) {
			f.addPull(2, nativePublicationBot, "dependabot/docker", "chore(deps): bump Docker base image", "", "api/Makefile", false)
		}},
		{name: "partial Docker file evidence", action: "blocked", reason: "PR #2: incomplete changed-file evidence", setup: func(f *nativeInventoryTestFixture) {
			f.addPull(2, nativePublicationBot, "dependabot/docker", "native docker", "", "backend/Dockerfile", false)
			f.set("pulls/2/files", []any{})
		}},
		{name: "closed unmerged is not a replacement", action: "prepare_replacement", setup: func(f *nativeInventoryTestFixture) {
			f.addReplacement(100, true, identity)["merged"] = false
		}},
		{name: "malformed manifest is classified as a per-source blocker", action: "prepare_replacement", setup: func(f *nativeInventoryTestFixture) {
			file := "api/package.json"
			f.addPull(2, nativePublicationBot, "dependabot/npm", "native npm", "", file, false)
			f.set("contents/"+file+"?ref="+nativeInventoryTestSHA(0), nativePublicationFixtureBlob(`{"dependencies":{"test":"1.0.0"}}`))
			f.set("contents/"+file+"?ref="+nativeInventoryTestSHA(2), nativePublicationFixtureBlob(`{"dependencies":`))
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			f := newNativeInventoryTestFixture(t)
			test.setup(f)
			inventory, err := f.publisher().Inventory(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			groups := inventory["groups"].([]*nativeInventoryGroupRecord)
			if len(groups) == 0 || groups[0].Group == nil || groups[0].NextAction.Action != test.action || !strings.Contains(groups[0].NextAction.Reason, test.reason) {
				t.Fatalf("classify %s = %+v", test.name, inventory)
			}
			if test.action == "maintain_replacement" {
				replacements := inventory["replacements"].([]nativeInventoryReplacement)
				if len(replacements) != 1 || !replacements[0].PublicationPending || groups[0].NextAction.ReplacementPR != 100 {
					t.Fatalf("open publication window not preserved: %+v", inventory)
				}
			}
		})
	}
}

// TestNativeInventoryNativeCIShowsEvidence verifies each current-head status without using it as a preparation gate.
func TestNativeInventoryNativeCIShowsEvidence(t *testing.T) {
	for _, state := range []string{"pending", "queued", "in_progress", "failure", "success"} {
		t.Run(state, func(t *testing.T) {
			f := newNativeInventoryTestFixture(t)
			const name = "ci/prow/e2e-parallel"
			head := nativeInventoryTestSHA(1)
			if state == "queued" || state == "in_progress" {
				f.ci["commits/"+head+"/check-runs"] = nativeInventoryTestResponse{body: map[string]any{
					"total_count": 1, "check_runs": []any{map[string]any{"id": 1, "name": name, "head_sha": head, "status": state}},
				}}
			} else {
				f.ci["commits/"+head+"/statuses"] = nativeInventoryTestResponse{body: []any{map[string]any{
					"id": 1, "context": name, "state": state, "created_at": nativeInventoryTestTime,
				}}}
			}
			inventory, err := f.publisher().Inventory(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			group := inventory["groups"].([]*nativeInventoryGroupRecord)[0]
			ci := group.Sources[0]["ci"].(map[string]any)
			if group.NextAction.Action != "prepare_replacement" {
				t.Fatalf("native CI %s incorrectly gates preparation: %+v", state, group)
			}
			if state == "success" {
				if !slices.Equal(ci["positive"].([]string), []string{name}) {
					t.Fatalf("positive CI = %+v", ci)
				}
				return
			}
			blocked := ci["blocked"].([]map[string]any)
			want := map[string]any{"name": name, "state": state, "url": "", "pending": state != "failure"}
			if len(blocked) != 1 || !reflect.DeepEqual(blocked[0], want) {
				t.Fatalf("blocked CI = %+v, want %+v", blocked, want)
			}
		})
	}
}

// TestNativeInventoryRequiresOfficialActionTags proves the pin only through the upstream release inventory.
func TestNativeInventoryRequiresOfficialActionTags(t *testing.T) {
	current, previous := nativeInventoryTestSHA(107), nativeInventoryTestSHA(106)
	tag := func(name, sha string) map[string]any {
		return map[string]any{"name": name, "commit": map[string]any{"sha": sha}}
	}
	tests := []struct {
		name, action string
		tags         []any
	}{
		{name: "official version matches immutable pin", action: "prepare_replacement", tags: []any{tag("v4.2.0", current)}},
		{name: "movable major does not prove release pin", action: "blocked", tags: []any{tag("v4", current)}},
		{name: "version on stale pin is not evidence", action: "blocked", tags: []any{tag("v4.2.0", previous)}},
		{name: "duplicate upstream records are incomplete", action: "blocked", tags: []any{tag("v4.2.0", current), tag("v4.2.0", previous)}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			f := newNativeInventoryTestFixture(t)
			f.sources = []any{}
			file := ".github/workflows/unit.yml"
			f.addPull(2, nativePublicationBot, "dependabot/actions", "native Actions pin", "", file, false)
			f.set("contents/"+file+"?ref="+nativeInventoryTestSHA(0), nativePublicationFixtureBlob("    - uses: actions/checkout@"+previous+" # v4.1.0\n"))
			f.set("contents/"+file+"?ref="+nativeInventoryTestSHA(2), nativePublicationFixtureBlob("    - uses: actions/checkout@"+current+" # v4.2.0\n"))
			f.set("/repos/actions/checkout/tags", test.tags)
			inventory, err := f.publisher().Inventory(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			group := inventory["groups"].([]*nativeInventoryGroupRecord)[0]
			if group.NextAction.Action != test.action || (test.action == "prepare_replacement" && (group.Ecosystem != "actions" || group.Target != current)) {
				t.Fatalf("action tag evidence produced %+v", group)
			}
		})
	}
}

// TestNativeInventoryObservesClosure covers newest exact merged ownership, pending subsets and comment provenance.
func TestNativeInventoryObservesClosure(t *testing.T) {
	first := nativePublicationSource{Number: 1, HeadSHA: nativeInventoryTestSHA(1)}
	second := nativePublicationSource{Number: 2, HeadSHA: nativeInventoryTestSHA(2)}
	stale := nativePublicationSource{Number: 1, HeadSHA: nativeInventoryTestSHA(9)}
	marker := func(head string, replacement int) string {
		return "<!-- " + nativePublicationMarker + "close:" + head + ":" + strconv.Itoa(replacement) + " -->\n"
	}
	tests := []struct {
		name, action string
		replacement  int
		sources      []nativePublicationSource
		setup        func(*nativeInventoryTestFixture)
	}{
		{name: "exact subset and rebased source", action: "close_superseded", replacement: 100, sources: []nativePublicationSource{second}, setup: func(f *nativeInventoryTestFixture) { f.addReplacement(100, true, stale, second) }},
		{name: "stale head and wrong source number", action: "prepare_replacement", setup: func(f *nativeInventoryTestFixture) {
			f.addReplacement(100, true, stale, nativePublicationSource{Number: 4, HeadSHA: second.HeadSHA})
		}},
		{name: "newest merged exact subset before open", action: "close_superseded", replacement: 101, sources: []nativePublicationSource{second}, setup: func(f *nativeInventoryTestFixture) {
			f.addReplacement(102, false, first, second)
			f.addReplacement(101, true, second)
			f.addReplacement(100, true, first)
		}},
		{name: "fall back to older exact subset", action: "close_superseded", replacement: 100, sources: []nativePublicationSource{first}, setup: func(f *nativeInventoryTestFixture) {
			f.addReplacement(100, true, first)
			f.addReplacement(101, true, stale)
		}},
		{name: "deduplicate trusted requests while ignoring humans stale copied and non-final commands", action: "close_superseded", replacement: 100, sources: []nativePublicationSource{second}, setup: func(f *nativeInventoryTestFixture) {
			f.addReplacement(100, true, first, second)
			f.addComment(1, nativePublicationRobot, marker(first.HeadSHA, 100)+"verified\n/close\n")
			f.addComment(1, nativePublicationRobot, marker(first.HeadSHA, 100)+"/close\t")
			f.addComment(2, "human", marker(second.HeadSHA, 100)+"/close")
			f.addComment(2, nativePublicationRobot, marker(stale.HeadSHA, 100)+"/close")
			f.addComment(2, nativePublicationRobot, "copied\n"+marker(second.HeadSHA, 100)+"/close")
			f.addComment(2, nativePublicationRobot, marker(second.HeadSHA, 100)+"/close\nnot final")
			f.addComment(2, nativePublicationRobot, marker(second.HeadSHA, 999)+"/close")
		}},
		{name: "all verified current-head requests pending", action: "wait_for_closure", sources: []nativePublicationSource{first, second}, setup: func(f *nativeInventoryTestFixture) {
			f.addReplacement(100, true, first, second)
			f.addComment(2, nativePublicationRobot, marker(second.HeadSHA, 100)+"/close")
			f.addComment(1, nativePublicationRobot, marker(first.HeadSHA, 100)+"/close")
		}},
		{name: "source hold blocks closure", action: "blocked", setup: func(f *nativeInventoryTestFixture) {
			f.addReplacement(100, true, first)
			f.hold(1)
		}},
		{name: "open hold blocks closure", action: "blocked", replacement: 101, setup: func(f *nativeInventoryTestFixture) {
			f.addReplacement(100, true, first)
			f.addReplacement(101, false, first)
			f.hold(101)
		}},
		{name: "ambiguous open replacements block closure", action: "blocked", setup: func(f *nativeInventoryTestFixture) {
			f.addReplacement(100, true, first)
			f.addReplacement(101, false, first)
			f.addReplacement(102, false, second)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			f := newNativeInventoryTestFixture(t)
			f.addGoSource(2, "internal/go.mod", "v1.1.0")
			test.setup(f)
			inventory, err := f.publisher().Inventory(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			group := inventory["groups"].([]*nativeInventoryGroupRecord)[0]
			got := group.NextAction
			if got.Action != test.action || got.ReplacementPR != test.replacement || !slices.Equal(got.SourcePRs, test.sources) || len(group.Sources) != 2 {
				t.Fatalf("decision = %+v; want %s on #%d for %+v; group sources %+v", got, test.action, test.replacement, test.sources, group.Sources)
			}
		})
	}
}

// TestNativeInventoryFailsClosed rejects incomplete creator inventory, unavailable evidence and base races.
func TestNativeInventoryFailsClosed(t *testing.T) {
	tests := []struct {
		name, want string
		setup      func(*nativeInventoryTestFixture)
	}{
		{name: "main moves", want: "main changed during inventory", setup: func(f *nativeInventoryTestFixture) { f.moveMain = true }},
		{name: "duplicate creator inventory", want: "foreign, duplicate or incomplete creator inventory", setup: func(f *nativeInventoryTestFixture) { f.sources = append(f.sources, f.sources[0]) }},
		{name: "wrong creator", want: "foreign, duplicate or incomplete creator inventory", setup: func(f *nativeInventoryTestFixture) {
			f.sources[0].(map[string]any)["user"] = map[string]any{"login": "unrelated"}
		}},
		{name: "transport failure", want: "read source snapshot", setup: func(f *nativeInventoryTestFixture) {
			f.api["pulls/1/files"] = nativeInventoryTestResponse{status: http.StatusServiceUnavailable, body: map[string]any{"message": "temporarily unavailable"}}
		}},
		{name: "malformed owned replacement", want: "validate inventory replacement #100", setup: func(f *nativeInventoryTestFixture) {
			f.addReplacement(100, false, nativePublicationSource{Number: 1, HeadSHA: nativeInventoryTestSHA(1)})["body"] = "<!-- " + nativePublicationMarker + "invalid -->"
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			f := newNativeInventoryTestFixture(t)
			test.setup(f)
			inventory, err := f.publisher().Inventory(context.Background())
			if inventory != nil || err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("inventory = %+v, err = %v; want %q", inventory, err, test.want)
			}
		})
	}
}

// TestNativeInventoryJSON retains empty arrays, booleans and merged-versus-open field presence.
func TestNativeInventoryJSON(t *testing.T) {
	f := newNativeInventoryTestFixture(t)
	identity := nativePublicationSource{Number: 1, HeadSHA: nativeInventoryTestSHA(1)}
	f.addReplacement(100, true, identity)
	f.addReplacement(101, false, identity)
	inventory, err := f.publisher().Inventory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(inventory)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Groups []struct {
			Blockers []string                     `json:"blockers"`
			Sources  []map[string]json.RawMessage `json:"sources"`
		} `json:"groups"`
		Replacements []map[string]json.RawMessage `json:"replacements"`
	}
	if err := json.Unmarshal(payload, &wire); err != nil {
		t.Fatal(err)
	}
	if len(wire.Groups) != 1 || len(wire.Groups[0].Sources) != 1 || wire.Groups[0].Blockers == nil || len(wire.Replacements) != 2 {
		t.Fatalf("wrong inventory wire shape: %s", payload)
	}
	source := wire.Groups[0].Sources[0]
	if string(source["held"]) != "false" || string(source["blocked_holds"]) != "false" || string(source["closure_requests"]) != "[]" {
		t.Fatalf("wrong source evidence wire shape: %s", payload)
	}
	merged, open := wire.Replacements[0], wire.Replacements[1]
	if merged["ci"] != nil || merged["held"] != nil || merged["blocked_holds"] != nil || string(merged["publication_pending"]) != "false" ||
		string(open["held"]) != "false" || string(open["blocked_holds"]) != "false" || string(open["publication_pending"]) != "true" {
		t.Fatalf("wrong replacement wire shape: %s", payload)
	}
	var ci struct {
		Positive []string          `json:"positive"`
		Blocked  []json.RawMessage `json:"blocked"`
	}
	if err := json.Unmarshal(open["ci"], &ci); err != nil || ci.Positive == nil || ci.Blocked == nil {
		t.Fatalf("wrong CI wire shape: %s, %v", payload, err)
	}
}

// TestNativeInventoryCannotPublish verifies the read-only option rejects a publication request before reading credentials or files.
func TestNativeInventoryCannotPublish(t *testing.T) {
	client := github.NewClient(nil)
	publisher, err := NewNativePublisher(nativeInventoryTestRepository, WithNativeInventoryClients(client, client))
	if err != nil {
		t.Fatal(err)
	}
	result, err := publisher.Publish(context.Background(), NativePublishOptions{Request: &NativePublicationRequest{}})
	if result != nil || err == nil || !strings.Contains(err.Error(), "requires an injected writer client") {
		t.Fatalf("publication = %+v, %v", result, err)
	}
}
