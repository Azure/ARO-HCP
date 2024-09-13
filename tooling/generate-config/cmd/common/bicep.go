package common

import (
	"fmt"
	"strings"

	"github.com/spf13/pflag"
	"k8s.io/apimachinery/pkg/util/sets"
)

const (
	OutputFormatBicepParam = "bicepparam"
	OutputFormatBicepQuery = "bicepquery"
)

func DefaultBicepOptions() *RawBicepOptions {
	return &RawBicepOptions{
		OutputFormat: OutputFormatBicepParam,
	}
}

func BindBicepOptions(options *RawBicepOptions, flags *pflag.FlagSet) {
	flags.StringVar(&options.OutputFormat, "format", options.OutputFormat, "Format for output, either a .bicepparam file to input to a module or a .bicep file to query Azure state.")
}

// RawBicepOptions holds input values.
type RawBicepOptions struct {
	OutputFormat string
}

func (o *RawBicepOptions) Validate() (*ValidatedBicepOptions, error) {
	allFormats := sets.New[string](OutputFormatBicepParam, OutputFormatBicepQuery)
	if !allFormats.Has(o.OutputFormat) {
		return nil, fmt.Errorf("unknown format %q, valid formats: %s", o.OutputFormat, strings.Join(allFormats.UnsortedList(), ", "))
	}

	return &ValidatedBicepOptions{
		validatedBicepOptions: &validatedBicepOptions{
			RawBicepOptions: o,
		},
	}, nil
}

// validatedBicepOptions is a private wrapper that enforces a call of Validate() before Complete() can be invoked.
type validatedBicepOptions struct {
	*RawBicepOptions
}

type ValidatedBicepOptions struct {
	// Embed a private pointer that cannot be instantiated outside of this package.
	*validatedBicepOptions
}

func (o *ValidatedBicepOptions) Complete() (*BicepOptions, error) {
	return &BicepOptions{
		completedBicepOptions: &completedBicepOptions{
			OutputFormat: o.OutputFormat,
		},
	}, nil
}

// completedBicepOptions is a private wrapper that enforces a call of Complete() before config generation can be invoked.
type completedBicepOptions struct {
	OutputFormat string
}

type BicepOptions struct {
	// Embed a private pointer that cannot be instantiated outside of this package.
	*completedBicepOptions
}

type BicepParameters map[string]string

func (p BicepParameters) Render(opts *BicepOptions) string {
	switch opts.OutputFormat {
	case OutputFormatBicepParam:
		output := strings.Builder{}
		for key, value := range p {
			fmt.Fprintf(&output, "parameter %s = %s\n", key, value)
		}
		return output.String()
	case OutputFormatBicepQuery:
		panic("TODO: need more type information on the objects for which the names are for, so we can render .bicep query correctly")
	default:
		panic(fmt.Sprintf("programmer error: should never have output format %q in completed options", opts.OutputFormat))
	}
}

type BicepParameter struct {
	Key   string
	Value string
}

func (b BicepParameters) Register(params ...BicepParameter) {
	for _, param := range params {
		b[param.Key] = param.Value
	}
}

func RegionalResourceGroup(opts *PrimitiveOptions) BicepParameter {
	return BicepParameter{Key: "regionalResourceGroup", Value: fmt.Sprintf("aro-hcp-%s-%s", opts.Region, opts.Suffix)}
}

func Location(opts *PrimitiveOptions) BicepParameter {
	return BicepParameter{Key: "location", Value: opts.Region}
}
