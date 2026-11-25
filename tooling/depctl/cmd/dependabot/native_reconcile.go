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
	"time"

	gh "github.com/google/go-github/v69/github"
	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/tooling/depctl/internal/cli"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/dependabot"
)

type rawNativeReconcileOptions struct {
	repository, outputFile string
}

type validatedNativeReconcileOptions struct {
	repository string
	request    *dependabot.NativeActionRequest
}

type nativeReconcileOptions struct {
	request   *dependabot.NativeActionRequest
	publisher *dependabot.NativePublisher
	output    *json.Encoder
}

// Builds the command that applies at most one guarded native lifecycle action.
func newNativeReconcileCommand() *cobra.Command {
	var options rawNativeReconcileOptions
	command := &cobra.Command{
		Use:   "reconcile",
		Short: "Apply one verified native Dependabot lifecycle action",
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
	command.Flags().StringVar(&options.outputFile, "output-file", "", "agent safe-output JSON file")
	return command
}

// Validate checks repository syntax and the complete offline action request before consulting credentials.
func (o rawNativeReconcileOptions) Validate() (*validatedNativeReconcileOptions, error) {
	if !nativeRequestRepositoryPattern.MatchString(o.repository) || o.outputFile == "" {
		return nil, cli.Invalid(errors.New("--repository OWNER/REPO and --output-file are required"))
	}
	request, err := dependabot.ReadNativeActionRequest(o.outputFile)
	if err != nil {
		return nil, cli.Invalid(fmt.Errorf("validate native action request: %w", err))
	}
	return &validatedNativeReconcileOptions{repository: o.repository, request: request}, nil
}

// Complete binds output and prepares separate GitHub action and CI credentials for a validated request.
func (o *validatedNativeReconcileOptions) Complete(output io.Writer) (*nativeReconcileOptions, error) {
	completed := &nativeReconcileOptions{request: o.request, output: json.NewEncoder(output)}
	if o.request == nil {
		return completed, nil
	}
	token := os.Getenv("GH_TOKEN")
	if token == "" {
		token = os.Getenv("GITHUB_TOKEN")
	}
	if token == "" {
		return nil, errors.New("native reconciliation requires a GitHub write credential (GH_TOKEN or GITHUB_TOKEN)")
	}
	ciToken := os.Getenv("CI_TOKEN")
	if ciToken == "" {
		ciToken = token
	}
	api := gh.NewClient(&http.Client{Timeout: 2 * time.Minute}).WithAuthToken(token)
	ciReader := gh.NewClient(&http.Client{Timeout: 2 * time.Minute}).WithAuthToken(ciToken)
	publisher, err := dependabot.NewNativePublisher(o.repository, dependabot.WithNativePublicationClients(api, ciReader, api))
	if err != nil {
		return nil, fmt.Errorf("complete native reconciliation: %w", err)
	}
	completed.publisher = publisher
	return completed, nil
}

// Run executes the completed action, if present, and writes one JSON receipt.
func (o *nativeReconcileOptions) Run(ctx context.Context) error {
	result := map[string]any{"status": "no-request"}
	if o.request != nil {
		var err error
		result, err = o.publisher.Reconcile(ctx, o.request)
		if err != nil {
			return fmt.Errorf("reconcile native Dependabot PR: %w", err)
		}
	}
	if err := o.output.Encode(result); err != nil {
		return fmt.Errorf("encode native reconciliation receipt: %w", err)
	}
	return nil
}
