package resourcegroup

import (
	"context"
	"strings"
	"testing"
	"time"

	cleanuprunner "github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/pkg/engine/runner"
	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/util/sets"
)

func TestDiscoverCandidates_ExplicitOnly(t *testing.T) {
	t.Parallel()

	opts := RunOptions{
		DiscoverResourceGroups: false,
		ResourceGroups:         sets.New("rg-b", "rg-a"),
	}

	ctx := cleanuprunner.ContextWithLogger(context.Background(), logr.Discard())
	got, err := discoverCandidates(ctx, opts)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if len(got) != 2 {
		t.Fatalf("expected 2 resource groups, got %d", len(got))
	}
	if got[0] != "rg-a" || got[1] != "rg-b" {
		t.Fatalf("expected sorted resource groups [rg-a rg-b], got %v", got)
	}
}

func TestDiscoverCandidates_RequiresReferenceTimeWhenDiscoveryEnabled(t *testing.T) {
	t.Parallel()

	opts := RunOptions{
		DiscoverResourceGroups: true,
		ResourceGroups:         sets.New[string](),
		ReferenceTime:          time.Time{},
	}

	ctx := cleanuprunner.ContextWithLogger(context.Background(), logr.Discard())
	_, err := discoverCandidates(ctx, opts)
	if err == nil {
		t.Fatalf("expected error when reference time is zero")
	}
	if !strings.Contains(err.Error(), "reference time is required") {
		t.Fatalf("unexpected error: %v", err)
	}
}
