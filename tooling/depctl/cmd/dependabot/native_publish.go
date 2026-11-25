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
	"net/http"
	"os"
	"path/filepath"
	"time"

	gh "github.com/google/go-github/v69/github"
	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/tooling/depctl/internal/cli"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/dependabot"
)

type rawNativePublishOptions struct {
	repository, root, outputFile string
}

type validatedNativePublishOptions struct {
	repository, root string
	request          *dependabot.NativePublicationRequest
}

type nativePublishOptions struct {
	root      string
	request   *dependabot.NativePublicationRequest
	publisher *dependabot.NativePublisher
	output    io.Writer
}

// Builds guarded publication of one digest-bound native replacement request.
func newNativePublishCommand() *cobra.Command {
	var options rawNativePublishOptions
	command := &cobra.Command{
		Use:   "publish",
		Short: "Publish one verified native dependency replacement request",
		Args:  cli.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			validated, err := options.Validate()
			if err != nil {
				return err
			}
			completed, err := validated.Complete(cmd.OutOrStdout())
			if err != nil {
				return err
			}
			return completed.Run(cmd.Context())
		},
	}
	command.Flags().StringVar(&options.repository, "repository", "", "OWNER/REPO")
	command.Flags().StringVar(&options.root, "repo", ".", "trusted publication checkout")
	command.Flags().StringVar(&options.outputFile, "output-file", "", "agent safe-output JSON beside its agent artifact directory")
	return command
}

// Validate checks all local request evidence before constructing credentialed clients.
func (o rawNativePublishOptions) Validate() (*validatedNativePublishOptions, error) {
	if !nativeRequestRepositoryPattern.MatchString(o.repository) || o.outputFile == "" {
		return nil, cli.Invalid(errors.New("--repository OWNER/REPO and --output-file are required"))
	}
	root, err := filepath.Abs(o.root)
	if err != nil {
		return nil, cli.Invalid(fmt.Errorf("resolve publication checkout: %w", err))
	}
	request, err := dependabot.ReadNativePublicationRequest(o.outputFile)
	if err != nil {
		return nil, cli.Invalid(fmt.Errorf("validate native publication request: %w", err))
	}
	return &validatedNativePublishOptions{repository: o.repository, root: root, request: request}, nil
}

// Complete supplies authenticated publication dependencies without executing requests.
func (o *validatedNativePublishOptions) Complete(output io.Writer) (*nativePublishOptions, error) {
	completed := &nativePublishOptions{root: o.root, request: o.request, output: output}
	if o.request == nil {
		return completed, nil
	}
	token := os.Getenv("GH_TOKEN")
	if token == "" {
		token = os.Getenv("GITHUB_TOKEN")
	}
	if token == "" {
		return nil, errors.New("native publication requires a GitHub write credential")
	}
	ciToken := os.Getenv("CI_TOKEN")
	if ciToken == "" {
		ciToken = token
	}
	api := gh.NewClient(&http.Client{Timeout: 2 * time.Minute}).WithAuthToken(token)
	ciReader := gh.NewClient(&http.Client{Timeout: 2 * time.Minute}).WithAuthToken(ciToken)
	publisher, err := dependabot.NewNativePublisher(o.repository, dependabot.WithNativePublicationClients(api, ciReader, api))
	if err != nil {
		return nil, fmt.Errorf("complete native publisher: %w", err)
	}
	completed.publisher = publisher
	return completed, nil
}

// Run publishes only the completed request and emits its verified JSON receipt.
func (o *nativePublishOptions) Run(ctx context.Context) error {
	result := map[string]any{"status": "no-request"}
	if o.request != nil {
		var err error
		result, err = o.publisher.Publish(ctx, dependabot.NativePublishOptions{Root: o.root, Request: o.request})
		if err != nil {
			return fmt.Errorf("publish native replacement: %w", err)
		}
	}
	if err := json.NewEncoder(o.output).Encode(result); err != nil {
		return fmt.Errorf("encode native publication receipt: %w", err)
	}
	return nil
}
