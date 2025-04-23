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

package inspect

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	options "github.com/Azure/ARO-HCP/tooling/templatize/cmd"
	output "github.com/Azure/ARO-HCP/tooling/templatize/internal/utils"
)

func NewCommand() (*cobra.Command, error) {
	opts := options.DefaultRolloutOptions()

	format := "json"
	cmd := &cobra.Command{
		Use:   "inspect",
		Short: "inspect",
		Long:  "inspect",
		RunE: func(cmd *cobra.Command, args []string) error {
			return dumpConfig(cmd.Context(), format, opts)
		},
	}
	if err := options.BindRolloutOptions(opts, cmd); err != nil {
		return nil, err
	}
	cmd.Flags().StringVar(&format, "format", format, "output format (json, yaml)")
	return cmd, nil
}

func dumpConfig(ctx context.Context, format string, opts *options.RawRolloutOptions) error {
	validated, err := opts.Validate()
	if err != nil {
		return err
	}
	completed, err := validated.Complete()
	if err != nil {
		return err
	}

	var dumpFunc func(interface{}) (string, error)
	switch format {
	case "json":
		dumpFunc = output.PrettyPrintJSON
	case "yaml":
		dumpFunc = output.PrettyPrintYAML
	default:
		return fmt.Errorf("unsupported format: %s", format)
	}
	data, err := dumpFunc(completed.Config)
	if err != nil {
		return err
	}
	fmt.Println(data)
	return nil
}
