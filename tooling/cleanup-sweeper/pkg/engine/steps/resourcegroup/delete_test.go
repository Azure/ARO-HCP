package resourcegroup

import (
	"context"
	"testing"

	"github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/pkg/engine/runner"
	"github.com/go-logr/logr"
)

func TestDeleteStepConfig_StepOptions(t *testing.T) {
	t.Parallel()

	cfg := DeleteStepConfig{
		Name:            "custom-name",
		Retries:         3,
		ContinueOnError: true,
	}

	opts := cfg.StepOptions()
	if opts.Name != cfg.Name {
		t.Fatalf("expected name %q, got %q", cfg.Name, opts.Name)
	}
	if opts.Retries != cfg.Retries {
		t.Fatalf("expected retries %d, got %d", cfg.Retries, opts.Retries)
	}
	if opts.ContinueOnError != cfg.ContinueOnError {
		t.Fatalf("expected continueOnError %t, got %t", cfg.ContinueOnError, opts.ContinueOnError)
	}
}

func TestNewDeleteStep_DefaultName(t *testing.T) {
	t.Parallel()

	step := NewDeleteStep(DeleteStepConfig{})
	if got, want := step.Name(), "Delete resource group"; got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestDeleteStepDiscover_RequiresResourceGroupName(t *testing.T) {
	t.Parallel()

	step := NewDeleteStep(DeleteStepConfig{})
	ctx := runner.ContextWithLogger(context.Background(), logr.Discard())
	if _, err := step.Discover(ctx); err == nil {
		t.Fatalf("expected error for empty resource group name")
	}
}

func TestDeleteStepDiscover_ReturnsTarget(t *testing.T) {
	t.Parallel()

	step := NewDeleteStep(DeleteStepConfig{ResourceGroupName: "rg-example"})
	ctx := runner.ContextWithLogger(context.Background(), logr.Discard())

	targets, err := step.Discover(ctx)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(targets) != 1 {
		t.Fatalf("expected 1 target, got %d", len(targets))
	}
	if targets[0].Name != "rg-example" {
		t.Fatalf("unexpected target name %q", targets[0].Name)
	}
	if targets[0].Type != ResourceType {
		t.Fatalf("unexpected target type %q", targets[0].Type)
	}
}
