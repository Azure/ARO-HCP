package shared

import (
	"context"
	"strings"
	"testing"

	cleanuprunner "github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/pkg/engine/runner"
	"github.com/go-logr/logr"
)

func TestRun_ReturnsStepDiscoveryError(t *testing.T) {
	t.Parallel()

	ctx := cleanuprunner.ContextWithLogger(context.Background(), logr.Discard())
	err := Run(ctx, RunOptions{
		SubscriptionID:  "00000000-0000-0000-0000-000000000000",
		AzureCredential: nil,
	})
	if err == nil {
		t.Fatalf("expected error when credential is nil")
	}
	if !strings.Contains(err.Error(), "azure credential is required") {
		t.Fatalf("unexpected error: %v", err)
	}
}
