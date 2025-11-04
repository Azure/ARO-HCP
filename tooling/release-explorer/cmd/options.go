package cmd

import (
	"fmt"
	"time"

	"github.com/Azure/ARO-Tools/pkg/release/output"
	"github.com/spf13/cobra"
)

func DefaultOptions() *RawOptions {
	return &RawOptions{
		OutputFormat: string(output.FormatHuman),
		UseLocalTime: false,
	}
}

func BindOptions(opts *RawOptions, cmd *cobra.Command) error {
	cmd.Flags().StringVarP(&opts.OutputFormat, "output", "o", string(output.FormatHuman), "Output format (json, yaml, human)")
	cmd.Flags().BoolVar(&opts.UseLocalTime, "local-time", false, "Use local time for output")
	return nil
}

type RawOptions struct {
	OutputFormat string
	UseLocalTime bool
}

type validatedOptions struct {
	*RawOptions
}

type ValidatedOptions struct {
	*validatedOptions
}

type Options struct {
	OutputFormat output.Format
	Locale       *time.Location
}

func (o *RawOptions) Validate() (*ValidatedOptions, error) {
	switch o.OutputFormat {
	case string(output.FormatJSON), string(output.FormatYAML), string(output.FormatHuman):
	default:
		return nil, fmt.Errorf("invalid output format: %s", o.OutputFormat)
	}

	return &ValidatedOptions{
		validatedOptions: &validatedOptions{
			RawOptions: o,
		},
	}, nil
}

func (o *ValidatedOptions) Complete() (*Options, error) {

	loc := time.UTC
	if o.UseLocalTime {
		loc = time.Local
	}
	return &Options{
		OutputFormat: output.Format(o.OutputFormat),
		Locale:       loc,
	}, nil
}
