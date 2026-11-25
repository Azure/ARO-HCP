// Copyright 2026 Microsoft Corporation
// Licensed under the Apache License, Version 2.0.

package dependabot

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	gh "github.com/google/go-github/v69/github"
)

// NativeIntegrationFixture couples real Git object/ref storage to the public SDK HTTP boundary.
type nativeIntegrationFixture struct {
	t                          *testing.T
	root, remote, base, branch string
	publisher                  *NativePublisher
	pulls                      map[int]map[string]any
	files                      map[int][]any
	comments, events           map[int][]any
	overrides                  map[string]any
	writes                     []string
	failWrite                  string
	labelExists                bool
	boundary                   func(string)
	raceFile                   string
}

// NewNativeIntegrationFixture isolates Git transport in PATH without weakening production URL validation.
func newNativeIntegrationFixture(t *testing.T) *nativeIntegrationFixture {
	t.Helper()
	f := &nativeIntegrationFixture{t: t, root: t.TempDir(), remote: filepath.Join(t.TempDir(), "remote.git"), pulls: map[int]map[string]any{}, files: map[int][]any{}, comments: map[int][]any{}, events: map[int][]any{}, overrides: map[string]any{}, labelExists: true}
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	nativePublicationTestGit(t, filepath.Dir(f.remote), "init", "--bare", "--quiet", "--template=", f.remote)
	nativePublicationTestGit(t, f.root, "init", "--quiet", "--template=")
	nativePublicationTestGit(t, f.root, "symbolic-ref", "HEAD", "refs/heads/main")
	f.writeGo("v1.0.0", "1")
	f.write("backend/Dockerfile", "FROM example.invalid/base:1.0\n")
	f.write("api/Makefile", "models:\n\t@echo unchanged\n")
	f.commit("base")
	f.base = f.git("rev-parse", "HEAD")
	f.git("push", f.remote, "HEAD:refs/heads/main")
	f.git("remote", "add", "origin", "https://github.com/owner/repo.git")
	f.addSource(1, "backend/go.mod", strings.ReplaceAll(f.git("show", f.base+":backend/go.mod")+"\n", "v1.0.0", "v1.1.0"))
	f.branch = "native-dependabot-" + fmt.Sprintf("%x", sha256.Sum256([]byte("go:example.com/library")))[:24]
	bin := t.TempDir()
	f.raceFile = filepath.Join(bin, "race")
	// The test alone translates the immutable canonical endpoint and its transport prohibition.
	// A one-shot competing ref is installed immediately before an actual non-force push.
	wrapper := "#!/bin/bash\nset -eu\nargs=()\npush=false\nfor arg in \"$@\"; do\n case \"$arg\" in\n https://github.com/owner/repo.git) args+=(" + strconv.Quote(f.remote) + ");;\n protocol.file.allow=never) args+=(protocol.file.allow=always);;\n push) push=true; args+=(\"$arg\");;\n *) args+=(\"$arg\");;\n esac\ndone\nif $push && [[ -f " + strconv.Quote(f.raceFile) + " ]]; then\n read -r ref sha < " + strconv.Quote(f.raceFile) + "\n " + strconv.Quote(realGit) + " --git-dir=" + strconv.Quote(f.remote) + " update-ref \"$ref\" \"$sha\"\n rm " + strconv.Quote(f.raceFile) + "\nfi\nexec " + strconv.Quote(realGit) + " \"${args[@]}\"\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(wrapper), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	server := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(server.Close)
	api := gh.NewClient(server.Client())
	api.BaseURL, err = url.Parse(server.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	f.publisher, err = NewNativePublisher("owner/repo", WithNativePublicationClients(api, api, api))
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// Git executes real hardened Git plumbing against the disposable working repository.
func (f *nativeIntegrationFixture) git(args ...string) string {
	f.t.Helper()
	return nativePublicationTestGit(f.t, f.root, args...)
}

// RemoteGit reads or changes only the disposable bare fixture repository.
func (f *nativeIntegrationFixture) remoteGit(args ...string) string {
	f.t.Helper()
	return nativePublicationTestGit(f.t, f.remote, args...)
}

// Write materializes one ordinary fixture file without executing consumer content.
func (f *nativeIntegrationFixture) write(name, text string) {
	f.t.Helper()
	name = filepath.Join(f.root, name)
	if err := os.MkdirAll(filepath.Dir(name), 0700); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(text), 0600); err != nil {
		f.t.Fatal(err)
	}
}

// WriteGo updates every real Go consumer, including both vendored dependency trees.
func (f *nativeIntegrationFixture) writeGo(version, revision string) {
	for _, directory := range []string{"backend", "internal", "image-sync/oc-mirror/acrauth"} {
		f.write(directory+"/go.mod", "module example.test/"+directory+"\n\ngo 1.26.0\n\nrequire example.com/library "+version+"\n")
	}
	for _, prefix := range []string{"vendor/", "image-sync/oc-mirror/acrauth/vendor/"} {
		f.write(prefix+"modules.txt", "# example.com/library "+version+"\n## explicit; go 1.26.0\nexample.com/library\n")
		f.write(prefix+"example.com/library/library.go", "package library\nconst Version = "+revision+"\n")
	}
}

// Commit records fixture content using Git's normal object and index implementation.
func (f *nativeIntegrationFixture) commit(message string) {
	f.t.Helper()
	f.git("add", ".")
	f.git("commit", "--quiet", "-m", message)
}

// AddSource creates immutable native-branch bytes and their complete GitHub snapshot metadata.
func (f *nativeIntegrationFixture) addSource(number int, filename, content string) string {
	f.t.Helper()
	ref := "dependabot/" + strconv.Itoa(number)
	f.git("checkout", "--quiet", "-B", ref, f.base)
	f.write(filename, content)
	f.commit("native upgrade " + strconv.Itoa(number))
	sha := f.git("rev-parse", "HEAD")
	f.git("push", f.remote, "HEAD:refs/heads/"+ref)
	f.git("checkout", "--quiet", "main")
	f.pulls[number] = f.pull(number, nativePublicationBot, ref, sha, "native source", "")
	f.files[number] = []any{map[string]any{"filename": filename, "status": "modified"}}
	f.comments[number], f.events[number] = []any{}, []any{}
	return sha
}

// Pull supplies all detail and list fields required for complete shared and native authorization.
func (f *nativeIntegrationFixture) pull(number int, actor, ref, sha, title, body string) map[string]any {
	return map[string]any{"number": number, "state": "open", "merged": false, "draft": false, "title": title, "body": body, "labels": []any{}, "user": map[string]any{"login": actor, "type": "Bot"}, "head": map[string]any{"ref": ref, "sha": sha, "repo": map[string]any{"full_name": "owner/repo"}}, "base": map[string]any{"ref": "main", "sha": f.base, "repo": map[string]any{"full_name": "owner/repo"}}, "commits": 1, "changed_files": 1, "comments": 0}
}

// Request preserves exact real diff bytes for all regenerated dependency consumers.
func (f *nativeIntegrationFixture) request(version, revision string, numbers ...int) *NativePublicationRequest {
	f.t.Helper()
	f.writeGo(version, revision)
	patch := f.git("diff", "--no-ext-diff", "--no-textconv", "--no-renames") + "\n"
	f.git("restore", ".")
	sources := make([]nativePublicationSource, 0, len(numbers))
	for _, number := range numbers {
		sources = append(sources, nativePublicationSource{Number: number, HeadSHA: f.pulls[number]["head"].(map[string]any)["sha"].(string)})
	}
	return &NativePublicationRequest{request: nativePublicationRequest{SourcePRs: sources, ExpectedBaseSHA: f.base, Patch: patch, Title: "chore(deps): upgrade @library", Body: "Validated @library consumers. Unicode: café."}}
}

// Publish invokes the actual production publication entry point with the local SDK clients.
func (f *nativeIntegrationFixture) publish(request *NativePublicationRequest) (map[string]any, error) {
	return f.publisher.Publish(context.Background(), NativePublishOptions{Root: f.root, Request: request})
}

// Published requires a successful verified production receipt, not merely an accepted HTTP mutation.
func (f *nativeIntegrationFixture) published(request *NativePublicationRequest) map[string]any {
	f.t.Helper()
	result, err := f.publish(request)
	if err != nil {
		f.t.Fatal(err)
	}
	return result
}

// Detail reflects real remote branch movement for open proposals, as GitHub does after a push.
func (f *nativeIntegrationFixture) detail(number int) map[string]any {
	pull := f.pulls[number]
	if pull == nil {
		return nil
	}
	if pull["state"] == "open" && pull["user"].(map[string]any)["login"] == nativePublicationRobot {
		head := pull["head"].(map[string]any)
		if sha := f.remoteGit("for-each-ref", "--format=%(objectname)", "refs/heads/"+head["ref"].(string)); sha != "" {
			head["sha"] = sha
		}
	}
	return pull
}

// Respond encodes complete raw JSON maps, preserving explicitly empty label arrays.
func (f *nativeIntegrationFixture) respond(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		f.t.Error(err)
	}
}

