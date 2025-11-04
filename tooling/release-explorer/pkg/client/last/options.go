package last

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/tooling/release-explorer/pkg/client/list"
	"github.com/Azure/ARO-HCP/tooling/release-explorer/pkg/client/types"
	"github.com/Azure/ARO-HCP/tooling/release-explorer/pkg/timeparse"
)

var (
	DefaultStep        = 7 * 24 * time.Hour
	DefaultMaxLookback = 12 * DefaultStep
)

var ErrNoDeploymentsFound = errors.New("no deployments found in lookback window")

func DefaultOptions() *RawOptions {
	return &RawOptions{
		RawOptions:  list.DefaultOptions(),
		Step:        DefaultStep,
		MaxLookback: DefaultMaxLookback,
	}
}

func (opts *RawOptions) BindOptions(cmd *cobra.Command) error {
	if err := opts.RawOptions.BindOptions(cmd); err != nil {
		return fmt.Errorf("failed to bind list options: %w", err)
	}

	cmd.Flags().Func("step", "Step duration for backwards search (e.g. 1w, 3d, 48h).", func(s string) error {
		if s == "" {
			return nil
		}
		d, err := timeparse.ParseDuration(s)
		if err != nil {
			return fmt.Errorf("failed to parse step duration: %w", err)
		}
		opts.Step = d
		return nil
	})

	cmd.Flags().Func("max-lookback", "Maximum lookback duration for backwards search (e.g. 12w, 90d).", func(s string) error {
		if s == "" {
			return nil
		}
		d, err := timeparse.ParseDuration(s)
		if err != nil {
			return fmt.Errorf("failed to parse max-lookback duration: %w", err)
		}
		opts.MaxLookback = d
		return nil
	})

	return nil
}

type RawOptions struct {
	*list.RawOptions
	Step        time.Duration
	MaxLookback time.Duration
}

// validatedOptions enforces a call to Validate before Complete can be invoked.
type validatedOptions struct {
	*RawOptions
}

type ValidatedOptions struct {
	*validatedOptions
}

type Options struct {
	ListOptions *list.Options
	Step        time.Duration
	MaxLookback time.Duration
}

func (o *RawOptions) Validate() (*ValidatedOptions, error) {
	if o.RawOptions == nil {
		return nil, fmt.Errorf("list options must not be nil")
	}

	listValidated, err := o.RawOptions.Validate()
	if err != nil {
		return nil, err
	}

	if o.Step <= 0 {
		return nil, fmt.Errorf("step must be greater than zero")
	}
	if o.MaxLookback <= 0 {
		return nil, fmt.Errorf("max-lookback must be greater than zero")
	}
	if o.MaxLookback < o.Step {
		return nil, fmt.Errorf("max-lookback must be greater than or equal to step")
	}

	return &ValidatedOptions{
		validatedOptions: &validatedOptions{
			RawOptions: &RawOptions{
				RawOptions:  listValidated.RawOptions,
				Step:        o.Step,
				MaxLookback: o.MaxLookback,
			},
		},
	}, nil
}

func (v *ValidatedOptions) Complete() (*Options, error) {
	listValidated, err := v.RawOptions.RawOptions.Validate()
	if err != nil {
		return nil, err
	}

	listOpts, err := listValidated.Complete()
	if err != nil {
		return nil, fmt.Errorf("failed to complete list options: %w", err)
	}

	return &Options{
		ListOptions: listOpts,
		Step:        v.Step,
		MaxLookback: v.MaxLookback,
	}, nil
}

// LastReleaseDeployment searches backwards in time using the configured step
// and max-lookback, returning the most recent deployment that matches the
// underlying list options, or ErrNoDeploymentsFound if none are found.
func (o *Options) LastReleaseDeployment(ctx context.Context) (*types.ReleaseDeployment, error) {
	// Anchor end time: prefer the Until from list options if set, otherwise now.
	end := o.ListOptions.Until
	if end.IsZero() {
		end = time.Now().UTC()
	}

	// Preserve original window so the caller can reuse ListOptions after this call.
	origSince, origUntil := o.ListOptions.Since, o.ListOptions.Until
	defer func() {
		o.ListOptions.Since = origSince
		o.ListOptions.Until = origUntil
	}()

	for offset := time.Duration(0); offset < o.MaxLookback; offset += o.Step {
		windowUntil := end.Add(-offset)
		windowSince := windowUntil.Add(-o.Step)

		o.ListOptions.Since = windowSince
		o.ListOptions.Until = windowUntil

		deployments, err := o.ListOptions.ListReleaseDeployments(ctx)
		if err != nil {
			return nil, err
		}
		if len(deployments) > 0 {
			return deployments[0], nil
		}
	}

	return nil, ErrNoDeploymentsFound
}
