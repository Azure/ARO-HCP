package runner

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/go-logr/logr"
)

type fakeStep struct {
	name            string
	targets         []Target
	discoverErr     error
	deleteErrByID   map[string]error
	verifyErr       error
	verifyCalls     int
	deleteCalls     int
	retryLimit      int
	continueOnError bool
}

func (f *fakeStep) Name() string { return f.name }

func (f *fakeStep) Discover(_ context.Context) ([]Target, error) {
	if f.discoverErr != nil {
		return nil, f.discoverErr
	}
	return append([]Target(nil), f.targets...), nil
}

func (f *fakeStep) Delete(_ context.Context, target Target, _ bool) error {
	f.deleteCalls++
	if err, ok := f.deleteErrByID[target.ID]; ok {
		return err
	}
	return nil
}

func (f *fakeStep) Verify(_ context.Context) error {
	f.verifyCalls++
	return f.verifyErr
}

func (f *fakeStep) RetryLimit() int {
	if f.retryLimit < 1 {
		return 1
	}
	return f.retryLimit
}

func (f *fakeStep) ContinueOnError() bool { return f.continueOnError }

func TestEngineRun_DryRunSkipsDeleteAndVerify(t *testing.T) {
	t.Parallel()

	step := &fakeStep{
		name:    "dry-run-step",
		targets: []Target{{ID: "a"}, {ID: "b"}},
	}
	engine := &Engine{
		Steps:       []Step{step},
		DryRun:      true,
		Parallelism: 1,
	}

	ctx := ContextWithLogger(context.Background(), logr.Discard())
	if err := engine.Run(ctx); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if step.deleteCalls != 0 {
		t.Fatalf("expected no delete calls in dry-run, got %d", step.deleteCalls)
	}
	if step.verifyCalls != 0 {
		t.Fatalf("expected no verify calls in dry-run, got %d", step.verifyCalls)
	}
}

func TestEngineRun_DeleteErrorContinuesWhenConfigured(t *testing.T) {
	t.Parallel()

	step := &fakeStep{
		name:            "continue-step",
		targets:         []Target{{ID: "x"}},
		deleteErrByID:   map[string]error{"x": errors.New("boom")},
		continueOnError: true,
	}
	engine := &Engine{
		Steps:       []Step{step},
		Parallelism: 1,
	}

	ctx := ContextWithLogger(context.Background(), logr.Discard())
	if err := engine.Run(ctx); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestEngineRun_DeleteErrorFailsWhenNotContinuable(t *testing.T) {
	t.Parallel()

	step := &fakeStep{
		name:            "fail-step",
		targets:         []Target{{ID: "x", Name: "res-x", Type: "example/type"}},
		deleteErrByID:   map[string]error{"x": errors.New("boom")},
		continueOnError: false,
	}
	engine := &Engine{
		Steps:       []Step{step},
		Parallelism: 1,
	}

	ctx := ContextWithLogger(context.Background(), logr.Discard())
	err := engine.Run(ctx)
	if err == nil {
		t.Fatalf("expected error")
	}
	if !strings.Contains(err.Error(), "failed deleting") {
		t.Fatalf("expected delete failure error, got %v", err)
	}
}

func TestDeletionStepDiscover_AppliesSkipFilter(t *testing.T) {
	t.Parallel()

	step := DeletionStep{
		ResourceType: "example/type",
		Options:      StepOptions{Name: "filter-step"},
		DiscoverFn: func(_ context.Context, _ string) ([]Target, error) {
			return []Target{
				{ID: "a", Name: "skip-me"},
				{ID: "b", Name: "keep-me"},
			}, nil
		},
		SkipFn: func(_ context.Context, target Target) (bool, string, error) {
			return target.Name == "skip-me", "unit-test", nil
		},
	}

	ctx := ContextWithLogger(context.Background(), logr.Discard())
	targets, err := step.Discover(ctx)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(targets) != 1 || targets[0].Name != "keep-me" {
		t.Fatalf("unexpected targets: %#v", targets)
	}
}

func TestDeletionStepRetryLimit_MinimumOne(t *testing.T) {
	t.Parallel()

	step := DeletionStep{Options: StepOptions{Retries: 0}}
	if got := step.RetryLimit(); got != 1 {
		t.Fatalf("expected retry limit 1, got %d", got)
	}
}
