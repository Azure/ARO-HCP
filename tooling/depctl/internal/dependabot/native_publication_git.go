// Copyright 2026 Microsoft Corporation
// Licensed under the Apache License, Version 2.0.

package dependabot

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"

	gh "github.com/google/go-github/v69/github"

	"github.com/Azure/ARO-HCP/tooling/depctl/internal/repository"
	automation "github.com/Azure/ARO-HCP/tooling/github"
)

// Native publication Git uses only a private repository and a caller-validated remote.
type nativePublicationGit struct {
	repo   *repository.Repo
	remote string
}

// Run preserves NativeGit's config, hook, filter and environment safeguards; only the fixed compiled helper sees inherited GH_TOKEN.
func (g nativePublicationGit) run(ctx context.Context, input []byte, args ...string) (string, error) {
	fixed := []string{"-c", "credential.https://github.com.helper=!gh auth git-credential", "-c", "protocol.ext.allow=never", "-c", "protocol.file.allow=never", "-c", "fetch.fsckObjects=true", "-c", "transfer.fsckObjects=true"}
	output, err := g.repo.NativeGit(ctx, input, "", append(fixed, args...)...)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(output), "\n"), nil
}

// RemoteHead rejects missing, duplicate, malformed or mismatched advertised refs without displaying remote diagnostics.
func (g nativePublicationGit) remoteHead(ctx context.Context, branch string) (string, error) {
	output, err := g.run(ctx, nil, "ls-remote", "--heads", g.remote, "refs/heads/"+branch)
	if err != nil {
		return "", fmt.Errorf("read remote publication branch failed")
	}
	if output == "" {
		return "", nil
	}
	fields := strings.Fields(output)
	if len(fields) != 2 || !nativeSHA.MatchString(fields[0]) || fields[1] != "refs/heads/"+branch {
		return "", fmt.Errorf("ambiguous remote publication branch")
	}
	return fields[0], nil
}

// Fetch downloads exact refs into the private object database without hooks or local configuration.
func (g nativePublicationGit) fetch(ctx context.Context, ref string) error {
	if _, err := g.run(ctx, nil, "fetch", "--no-tags", "--no-recurse-submodules", g.remote, "refs/heads/"+ref); err != nil {
		return fmt.Errorf("fetch authorized publication branch failed")
	}
	return nil
}

// Publish uses a normal fast-forward push only after exact main and branch advertisements are reread.
func (g nativePublicationGit) publish(ctx context.Context, base, branch, old, head string, unchanged bool) error {
	main, err := g.remoteHead(ctx, "main")
	if err != nil {
		return fmt.Errorf("read remote main before publication: %w", err)
	}
	if main == "" {
		return fmt.Errorf("remote main is missing before publication")
	}
	if main != base {
		return errNativePublicationBaseMoved
	}
	current, err := g.remoteHead(ctx, branch)
	if err != nil {
		return err
	}
	if current != old {
		return fmt.Errorf("remote replacement branch moved before publication")
	}
	if unchanged {
		return nil
	}
	if _, err := g.run(ctx, nil, "push", "--porcelain", g.remote, head+":refs/heads/"+branch); err != nil {
		return fmt.Errorf("push replacement failed; inspect remote before retry: %w", automation.ErrIndeterminate)
	}
	current, err = g.remoteHead(ctx, branch)
	if err != nil || current != head {
		return fmt.Errorf("pushed branch could not be verified: %w", automation.ErrIndeterminate)
	}
	return nil
}

