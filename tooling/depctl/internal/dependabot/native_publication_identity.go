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
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/google/go-github/v69/github"

	"github.com/Azure/ARO-HCP/tooling/depctl/internal/forge"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/model"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/policy"
	automation "github.com/Azure/ARO-HCP/tooling/github"
)

const (
	nativePublicationBot    = "dependabot[bot]"
	nativePublicationRobot  = "aro-hcp-robot[bot]"
	nativePublicationLabel  = "agentic-dependency-upgrade"
	nativePublicationMarker = "native-dependabot-maintenance:"
)

var nativePublicationMarkerLine = regexp.MustCompile(`(?m)^<!-- native-dependabot-maintenance:(\{[^\n]*\}) -->$`)
var nativePublicationGroupID = regexp.MustCompile(`^[0-9a-f]{24}$`)

// Native publication authorization retains verified SDK records for later rechecks.
type nativePublicationAuthorization struct {
	group    map[string]any
	existing *github.PullRequest
	merged   []*github.PullRequest
	closed   []*github.PullRequest
}

// Native publication snapshots supplement shared evidence with source file identities.
type nativePublicationSnapshot struct {
	pull                *github.PullRequest
	files               []*github.CommitFile
	blocked             bool
	holds               []model.ActiveHold
	cancellationPending bool
	comments            []automation.Comment
	ci                  automation.CommitCI
}

// NativePublicationMetadata validates the exact ownership marker and its immutable source identities.
func nativePublicationMetadata(body string) (map[string]any, error) {
	matches := nativePublicationMarkerLine.FindAllStringSubmatch(body, -1)
	if len(matches) != 1 {
		return nil, nativeReject("replacement must have exactly one ownership marker")
	}
	value, err := nativeDecode([]byte(matches[0][1]))
	if err != nil {
		return nil, fmt.Errorf("decode replacement ownership: %w", err)
	}
	meta, err := nativeObject(value)
	if err != nil {
		return nil, fmt.Errorf("read replacement ownership: %w", err)
	}
	group, _ := meta["group"].(string)
	if len(meta) != 2 || !nativePublicationGroupID.MatchString(group) {
		return nil, nativeReject("malformed replacement ownership marker")
	}
	sources, ok := meta["sources"].([]any)
	if !ok || len(sources) == 0 || len(sources) > 100 {
		return nil, nativeReject("missing source PRs")
	}
	var previous int64
	for _, value := range sources {
		source, ok := value.(map[string]any)
		if !ok || len(source) != 2 {
			return nil, nativeReject("invalid source schema")
		}
		number, ok := nativeInteger(source["number"])
		head, _ := source["head_sha"].(string)
		if !ok || number <= previous || !nativeSHA.MatchString(head) {
			return nil, nativeReject("invalid or unsorted source identity")
		}
		previous = number
	}
	return meta, nil
}

// MainSHA reads the exact main ref without resolving a mutable abbreviation.
func (p *NativePublisher) mainSHA(ctx context.Context) (string, error) {
	ref, _, err := p.api.Git.GetRef(ctx, p.owner, p.repo, "heads/main")
	if err != nil {
		return "", fmt.Errorf("read main ref: %w", err)
	}
	if ref == nil || ref.GetRef() != "refs/heads/main" || ref.GetObject().GetType() != "commit" || !nativeSHA.MatchString(ref.GetObject().GetSHA()) {
		return "", nativeReject("missing main SHA")
	}
	return ref.GetObject().GetSHA(), nil
}

// PullDetail binds a detail response to the requested positive pull number.
func (p *NativePublisher) pullDetail(ctx context.Context, number int) (*github.PullRequest, error) {
	if number <= 0 {
		return nil, nativeReject("invalid pull request number")
	}
	pull, _, err := p.api.PullRequests.Get(ctx, p.owner, p.repo, number)
	if err != nil {
		return nil, fmt.Errorf("read pull request #%d: %w", number, err)
	}
	if pull == nil || pull.GetNumber() != number {
		return nil, nativeReject("pull request number mismatch")
	}
	return pull, nil
}

// NativePublicationIdentity enforces bot, ready state, same-repository head and main base.
func (p *NativePublisher) nativePublicationIdentity(pull *github.PullRequest, author string, merged bool) error {
	if pull == nil || pull.GetNumber() <= 0 || pull.GetUser().GetLogin() != author || pull.GetUser().GetType() != "Bot" {
		return nativeReject("foreign or non-bot pull request author")
	}
	if pull.GetBase().GetRepo().GetFullName() != p.repository || pull.GetHead().GetRepo().GetFullName() != p.repository || pull.GetBase().GetRef() != "main" {
		return nativeReject("foreign repository, fork, or base")
	}
	if !nativeSHA.MatchString(pull.GetHead().GetSHA()) || pull.GetHead().GetRef() == "" {
		return nativeReject("missing pull request SHA or branch")
	}
	if pull.Labels == nil {
		return nativeReject("missing labels")
	}
	for _, label := range pull.Labels {
		if label == nil || label.Name == nil {
			return nativeReject("missing label identity")
		}
	}
	state := "open"
	if merged {
		state = "closed"
	}
	if pull.GetState() != state || pull.Merged == nil || pull.GetMerged() != merged || pull.Draft == nil || pull.GetDraft() {
		return nativeReject("pull request is not in the required ready/merged state")
	}
	return nil
}

