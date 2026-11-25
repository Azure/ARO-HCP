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

package publish

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	githublib "github.com/Azure/ARO-HCP/tooling/github"
)

const (
	testHeadSHA = "cccccccccccccccccccccccccccccccccccccccc"
	testBaseSHA = "dddddddddddddddddddddddddddddddddddddddd"
	testTipSHA  = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	testActor   = "aro-hcp-robot[bot]"
	testMarker  = "<!-- aro-hcp-image-updater -->"
	testBranch  = "bot/image-updater"
	testFile    = "tooling/image-updater/config.yaml"
)

// fakePublisher records publication calls for assertions.
type fakePublisher struct {
	open      []githublib.PullRequest
	openErr   error
	published []githublib.PublicationRequest
	receipt   githublib.Receipt
}

// OpenPullRequests returns the configured open proposal inventory.
func (f *fakePublisher) OpenPullRequests(context.Context) ([]githublib.PullRequest, error) {
	return f.open, f.openErr
}

// Publish records the request and returns the configured receipt.
func (f *fakePublisher) Publish(_ context.Context, request githublib.PublicationRequest) (githublib.Receipt, error) {
	f.published = append(f.published, request)
	return f.receipt, nil
}

// gitRecorder records git interactions and serves fixed repository state.
type gitRecorder struct {
	committed    []string
	commitPaths  []string
	head         string
	baseCommit   string
	files        []string
	remoteBase   string
	branchTip    string
	pushes       []string
	pushExpected []string
	err          error
}

// ops builds the injectable git surface from the recorded state.
func (r *gitRecorder) ops() gitOps {
	return gitOps{
		stagedPaths: func(context.Context) ([]string, error) { return nil, r.err },
		commit: func(_ context.Context, _ string, _ string, _ string, paths []string) ([]string, error) {
			r.commitPaths = paths
			return r.committed, r.err
		},
		head: func(context.Context) (string, error) { return r.head, r.err },
		revParse: func(context.Context, string) (string, error) {
			return r.baseCommit, r.err
		},
		changedFiles: func(context.Context, string) ([]string, error) {
			return r.files, r.err
		},
		remoteTip: func(_ context.Context, ref string) (string, error) {
			if ref == "main" {
				return r.remoteBase, r.err
			}
			return r.branchTip, r.err
		},
		push: func(_ context.Context, branch, expected string) error {
			r.pushes = append(r.pushes, branch)
			r.pushExpected = append(r.pushExpected, expected)
			return r.err
		},
	}
}

// newTestOptions builds publication options over the recorded state.
func newTestOptions(git *gitRecorder, publisher *fakePublisher, out *bytes.Buffer) *Options {
	return &Options{
		input: Input{
			Repo:          ".",
			Base:          "main",
			BaseCommit:    testBaseSHA,
			HeadRepo:      "Azure/ARO-HCP",
			Branch:        testBranch,
			Title:         "Automated - Update component image digests",
			Marker:        testMarker,
			CommitMessage: "chore: update component image digests",
			GitName:       testActor,
			GitEmail:      "aro-hcp-robot[bot]@users.noreply.github.com",
			Actor:         testActor,
			Paths:         []string{testFile},
			Body:          []byte("digest summary"),
		},
		out:          out,
		git:          git.ops(),
		publisherFor: func(context.Context) (Publisher, error) { return publisher, nil },
	}
}

// ownedProposal returns the rolling proposal owned by the bot.
func ownedProposal() githublib.PullRequest {
	return githublib.PullRequest{
		Number:   42,
		Author:   testActor,
		Title:    "Automated - Update component image digests",
		Body:     "previous summary\n" + testMarker + "\n",
		HeadRepo: "Azure/ARO-HCP",
		HeadRef:  testBranch,
		HeadSHA:  testTipSHA,
		BaseRef:  "main",
		BaseRepo: "Azure/ARO-HCP",
		State:    "open",
	}
}

