package options

import (
	"fmt"

	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/Azure/ARO-Tools/pkg/config"
)

func DefaultOptions() *RawOptions {
	return &RawOptions{}
}

func BindOptions(opts *RawOptions, cmd *cobra.Command) error {
	cmd.Flags().StringVar(&opts.ConfigFile, "config-file", opts.ConfigFile, "config file path")
	cmd.Flags().StringVar(&opts.Cloud, "cloud", opts.Cloud, "the cloud (public, fairfax)")
	cmd.Flags().StringVar(&opts.DeployEnv, "deploy-env", opts.DeployEnv, "the deploy environment")
	return nil
}

// RawGenerationOptions holds input values.
type RawOptions struct {
	ConfigFile string
	Cloud      string
	DeployEnv  string
}

func (o *RawOptions) Validate() (*ValidatedOptions, error) {
	validClouds := sets.NewString("public", "fairfax")
	if !validClouds.Has(o.Cloud) {
		return nil, fmt.Errorf("invalid cloud %s, must be one of %v", o.Cloud, validClouds.List())
	}

	return &ValidatedOptions{
		validatedOptions: &validatedOptions{
			RawOptions: o,
		},
	}, nil
}

// validatedOptions is a private wrapper that enforces a call of Validate() before Complete() can be invoked.
type validatedOptions struct {
	*RawOptions
}

type ValidatedOptions struct {
	// Embed a private pointer that cannot be instantiated outside of this package.
	*validatedOptions
}

func (o *ValidatedOptions) Complete() (*Options, error) {
	configProvider := config.NewConfigProvider(o.ConfigFile)
	err := configProvider.Validate(o.Cloud, o.DeployEnv)
	if err != nil {
		return nil, fmt.Errorf("failed to validate config: %w", err)
	}

	return &Options{
		completedOptions: &completedOptions{
			ConfigProvider: configProvider,
		},
	}, nil
}

// completedGenerationOptions is a private wrapper that enforces a call of Complete() before config generation can be invoked.
type completedOptions struct {
	ConfigProvider config.ConfigProvider
}

type Options struct {
	// Embed a private pointer that cannot be instantiated outside of this package.
	*completedOptions
}
