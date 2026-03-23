package resourcegroup

import (
	"context"
	"testing"

	cleanuprunner "github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/pkg/engine/runner"
	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/util/sets"
)

func TestRun_NoCandidatesReturnsNil(t *testing.T) {
	t.Parallel()

	ctx := cleanuprunner.ContextWithLogger(context.Background(), logr.Discard())
	err := Run(ctx, RunOptions{
		DiscoverResourceGroups: false,
		ResourceGroups:         sets.New[string](),
	})
	if err != nil {
		t.Fatalf("expected no error when no candidates exist, got %v", err)
	}
}