// NativePublicationHasLabel matches the fixed label exactly, never a substring.
func nativePublicationHasLabel(pull *github.PullRequest, name string) bool {
	for _, label := range pull.Labels {
		if label.GetName() == name {
			return true
		}
	}
	return false
}

// ValidateReplacement requires both verified robot identity and structural ownership evidence.
func (p *NativePublisher) validateReplacement(pull *github.PullRequest, merged, publicationWindow bool) (map[string]any, error) {
	if err := p.nativePublicationIdentity(pull, nativePublicationRobot, merged); err != nil {
		return nil, fmt.Errorf("validate replacement identity: %w", err)
	}
	meta, err := nativePublicationMetadata(pull.GetBody())
	if err != nil {
		return nil, fmt.Errorf("validate replacement marker: %w", err)
	}
	if (!publicationWindow && !nativePublicationHasLabel(pull, nativePublicationLabel)) || !strings.HasPrefix(pull.GetTitle(), "chore(deps): ") || pull.GetHead().GetRef() != "native-dependabot-"+meta["group"].(string) {
		return nil, nativeReject("unverified replacement identity")
	}
	return meta, nil
}

// NativePublicationBlockedHolds uses shared hold replay without letting native-source CI authorize writes.
func nativePublicationBlockedHolds(snapshot automation.Snapshot) (holds []model.ActiveHold, pending, blocked bool, err error) {
	comments := make([]forge.Comment, len(snapshot.Comments))
	for index, comment := range snapshot.Comments {
		comments[index] = forge.Comment(comment)
	}
	events := make([]forge.HoldEvent, len(snapshot.HoldEvents))
	for index, event := range snapshot.HoldEvents {
		events[index] = forge.HoldEvent(event)
	}
	commits := make(map[string]bool, len(snapshot.Commits))
	for _, sha := range snapshot.Commits {
		commits[sha] = true
	}
	holds, pending, err = policy.ActiveHolds(comments, commits)
	if err != nil {
		return nil, false, false, fmt.Errorf("evaluate hold commands: %w", err)
	}
	trusted, err := policy.TrustedHoldLabel(events)
	if err != nil {
		return nil, false, false, fmt.Errorf("evaluate hold label provenance: %w", err)
	}
	for _, hold := range holds {
		if hold.Kind != "retry-exhaustion" {
			return holds, pending, true, nil
		}
	}
	for _, label := range snapshot.Pull.Labels {
		if label == policy.HoldLabel && (!trusted || len(holds) == 0 && !pending) {
			return holds, pending, true, nil
		}
	}
	return holds, pending, false, nil
}

// SameNativePublicationSnapshotPull binds authorization-relevant metadata, not mutable embedded repository data.
func sameNativePublicationSnapshotPull(before, after *github.PullRequest) bool {
	if before.GetNumber() != after.GetNumber() || before.GetState() != after.GetState() ||
		before.GetUser().GetLogin() != after.GetUser().GetLogin() || before.GetUser().GetType() != after.GetUser().GetType() ||
		before.GetHead().GetSHA() != after.GetHead().GetSHA() || before.GetHead().GetRef() != after.GetHead().GetRef() ||
		before.GetHead().GetRepo().GetFullName() != after.GetHead().GetRepo().GetFullName() ||
		before.GetBase().GetSHA() != after.GetBase().GetSHA() || before.GetBase().GetRef() != after.GetBase().GetRef() ||
		before.GetBase().GetRepo().GetFullName() != after.GetBase().GetRepo().GetFullName() ||
		after.Draft == nil || before.GetDraft() != after.GetDraft() || after.Merged == nil || before.GetMerged() != after.GetMerged() ||
		after.Commits == nil || before.GetCommits() != after.GetCommits() ||
		after.Comments == nil || before.GetComments() != after.GetComments() ||
		after.ChangedFiles == nil || before.GetChangedFiles() != after.GetChangedFiles() ||
		before.GetTitle() != after.GetTitle() || before.GetBody() != after.GetBody() || after.Labels == nil {
		return false
	}
	return slices.EqualFunc(before.Labels, after.Labels, func(a, b *github.Label) bool {
		return b != nil && b.Name != nil && a.GetName() == b.GetName()
	})
}

