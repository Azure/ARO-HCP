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

package helloworld

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/admin/client/cmd/base"
	adminClient "github.com/Azure/ARO-HCP/admin/client/pkg/client"
)

func NewHelloWorldCommand() (*cobra.Command, error) {
	opts := base.DefaultAuthOptions()
	cmd := &cobra.Command{
		Use:           "hello-world",
		Short:         "Execute Hello World Admin API endpoint",
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return execute(cmd.Context(), opts)
		},
	}

	if err := opts.BindFlags(cmd); err != nil {
		return nil, err
	}

	return cmd, nil
}

func execute(ctx context.Context, opts *base.RawAuthOptions) error {
	validated, err := opts.Validate(ctx)
	if err != nil {
		return err
	}

	completed, err := validated.Complete(ctx)
	if err != nil {
		return err
	}

	logger, err := logr.FromContext(ctx)
	if err != nil {
		return fmt.Errorf("failed to get logger from context: %w", err)
	}
	logger.Info("Executing hellow world")

	client := adminClient.NewClient(completed.Endpoint, completed.Host, completed.Token, completed.Insecure, false)
	err = client.HelloWorld(ctx)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}

	logger.Info("Request successful")

	return nil
}
