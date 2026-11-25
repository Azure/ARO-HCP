// Copyright 2026 Microsoft Corporation
// Licensed under the Apache License, Version 2.0.

package dependabot

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	"github.com/google/go-github/v69/github"
)

// NativeActionAdmission proves immutable consumers before invoking shared admission policy.
func (p *NativePublisher) nativeActionAdmission(ctx context.Context, snapshot nativePublicationSnapshot, group map[string]any) error {
	intents, err := nativeRowsValue(group["intents"])
	if err != nil {
		return fmt.Errorf("read admission intents: %w", err)
	}
	wanted := NativeCoveragePaths(intents)
	head := snapshot.pull.GetHead().GetSHA()
	cache := make(map[string]map[string]*github.TreeEntry)
	available, err := p.nativeActionTreePaths(ctx, head, wanted, cache)
	if err != nil {
		return fmt.Errorf("read admission head tree: %w", err)
	}
	var live map[string]bool
	if group["ecosystem"] == "actions" {
		base, err := p.mainSHA(ctx)
		if err != nil {
			return fmt.Errorf("read main for admission: %w", err)
		}
		live, err = p.nativeActionTreePaths(ctx, base, wanted, cache)
		if err != nil {
			return fmt.Errorf("read live admission tree: %w", err)
		}
	}
	paths := make(map[string]bool, len(snapshot.files))
	for _, file := range snapshot.files {
		paths[file.GetFilename()] = true
	}
	return NativeAdmissionSafe(ctx, p.nativePublicationRead, group, paths, available, live, func(path string) (string, error) {
		if !available[path] {
			return "", nativeReject("missing immutable admission consumer: " + path)
		}
		return p.nativePublicationContent(ctx, head, path)
	})
}

// NativeActionClosure proves coverage by the exact merged replacement at its head, merge, and current main.
func (p *NativePublisher) nativeActionClosure(ctx context.Context, request *NativeActionRequest, intent map[string]any) error {
	target, err := p.pullDetail(ctx, request.replacement)
	if err != nil {
		return fmt.Errorf("read merged replacement: %w", err)
	}
	meta, err := p.validateReplacement(target, true, false)
	if err != nil {
		return fmt.Errorf("validate merged replacement: %w", err)
	}
	identified := false
	for _, value := range meta["sources"].([]any) {
		source := value.(map[string]any)
		number, _ := nativeInteger(source["number"])
		if number == int64(request.number) && source["head_sha"] == request.head {
			identified = true
			break
		}
	}
	if !identified || meta["group"] != intent["group"] {
		return nativeReject("replacement does not identify exact native source")
	}
	merge := target.GetMergeCommitSHA()
	if !nativeSHA.MatchString(merge) {
		return nativeReject("missing merged replacement commit")
	}
	base, err := p.mainSHA(ctx)
	if err != nil {
		return fmt.Errorf("read main for closure: %w", err)
	}
	comparison, _, err := p.api.Repositories.CompareCommits(ctx, p.owner, p.repo, merge, base, nil)
	if err != nil {
		return fmt.Errorf("compare merged replacement with main: %w", err)
	}
	if comparison.GetStatus() != "ahead" && comparison.GetStatus() != "identical" || comparison.GetMergeBaseCommit().GetSHA() != merge {
		return nativeReject("replacement merge is not on current main")
	}
	intents, err := nativeRowsValue(intent["intents"])
	if err != nil {
		return fmt.Errorf("read closure intents: %w", err)
	}
	wanted := NativeCoveragePaths(intents)
	cache := make(map[string]map[string]*github.TreeEntry)
	for _, ref := range []string{target.GetHead().GetSHA(), merge, base} {
		available, err := p.nativeActionTreePaths(ctx, ref, wanted, cache)
		if err != nil {
			return fmt.Errorf("read closure tree at %s: %w", ref, err)
		}
		if err := NativeVerifyCoverage(ctx, p.nativePublicationRead, intents, func(path string) (string, error) {
			if !available[path] {
				return "", nativeReject("missing immutable coverage consumer: " + path)
			}
			return p.nativePublicationContent(ctx, ref, path)
		}, available); err != nil {
			return fmt.Errorf("verify coverage at %s: %w", ref, err)
		}
	}
	latestBase, err := p.mainSHA(ctx)
	if err != nil {
		return fmt.Errorf("recheck main for closure: %w", err)
	}
	if latestBase != base {
		return nativeReject("merged coverage evidence changed")
	}
	latestTarget, err := p.pullDetail(ctx, request.replacement)
	if err != nil {
		return fmt.Errorf("reread merged replacement: %w", err)
	}
	if _, err := p.validateReplacement(latestTarget, true, false); err != nil {
		return fmt.Errorf("revalidate merged replacement: %w", err)
	}
	originalIdentity, err := nativePublicationHistoryIdentity(target)
	if err != nil {
		return fmt.Errorf("read original closure identity: %w", err)
	}
	latestIdentity, err := nativePublicationHistoryIdentity(latestTarget)
	if err != nil {
		return fmt.Errorf("read current closure identity: %w", err)
	}
	if !reflect.DeepEqual(latestIdentity, originalIdentity) {
		return nativeReject("merged coverage evidence changed")
	}
	return nil
}

// NativeActionTreePaths walks only requested immutable paths through complete nonrecursive directory trees.
func (p *NativePublisher) nativeActionTreePaths(ctx context.Context, head string, wanted map[string]bool, cache map[string]map[string]*github.TreeEntry) (map[string]bool, error) {
	found := make(map[string]bool, len(wanted))
	for path := range wanted {
		if !safeNativePath(path) {
			return nil, nativeReject("unsafe immutable coverage path")
		}
		parts := strings.Split(path, "/")
		sha := head
		for index, part := range parts {
			entries, ok := cache[sha]
			if !ok {
				tree, _, err := p.api.Git.GetTree(ctx, p.owner, p.repo, sha, false)
				if err != nil {
					return nil, fmt.Errorf("read immutable tree %s: %w", sha, err)
				}
				if tree == nil || tree.Truncated == nil || tree.GetTruncated() || tree.Entries == nil {
					return nil, nativeReject("incomplete targeted immutable tree")
				}
				entries = make(map[string]*github.TreeEntry, len(tree.Entries))
				for _, entry := range tree.Entries {
					name := entry.GetPath()
					if name == "" || strings.Contains(name, "/") || !nativeSHA.MatchString(entry.GetSHA()) || entries[name] != nil {
						return nil, nativeReject("malformed targeted immutable tree")
					}
					entries[name] = entry
				}
				cache[sha] = entries
			}
			entry := entries[part]
			if entry == nil {
				break
			}
			if index == len(parts)-1 {
				if entry.GetType() != "blob" || entry.GetMode() != "100644" {
					return nil, nativeReject("coverage manifest is not a regular immutable file")
				}
				found[path] = true
				break
			}
			if entry.GetType() != "tree" || entry.GetMode() != "040000" {
				return nil, nativeReject("coverage path crosses a non-directory")
			}
			sha = entry.GetSHA()
		}
	}
	return found, nil
}
