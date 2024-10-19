package generate

import (
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"text/template"

	"github.com/spf13/cobra"

	options "github.com/Azure/ARO-HCP/tooling/templatize/cmd"
	"github.com/Azure/ARO-HCP/tooling/templatize/internal/config"
)

func DefaultGenerationOptions() *RawGenerationOptions {
	return &RawGenerationOptions{}
}

func BindGenerationOptions(opts *RawGenerationOptions, cmd *cobra.Command) error {
	err := options.BindOptions(&opts.RawOptions, cmd)
	if err != nil {
		return fmt.Errorf("failed to bind raw options: %w", err)
	}
	cmd.Flags().StringVar(&opts.Input, "input", opts.Input, "input file path")
	cmd.Flags().StringVar(&opts.Output, "output", opts.Output, "output file directory")

	for _, flag := range []string{"config-file", "input", "output"} {
		if err := cmd.MarkFlagFilename("config-file"); err != nil {
			return fmt.Errorf("failed to mark flag %q as a file: %w", flag, err)
		}
	}
	return nil
}

// RawGenerationOptions holds input values.
type RawGenerationOptions struct {
	options.RawOptions
	Input  string
	Output string
}

func (o *RawGenerationOptions) Validate() (*ValidatedGenerationOptions, error) {
	if _, err := o.RawOptions.Validate(); err != nil {
		return nil, fmt.Errorf("validation failed for raw options: %w", err)
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

	if err := os.MkdirAll(o.Output, os.ModePerm); err != nil {
		return nil, fmt.Errorf("failed to create output directory %s: %w", o.Output, err)
	}

	output, err := os.Create(filepath.Join(o.Output, inputFile))
	if err != nil {
		return nil, fmt.Errorf("failed to create output file %s: %w", o.Input, err)
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

func (opts *GenerationOptions) ExecuteTemplate() error {
	tmpl, err := template.New(opts.InputFile).ParseFS(opts.Input, opts.InputFile)
	if err != nil {
		return err
	}

	defer func() {
		if err := opts.Output.Close(); err != nil {
			log.Printf("error closing output: %v\n", err)
		}
	}()
	return tmpl.ExecuteTemplate(opts.Output, opts.InputFile, opts.Config)
}