// TestRunPublishesRollingProposal covers the update and creation paths.
func TestRunPublishesRollingProposal(t *testing.T) {
	tests := []struct {
		name          string
		open          []githublib.PullRequest
		branchTip     string
		files         []string
		committed     []string
		wantErr       error
		wantNumber    int
		wantTitle     string
		wantPrevious  string
		wantBase      string
		wantLease     string
		wantPublishes int
	}{
		{
			name:          "updates the rolling proposal",
			open:          []githublib.PullRequest{ownedProposal()},
			branchTip:     testTipSHA,
			files:         []string{testFile},
			committed:     []string{testFile},
			wantNumber:    42,
			wantTitle:     "Automated - Update component image digests",
			wantPrevious:  "previous summary\n" + testMarker + "\n",
			wantLease:     testTipSHA,
			wantPublishes: 1,
		},
		{
			name:          "creates the first proposal",
			files:         []string{testFile},
			committed:     []string{testFile},
			wantPublishes: 1,
		},
		{
			name:      "refuses a branch proposal it does not own",
			open:      []githublib.PullRequest{{Number: 7, Author: "someone", HeadRepo: "Azure/ARO-HCP", HeadRef: testBranch, HeadSHA: testTipSHA, Body: "manual"}},
			branchTip: testTipSHA,
			files:     []string{testFile},
			committed: []string{testFile},
			wantErr:   githublib.ErrConflict,
		},
		{
			name:      "refuses an owned proposal on another branch",
			open:      []githublib.PullRequest{{Number: 8, Author: testActor, HeadRepo: "Azure/ARO-HCP", HeadRef: "bot/other", HeadSHA: testTipSHA, Body: testMarker}},
			files:     []string{testFile},
			committed: []string{testFile},
			wantErr:   githublib.ErrConflict,
		},
		{
			name:      "refuses several proposals claiming the branch",
			open:      []githublib.PullRequest{ownedProposal(), {Number: 43, Author: testActor, HeadRepo: "Azure/ARO-HCP", HeadRef: testBranch, Body: testMarker}},
			branchTip: testTipSHA,
			files:     []string{testFile},
			committed: []string{testFile},
			wantErr:   githublib.ErrConflict,
		},
		{
			name:      "refuses a proposal whose head moved",
			open:      []githublib.PullRequest{ownedProposal()},
			branchTip: testBaseSHA,
			files:     []string{testFile},
			committed: []string{testFile},
			wantErr:   githublib.ErrConflict,
		},
		{
			name:      "refuses a change set outside the intended paths",
			files:     []string{testFile, "backend/Dockerfile"},
			committed: []string{testFile, "backend/Dockerfile"},
			wantErr:   githublib.ErrConflict,
		},
		{
			name:          "publishes against an advanced base",
			files:         []string{testFile},
			committed:     []string{testFile},
			wantBase:      testTipSHA,
			wantPublishes: 1,
		},
		{
			name:    "refuses an empty commit",
			wantErr: errNoChanges,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			git := &gitRecorder{
				head:       testHeadSHA,
				baseCommit: testBaseSHA,
				remoteBase: testBaseSHA,
				files:      test.files,
				committed:  test.committed,
				branchTip:  test.branchTip,
			}
			if test.wantBase != "" {
				git.remoteBase = test.wantBase
			}
			publisher := &fakePublisher{open: test.open, receipt: githublib.Receipt{Outcome: "observed"}}
			out := &bytes.Buffer{}
			err := newTestOptions(git, publisher, out).Run(context.Background())
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("Run() error = %v, want %v", err, test.wantErr)
			}
			if test.wantErr != nil {
				if len(publisher.published) != 0 || len(git.pushes) != 0 {
					t.Fatalf("failure still published %d requests and pushed %v", len(publisher.published), git.pushes)
				}
				return
			}
			if len(git.commitPaths) != 1 || git.commitPaths[0] != testFile {
				t.Errorf("commit paths = %v, want [%s]", git.commitPaths, testFile)
			}
			if len(publisher.published) != test.wantPublishes {
				t.Fatalf("published %d requests, want %d", len(publisher.published), test.wantPublishes)
			}
			request := publisher.published[0]
			if request.ExpectedNumber != test.wantNumber {
				t.Errorf("ExpectedNumber = %d, want %d", request.ExpectedNumber, test.wantNumber)
			}
			if request.ExpectedTitle != test.wantTitle {
				t.Errorf("ExpectedTitle = %q, want %q", request.ExpectedTitle, test.wantTitle)
			}
			if request.ExpectedBody != test.wantPrevious {
				t.Errorf("ExpectedBody = %q, want %q", request.ExpectedBody, test.wantPrevious)
			}
			wantBase := testBaseSHA
			if test.wantBase != "" {
				wantBase = test.wantBase
			}
			if request.ExpectedHead != testHeadSHA || request.ExpectedBase != wantBase {
				t.Errorf("expected head/base = %s/%s, want %s/%s", request.ExpectedHead, request.ExpectedBase, testHeadSHA, wantBase)
			}
			if !request.ReadyForReview {
				t.Error("ReadyForReview = false, want true")
			}
			if want := "digest summary\n" + testMarker + "\n"; request.Body != want {
				t.Errorf("Body = %q, want %q", request.Body, want)
			}
			if len(request.ExpectedFiles) != len(test.files) {
				t.Errorf("ExpectedFiles = %v, want %v", request.ExpectedFiles, test.files)
			}
			if len(git.pushExpected) != 1 || git.pushExpected[0] != test.wantLease {
				t.Errorf("push lease = %v, want [%q]", git.pushExpected, test.wantLease)
			}
			var receipt githublib.Receipt
			if err := json.Unmarshal(out.Bytes(), &receipt); err != nil {
				t.Fatalf("decode the receipt: %v", err)
			}
			if receipt.Outcome != "observed" {
				t.Errorf("receipt outcome = %q, want observed", receipt.Outcome)
			}
		})
	}
}

// TestRunDryRunCommitsWithoutPublishing covers the dry-run boundary.
func TestRunDryRunCommitsWithoutPublishing(t *testing.T) {
	git := &gitRecorder{committed: []string{testFile}}
	publisher := &fakePublisher{}
	out := &bytes.Buffer{}
	options := newTestOptions(git, publisher, out)
	options.input.DryRun = true
	options.publisherFor = func(context.Context) (Publisher, error) {
		t.Fatal("a dry run must not build a publisher")
		return nil, nil
	}
	options.token = func(context.Context) (string, error) {
		t.Fatal("a dry run must not read credentials")
		return "", nil
	}
	if err := options.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(git.commitPaths) != 1 || git.commitPaths[0] != testFile {
		t.Errorf("commit paths = %v, want [%s]", git.commitPaths, testFile)
	}
	if len(git.pushes) != 0 || len(publisher.published) != 0 {
		t.Fatalf("dry run pushed %v or published %d requests", git.pushes, len(publisher.published))
	}
	if !strings.Contains(out.String(), "Dry run") {
		t.Errorf("dry run output = %q", out.String())
	}
}