// PreparePublication applies the safe patch and proves every predecessor/orphan before constructing a commit.
func (p *NativePublisher) preparePublication(ctx context.Context, git nativePublicationGit, request nativePublicationRequest, authorization nativePublicationAuthorization, paths []string, marker, title, branch string) (string, string, *gh.PullRequest, bool, error) {
	base := request.ExpectedBaseSHA
	if err := git.fetch(ctx, "main"); err != nil {
		return "", "", nil, false, err
	}
	fetched, err := git.run(ctx, nil, "rev-parse", "FETCH_HEAD")
	if err != nil {
		return "", "", nil, false, fmt.Errorf("resolve fetched publication base: %w", err)
	}
	if fetched != base {
		return "", "", nil, false, errNativePublicationBaseMoved
	}
	old, err := git.remoteHead(ctx, branch)
	if err != nil {
		return "", "", nil, false, err
	}
	if authorization.existing != nil && (old == "" || old != authorization.existing.GetHead().GetSHA()) {
		return "", "", nil, false, fmt.Errorf("verified replacement branch missing or moved")
	}
	if old != "" {
		if err := git.fetch(ctx, branch); err != nil {
			return "", "", nil, false, err
		}
		fetched, err := git.run(ctx, nil, "rev-parse", "FETCH_HEAD")
		if err != nil || fetched != old {
			return "", "", nil, false, fmt.Errorf("replacement branch moved during fetch")
		}
	}
	if _, err := git.run(ctx, nil, "read-tree", base); err != nil {
		return "", "", nil, false, fmt.Errorf("read publication base tree: %w", err)
	}
	if _, err := git.run(ctx, nil, "checkout-index", "--all", "--index"); err != nil {
		return "", "", nil, false, fmt.Errorf("materialize publication base: %w", err)
	}
	if _, err := git.run(ctx, []byte(request.Patch), "apply", "--index", "--whitespace=error", "-"); err != nil {
		return "", "", nil, false, fmt.Errorf("apply publication patch: %w", err)
	}
	tree, err := git.run(ctx, nil, "write-tree")
	if err != nil {
		return "", "", nil, false, fmt.Errorf("write publication tree: %w", err)
	}
	if err := NativeValidateTree(ctx, p.nativePublicationRead, git.repo.Path, base, tree, paths, authorization.group, true); err != nil {
		return "", "", nil, false, fmt.Errorf("validate publication tree: %w", err)
	}
	var previous *gh.PullRequest
	if old != "" && authorization.existing == nil {
		previous, err = p.publicationPredecessor(ctx, old, base, authorization)
		if err != nil {
			return "", "", nil, false, err
		}
		if previous == nil {
			previous, err = p.verifyPublicationOrphan(ctx, git, old, base, tree, marker, authorization)
			if err != nil {
				return "", "", nil, false, err
			}
		}
	}
	if previous != nil {
		covered, err := p.publicationAlreadyCovered(ctx, git, base, authorization.group)
		if err != nil {
			return "", "", nil, false, err
		}
		if covered {
			return "", "", nil, false, fmt.Errorf("merged replacement already covers current native intent; do not create another PR")
		}
	}
	if old != "" {
		oldTree, err := git.run(ctx, nil, "rev-parse", old+"^{tree}")
		if err != nil {
			return "", "", nil, false, fmt.Errorf("read previous publication tree: %w", err)
		}
		if oldTree == tree {
			return old, old, previous, true, nil
		}
	}
	bot, _, err := p.api.Users.Get(ctx, nativePublicationRobot)
	if err != nil {
		return "", "", nil, false, fmt.Errorf("read publishing App bot identity: %w", err)
	}
	if bot == nil || bot.GetLogin() != nativePublicationRobot || bot.GetType() != "Bot" || bot.GetID() <= 0 {
		return "", "", nil, false, fmt.Errorf("missing publishing App bot identity")
	}
	parents := []string{}
	if old != "" {
		parents = append(parents, old)
	}
	if old != base {
		parents = append(parents, base)
	}
	head, err := git.commit(ctx, tree, parents, bot.GetID(), title+"\n\n"+marker+"\nnative-dependabot-base: "+base+"\n")
	if err != nil {
		return "", "", nil, false, err
	}
	return head, old, previous, false, nil
}

// Commit writes an ordinary unsigned Git commit with the verified App identity, independent of inherited author variables.
// NativeGit intentionally fixes author environment variables, so hash-object supplies the exact verified commit object instead.
func (g nativePublicationGit) commit(ctx context.Context, tree string, parents []string, botID int64, message string) (string, error) {
	var object strings.Builder
	fmt.Fprintf(&object, "tree %s\n", tree)
	for _, parent := range parents {
		fmt.Fprintf(&object, "parent %s\n", parent)
	}
	identity := nativePublicationRobot + " <" + strconv.FormatInt(botID, 10) + "+" + nativePublicationRobot + "@users.noreply.github.com> " + strconv.FormatInt(time.Now().Unix(), 10) + " +0000"
	fmt.Fprintf(&object, "author %s\ncommitter %s\n\n%s", identity, identity, message)
	head, err := g.run(ctx, []byte(object.String()), "hash-object", "-t", "commit", "-w", "--stdin")
	if err != nil {
		return "", fmt.Errorf("write verified App publication commit: %w", err)
	}
	if !nativeSHA.MatchString(head) {
		return "", fmt.Errorf("invalid publication commit identity")
	}
	return head, nil
}