// Serve implements GitHub HTTP records and mutations only; all publication decisions remain production code.
func (f *nativeIntegrationFixture) serve(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/repos/owner/repo/")
	if f.boundary != nil {
		f.boundary(r.Method + " " + path)
	}
	key := path
	if ref := r.URL.Query().Get("ref"); ref != "" {
		key += "?ref=" + ref
	}
	if value, exists := f.overrides[key]; exists {
		f.respond(w, value)
		return
	}
	if r.Method != http.MethodGet {
		write := r.Method + " " + path
		f.writes = append(f.writes, write)
		if f.failWrite == write {
			http.Error(w, "uncertain mutation", http.StatusInternalServerError)
			return
		}
		switch {
		case r.Method == http.MethodPost && path == "labels":
			if f.remoteGit("for-each-ref", "--format=%(refname)", "refs/heads/native-dependabot-") != "" {
				f.t.Error("label created after branch publication")
			}
			f.labelExists = true
			f.respond(w, map[string]any{"id": 42, "name": nativePublicationLabel})
		case r.Method == http.MethodPost && path == "pulls":
			var input gh.NewPullRequest
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				f.t.Error(err)
				http.Error(w, "invalid request", 400)
				return
			}
			number := 100
			for f.pulls[number] != nil {
				number++
			}
			sha := f.remoteGit("rev-parse", "refs/heads/"+input.GetHead())
			pull := f.pull(number, nativePublicationRobot, input.GetHead(), sha, input.GetTitle(), input.GetBody())
			pull["draft"] = input.GetDraft()
			f.pulls[number] = pull
			names := strings.Split(f.remoteGit("diff", "--name-only", f.base, sha), "\n")
			files := make([]any, 0, len(names))
			for _, name := range names {
				files = append(files, map[string]any{"filename": name, "status": "modified"})
			}
			f.files[number], f.comments[number], f.events[number] = files, []any{}, []any{}
			pull["changed_files"] = len(files)
			f.respond(w, pull)
		case r.Method == http.MethodPatch && strings.HasPrefix(path, "pulls/"):
			number, _ := strconv.Atoi(strings.TrimPrefix(path, "pulls/"))
			var input gh.PullRequest
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				f.t.Error(err)
				http.Error(w, "invalid request", 400)
				return
			}
			f.pulls[number]["title"], f.pulls[number]["body"] = input.GetTitle(), input.GetBody()
			f.respond(w, f.detail(number))
		case r.Method == http.MethodPost && strings.HasSuffix(path, "/labels"):
			var number int
			_, _ = fmt.Sscanf(path, "issues/%d/labels", &number)
			var labels []string
			if err := json.NewDecoder(r.Body).Decode(&labels); err != nil {
				f.t.Error(err)
			}
			if len(labels) != 1 || labels[0] != nativePublicationLabel {
				f.t.Errorf("unexpected label mutation: %v", labels)
			}
			pull := f.pulls[number]
			pull["labels"] = append(pull["labels"].([]any), map[string]any{"id": 42, "name": nativePublicationLabel})
			f.respond(w, pull["labels"])
		default:
			f.t.Errorf("unexpected mutation %s", write)
			http.Error(w, "unexpected mutation", http.StatusMethodNotAllowed)
		}
		return
	}
	switch {
	case r.URL.Path == "/users/"+nativePublicationRobot:
		f.respond(w, map[string]any{"id": 42, "login": nativePublicationRobot, "type": "Bot"})
	case path == "git/ref/heads/main":
		f.respond(w, map[string]any{"ref": "refs/heads/main", "object": map[string]any{"type": "commit", "sha": f.base}})
	case strings.HasPrefix(path, "branches/"):
		ref := strings.TrimPrefix(path, "branches/")
		f.respond(w, map[string]any{"commit": map[string]any{"sha": f.remoteGit("rev-parse", "refs/heads/"+ref)}})
	case path == "pulls" || path == "issues":
		rows := []any{}
		numbers := make([]int, 0, len(f.pulls))
		for number := range f.pulls {
			numbers = append(numbers, number)
		}
		sort.Ints(numbers)
		for _, number := range numbers {
			pull := f.detail(number)
			if path == "pulls" {
				if pull["state"] == "open" {
					rows = append(rows, pull)
				}
				continue
			}
			if pull["user"].(map[string]any)["login"] != nativePublicationRobot {
				continue
			}
			rows = append(rows, map[string]any{"number": number, "user": pull["user"], "state": pull["state"], "body": pull["body"], "labels": pull["labels"], "pull_request": map[string]any{"url": "https://github.com/owner/repo/pull/" + strconv.Itoa(number)}})
		}
		f.respond(w, rows)
	case strings.HasPrefix(path, "pulls/"):
		parts := strings.Split(path, "/")
		number, _ := strconv.Atoi(parts[1])
		pull := f.detail(number)
		if pull == nil {
			http.NotFound(w, r)
			return
		}
		if len(parts) == 2 {
			f.respond(w, pull)
			return
		}
		switch parts[2] {
		case "files":
			f.respond(w, f.files[number])
		case "commits":
			f.respond(w, []any{map[string]any{"sha": pull["head"].(map[string]any)["sha"], "author": pull["user"]}})
		default:
			f.t.Errorf("unexpected pull evidence %s", path)
			http.NotFound(w, r)
		}
	case strings.HasPrefix(path, "issues/"):
		var number int
		_, _ = fmt.Sscanf(path, "issues/%d/", &number)
		if strings.HasSuffix(path, "/comments") {
			f.respond(w, f.comments[number])
			return
		}
		if strings.HasSuffix(path, "/events") {
			f.respond(w, f.events[number])
			return
		}
		f.t.Errorf("unexpected issue evidence %s", path)
		http.NotFound(w, r)
	case strings.HasPrefix(path, "contents/"):
		filename := strings.TrimPrefix(path, "contents/")
		text := f.remoteGit("show", r.URL.Query().Get("ref")+":"+filename) + "\n"
		f.respond(w, nativePublicationFixtureBlob(text))
	case strings.HasPrefix(path, "compare/"):
		refs := strings.Split(strings.TrimPrefix(path, "compare/"), "...")
		mergeBase := f.remoteGit("merge-base", refs[0], refs[1])
		status := "diverged"
		if mergeBase == refs[0] {
			status = "ahead"
		}
		if refs[0] == refs[1] {
			status = "identical"
		}
		f.respond(w, map[string]any{"status": status, "merge_base_commit": map[string]any{"sha": mergeBase}})
	case strings.HasPrefix(path, "commits/"):
		switch {
		case strings.HasSuffix(path, "/statuses"):
			f.respond(w, []any{})
		case strings.HasSuffix(path, "/check-runs"):
			f.respond(w, map[string]any{"total_count": 0, "check_runs": []any{}})
		default:
			f.respond(w, map[string]any{"sha": strings.TrimPrefix(path, "commits/"), "author": map[string]any{"login": nativePublicationRobot, "type": "Bot"}})
		}
	case path == "labels/"+nativePublicationLabel:
		if !f.labelExists {
			http.NotFound(w, r)
			return
		}
		f.respond(w, map[string]any{"id": 42, "name": nativePublicationLabel})
	default:
		f.t.Errorf("unexpected SDK request %s %s", r.Method, r.URL)
		http.NotFound(w, r)
	}
}
