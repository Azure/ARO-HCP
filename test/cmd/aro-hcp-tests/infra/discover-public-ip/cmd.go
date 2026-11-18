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

package discoverpublicip

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/test/pkg/publicip"
)

type RawOptions struct {
	Out io.Writer
}

type validatedOptions struct {
	*RawOptions
}

type ValidatedOptions struct {
	*validatedOptions
}

type completedOptions struct {
	out io.Writer
}

type Options struct {
	*completedOptions
}

func DefaultOptions() *RawOptions {
	return &RawOptions{Out: os.Stdout}
}

func NewCommand() *cobra.Command {
	opts := DefaultOptions()
	return &cobra.Command{
		Use:          "discover-public-ip",
		Short:        "Print the test runner's public IPv4 address",
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
}

func (o *RawOptions) Validate() (*ValidatedOptions, error) {
	if o.Out == nil {
		return nil, fmt.Errorf("an output writer is required")
	}
	return &ValidatedOptions{validatedOptions: &validatedOptions{RawOptions: o}}, nil
}

func (o *ValidatedOptions) Complete(_ context.Context) (*Options, error) {
	return &Options{completedOptions: &completedOptions{
		out: o.Out,
	}}, nil
}

func (o *Options) Run(ctx context.Context) error {
	address, err := publicip.DiscoverIPv4(ctx)
	if err != nil {
		return fmt.Errorf("discovering public IP: %w", err)
	}
	if _, err := fmt.Fprintln(o.out, address.String()); err != nil {
		return fmt.Errorf("writing public IP: %w", err)
	}
	return nil
}
