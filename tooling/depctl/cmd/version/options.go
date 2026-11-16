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

package version

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/tooling/depctl/internal/schema"
	"github.com/Azure/ARO-HCP/tooling/depctl/pkg/version"
)

// RawOptions are the unvalidated version flags.
type RawOptions struct {
	AsJSON bool

	out io.Writer
}

// DefaultOptions returns the raw version options.
func DefaultOptions() *RawOptions {
	return &RawOptions{}
}

// BindOptions registers the version flags.
func BindOptions(options *RawOptions, command *cobra.Command) error {
	command.Flags().BoolVar(&options.AsJSON, "output-json", options.AsJSON, "print machine-readable provenance")
	return nil
}

// ValidatedOptions are the version options that passed validation.
type ValidatedOptions struct {
	*RawOptions
}

// Validate accepts every version invocation.
func (o *RawOptions) Validate(_ context.Context) (*ValidatedOptions, error) {
	return &ValidatedOptions{RawOptions: o}, nil
}

// CompletedOptions are the version options with their provenance.
type CompletedOptions struct {
	*ValidatedOptions
	info version.VersionInfo
}

// Complete reads the build provenance.
func (v *ValidatedOptions) Complete(_ context.Context) (*CompletedOptions, error) {
	return &CompletedOptions{ValidatedOptions: v, info: version.GetVersionInfo()}, nil
}

// Run prints the provenance.
func (c *CompletedOptions) Run(_ context.Context) error {
	if !c.AsJSON {
		fmt.Fprintf(c.out, "commit: %s\nbuild date: %s\nplatform: %s\nschema version: %s\n",
			c.info.Commit, c.info.BuildDate, version.Platform(), schema.Version)
		return nil
	}
	encoded, err := json.Marshal(map[string]string{
		"commit":        c.info.Commit,
		"buildDate":     c.info.BuildDate,
		"platform":      version.Platform(),
		"schemaVersion": schema.Version,
	})
	if err != nil {
		return err
	}
	fmt.Fprintln(c.out, string(encoded))
	return nil
}
