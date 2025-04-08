package options

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Azure/ARO-Tools/pkg/config"
)

func DefaultRolloutOptions() *RawRolloutOptions {
	return &RawRolloutOptions{
		BaseOptions: DefaultOptions(),
	}
}

func NewRolloutOptions(config config.Variables) *RolloutOptions {
	return &RolloutOptions{
		completedRolloutOptions: &completedRolloutOptions{
			Config: config,
		},
	}
}

func EV2RolloutOptions() *RawRolloutOptions {
	return &RawRolloutOptions{
		Region:      "$location()",
		RegionShort: "$(regionShort)",
		Stamp:       "$stamp()",
	}
}

func BindRolloutOptions(opts *RawRolloutOptions, cmd *cobra.Command) error {
	err := BindOptions(opts.BaseOptions, cmd)
	if err != nil {
		return fmt.Errorf("failed to bind options: %w", err)
	}
	cmd.Flags().StringVar(&opts.Region, "region", opts.Region, "resources location")
	cmd.Flags().StringVar(&opts.RegionShort, "region-short", opts.RegionShort, "short region string")
	cmd.Flags().StringVar(&opts.Stamp, "stamp", opts.Stamp, "stamp")
	cmd.Flags().StringToStringVar(&opts.ExtraVars, "extra-args", opts.ExtraVars, "Extra arguments to be used config templating")
	return nil
}

// RawRolloutOptions holds input values.
type RawRolloutOptions struct {
	Region      string
	RegionShort string
	Stamp       string
	ExtraVars   map[string]string
	BaseOptions *RawOptions
}

// validatedRolloutOptions is a private wrapper that enforces a call of Validate() before Complete() can be invoked.
type validatedRolloutOptions struct {
	*RawRolloutOptions
	*ValidatedOptions
}

type ValidatedRolloutOptions struct {
	// Embed a private pointer that cannot be instantiated outside of this package.
	*validatedRolloutOptions
}

type completedRolloutOptions struct {
	*ValidatedRolloutOptions
	Options *Options
	Config  config.Variables
}

type RolloutOptions struct {
	// Embed a private pointer that cannot be instantiated outside of this package.
	*completedRolloutOptions
}

func (o *RawRolloutOptions) Validate() (*ValidatedRolloutOptions, error) {
	validatedBaseOptions, err := o.BaseOptions.Validate()
	if err != nil {
		return nil, err
	}

	return &ValidatedRolloutOptions{
		validatedRolloutOptions: &validatedRolloutOptions{
			RawRolloutOptions: o,
			ValidatedOptions:  validatedBaseOptions,
		},
	}, nil
}

func (o *ValidatedRolloutOptions) Complete() (*RolloutOptions, error) {
	completed, err := o.ValidatedOptions.Complete()
	if err != nil {
		return nil, err
	}

	variables, err := completed.ConfigProvider.GetVariables(o.Cloud, o.DeployEnv, o.Region, config.NewConfigReplacements(o.Region, o.RegionShort, o.Stamp))
	if err != nil {
		return nil, fmt.Errorf("failed to get variables: %w", err)
	}
	extraVars := make(map[string]interface{})
	for k, v := range o.ExtraVars {
		extraVars[k] = v
	}
	variables["extraVars"] = extraVars

	return &RolloutOptions{
		completedRolloutOptions: &completedRolloutOptions{
			ValidatedRolloutOptions: o,
			Options:                 completed,
			Config:                  variables,
		},
	}, nil
}
