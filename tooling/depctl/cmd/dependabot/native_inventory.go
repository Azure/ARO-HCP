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

type rawNativeInventoryOptions struct {
	repository string
}

type validatedNativeInventoryOptions struct {
	repository string
}

type nativeInventoryOptions struct {
	publisher *dependabot.NativePublisher
	output    io.Writer
}

// Builds the read-only native Dependabot inventory command.
func newNativeInventoryCommand() *cobra.Command {
	var options rawNativeInventoryOptions
	command := &cobra.Command{
		Use:   "inventory",
		Short: "List native Dependabot groups and verified robot replacements as JSON",
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
	return command
}

// Validate checks repository syntax before consulting credentials.
func (o rawNativeInventoryOptions) Validate() (*validatedNativeInventoryOptions, error) {
	if !nativeRequestRepositoryPattern.MatchString(o.repository) {
		return nil, cli.Invalid(errors.New("--repository OWNER/REPO is required"))
	}
	return &validatedNativeInventoryOptions{repository: o.repository}, nil
}

// Complete builds inventory-only API and CI readers without making requests.
func (o *validatedNativeInventoryOptions) Complete(output io.Writer) (*nativeInventoryOptions, error) {
	token := os.Getenv("GH_TOKEN")
	if token == "" {
		token = os.Getenv("GITHUB_TOKEN")
	}
	if token == "" {
		return nil, errors.New("native inventory requires a GitHub read credential (GH_TOKEN or GITHUB_TOKEN)")
	}
	ciToken := os.Getenv("CI_TOKEN")
	if ciToken == "" {
		ciToken = token
	}
	api := gh.NewClient(&http.Client{Timeout: 2 * time.Minute}).WithAuthToken(token)
	ciReader := gh.NewClient(&http.Client{Timeout: 2 * time.Minute}).WithAuthToken(ciToken)
	publisher, err := dependabot.NewNativePublisher(o.repository, dependabot.WithNativeInventoryClients(api, ciReader))
	if err != nil {
		return nil, fmt.Errorf("complete native inventory: %w", err)
	}
	return &nativeInventoryOptions{publisher: publisher, output: output}, nil
}

// Run reads the inventory and emits one JSON object on the configured stream.
func (o *nativeInventoryOptions) Run(ctx context.Context) error {
	result, err := o.publisher.Inventory(ctx)
	if err != nil {
		return fmt.Errorf("read native inventory: %w", err)
	}
	if err := json.NewEncoder(o.output).Encode(result); err != nil {
		return fmt.Errorf("encode native inventory: %w", err)
	}
	return nil
}
