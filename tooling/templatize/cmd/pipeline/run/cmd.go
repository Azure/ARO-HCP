// Copyright 2025 Microsoft Corporation
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

package run

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/Masterminds/semver/v3"
	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/tooling/templatize/pkg/azauth"
)

type Version struct {
	Cmd        string
	Name       string
	Constraint string
}

var versionConstraints = []Version{
	{
		Name:       "Azure CLI",
		Cmd:        `az version --query '"azure-cli"' -otsv`,
		Constraint: ">=2.68.0",
	},
	{
		Name:       "bicep module",
		Cmd:        "az bicep version --only-show-errors |grep 'CLI version' |awk '{print $4}'",
		Constraint: ">=0.31.0",
	},
}

func NewCommand() (*cobra.Command, error) {
	opts := DefaultOptions()
	cmd := &cobra.Command{
		Use:   "run",
		Short: "run a pipeline.yaml file towards an Azure Resourcegroup / AKS cluster",
		Long:  "run a pipeline.yaml file towards an Azure Resourcegroup / AKS cluster",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPipeline(cmd.Context(), opts)
		},
	}
	if err := BindOptions(opts, cmd); err != nil {
		return nil, err
	}
	return cmd, nil
}

func ensureDependencies(ctx context.Context) error {
	for _, c := range versionConstraints {
		cmd := exec.CommandContext(ctx, "/bin/bash", "-c", c.Cmd)
		output, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("error checking version %s, error: %v", c.Name, err)
		}
		semverConstraint, err := semver.NewConstraint(c.Constraint)
		if err != nil {
			return fmt.Errorf("error creation version constraint '%s', %v", c.Name, err)
		}
		trimmedOutput := strings.TrimSuffix(string(output), "\n")
		v, err := semver.NewVersion(trimmedOutput)
		if err != nil {
			return fmt.Errorf("error parsing version of '%s', '%s' %v", c.Name, trimmedOutput, err)
		}

		if !semverConstraint.Check(v) {
			return fmt.Errorf("wrong version of '%s', expected '%s', found: '%s'", c.Name, c.Constraint, trimmedOutput)
		}

	}
	return nil
}

func runPipeline(ctx context.Context, opts *RawRunOptions) error {
	validated, err := opts.Validate()
	if err != nil {
		return err
	}
	completed, err := validated.Complete()
	if err != nil {
		return err
	}
	err = azauth.SetupAzureAuth(ctx)
	if err != nil {
		return err
	}
	err = ensureDependencies(ctx)
	if err != nil {
		return err
	}
	return completed.RunPipeline(ctx)
}
