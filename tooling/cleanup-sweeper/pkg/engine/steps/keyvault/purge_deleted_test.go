package keyvault

import "testing"

func TestPurgeDeletedStepConfig_StepOptions(t *testing.T) {
	t.Parallel()

	cfg := PurgeDeletedStepConfig{
		Name:            "custom-name",
		Retries:         4,
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

func TestNewPurgeDeletedStep_DefaultName(t *testing.T) {
	t.Parallel()

	step := NewPurgeDeletedStep(PurgeDeletedStepConfig{})
	if got, want := step.Name(), "Purge soft-deleted Key Vaults"; got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}
