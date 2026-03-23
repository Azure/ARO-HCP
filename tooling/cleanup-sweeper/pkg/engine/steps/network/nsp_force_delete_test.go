package network

import "testing"

func TestNSPForceDeleteStepConfig_StepOptions(t *testing.T) {
	t.Parallel()

	cfg := NSPForceDeleteStepConfig{
		Name:            "custom-name",
		Retries:         2,
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

func TestNewNSPForceDeleteStep_DefaultName(t *testing.T) {
	t.Parallel()

	step := NewNSPForceDeleteStep(NSPForceDeleteStepConfig{})
	if got, want := step.Name(), "Delete network security perimeters"; got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}