// NativePublicationSnapshot verifies complete immutable files, shared commit attribution and hold evidence.
func (p *NativePublisher) nativePublicationSnapshot(ctx context.Context, number int, head, author string) (nativePublicationSnapshot, error) {
	var result nativePublicationSnapshot
	pull, err := p.pullDetail(ctx, number)
	if err != nil {
		return result, err
	}
	if err := p.nativePublicationIdentity(pull, author, false); err != nil {
		return result, fmt.Errorf("validate snapshot #%d: %w", number, err)
	}
	if pull.GetHead().GetSHA() != head || !nativeSHA.MatchString(pull.GetBase().GetSHA()) || pull.Commits == nil || pull.ChangedFiles == nil || pull.Comments == nil || pull.GetCommits() <= 0 || pull.GetChangedFiles() <= 0 {
		return result, nativeReject("stale head or missing complete snapshot counts/base")
	}
	shared, err := p.shared.Snapshot(ctx, number, head)
	if err != nil {
		return result, fmt.Errorf("collect shared snapshot #%d: %w", number, err)
	}
	if len(shared.Commits) != pull.GetCommits() || len(shared.Comments) != pull.GetComments() {
		return result, nativeReject("incomplete commit or comment evidence")
	}
	if shared.Pull.Draft != pull.GetDraft() || shared.Pull.Merged != pull.GetMerged() || shared.Pull.Title != pull.GetTitle() || shared.Pull.Body != pull.GetBody() || len(shared.Pull.Labels) != len(pull.Labels) {
		return result, nativeReject("pull request metadata changed during shared snapshot")
	}
	for index, label := range pull.Labels {
		if shared.Pull.Labels[index] != label.GetName() {
			return result, nativeReject("hold labels changed during shared snapshot")
		}
	}
	// Attribution must describe the same complete immutable commits used by shared hold replay.
	if author == nativePublicationBot {
		if len(shared.CommitDetails) != len(shared.Commits) {
			return result, nativeReject("incomplete native commit attribution")
		}
		for index, commit := range shared.CommitDetails {
			if commit == nil || commit.GetSHA() != shared.Commits[index] || commit.GetAuthor().GetLogin() != nativePublicationBot || commit.GetAuthor().GetType() != "Bot" {
				return result, nativeReject("native branch contains non-Dependabot or unattributed commit")
			}
		}
	}
	seen := map[string]bool{}
	options := &github.ListOptions{Page: 1, PerPage: 100}
	for {
		files, response, err := p.api.PullRequests.ListFiles(ctx, p.owner, p.repo, number, options)
		if err != nil {
			return result, fmt.Errorf("read native files #%d: %w", number, err)
		}
		if files == nil || response == nil {
			return result, nativeReject("missing changed-file page")
		}
		for _, file := range files {
			if file == nil || !safeNativePath(file.GetFilename()) || seen[file.GetFilename()] {
				return result, nativeReject("missing or duplicate changed-file identity")
			}
			seen[file.GetFilename()] = true
			result.files = append(result.files, file)
		}
		if len(result.files) >= 3000 {
			return result, nativeReject("changed-file evidence reaches GitHub truncation limit")
		}
		if response.NextPage == 0 {
			break
		}
		if response.NextPage != options.Page+1 {
			return result, nativeReject("nonadvancing changed-file pages")
		}
		options.Page = response.NextPage
	}
	if len(result.files) != pull.GetChangedFiles() {
		return result, nativeReject("incomplete changed-file evidence")
	}
	latest, err := p.pullDetail(ctx, number)
	if err != nil {
		return result, err
	}
	if !sameNativePublicationSnapshotPull(pull, latest) {
		return result, nativeReject("pull request changed during snapshot")
	}
	result.holds, result.cancellationPending, result.blocked, err = nativePublicationBlockedHolds(shared)
	if err != nil {
		return result, fmt.Errorf("evaluate native snapshot #%d: %w", number, err)
	}
	result.pull = pull
	result.comments = shared.Comments
	result.ci = automation.CommitCI{Statuses: shared.Statuses, Checks: shared.Checks}
	return result, nil
}

// NativePublicationInventory obtains complete source or robot publication inventories with exact detail records.
func (p *NativePublisher) nativePublicationInventory(ctx context.Context, author string) ([]*github.PullRequest, error) {
	var result []*github.PullRequest
	if author == nativePublicationBot {
		open, err := p.shared.OpenPullRequests(ctx)
		if err != nil {
			return nil, fmt.Errorf("inventory native sources: %w", err)
		}
		for _, entry := range open {
			if entry.Author != author {
				continue
			}
			pull, err := p.pullDetail(ctx, entry.Number)
			if err != nil {
				return nil, err
			}
			if pull.GetState() != "open" || pull.GetUser().GetLogin() != author || pull.GetHead().GetSHA() != entry.HeadSHA {
				return nil, nativeReject("native inventory changed during collection")
			}
			result = append(result, pull)
		}
		return result, nil
	}
	return p.nativeCreatorInventory(ctx, author)
}

