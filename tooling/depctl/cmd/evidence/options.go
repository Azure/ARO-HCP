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

package evidence

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/tooling/depctl/internal/cli"
	evidencepkg "github.com/Azure/ARO-HCP/tooling/depctl/internal/evidence"
)

// RawOptions are the unvalidated evidence flags.
type RawOptions struct {
	ProwURL   string
	OutputDir string
	Output    string

	out io.Writer
	err io.Writer
}

// DefaultOptions returns the raw evidence options.
func DefaultOptions() *RawOptions {
	return &RawOptions{}
}

// BindOptions registers the evidence flags.
func BindOptions(options *RawOptions, command *cobra.Command) error {
	command.Flags().StringVar(&options.ProwURL, "url", options.ProwURL, "canonical prow.ci.openshift.org run URL")
	command.Flags().StringVar(&options.OutputDir, "output-dir", options.OutputDir, "empty directory for the artifacts")
	command.Flags().StringVar(&options.Output, "output", options.Output, "write the evidence manifest here")
	return nil
}

// ValidatedOptions are the evidence options that passed validation.
type ValidatedOptions struct {
	*RawOptions
}

// Validate checks the evidence inputs.
func (o *RawOptions) Validate(_ context.Context) (*ValidatedOptions, error) {
	if o.ProwURL == "" || o.OutputDir == "" {
		return nil, cli.Invalid(errURLAndOutputDirRequired)
	}
	// A malformed URL is invalid input, not missing remote evidence.
	if _, err := evidencepkg.ArtifactBase(o.ProwURL); err != nil {
		return nil, cli.Invalid(err)
	}
	return &ValidatedOptions{RawOptions: o}, nil
}

// CompletedOptions are the evidence options with their fetcher.
type CompletedOptions struct {
	*ValidatedOptions
	fetcher evidencepkg.Fetcher
}

// Complete builds the public artifact fetcher.
func (v *ValidatedOptions) Complete(_ context.Context) (*CompletedOptions, error) {
	return &CompletedOptions{ValidatedOptions: v, fetcher: evidencepkg.NewHTTPFetcher()}, nil
}

// Run retrieves the artifacts and emits the manifest.
func (c *CompletedOptions) Run(ctx context.Context) error {
	manifest, err := c.fetcher.Fetch(ctx, c.ProwURL, c.OutputDir)
	if err != nil {
		return cli.Incomplete("evidence retrieval failed: %v (missing: %v)", err, manifest.Missing)
	}
	if err := cli.EmitRecord(c.out, c.Output, manifest); err != nil {
		return err
	}
	fmt.Fprintf(c.err, "retrieved %d artifacts for %s (result %s)\n",
		len(manifest.Files), c.ProwURL, manifest.Result)
	return nil
}

// Errors reported by the evidence command.
var errURLAndOutputDirRequired = errors.New("--url and --output-dir are required")
