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

	"github.com/Azure/ARO-Tools/pkg/config/ev2config"
	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/Azure/ARO-Tools/pkg/config"

	options "github.com/Azure/ARO-HCP/tooling/templatize/cmd"
)

func DefaultLookupOptions() *RawLookupOptions {
	return &RawLookupOptions{
		Region: "uksouth",
	}
}

func BindLookupOptions(opts *RawLookupOptions, cmd *cobra.Command) error {
	cmd.Flags().StringVar(&opts.Region, "region", opts.Region, "Region name to do lookup in.")
	cmd.Flags().StringVar(&opts.Path, "path", opts.Path, "JSONPath expression to look up.")
	return nil
}

// RawLookupOptions holds input values.
type RawLookupOptions struct {
	Region string
	Path   string
}

// validatedLookupOptions is a private wrapper that enforces a call of Validate() before Complete() can be invoked.
type validatedLookupOptions struct {
	*RawLookupOptions
	*options.ValidatedRolloutOptions
}

type ValidatedLookupOptions struct {
	// Embed a private pointer that cannot be instantiated outside of this package.
	*validatedLookupOptions
}

// completedLookupOptions is a private wrapper that enforces a call of Complete() before config generation can be invoked.
type completedLookupOptions struct {
	Ev2Config config.Configuration
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
	ev2Cfg, err := ev2config.Config()
	if err != nil {
		return nil, fmt.Errorf("error loading embedded ev2 config: %v", err)
	}
	if !sets.New(ev2Cfg.GetRegions("public", "prod")...).Has(o.Region) {
		return nil, fmt.Errorf("invalid region %q", o.Region)
	}

	return &LookupOptions{
		completedLookupOptions: &completedLookupOptions{
			Ev2Config: ev2Cfg.ResolveRegion("public", "prod", o.Region),
			Path:      o.Path,
		},
	}, nil
}

func (opts *LookupOptions) Lookup() error {
	val, ok := opts.Ev2Config.GetByPath(opts.Path)
	if !ok {
		return fmt.Errorf("invalid path %q", opts.Path)
	}
	fmt.Printf("%v\n", val)
	return nil
}