// NativeCreatorInventory verifies every creator-filtered issue and resolves relevant pull details.
func (p *NativePublisher) nativeCreatorInventory(ctx context.Context, author string) ([]*github.PullRequest, error) {
	state := "open"
	switch author {
	case nativePublicationBot:
	case nativePublicationRobot:
		state = "all"
	default:
		return nil, nativeReject("unsupported publication inventory author")
	}
	var result []*github.PullRequest
	seen := map[int]bool{}
	options := &github.IssueListByRepoOptions{Creator: author, State: state, ListOptions: github.ListOptions{Page: 1, PerPage: 100}}
	for {
		issues, response, err := p.api.Issues.ListByRepo(ctx, p.owner, p.repo, options)
		if err != nil {
			return nil, fmt.Errorf("inventory %s pull requests: %w", author, err)
		}
		if issues == nil || response == nil {
			return nil, nativeReject("missing creator inventory page")
		}
		for _, issue := range issues {
			if issue == nil || issue.GetNumber() <= 0 || seen[issue.GetNumber()] || issue.GetUser().GetLogin() != author ||
				(issue.GetState() != "open" && (state != "all" || issue.GetState() != "closed")) {
				return nil, nativeReject("foreign, duplicate or incomplete creator inventory evidence")
			}
			seen[issue.GetNumber()] = true
			if issue.PullRequestLinks == nil {
				continue
			}
			if author == nativePublicationRobot {
				if issue.Labels == nil {
					return nil, nativeReject("incomplete robot publication metadata")
				}
				owned := strings.Contains(issue.GetBody(), nativePublicationMarker)
				for _, label := range issue.Labels {
					owned = owned || label.GetName() == nativePublicationLabel
				}
				if !owned {
					continue
				}
			}
			pull, err := p.pullDetail(ctx, issue.GetNumber())
			if err != nil {
				return nil, err
			}
			if pull.GetUser().GetLogin() != author || pull.GetState() != issue.GetState() {
				return nil, nativeReject("pull request creator inventory changed")
			}
			result = append(result, pull)
		}
		if response.NextPage == 0 {
			return result, nil
		}
		if response.NextPage != options.Page+1 {
			return nil, nativeReject("nonadvancing publication inventory pages")
		}
		options.Page = response.NextPage
	}
}

// Authorize binds the exact base, complete source family and every owned predecessor before Git writes.
func (p *NativePublisher) authorize(ctx context.Context, request nativePublicationRequest) (nativePublicationAuthorization, error) {
	var result nativePublicationAuthorization
	base, err := p.mainSHA(ctx)
	if err != nil {
		return result, err
	}
	if base != request.ExpectedBaseSHA {
		return result, errNativePublicationBaseMoved
	}
	result.group, err = p.sourceGroup(ctx, request.SourcePRs)
	if err != nil {
		return result, fmt.Errorf("authorize source group: %w", err)
	}
	inventory, err := p.nativePublicationInventory(ctx, nativePublicationRobot)
	if err != nil {
		return result, err
	}
	branch := "native-dependabot-" + result.group["group"].(string)
	for _, pull := range inventory {
		if !strings.Contains(pull.GetBody(), nativePublicationMarker) && !nativePublicationHasLabel(pull, nativePublicationLabel) {
			continue
		}
		if pull.GetState() == "closed" && pull.Merged != nil && !pull.GetMerged() {
			if pull.GetHead().GetRef() == branch {
				result.closed = append(result.closed, pull)
			}
			continue
		}
		meta, err := p.validateReplacement(pull, pull.GetMerged(), !pull.GetMerged())
		if err != nil {
			return result, fmt.Errorf("authorize replacement #%d: %w", pull.GetNumber(), err)
		}
		if meta["group"] != result.group["group"] {
			continue
		}
		if pull.GetMerged() {
			result.merged = append(result.merged, pull)
			continue
		}
		if result.existing != nil {
			return result, nativeReject("multiple replacement PRs for group")
		}
		result.existing = pull
	}
	if result.existing != nil {
		snapshot, err := p.nativePublicationSnapshot(ctx, result.existing.GetNumber(), result.existing.GetHead().GetSHA(), nativePublicationRobot)
		if err != nil {
			return result, fmt.Errorf("authorize existing replacement: %w", err)
		}
		if snapshot.blocked {
			return result, nativeReject("human/unknown replacement hold blocks publication")
		}
	}
	return result, nil
}
