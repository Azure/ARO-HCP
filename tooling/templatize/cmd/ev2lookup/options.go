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

package ev2lookup

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Azure/ARO-Tools/config/ev2config"
	"github.com/Azure/ARO-Tools/config/types"
)

func DefaultLookupOptions() *RawLookupOptions {
	return &RawLookupOptions{
		Cloud:  "public",
		Region: "uksouth",
	}
}

func BindLookupOptions(opts *RawLookupOptions, cmd *cobra.Command) error {
	cmd.Flags().StringVar(&opts.Cloud, "cloud", opts.Cloud, "Cloud name to do lookup in.")
	cmd.Flags().StringVar(&opts.Region, "region", opts.Region, "Region name to do lookup in.")
	cmd.Flags().StringVar(&opts.Path, "path", opts.Path, "JSONPath expression to look up.")
	return nil
}

// RawLookupOptions holds input values.
type RawLookupOptions struct {
	Cloud  string
	Region string
	Path   string
}

// validatedLookupOptions is a private wrapper that enforces a call of Validate() before Complete() can be invoked.
type validatedLookupOptions struct {
	*RawLookupOptions
}

type ValidatedLookupOptions struct {
	// Embed a private pointer that cannot be instantiated outside of this package.
	*validatedLookupOptions
}

// completedLookupOptions is a private wrapper that enforces a call of Complete() before config generation can be invoked.
type completedLookupOptions struct {
	Ev2Config types.Configuration
	Path      string
}

type LookupOptions struct {
	// Embed a private pointer that cannot be instantiated outside of this package.
	*completedLookupOptions
}

func (o *RawLookupOptions) Validate() (*ValidatedLookupOptions, error) {
	return &ValidatedLookupOptions{
		validatedLookupOptions: &validatedLookupOptions{
			RawLookupOptions: o,
		},
	}, nil
}

func (o *ValidatedLookupOptions) Complete() (*LookupOptions, error) {
	ev2Cfg, err := ev2config.ResolveConfig(o.Cloud, o.Region)
	if err != nil {
		return nil, fmt.Errorf("error loading embedded ev2 config: %v", err)
	}

	return &LookupOptions{
		completedLookupOptions: &completedLookupOptions{
			Ev2Config: ev2Cfg,
			Path:      o.Path,
		},
	}, nil
}

func (opts *LookupOptions) Lookup() error {
	val, err := opts.Ev2Config.GetByPath(opts.Path)
	if err != nil {
		return fmt.Errorf("failed to look up value: %w", err)
	}
	fmt.Printf("%v\n", val)
	return nil
}
