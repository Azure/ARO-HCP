package main

import (
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/Azure/ARO-HCP/tooling/templatize/config"
)

func DefaultGenerationOptions() *RawGenerationOptions {
	return &RawGenerationOptions{}
}

func BindGenerationOptions(opts *RawGenerationOptions, cmd *cobra.Command) error {
	cmd.Flags().StringVar(&opts.ConfigFile, "config-file", opts.ConfigFile, "config file path")
	cmd.Flags().StringVar(&opts.Input, "input", opts.Input, "input file path")
	cmd.Flags().StringVar(&opts.Output, "output", opts.Output, "output file directory or '-' for stdout")
	cmd.Flags().StringVar(&opts.Cloud, "cloud", opts.Cloud, "the cloud (public, fairfax)")
	cmd.Flags().StringVar(&opts.DeployEnv, "deploy-env", opts.DeployEnv, "the deploy environment")
	cmd.Flags().StringVar(&opts.Region, "region", opts.Region, "resources location")
	cmd.Flags().StringVar(&opts.RegionStamp, "region-stamp", opts.RegionStamp, "region stamp")
	cmd.Flags().StringVar(&opts.CXStamp, "cx-stamp", opts.CXStamp, "CX stamp")

	for _, flag := range []string{"config-file", "input", "output"} {
		if err := cmd.MarkFlagFilename("config-file"); err != nil {
			return fmt.Errorf("failed to mark flag %q as a file: %w", flag, err)
		}
	}
	return nil
}

// RawGenerationOptions holds input values.
type RawGenerationOptions struct {
	ConfigFile  string
	Input       string
	Output      string
	Cloud       string
	DeployEnv   string
	Region      string
	RegionStamp string
	CXStamp     string
}

func (o *RawGenerationOptions) Validate() (*ValidatedGenerationOptions, error) {
	validClouds := sets.NewString("public", "fairfax")
	if !validClouds.Has(o.Cloud) {
		return nil, fmt.Errorf("invalid cloud %s, must be one of %v", o.Cloud, validClouds.List())
	}

	return &ValidatedGenerationOptions{
		validatedGenerationOptions: &validatedGenerationOptions{
			RawGenerationOptions: o,
		},
	}, nil
}

// validatedGenerationOptions is a private wrapper that enforces a call of Validate() before Complete() can be invoked.
type validatedGenerationOptions struct {
	*RawGenerationOptions
}

type ValidatedGenerationOptions struct {
	// Embed a private pointer that cannot be instantiated outside of this package.
	*validatedGenerationOptions
}

func (o *ValidatedGenerationOptions) Complete() (*GenerationOptions, error) {
	cfg := config.NewConfigProvider(o.ConfigFile, o.Region, o.RegionStamp, o.CXStamp)
	vars, err := cfg.GetVariables(o.Cloud, o.DeployEnv)
	if err != nil {
		return nil, fmt.Errorf("failed to get variables for cloud %s: %w", o.Cloud, err)
	}

	inputFile := filepath.Base(o.Input)

	var output *os.File
	if o.Output == "-" {
		output = os.Stdout
	} else {
		if err = os.MkdirAll(o.Output, os.ModePerm); err != nil {
			return nil, fmt.Errorf("failed to create output directory %s: %w", o.Output, err)
		}

		output, err = os.Create(filepath.Join(o.Output, inputFile))
		if err != nil {
			return nil, fmt.Errorf("failed to create output file %s: %w", o.Input, err)
		}
	}

	return &GenerationOptions{
		completedGenerationOptions: &completedGenerationOptions{
			Config:    vars,
			Input:     os.DirFS(filepath.Dir(o.Input)),
			InputFile: inputFile,
			Output:    output,
		},
	}, nil
}

// completedGenerationOptions is a private wrapper that enforces a call of Complete() before config generation can be invoked.
type completedGenerationOptions struct {
	Config    config.Variables
	Input     fs.FS
	InputFile string
	Output    io.WriteCloser
}

type GenerationOptions struct {
	// Embed a private pointer that cannot be instantiated outside of this package.
	*completedGenerationOptions
}

func main() {
	opts := DefaultGenerationOptions()
	cmd := &cobra.Command{
		Use:   "templatize",
		Short: "templatize",
		Long:  "templatize",
		RunE: func(cmd *cobra.Command, args []string) error {
			return executeTemplate(opts)
		},
	}
	if err := BindGenerationOptions(opts, cmd); err != nil {
		log.Fatal(err)
	}

	if err := cmd.Execute(); err != nil {
		log.Fatal(err)
	}
}

func executeTemplate(opts *RawGenerationOptions) error {
	fmt.Printf("%-20s: %s\n", "Config", opts.ConfigFile)
	fmt.Printf("%-20s: %s\n", "Input", opts.Input)
	fmt.Printf("%-20s: %s\n", "Cloud", opts.Cloud)
	fmt.Printf("%-20s: %s\n", "Deployment Env", opts.DeployEnv)
	fmt.Printf("%-20s: %s\n", "Region", opts.Region)
	fmt.Printf("%-20s: %s\n", "Region Stamp", opts.RegionStamp)
	fmt.Printf("%-20s: %s\n", "CX Stamp", opts.CXStamp)

	validated, err := opts.Validate()
	if err != nil {
		return err
	}
	completed, err := validated.Complete()
	if err != nil {
		return err
	}
	return completed.ExecuteTemplate()
}
