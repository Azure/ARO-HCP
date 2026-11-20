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

package dependabot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"regexp"

	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/tooling/depctl/internal/cli"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/dependabot"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/forge/ghapi"
)

var (
	nativeRequestRepositoryPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*/[A-Za-z0-9][A-Za-z0-9_.-]*$`)
	nativeRequestGroupPattern      = regexp.MustCompile(`^[0-9a-f]{24}$`)
)

type rawNativeRequestOptions struct {
	repository, root, inventory, group, target, titleFile, bodyFile, artifacts string
}

type validatedNativeRequestOptions struct {
	rawNativeRequestOptions
}

type nativeRequestOptions struct {
	config dependabot.NativePrepareOptions
	output io.Writer
}

type rawNativeCandidateOptions struct {
	rawNativeRequestOptions
	compiler string
}

type validatedNativeCandidateOptions struct {
	validatedNativeRequestOptions
	compiler string
}

type nativeCandidateOptions struct {
	config dependabot.NativeCandidateOptions
	read   dependabot.NativeReadFunc
	output io.Writer
}

// BindFlags binds the shared publication inputs without exposing domain options.
func (o *rawNativeRequestOptions) BindFlags(command *cobra.Command, artifactsHelp string) {
	command.Flags().StringVar(&o.repository, "repository", "", "OWNER/REPO")
	command.Flags().StringVar(&o.root, "repo", ".", "candidate repository root")
	command.Flags().StringVar(&o.inventory, "inventory", "", "native inventory JSON file")
	command.Flags().StringVar(&o.group, "group", "", "native family group ID")
	command.Flags().StringVar(&o.target, "target", "", "exact inventory target")
	command.Flags().StringVar(&o.titleFile, "title-file", "", "UTF-8 publication title file")
	command.Flags().StringVar(&o.bodyFile, "body-file", "", "UTF-8 publication body file")
	command.Flags().StringVar(&o.artifacts, "artifact-dir", "", artifactsHelp)
}

// Validate checks required flags and direct selection syntax before execution.
func (o *rawNativeRequestOptions) Validate() (*validatedNativeRequestOptions, error) {
	if o.repository == "" || o.inventory == "" || o.group == "" || o.target == "" || o.titleFile == "" || o.bodyFile == "" || o.artifacts == "" {
		return nil, cli.Invalid(errors.New("--repository, --inventory, --group, --target, --title-file, --body-file and --artifact-dir are required"))
	}
	if !nativeRequestRepositoryPattern.MatchString(o.repository) {
		return nil, cli.Invalid(errors.New("repository must be OWNER/REPO"))
	}
	if !nativeRequestGroupPattern.MatchString(o.group) {
		return nil, cli.Invalid(errors.New("invalid inventory group or target"))
	}
	return &validatedNativeRequestOptions{rawNativeRequestOptions: *o}, nil
}

// RequestConfig resolves the shared execution paths into the domain request inputs.
func (o *validatedNativeRequestOptions) requestConfig() (dependabot.NativePrepareOptions, error) {
	root := o.root
	if root == "" {
		root = "."
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return dependabot.NativePrepareOptions{}, fmt.Errorf("resolve candidate repository root: %w", err)
	}
	artifacts, err := filepath.Abs(o.artifacts)
	if err != nil {
		return dependabot.NativePrepareOptions{}, fmt.Errorf("resolve native artifact directory: %w", err)
	}
	return dependabot.NativePrepareOptions{
		Repository: o.repository, Root: root, Inventory: o.inventory, Group: o.group,
		Target: o.target, TitleFile: o.titleFile, BodyFile: o.bodyFile, Artifacts: artifacts,
	}, nil
}

// Complete supplies the offline request configuration and command output.
func (o *validatedNativeRequestOptions) Complete(output io.Writer) (*nativeRequestOptions, error) {
	config, err := o.requestConfig()
	if err != nil {
		return nil, err
	}
	return &nativeRequestOptions{config: config, output: output}, nil
}

// Run packages the validated request without constructing a network reader.
func (o *nativeRequestOptions) Run(ctx context.Context) error {
	result, err := dependabot.PrepareNativeRequest(ctx, o.config)
	if err != nil {
		return cli.Invalid(err)
	}
	return json.NewEncoder(o.output).Encode(result)
}

// Validate checks shared inputs while retaining the optional compiler flag.
func (o *rawNativeCandidateOptions) Validate() (*validatedNativeCandidateOptions, error) {
	validated, err := o.rawNativeRequestOptions.Validate()
	if err != nil {
		return nil, err
	}
	return &validatedNativeCandidateOptions{validatedNativeRequestOptions: *validated, compiler: o.compiler}, nil
}

// Complete supplies the candidate configuration, isolated read boundary and output.
func (o *validatedNativeCandidateOptions) Complete(output io.Writer) (*nativeCandidateOptions, error) {
	config, err := o.requestConfig()
	if err != nil {
		return nil, err
	}
	return &nativeCandidateOptions{
		config: dependabot.NativeCandidateOptions{
			Repository: config.Repository, Root: config.Root, Inventory: config.Inventory,
			Group: config.Group, Target: config.Target, TitleFile: config.TitleFile,
			BodyFile: config.BodyFile, Artifacts: config.Artifacts, Compiler: o.compiler,
		},
		read: ghapi.NativeCLIRead, output: output,
	}, nil
}

// Run validates fixed consumers and writes the unchanged JSON request receipt.
func (o *nativeCandidateOptions) Run(ctx context.Context) error {
	result, err := dependabot.ValidateNativeCandidate(ctx, o.read, o.config)
	if err != nil {
		return err
	}
	return json.NewEncoder(o.output).Encode(result)
}
