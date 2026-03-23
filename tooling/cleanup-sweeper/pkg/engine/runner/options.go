package runner

// StepOptions holds common execution semantics shared by all step kinds.
type StepOptions struct {
	Name            string
	Retries         int
	ContinueOnError bool
	Verify          VerifyFn
}

// StepOptionsProvider is implemented by step config structs that can
// project their common execution semantics into StepOptions.
type StepOptionsProvider interface {
	StepOptions() StepOptions
}
