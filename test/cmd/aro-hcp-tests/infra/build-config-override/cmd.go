// Copyright 2026 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package buildconfigoverride

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/test/pkg/configoverride"
	"github.com/Azure/ARO-HCP/test/pkg/leaseenv"
)

func DefaultOptions() *RawOptions {
	opts := &RawOptions{
		SourceDir: ".",
		Environ:   os.Environ(),
		Out:       os.Stdout,
		ErrOut:    os.Stderr,
	}
	// All defaults and generator inputs come from the same environment snapshot.
	for _, entry := range opts.Environ {
		key, value, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		switch key {
		case "SHARED_DIR":
			opts.SharedDir = value
		case leaseenv.DeployEnvironment:
			opts.DeployEnvironment = value
		}
	}
	return opts
}

func BindOptions(opts *RawOptions, cmd *cobra.Command) {
	flags := cmd.Flags()
	flags.StringVar(&opts.SourceDir, "source-dir", opts.SourceDir, "Selected source checkout")
	flags.StringVar(&opts.SharedDir, "shared-dir", opts.SharedDir, "Shared CI directory")
	flags.StringVar(&opts.DeployEnvironment, "deploy-env", opts.DeployEnvironment, "Deployment environment")
	flags.StringVar(&opts.OutputFile, "output-file", opts.OutputFile, "Override output file (default shared-dir/config-override.yaml)")
	flags.StringVar(&opts.HypershiftOverlay, "hypershift-override-file", opts.HypershiftOverlay, "Hypershift overlay (default shared-dir/hypershift-image-overrides.yaml)")
	flags.StringArrayVar(&opts.RawOverrides, "raw-config-override", opts.RawOverrides, fmt.Sprintf(
		"Set a configuration key as path.to.key=value, repeatable. The path is rooted at the override document (e.g. clouds.dev.environments.ci00.defaults.mgmt.aks.userAgentPool.vmSize) "+
			"and the value is YAML. Applied after, and over, every generated value, in addition to newline-separated entries in $%s", configoverride.RawConfigOverridesEnv))
}

type RawOptions struct {
	SourceDir         string
	SharedDir         string
	DeployEnvironment string
	OutputFile        string
	HypershiftOverlay string
	RawOverrides      []string
	Environ           []string
	Out               io.Writer
	ErrOut            io.Writer
}

type validatedOptions struct {
	*RawOptions
	input configoverride.Inputs
}

type ValidatedOptions struct {
	*validatedOptions
}

type completedOptions struct {
	input      configoverride.Inputs
	paths      configoverride.Paths
	outputFile string
	out        io.Writer
	errOut     io.Writer
}

type Options struct {
	*completedOptions
}

func NewCommand() *cobra.Command {
	opts := DefaultOptions()
	command := &cobra.Command{
		Use:          "build-config-override",
		Short:        "Atomically publish a CI configuration override and print its login registries",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(command *cobra.Command, _ []string) error {
			opts.Out = command.OutOrStdout()
			opts.ErrOut = command.ErrOrStderr()
			validated, err := opts.Validate()
			if err != nil {
				return err
			}
			completed, err := validated.Complete(command.Context())
			if err != nil {
				return err
			}
			return completed.Run(command.Context())
		},
	}
	BindOptions(opts, command)
	return command
}

func (o *RawOptions) Validate() (*ValidatedOptions, error) {
	if o.OutputFile == "" && o.SharedDir == "" {
		return nil, fmt.Errorf("--shared-dir or --output-file is required")
	}
	input, err := configoverride.InputsFromEnvironment(o.Environ)
	if err != nil {
		return nil, err
	}
	flagOverrides, err := configoverride.ParseRawOverrides(o.RawOverrides)
	if err != nil {
		return nil, fmt.Errorf("--raw-config-override: %w", err)
	}
	input.RawOverrides = append(input.RawOverrides, flagOverrides...)
	input.DeployEnvironment = o.DeployEnvironment
	return &ValidatedOptions{
		validatedOptions: &validatedOptions{RawOptions: o, input: input},
	}, nil
}

func (o *ValidatedOptions) Complete(_ context.Context) (*Options, error) {
	outputFile := o.OutputFile
	if outputFile == "" {
		outputFile = filepath.Join(o.SharedDir, "config-override.yaml")
	}
	hypershiftOverlay := o.HypershiftOverlay
	if hypershiftOverlay == "" && o.SharedDir != "" {
		hypershiftOverlay = filepath.Join(o.SharedDir, "hypershift-image-overrides.yaml")
	}
	return &Options{
		completedOptions: &completedOptions{
			input: o.input,
			paths: configoverride.Paths{
				HypershiftOverlay: hypershiftOverlay,
			},
			outputFile: outputFile,
			out:        o.Out,
			errOut:     o.ErrOut,
		},
	}, nil
}

func (o *Options) Run(_ context.Context) error {
	result, err := configoverride.Generate(o.input, o.paths)
	if err != nil {
		return err
	}
	if err := configoverride.WriteOverride(o.outputFile, result.YAML); err != nil {
		return err
	}
	// stdout is registry data for the shell adapter; report on stderr. Values
	// are echoed verbatim, so raw overrides must never carry secrets.
	for _, override := range o.input.RawOverrides {
		if _, err := fmt.Fprintf(o.errOut, "raw config override applied: %s\n", override.Entry); err != nil {
			return err
		}
	}
	_, err = fmt.Fprintln(o.out, result.OCLoginRegistries)
	return err
}
