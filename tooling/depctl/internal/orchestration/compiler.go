package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"slices"
	"strings"
)

var compilerVersionPattern = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`)

// GitRunner executes read-only git plumbing without evaluating the target tree.
type GitRunner func(context.Context, ...string) ([]byte, error)

// RunGit invokes git directly, never through a shell.
func RunGit(ctx context.Context, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, "git", args...).Output()
}

// CompilerVersions reads every compiled workflow's release provenance at ref.
func CompilerVersions(ctx context.Context, ref string, run GitRunner) ([]string, error) {
	if ref == "" || strings.HasPrefix(ref, "-") || strings.ContainsAny(ref, "\x00\n\r:") {
		return nil, errors.New("invalid compiler revision")
	}
	names, err := run(ctx, "ls-tree", "-r", "--name-only", ref, "--", ".github/workflows")
	if err != nil {
		return nil, err
	}
	versions := []string{}
	for _, name := range strings.Split(strings.TrimSpace(string(names)), "\n") {
		if !strings.HasSuffix(name, ".lock.yml") {
			continue
		}
		content, err := run(ctx, "show", ref+":"+name)
		if err != nil {
			return nil, err
		}
		first, _, _ := strings.Cut(string(content), "\n")
		const prefix = "# gh-aw-metadata: "
		if !strings.HasPrefix(first, prefix) {
			return nil, fmt.Errorf("missing compiler provenance in %s", name)
		}
		var metadata struct {
			CompilerVersion string `json:"compiler_version"`
		}
		if err := json.Unmarshal([]byte(strings.TrimPrefix(first, prefix)), &metadata); err != nil || !compilerVersionPattern.MatchString(metadata.CompilerVersion) {
			return nil, fmt.Errorf("invalid compiler provenance in %s", name)
		}
		if !slices.Contains(versions, metadata.CompilerVersion) {
			versions = append(versions, metadata.CompilerVersion)
		}
	}
	if len(versions) == 0 {
		return nil, errors.New("no compiled workflow provenance found")
	}
	slices.Sort(versions)
	return versions, nil
}

// ValidateRegeneration requires ancestry, compiler-owned paths and new provenance.
func ValidateRegeneration(ctx context.Context, previous, head, base string, run GitRunner) error {
	if previous == head {
		return errors.New("regeneration must produce a genuinely new head")
	}
	if _, err := run(ctx, "merge-base", "--is-ancestor", previous, head); err != nil {
		return fmt.Errorf("regeneration ancestry: %w", err)
	}
	changed, err := run(ctx, "diff", "--name-only", "origin/"+base+"..."+head)
	if err != nil {
		return err
	}
	paths := strings.Fields(string(changed))
	if len(paths) == 0 {
		return errors.New("regeneration must change only compiler-owned .github content")
	}
	for _, path := range paths {
		if !strings.HasPrefix(path, ".github/") {
			return errors.New("regeneration must change only compiler-owned .github content")
		}
	}
	before, err := CompilerVersions(ctx, previous, run)
	if err != nil {
		return err
	}
	after, err := CompilerVersions(ctx, head, run)
	if err != nil {
		return err
	}
	if slices.Equal(before, after) {
		return errors.New("regeneration must change the compiler release provenance")
	}
	return nil
}