// PublicationPredecessor rejects closed-unmerged revival and proves merged predecessor ancestry on exact main.
func (p *NativePublisher) publicationPredecessor(ctx context.Context, head, base string, authorization nativePublicationAuthorization) (*gh.PullRequest, error) {
	for _, pull := range authorization.closed {
		if pull.GetHead().GetSHA() == head {
			return nil, fmt.Errorf("closed-unmerged replacement branch must not be revived")
		}
	}
	var prior *gh.PullRequest
	for _, pull := range authorization.merged {
		if pull.GetHead().GetSHA() != head {
			continue
		}
		if prior != nil {
			return nil, fmt.Errorf("ambiguous merged replacement branch ownership")
		}
		prior = pull
	}
	if prior == nil {
		return nil, nil
	}
	if _, err := p.validateReplacement(prior, true, false); err != nil {
		return nil, fmt.Errorf("validate merged predecessor: %w", err)
	}
	merge := prior.GetMergeCommitSHA()
	if !nativeSHA.MatchString(merge) {
		return nil, fmt.Errorf("missing predecessor merge commit")
	}
	comparison, _, err := p.api.Repositories.CompareCommits(ctx, p.owner, p.repo, merge, base, nil)
	if err != nil {
		return nil, fmt.Errorf("compare predecessor merge with main: %w", err)
	}
	if comparison == nil || comparison.GetStatus() != "ahead" && comparison.GetStatus() != "identical" || comparison.GetMergeBaseCommit().GetSHA() != merge {
		return nil, fmt.Errorf("previous replacement merge is not an ancestor of current main")
	}
	return prior, nil
}

// VerifyPublicationOrphan recovers only an exact robot-attributed interrupted publication, never an arbitrary branch.
func (p *NativePublisher) verifyPublicationOrphan(ctx context.Context, git nativePublicationGit, old, base, tree, marker string, authorization nativePublicationAuthorization) (*gh.PullRequest, error) {
	detail, _, err := p.api.Repositories.GetCommit(ctx, p.owner, p.repo, old, nil)
	if err != nil {
		return nil, fmt.Errorf("read orphan publication identity: %w", err)
	}
	if detail == nil || detail.GetSHA() != old || detail.GetAuthor().GetLogin() != nativePublicationRobot || detail.GetAuthor().GetType() != "Bot" {
		return nil, fmt.Errorf("orphan publication commit is not attributed to the robot")
	}
	message, err := git.run(ctx, nil, "show", "-s", "--format=%B", old)
	if err != nil {
		return nil, fmt.Errorf("read orphan publication message: %w", err)
	}
	actual, err := nativePublicationMetadata(message)
	if err != nil {
		return nil, fmt.Errorf("read orphan ownership marker: %w", err)
	}
	expected, err := nativePublicationMetadata(marker)
	if err != nil {
		return nil, fmt.Errorf("read requested ownership marker: %w", err)
	}
	parentsText, err := git.run(ctx, nil, "show", "-s", "--format=%P", old)
	if err != nil {
		return nil, fmt.Errorf("read orphan publication parents: %w", err)
	}
	parents := strings.Fields(parentsText)
	var previous *gh.PullRequest
	if len(parents) == 2 && parents[1] == base {
		previous, err = p.publicationPredecessor(ctx, parents[0], base, authorization)
		if err != nil {
			return nil, err
		}
	}
	validParents := len(parents) == 1 && parents[0] == base || len(parents) == 2 && parents[1] == base && previous != nil
	actualTree, err := git.run(ctx, nil, "rev-parse", old+"^{tree}")
	if err != nil {
		return nil, fmt.Errorf("read orphan publication tree: %w", err)
	}
	hasBase := false
	for _, line := range strings.Split(message, "\n") {
		if line == "native-dependabot-base: "+base {
			hasBase = true
		}
	}
	if !reflect.DeepEqual(actual, expected) || !hasBase || !validParents || actualTree != tree {
		return nil, fmt.Errorf("orphan branch does not match exact authorized publication")
	}
	return previous, nil
}

// PublicationAlreadyCovered checks every selected intent against current main before reusing a merged predecessor.
func (p *NativePublisher) publicationAlreadyCovered(ctx context.Context, git nativePublicationGit, base string, group map[string]any) (bool, error) {
	listing, err := git.run(ctx, nil, "ls-tree", "-r", "--name-only", base)
	if err != nil {
		return false, fmt.Errorf("read main coverage paths: %w", err)
	}
	available := nativePathSet(listing)
	reader := func(path string) (string, error) { return git.run(ctx, nil, "show", base+":"+path) }
	for _, raw := range group["sources"].([]any) {
		source := raw.(map[string]any)
		intents := []map[string]any{}
		for _, rawIntent := range source["intents"].([]any) {
			intents = append(intents, rawIntent.(map[string]any))
		}
		if err := NativeVerifyCoverage(ctx, p.nativePublicationRead, intents, reader, available); err != nil {
			var rejection *NativePolicyError
			if errors.As(err, &rejection) {
				return false, nil
			}
			return false, fmt.Errorf("verify main intent coverage: %w", err)
		}
	}
	return true, nil
}
