package orchestration

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Azure/ARO-HCP/tooling/depctl/internal/forge"
)

func compilerRunner(change string) GitRunner {
	return func(_ context.Context, args ...string) ([]byte, error) {
		switch args[0] {
		case "merge-base":
			if change == "ancestry" {
				return nil, errors.New("not ancestor")
			}
			return nil, nil
		case "diff":
			if change == "empty" {
				return []byte(""), nil
			}
			if change == "foreign" {
				return []byte(".github/workflows/a.lock.yml\nmain.go\n"), nil
			}
			return []byte(".github/workflows/a.lock.yml\n"), nil
		case "ls-tree":
			if change == "no-locks" {
				return []byte(".github/workflows/a.md\n"), nil
			}
			return []byte(".github/workflows/a.lock.yml\n.github/workflows/b.lock.yml\n.github/workflows/a.md\n"), nil
		case "show":
			if change == "missing" {
				return []byte("name: bad\n"), nil
			}
			if change == "empty-lock" {
				return nil, nil
			}
			if change == "invalid" {
				return []byte(`# gh-aw-metadata: {"compiler_version":"next"}`), nil
			}
			if change == "malformed" {
				return []byte("# gh-aw-metadata: {"), nil
			}
			version := "v1.0.0"
			if strings.HasPrefix(args[1], maintenanceHead+":") && change != "unchanged" {
				version = "v2.0.0"
			}
			return []byte(`# gh-aw-metadata: {"compiler_version":"` + version + `"}` + "\n"), nil
		}
		return nil, errors.New("unexpected git command")
	}
}

func TestCompilerVersionsRejectsMissingOrInvalidProvenance(t *testing.T) {
	for _, change := range []string{"missing", "empty-lock", "invalid", "malformed", "no-locks"} {
		t.Run(change, func(t *testing.T) {
			if _, err := CompilerVersions(context.Background(), maintenanceHead, compilerRunner(change)); err == nil {
				t.Fatal("invalid provenance accepted")
			}
		})
	}
	versions, err := CompilerVersions(context.Background(), maintenanceHead, compilerRunner("valid"))
	if err != nil || len(versions) != 1 || versions[0] != "v2.0.0" {
		t.Fatalf("versions=%v err=%v", versions, err)
	}
}

func TestCompilerVersionsDoesNotCheckoutOrRunTarget(t *testing.T) {
	calls := 0
	run := func(_ context.Context, args ...string) ([]byte, error) {
		calls++
		switch args[0] {
		case "ls-tree":
			return []byte(".github/workflows/a.lock.yml\n.github/workflows/b.lock.yml\n.github/workflows/c.lock.yml\n"), nil
		case "show":
			version := "v1.0.0"
			if strings.Contains(args[1], "b.lock.yml") {
				version = "v2.0.0"
			}
			return []byte(`# gh-aw-metadata: {"compiler_version":"` + version + `"}`), nil
		default:
			t.Fatalf("executed unsafe git command %v", args)
			return nil, nil
		}
	}
	versions, err := CompilerVersions(context.Background(), "origin/main", run)
	if err != nil || strings.Join(versions, ",") != "v1.0.0,v2.0.0" || calls != 4 {
		t.Fatalf("versions=%v calls=%d err=%v", versions, calls, err)
	}
	for _, ref := range []string{"", "--output=/tmp/injected", "HEAD:path", "HEAD\n"} {
		if _, err := CompilerVersions(context.Background(), ref, run); err == nil {
			t.Fatalf("accepted ref %q", ref)
		}
	}
}

func TestRegenerationRequiresAncestryOwnedPathsAndChangedRelease(t *testing.T) {
	for _, change := range []string{"valid", "ancestry", "empty", "foreign", "unchanged", "missing", "invalid", "no-locks"} {
		t.Run(change, func(t *testing.T) {
			err := ValidateRegeneration(context.Background(), maintenancePrevious, maintenanceHead, "main", compilerRunner(change))
			if (err != nil) != (change != "valid") {
				t.Fatalf("err=%v", err)
			}
		})
	}
	if err := ValidateRegeneration(context.Background(), maintenanceHead, maintenanceHead, "main", compilerRunner("valid")); err == nil {
		t.Fatal("same revision accepted")
	}
}

func TestReleaseRegeneratedRequiresFreshCompilerAuthorization(t *testing.T) {
	for _, change := range []string{"valid", "ancestry", "unchanged", "human", "held-head", "wrong-kind", "scope-race"} {
		t.Run(change, func(t *testing.T) {
			first, second := heldMaintenanceSnapshot(), heldMaintenanceSnapshot()
			// The old revision can be red; regeneration is authorized by provenance.
			first.Statuses[0].State = "failure"
			second.Statuses[0].State = "failure"
			switch change {
			case "human":
				second.Comments[0].Author = "reviewer"
			case "held-head":
				second.Comments[0].Body = "/hold\n\nRevision " + maintenanceHead + " was retested 3 times: holding"
			case "wrong-kind", "scope-race":
				second.Pull.Title = "fix(deps): update"
				second.Pull.Labels = append(second.Pull.Labels, "agentic-dependabot")
			}
			remote := &actionForge{Fake: forge.NewFake(), snapshots: []forge.Snapshot{first, second}, receipt: 9}
			_, _, err := New(Deps{Forge: remote}).ExecuteMaintenance(context.Background(), MaintenanceAction{Mode: "release-regenerated", Repository: "Azure/ARO-HCP", Number: 1, Head: maintenanceHead, PreviousHead: maintenancePrevious, Kind: "upgrade", Git: compilerRunner(change)}, remote)
			if change == "valid" {
				if err != nil || remote.writes != 1 || !strings.HasSuffix(remote.body, "\n\n/hold cancel") {
					t.Fatalf("err=%v remote=%+v", err, remote)
				}
			} else if err == nil || remote.writes != 0 {
				t.Fatalf("err=%v writes=%d", err, remote.writes)
			}
		})
	}
}
