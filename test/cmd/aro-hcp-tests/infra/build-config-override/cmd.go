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
		case "ARM_HELPER_POOL_CATALOG":
			opts.ARMHelperCatalog = value
		}
	}
	return opts
}

func BindOptions(opts *RawOptions, cmd *cobra.Command) {
	flags := cmd.Flags()
	flags.StringVar(&opts.SourceDir, "source-dir", opts.SourceDir, "Selected source checkout for relative catalog paths")
	flags.StringVar(&opts.SharedDir, "shared-dir", opts.SharedDir, "Shared CI directory")
	flags.StringVar(&opts.DeployEnvironment, "deploy-env", opts.DeployEnvironment, "Deployment environment")
	flags.StringVar(&opts.OutputFile, "output-file", opts.OutputFile, "Override output file (default shared-dir/config-override.yaml)")
	flags.StringVar(&opts.MSIMockCatalog, "msi-mock-pool-catalog", opts.MSIMockCatalog, "MSI mock catalog (default source-dir/dev-infrastructure/openshift-ci/msi-mock-pool.yaml)")
	flags.StringVar(&opts.ARMHelperCatalog, "arm-helper-pool-catalog", opts.ARMHelperCatalog, "ARM helper catalog (default source-dir/dev-infrastructure/openshift-ci/arm-helper-pool.yaml)")
	flags.StringVar(&opts.HypershiftOverlay, "hypershift-override-file", opts.HypershiftOverlay, "Hypershift overlay (default shared-dir/hypershift-image-overrides.yaml)")
	flags.StringVar(&opts.TemplatizeSettings, "templatize-settings", opts.TemplatizeSettings, "Templatize settings deciding per-run environments (default source-dir/tooling/templatize/settings.yaml)")
	flags.StringVar(&opts.ServiceConfig, "service-config", opts.ServiceConfig, "Service config providing effective AKS tags (default source-dir/config/config.yaml)")
	flags.StringVar(&opts.RunCostTagFile, "run-cost-tag-file", opts.RunCostTagFile, "File receiving the run cost tag for the provisioning shell (default shared-dir/run-cost-tag.env)")
}

type RawOptions struct {
	SourceDir          string
	SharedDir          string
	DeployEnvironment  string
	OutputFile         string
	MSIMockCatalog     string
	ARMHelperCatalog   string
	HypershiftOverlay  string
	TemplatizeSettings string
	ServiceConfig      string
	RunCostTagFile     string
	Environ            []string
	Out                io.Writer
}

type validatedOptions struct {
	*RawOptions
	input configoverride.Inputs
}

type ValidatedOptions struct {
	*validatedOptions
}

type completedOptions struct {
	input          configoverride.Inputs
	paths          configoverride.Paths
	outputFile     string
	runCostTagFile string
	out            io.Writer
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
	msiMockCatalog := o.MSIMockCatalog
	if msiMockCatalog == "" {
		msiMockCatalog = "dev-infrastructure/openshift-ci/msi-mock-pool.yaml"
	}
	armHelperCatalog := o.ARMHelperCatalog
	if armHelperCatalog == "" {
		armHelperCatalog = "dev-infrastructure/openshift-ci/arm-helper-pool.yaml"
	}
	hypershiftOverlay := o.HypershiftOverlay
	if hypershiftOverlay == "" && o.SharedDir != "" {
		hypershiftOverlay = filepath.Join(o.SharedDir, "hypershift-image-overrides.yaml")
	}
	templatizeSettings := o.TemplatizeSettings
	if templatizeSettings == "" {
		templatizeSettings = "tooling/templatize/settings.yaml"
	}
	serviceConfig := o.ServiceConfig
	if serviceConfig == "" {
		serviceConfig = "config/config.yaml"
	}
	runCostTagFile := o.RunCostTagFile
	if runCostTagFile == "" && o.SharedDir != "" {
		runCostTagFile = filepath.Join(o.SharedDir, "run-cost-tag.env")
	}
	return &Options{
		completedOptions: &completedOptions{
			input: o.input,
			paths: configoverride.Paths{
				MSIMockCatalog:     sourcePath(o.SourceDir, msiMockCatalog),
				ARMHelperCatalog:   sourcePath(o.SourceDir, armHelperCatalog),
				HypershiftOverlay:  hypershiftOverlay,
				TemplatizeSettings: sourcePath(o.SourceDir, templatizeSettings),
				ServiceConfig:      sourcePath(o.SourceDir, serviceConfig),
			},
			outputFile:     outputFile,
			runCostTagFile: runCostTagFile,
			out:            o.Out,
		},
	}, nil
}

func (o *Options) Run(_ context.Context) error {
	result, err := configoverride.Generate(o.input, o.paths)
	if err != nil {
		return err
	}
	// Checked before writing so an invocation with invalid options cannot leave
	// a partially applied configuration behind.
	if result.RunCostTagValue != "" && o.runCostTagFile == "" {
		return fmt.Errorf("run cost tag file is required when %s is tagged; pass --shared-dir or --run-cost-tag-file", configoverride.RunCostTagKey)
	}
	if err := configoverride.WriteOverride(o.outputFile, result.YAML); err != nil {
		return err
	}
	if o.runCostTagFile != "" {
		// The file is rewritten on every successful run, including with empty
		// assignments when no tag applies, so a shell that sources the adapter
		// repeatedly never keeps a previous invocation's tag. The generator
		// validated BUILD_ID against [A-Za-z0-9._-], making the assignments
		// safe for the sourced adapter to import.
		env := "RUN_COST_TAG_KEY=" + result.RunCostTagKey + "\nRUN_COST_TAG_VALUE=" + result.RunCostTagValue + "\n"
		if err := configoverride.WriteOverride(o.runCostTagFile, []byte(env)); err != nil {
			return err
		}
	}
	_, err = fmt.Fprintln(o.out, result.OCLoginRegistries)
	return err
}

func sourcePath(sourceDir, path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(sourceDir, path)
}
